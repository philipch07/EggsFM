#!/usr/bin/env bash
set -euo pipefail

SERVICE_NAME="${SERVICE_NAME:-eggsfm}"
INSTALL_DIR="${INSTALL_DIR:-/opt/eggsfm}"
SYSTEMCTL_CMD="${SYSTEMCTL_CMD:-systemctl}"
RESTART="${RESTART:-1}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ ! -x "$(command -v go)" ]]; then
  echo "go is required to build the EggsFM binary" >&2
  exit 1
fi

if [[ ! -d "$INSTALL_DIR" ]]; then
  echo "Install dir $INSTALL_DIR not found (run scripts/install-systemd-service.sh first)" >&2
  exit 1
fi

if [[ ! -w "$INSTALL_DIR" ]]; then
  echo "No write access to $INSTALL_DIR. Add your user to the EggsFM group or rerun with sufficient permissions." >&2
  exit 1
fi

tmp_bin="$(mktemp)"
trap 'rm -f "$tmp_bin"' EXIT

echo "Building EggsFM (output: $tmp_bin)"
(cd "$REPO_ROOT" && go build -o "$tmp_bin" .)

install -m 755 "$tmp_bin" "$INSTALL_DIR/eggsfm"
echo "Updated binary at $INSTALL_DIR/eggsfm"

if [[ "$RESTART" == "1" ]]; then
  if ! "$SYSTEMCTL_CMD" restart "$SERVICE_NAME"; then
    echo "Restart failed. Try again with: SYSTEMCTL_CMD=\"sudo systemctl\" ./scripts/rebuild-eggsfm.sh" >&2
    exit 1
  fi
  "$SYSTEMCTL_CMD" status "$SERVICE_NAME" --no-pager || true
else
  echo "Skipping restart (set RESTART=1 to restart automatically)."
fi
