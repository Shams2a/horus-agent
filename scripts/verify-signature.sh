#!/usr/bin/env bash
# Vérifie une signature Ed25519 (base64) avec la clé publique intégrée à l'agent.
# Usage : scripts/verify-signature.sh <fichier> <fichier.sig>
set -euo pipefail

file="$1"
sig="$2"
root="$(cd "$(dirname "$0")/.." && pwd)"
pub_b64="$(sed -n 's/^const SigningPublicKey = "\(.*\)"$/\1/p' "$root/internal/release/signing_key.go")"
[ -n "$pub_b64" ] || { echo "Clé publique introuvable dans internal/release/signing_key.go" >&2; exit 1; }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
# En-tête DER SubjectPublicKeyInfo d'une clé Ed25519, suivi des 32 octets de la clé.
{ printf '302a300506032b6570032100' | xxd -r -p; printf '%s' "$pub_b64" | base64 -d; } > "$tmp/pub.der"
openssl pkey -pubin -inform DER -in "$tmp/pub.der" -out "$tmp/pub.pem"
base64 -d "$sig" > "$tmp/sig.bin"
openssl pkeyutl -verify -pubin -inkey "$tmp/pub.pem" -rawin -in "$file" -sigfile "$tmp/sig.bin"
