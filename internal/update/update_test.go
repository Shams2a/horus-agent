package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRelease struct {
	files map[string][]byte
}

func newRelease(t *testing.T, key ed25519.PrivateKey, version, binary string) *fakeRelease {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range map[string]string{"README.md": "lisez-moi", "horus-agent": binary} {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
		tw.Write([]byte(content))
	}
	tw.Close()
	gz.Close()
	archive := fmt.Sprintf("horus-agent_%s_linux_amd64.tar.gz", version)
	sum := sha256.Sum256(buf.Bytes())
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + archive + "\n")
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(key, sums))
	prefix := "/v" + version + "/"
	return &fakeRelease{files: map[string][]byte{
		prefix + archive:          buf.Bytes(),
		prefix + "SHA256SUMS":     sums,
		prefix + "SHA256SUMS.sig": []byte(sig),
	}}
}

func serve(t *testing.T, rel *fakeRelease) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases/latest" {
			w.Header().Set("Location", "https://github.com/Shams2a/horus-agent/releases/tag/v1.2.0")
			w.WriteHeader(http.StatusFound)
			return
		}
		data, ok := rel.files[strings.TrimPrefix(r.URL.Path, "/releases/download")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newUpdater(t *testing.T, srv *httptest.Server, pub ed25519.PublicKey) (*Updater, string) {
	bin := filepath.Join(t.TempDir(), "horus-agent")
	os.WriteFile(bin, []byte("ancien"), 0o755)
	return &Updater{
		DownloadBaseURL: srv.URL + "/releases/download",
		BinaryPath:      bin,
		Current:         "1.0.0",
		PublicKey:       pub,
		HTTP:            srv.Client(),
		Arch:            "amd64",
	}, bin
}

func TestApplyReplacesTheBinary(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	u, bin := newUpdater(t, serve(t, newRelease(t, key, "1.1.0", "nouveau")), pub)
	if err := u.Apply(context.Background(), "1.1.0"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(bin); string(got) != "nouveau" {
		t.Errorf("binaire = %q", got)
	}
	if info, _ := os.Stat(bin); info.Mode().Perm() != 0o755 {
		t.Errorf("droits = %v", info.Mode())
	}
}

func TestApplyRejectsAnotherKey(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	_, other, _ := ed25519.GenerateKey(nil)
	u, bin := newUpdater(t, serve(t, newRelease(t, other, "1.1.0", "piégé")), pub)
	if err := u.Apply(context.Background(), "1.1.0"); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("erreur = %v", err)
	}
	if got, _ := os.ReadFile(bin); string(got) != "ancien" {
		t.Error("le binaire ne doit pas changer")
	}
}

func TestApplyRejectsTamperedArchive(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	rel := newRelease(t, key, "1.1.0", "nouveau")
	rel.files["/v1.1.0/horus-agent_1.1.0_linux_amd64.tar.gz"] = append(rel.files["/v1.1.0/horus-agent_1.1.0_linux_amd64.tar.gz"], 0)
	u, _ := newUpdater(t, serve(t, rel), pub)
	if err := u.Apply(context.Background(), "1.1.0"); err == nil || !strings.Contains(err.Error(), "empreinte") {
		t.Fatalf("erreur = %v", err)
	}
}

func TestApplyRejectsOlderOrSameVersion(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	u, _ := newUpdater(t, serve(t, newRelease(t, key, "0.9.0", "vieux")), pub)
	for _, v := range []string{"0.9.0", "1.0.0", "pas-une-version"} {
		if err := u.Apply(context.Background(), v); err == nil {
			t.Errorf("version %s acceptée", v)
		}
	}
}

func TestLatest(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	u, _ := newUpdater(t, serve(t, newRelease(t, key, "1.2.0", "x")), pub)
	if v, err := u.Latest(context.Background()); err != nil || v != "1.2.0" {
		t.Errorf("Latest = %q, %v", v, err)
	}
}

func TestEmbeddedKeyIsValid(t *testing.T) {
	if _, err := New("https://example.test/releases/download", "/tmp/x", "1.0.0"); err != nil {
		t.Fatal(err)
	}
}
