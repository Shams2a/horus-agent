// Package update installe une version officielle de l'agent (docs/protocole.md § 7).
//
// Network ne transmet qu'un numéro de version. L'archive est téléchargée
// depuis les Releases, la signature de SHA256SUMS est vérifiée avec la clé
// publique intégrée au binaire, puis l'empreinte de l'archive, et toute
// version qui n'est pas strictement plus récente est refusée.
//
// Deux façons d'installer :
//   - Apply : remplace le binaire directement (root, ou installation 0.1.x
//     où le binaire appartient à l'agent) ;
//   - Stage puis InstallStaged : l'agent, sans droits root, dépose la version
//     dans requests/update ; le service d'aide root la revérifie entièrement
//     avec son propre binaire avant de l'installer.
package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Shams2a/horus-agent/internal/release"
	"github.com/Shams2a/horus-agent/internal/semver"
)

const (
	maxArchiveBytes = 100 << 20
	maxSumsBytes    = 1 << 20
	maxSigBytes     = 4096
)

// Fichiers déposés pour le service d'aide.
const (
	stagedArchive = "archive.tar.gz"
	stagedSums    = "SHA256SUMS"
	stagedSig     = "SHA256SUMS.sig"
	stagedVersion = "version"
	// ReadyFile : créé en dernier, c'est lui qui réveille le service d'aide.
	ReadyFile = "ready"
)

type Updater struct {
	// DownloadBaseURL : .../releases/download (configuration de l'agent).
	DownloadBaseURL string
	BinaryPath      string
	Current         string
	PublicKey       ed25519.PublicKey
	HTTP            *http.Client
	Arch            string
}

// New prépare un Updater avec la clé publique intégrée au binaire.
func New(downloadBaseURL, binaryPath, current string) (*Updater, error) {
	raw, err := base64.StdEncoding.DecodeString(release.SigningPublicKey)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("clé publique de signature invalide dans le binaire")
	}
	return &Updater{
		DownloadBaseURL: strings.TrimRight(downloadBaseURL, "/"),
		BinaryPath:      binaryPath,
		Current:         current,
		PublicKey:       ed25519.PublicKey(raw),
		HTTP:            &http.Client{Timeout: 5 * time.Minute},
		Arch:            runtime.GOARCH,
	}, nil
}

// Latest lit le numéro de la dernière Release GitHub, en suivant la
// redirection de .../releases/latest vers .../releases/tag/vX.Y.Z.
func (u *Updater) Latest(ctx context.Context) (string, error) {
	latestURL := strings.TrimSuffix(u.DownloadBaseURL, "/download") + "/latest"
	httpc := *u.HTTP
	httpc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, latestURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	tag := path.Base(resp.Header.Get("Location"))
	version := strings.TrimPrefix(tag, "v")
	if !semver.Valid(version) {
		return "", fmt.Errorf("dernière version introuvable (%s)", latestURL)
	}
	return version, nil
}

type bundle struct {
	version            string
	archive, sums, sig []byte
}

func (u *Updater) archiveName(version string) string {
	return fmt.Sprintf("horus-agent_%s_linux_%s.tar.gz", version, u.Arch)
}

func (u *Updater) download(ctx context.Context, version string) (*bundle, error) {
	version = strings.TrimPrefix(version, "v")
	if !semver.IsNewer(version, u.Current) {
		return nil, fmt.Errorf("version %s refusée : elle n'est pas plus récente que %s", version, u.Current)
	}
	base := fmt.Sprintf("%s/v%s/", u.DownloadBaseURL, version)
	b := &bundle{version: version}
	var err error
	if b.sums, err = u.get(ctx, base+"SHA256SUMS", maxSumsBytes); err != nil {
		return nil, err
	}
	if b.sig, err = u.get(ctx, base+"SHA256SUMS.sig", maxSigBytes); err != nil {
		return nil, err
	}
	if b.archive, err = u.get(ctx, base+u.archiveName(version), maxArchiveBytes); err != nil {
		return nil, err
	}
	return b, nil
}

// verify contrôle la version, la signature et l'empreinte, puis renvoie le
// binaire extrait de l'archive.
func (u *Updater) verify(b *bundle) ([]byte, error) {
	if !semver.IsNewer(b.version, u.Current) {
		return nil, fmt.Errorf("version %s refusée : elle n'est pas plus récente que %s", b.version, u.Current)
	}
	if err := VerifySums(u.PublicKey, b.sums, b.sig); err != nil {
		return nil, err
	}
	want, err := expectedHash(b.sums, u.archiveName(b.version))
	if err != nil {
		return nil, err
	}
	if got := sha256.Sum256(b.archive); hex.EncodeToString(got[:]) != want {
		return nil, errors.New("empreinte de l'archive incorrecte, mise à jour abandonnée")
	}
	return extractBinary(b.archive)
}

