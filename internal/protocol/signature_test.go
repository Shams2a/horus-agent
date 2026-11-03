package protocol

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

// Vecteur de l'annexe A de docs/protocole.md. Le backend Network doit
// retrouver exactement les mêmes valeurs.
const (
	vectorSeedHex   = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
	vectorPublicKey = "11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo="
	vectorMethod    = "POST"
	vectorPath      = "/agent/v1/report"
	vectorTimestamp = int64(1762195350000)
	vectorBody      = `{"agent_version":"1.0.0","samples":[]}`
	vectorBodyHash  = "f505c4b1229d0c3c57795157e63ff0c469a2e7142ffdc5d961f8e9bfd47893b7"
	vectorSignature = "PIMYX18loGN0RFLtU0uV0UT2a7l9UavpJO6Sex4r4+QY1mDVxxZuoQ6lj0rgRhfKPqg6TeHUQxRkGVXGl6D7DA=="
)

func vectorKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	seed, err := hex.DecodeString(vectorSeedHex)
	if err != nil {
		t.Fatal(err)
	}
	return ed25519.NewKeyFromSeed(seed)
}

func TestVecteurAnnexeA(t *testing.T) {
	key := vectorKey(t)
	pub := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	sum := sha256.Sum256([]byte(vectorBody))
	sig := Sign(key, vectorMethod, vectorPath, vectorTimestamp, []byte(vectorBody))

	if pub != vectorPublicKey {
		t.Errorf("clé publique = %s, attendu %s", pub, vectorPublicKey)
	}
	if got := hex.EncodeToString(sum[:]); got != vectorBodyHash {
		t.Errorf("sha256 du corps = %s, attendu %s", got, vectorBodyHash)
	}
	if sig != vectorSignature {
		t.Errorf("signature = %s, attendu %s", sig, vectorSignature)
	}
}

func TestVerifyRefuseUneModification(t *testing.T) {
	key := vectorKey(t)
	pub := key.Public().(ed25519.PublicKey)
	sig := Sign(key, vectorMethod, vectorPath, vectorTimestamp, []byte(vectorBody))

	if err := Verify(pub, vectorMethod, vectorPath, vectorTimestamp, []byte(vectorBody), sig); err != nil {
		t.Fatalf("signature valide refusée : %v", err)
	}
	cas := map[string]func() error{
		"corps": func() error { return Verify(pub, vectorMethod, vectorPath, vectorTimestamp, []byte(`{}`), sig) },
		"chemin": func() error {
			return Verify(pub, vectorMethod, "/agent/v1/enroll", vectorTimestamp, []byte(vectorBody), sig)
		},
		"horodatage": func() error { return Verify(pub, vectorMethod, vectorPath, vectorTimestamp+1, []byte(vectorBody), sig) },
		"méthode":    func() error { return Verify(pub, "PUT", vectorPath, vectorTimestamp, []byte(vectorBody), sig) },
		"pas base64": func() error { return Verify(pub, vectorMethod, vectorPath, vectorTimestamp, []byte(vectorBody), "@@@") },
		"trop courte": func() error {
			return Verify(pub, vectorMethod, vectorPath, vectorTimestamp, []byte(vectorBody), "AAAA")
		},
	}
	for nom, f := range cas {
		if err := f(); err != ErrBadSignature {
			t.Errorf("%s : erreur = %v, attendu ErrBadSignature", nom, err)
		}
	}
}

func TestMethodeEnMinuscules(t *testing.T) {
	if CanonicalString("post", "/p", 1, nil) != CanonicalString("POST", "/p", 1, nil) {
		t.Error("la méthode doit être mise en majuscules")
	}
}

func TestMachineHashIgnoreLesEspaces(t *testing.T) {
	if MachineHash("abc\n") != MachineHash("abc") {
		t.Error("le retour à la ligne final de /etc/machine-id doit être ignoré")
	}
}
