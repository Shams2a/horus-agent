package protocol

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

// En-têtes des requêtes signées (docs/protocole.md § 2).
const (
	HeaderAgent     = "X-Horus-Agent"
	HeaderTimestamp = "X-Horus-Timestamp"
	HeaderSignature = "X-Horus-Signature"
)

// CanonicalString construit le texte signé : méthode, chemin, horodatage en
// millisecondes et sha256 hexadécimal du corps, séparés par « \n ».
func CanonicalString(method, path string, timestampMs int64, body []byte) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{
		strings.ToUpper(method),
		path,
		strconv.FormatInt(timestampMs, 10),
		hex.EncodeToString(sum[:]),
	}, "\n")
}

// Sign renvoie la signature Ed25519 du texte canonique, en base64 standard.
func Sign(key ed25519.PrivateKey, method, path string, timestampMs int64, body []byte) string {
	sig := ed25519.Sign(key, []byte(CanonicalString(method, path, timestampMs, body)))
	return base64.StdEncoding.EncodeToString(sig)
}

// ErrBadSignature signale une signature absente, mal encodée ou fausse.
var ErrBadSignature = errors.New("signature invalide")

// Verify contrôle une signature produite par Sign. Le contrôle de fraîcheur
// de l'horodatage reste à la charge du serveur.
func Verify(pub ed25519.PublicKey, method, path string, timestampMs int64, body []byte, signature string) error {
	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrBadSignature
	}
	if !ed25519.Verify(pub, []byte(CanonicalString(method, path, timestampMs, body)), sig) {
		return ErrBadSignature
	}
	return nil
}

// MachineHash calcule l'identifiant de machine envoyé à Network à partir du
// contenu de /etc/machine-id, sans jamais transmettre ce dernier en clair.
func MachineHash(machineID string) string {
	sum := sha256.Sum256([]byte("horus-agent:" + strings.TrimSpace(machineID)))
	return hex.EncodeToString(sum[:])
}
