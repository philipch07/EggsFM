#!/usr/bin/env bash
set -euo pipefail

SERVICE_NAME="${SERVICE_NAME:-eggsfm}"
INSTALL_DIR="${INSTALL_DIR:-/opt/eggsfm}"
SERVICE_USER="${SERVICE_USER:-eggsfm}"
SERVICE_GROUP="${SERVICE_GROUP:-$SERVICE_USER}"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_TEMPLATE="${REPO_ROOT}/packaging/systemd/eggsfm.service"

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

cd "$REPO_ROOT"
echo "Building EggsFM into $INSTALL_DIR/eggsfm"
go build -o "$INSTALL_DIR/eggsfm" .
chown "$SERVICE_USER":"$SERVICE_GROUP" "$INSTALL_DIR/eggsfm"

if [[ ! -f "$INSTALL_DIR/.env.production" ]]; then
  echo "Copying .env.production to $INSTALL_DIR (edit it for your host)"
  install -m 640 "$REPO_ROOT/.env.production" "$INSTALL_DIR/.env.production"
else
  echo "Keeping existing $INSTALL_DIR/.env.production"
fi
chown "$SERVICE_USER":"$SERVICE_GROUP" "$INSTALL_DIR/.env.production"

install -d -m 2775 "$INSTALL_DIR/media"
chown "$SERVICE_USER":"$SERVICE_GROUP" "$INSTALL_DIR/media"

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