// Apply télécharge, vérifie et installe `version` à la place du binaire.
// Il ne redémarre pas le service : l'appelant s'en charge.
func (u *Updater) Apply(ctx context.Context, version string) error {
	b, err := u.download(ctx, version)
	if err != nil {
		return err
	}
	binary, err := u.verify(b)
	if err != nil {
		return err
	}
	return replace(u.BinaryPath, binary)
}

// Stage télécharge et vérifie `version`, puis la dépose dans dir pour le
// service d'aide. La vérification ici ne sert qu'à refuser tôt : le service
// d'aide recommence tout.
func (u *Updater) Stage(ctx context.Context, version, dir string) error {
	b, err := u.download(ctx, version)
	if err != nil {
		return err
	}
	if _, err := u.verify(b); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	for name, data := range map[string][]byte{
		stagedArchive: b.archive,
		stagedSums:    b.sums,
		stagedSig:     b.sig,
		stagedVersion: []byte(b.version + "\n"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o640); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, ReadyFile), nil, 0o640)
}

// InstallStaged installe la version déposée dans dir, après l'avoir
// entièrement revérifiée. Utilisé par le service d'aide root : dir appartient
// à l'agent, rien de ce qu'il contient n'est cru sur parole.
func (u *Updater) InstallStaged(dir string) (string, error) {
	rawVersion, err := readRegular(filepath.Join(dir, stagedVersion), 64)
	if err != nil {
		return "", err
	}
	b := &bundle{version: strings.TrimSpace(string(rawVersion))}
	if !semver.Valid(b.version) {
		return "", errors.New("numéro de version déposé illisible")
	}
	if b.archive, err = readRegular(filepath.Join(dir, stagedArchive), maxArchiveBytes); err != nil {
		return "", err
	}
	if b.sums, err = readRegular(filepath.Join(dir, stagedSums), maxSumsBytes); err != nil {
		return "", err
	}
	if b.sig, err = readRegular(filepath.Join(dir, stagedSig), maxSigBytes); err != nil {
		return "", err
	}
	binary, err := u.verify(b)
	if err != nil {
		return "", err
	}
	return b.version, replace(u.BinaryPath, binary)
}

// readRegular lit un fichier ordinaire, en refusant les liens symboliques et
// les fichiers trop gros.
//
// Le fichier est ouvert sans suivre de lien (O_NOFOLLOW) et sans bloquer
// (O_NONBLOCK, pour un tube), puis vérifié sur le descripteur ouvert : le
// dossier appartient à l'agent, qui pourrait remplacer le fichier entre une
// vérification et la lecture. La lecture reste bornée quoi qu'il arrive.
func readRegular(name string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s n'est pas un fichier ordinaire", filepath.Base(name))
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s dépasse la taille attendue", filepath.Base(name))
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%s dépasse la taille attendue", filepath.Base(name))
	}
	return raw, nil
}

// VerifySums vérifie la signature Ed25519 (base64) de SHA256SUMS.
func VerifySums(pub ed25519.PublicKey, sums, sigFile []byte) error {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigFile)))
	if err != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(pub, sums, sig) {
		return errors.New("signature de SHA256SUMS invalide, mise à jour abandonnée")
	}
	return nil
}

func expectedHash(sums []byte, name string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	for scanner.Scan() {
		f := strings.Fields(scanner.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("%s absent de SHA256SUMS", name)
}

func (u *Updater) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("téléchargement de %s : statut %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s dépasse la taille attendue", url)
	}
	return data, nil
}

func extractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("binaire horus-agent absent de l'archive")
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg && path.Base(hdr.Name) == "horus-agent" {
			return io.ReadAll(io.LimitReader(tr, maxArchiveBytes))
		}
	}
}

// replace remplace le binaire de façon atomique (même dossier, puis rename).
// Le nouveau binaire garde le propriétaire de l'ancien : root pour une
// installation 0.2.0, l'agent pour une installation 0.1.x.
func replace(target string, binary []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".horus-agent-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(binary); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := keepOwner(target, tmp.Name()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}
