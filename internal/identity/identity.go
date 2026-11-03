// Package identity gère la clé de l'agent et l'identifiant de la machine.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Shams2a/horus-agent/internal/protocol"
)

// Generate crée une nouvelle paire de clés Ed25519.
func Generate() (ed25519.PrivateKey, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	return key, err
}

// PublicKeyBase64 : la forme envoyée à Network à l'inscription.
func PublicKeyBase64(key ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
}

// Save écrit la graine de la clé privée (32 octets en base64), en 0600.
func Save(path string, key ed25519.PrivateKey, uid, gid int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	data := []byte(base64.StdEncoding.EncodeToString(key.Seed()) + "\n")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if uid >= 0 {
		if err := os.Chown(tmp, uid, gid); err != nil {
			return err
		}
	}
	return os.Rename(tmp, path)
}

// Load relit la clé privée écrite par Save.
func Load(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seed, err := base64.StdEncoding.DecodeString(string(trimSpace(raw)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New(path + " ne contient pas une clé Ed25519 valide")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// MachineHash lit /etc/machine-id et renvoie son empreinte (§ 2).
func MachineHash(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("lecture de %s : %w", path, err)
	}
	id := trimSpace(raw)
	if len(id) == 0 {
		return "", errors.New(path + " est vide")
	}
	return protocol.MachineHash(string(id)), nil
}

func trimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\n' || b[start] == '\r' || b[start] == '\t') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\n' || b[end-1] == '\r' || b[end-1] == '\t') {
		end--
	}
	return b[start:end]
}
