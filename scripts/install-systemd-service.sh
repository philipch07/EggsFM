#!/usr/bin/env bash
set -euo pipefail

SERVICE_NAME="${SERVICE_NAME:-eggsfm}"
INSTALL_DIR="${INSTALL_DIR:-/opt/eggsfm}"
SERVICE_USER="${SERVICE_USER:-eggsfm}"
SERVICE_GROUP="${SERVICE_GROUP:-$SERVICE_USER}"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_TEMPLATE="${REPO_ROOT}/packaging/systemd/eggsfm.service"
NPM_CMD="${NPM_CMD:-npm}"
WEB_DIR="${WEB_DIR:-$REPO_ROOT/web}"
WEB_BUILD_DIR="${WEB_BUILD_DIR:-$WEB_DIR/build}"
INSTALL_WEB_DIR="${INSTALL_WEB_DIR:-$INSTALL_DIR/web}"
ENV_FILE="${ENV_FILE:-$INSTALL_DIR/.env.production}"

if [[ $EUID -ne 0 ]]; then
  echo "this installer must be run as root (try: sudo $0)" >&2
  exit 1
fi

if [[ ! -x "$(command -v go)" ]]; then
  echo "go is required to build the EggsFM binary" >&2
  exit 1
fi

if [[ ! -x "$(command -v systemctl)" ]]; then
  echo "systemd is required to manage the EggsFM service" >&2
  exit 1
fi

if [[ ! -x "$(command -v "$NPM_CMD")" ]]; then
  echo "$NPM_CMD is required to build the EggsFM web UI" >&2
  exit 1
fi


if [[ ! -f "$SERVICE_TEMPLATE" ]]; then
  echo "Missing service template at $SERVICE_TEMPLATE" >&2
  exit 1
fi

if ! getent group "$SERVICE_GROUP" >/dev/null; then
  groupadd --system "$SERVICE_GROUP"
fi

if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
  useradd --system --home "$INSTALL_DIR" --no-create-home --gid "$SERVICE_GROUP" --shell /usr/sbin/nologin "$SERVICE_USER"
fi

install -d -m 2775 "$INSTALL_DIR"
chown "$SERVICE_USER":"$SERVICE_GROUP" "$INSTALL_DIR"

install -d -m 2775 "$INSTALL_DIR/media"
chown "$SERVICE_USER":"$SERVICE_GROUP" "$INSTALL_DIR/media"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "Copying $REPO_ROOT/.env.production to $ENV_FILE"
  install -m 640 "$REPO_ROOT/.env.production" "$ENV_FILE"
else
  echo "Using existing $ENV_FILE"
fi
chown "$SERVICE_USER":"$SERVICE_GROUP" "$ENV_FILE"

cd "$REPO_ROOT"
echo "Building EggsFM into $INSTALL_DIR/eggsfm"
go build -o "$INSTALL_DIR/eggsfm" .
chown "$SERVICE_USER":"$SERVICE_GROUP" "$INSTALL_DIR/eggsfm"

echo "Building EggsFM web UI"
set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

(cd "$WEB_DIR" && "$NPM_CMD" ci)
(cd "$WEB_DIR" && "$NPM_CMD" run build)

rm -rf "$INSTALL_WEB_DIR"
install -d -m 2775 "$INSTALL_WEB_DIR"
cp -a "$WEB_BUILD_DIR"/. "$INSTALL_WEB_DIR"/
chown -R "$SERVICE_USER":"$SERVICE_GROUP" "$INSTALL_WEB_DIR"

tmp_unit="$(mktemp)"
trap 'rm -f "$tmp_unit"' EXIT

sed -e "s|/opt/eggsfm|$INSTALL_DIR|g" \
    -e "s|^User=eggsfm|User=$SERVICE_USER|" \
    -e "s|^Group=eggsfm|Group=$SERVICE_GROUP|" \
    "$SERVICE_TEMPLATE" > "$tmp_unit"

install -m 644 "$tmp_unit" "$SERVICE_FILE"

systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl restart "$SERVICE_NAME"
systemctl status "$SERVICE_NAME" --no-pager
