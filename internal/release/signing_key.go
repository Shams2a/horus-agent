// Package release regroupe ce qui sert à vérifier les versions publiées.
package release

// SigningPublicKey est la clé publique Ed25519 (base64, 32 octets) qui signe
// le fichier SHA256SUMS de chaque Release GitHub. La clé privée correspondante
// n'existe que dans le secret HORUS_AGENT_SIGNING_KEY du dépôt et dans la
// sauvegarde de Shams. Changer cette clé impose une version de transition
// (voir docs/publication.md).
const SigningPublicKey = "HUHsVwF0+IfmrfMuXDM+Og1cHPo0IrgIaCRSpqb3cmE="
