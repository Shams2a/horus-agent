#!/usr/bin/env bash
# Installe horus-agent, l'agent de monitoring Horus Network.
#
#   curl -fsSL https://github.com/Shams2a/horus-agent/releases/latest/download/install.sh \
#     | sudo bash -s -- --token hn_enr_… --api-url https://networkapi.…
#
# Ce script ne fait que préparer la machine : il vérifie l'environnement,
# télécharge le binaire, contrôle son empreinte SHA-256, crée le compte
# horus-agent, puis passe la main à « horus-agent setup » avec les mêmes
# options. Toute la logique est dans le binaire (docs/protocole.md).
#
# Variables facultatives :
#   HORUS_AGENT_VERSION        version à installer (défaut : la dernière)
#   HORUS_AGENT_RELEASES_URL   adresse des Releases (défaut : GitHub)
set -euo pipefail

RELEASES_URL="${HORUS_AGENT_RELEASES_URL:-https://github.com/Shams2a/horus-agent/releases}"
VERSION="${HORUS_AGENT_VERSION:-latest}"
INSTALL_DIR=/opt/horus-agent
USER_NAME=horus-agent

fail() {
  echo "Erreur : $*" >&2
  exit 1
}

[ "$(id -u)" -eq 0 ] || fail "lancez l'installation en root (sudo)."
[ "$(uname -s)" = "Linux" ] || fail "horus-agent ne fonctionne que sous Linux."

case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) fail "architecture $(uname -m) non prise en charge (x86_64 ou ARM64 seulement)." ;;
esac

if [ ! -d /run/systemd/system ] && [[ " $* " != *" --no-service "* ]]; then
  fail "systemd est introuvable. L'agent v1 a besoin de systemd (sur un conteneur sans systemd, ajoutez --no-service)."
fi

for tool in tar sha256sum; do
  command -v "$tool" >/dev/null 2>&1 || fail "la commande $tool est introuvable."
done
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
  final_url() { curl -fsSLI -o /dev/null -w '%{url_effective}' "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
  final_url() { wget -q --max-redirect=5 -S --spider "$1" 2>&1 | awk '/^  Location: /{l=$2} END{print l}'; }
else
  fail "curl ou wget est nécessaire."
fi

if [ "$VERSION" = "latest" ]; then
  tag="$(final_url "$RELEASES_URL/latest")"
  tag="${tag##*/}"
  VERSION="${tag#v}"
fi
VERSION="${VERSION#v}"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$ ]] || fail "version introuvable ($VERSION)."

ARCHIVE="horus-agent_${VERSION}_linux_${ARCH}.tar.gz"
BASE="$RELEASES_URL/download/v$VERSION"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Téléchargement de horus-agent $VERSION ($ARCH)…"
fetch "$BASE/$ARCHIVE" "$tmp/$ARCHIVE"
fetch "$BASE/SHA256SUMS" "$tmp/SHA256SUMS"

expected="$(awk -v f="$ARCHIVE" '$2 == f || $2 == "*"f {print $1}' "$tmp/SHA256SUMS")"
[ -n "$expected" ] || fail "$ARCHIVE absent de SHA256SUMS."
actual="$(sha256sum "$tmp/$ARCHIVE" | awk '{print $1}')"
[ "$expected" = "$actual" ] || fail "empreinte SHA-256 incorrecte, installation abandonnée."

tar -xzf "$tmp/$ARCHIVE" -C "$tmp" horus-agent

if ! id "$USER_NAME" >/dev/null 2>&1; then
  nologin=/usr/sbin/nologin
  [ -x "$nologin" ] || nologin=/sbin/nologin
  useradd --system --user-group --no-create-home --home-dir /var/lib/horus-agent \
    --shell "$nologin" "$USER_NAME"
fi

# Le dossier du binaire appartient au compte de l'agent : c'est ce qui lui
# permet d'installer lui-même une mise à jour signée, sans droits root.
install -d -m 0755 -o "$USER_NAME" -g "$USER_NAME" "$INSTALL_DIR" "$INSTALL_DIR/bin"
install -d -m 0750 -o "$USER_NAME" -g "$USER_NAME" /var/lib/horus-agent
install -m 0755 -o "$USER_NAME" -g "$USER_NAME" "$tmp/horus-agent" "$INSTALL_DIR/bin/horus-agent"
ln -sf "$INSTALL_DIR/bin/horus-agent" /usr/local/bin/horus-agent

exec "$INSTALL_DIR/bin/horus-agent" setup --download-base "$RELEASES_URL/download" "$@"
