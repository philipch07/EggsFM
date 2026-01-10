#!/usr/bin/env bash
set -euo pipefail

SERVICE_NAME="${SERVICE_NAME:-eggsfm}"
INSTALL_DIR="${INSTALL_DIR:-/opt/eggsfm}"
SYSTEMCTL_CMD="${SYSTEMCTL_CMD:-systemctl}"
RESTART="${RESTART:-1}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_WEB="${BUILD_WEB:-1}"
INSTALL_WEB_DEPS="${INSTALL_WEB_DEPS:-0}"
NPM_CMD="${NPM_CMD:-npm}"
WEB_DIR="${WEB_DIR:-$REPO_ROOT/web}"
WEB_BUILD_DIR="${WEB_BUILD_DIR:-$WEB_DIR/build}"
ENV_FILE="${ENV_FILE:-$INSTALL_DIR/.env.production}"

if [[ ! -x "$(command -v go)" ]]; then
  echo "go is required to build the EggsFM binary" >&2
  exit 1
fi

if [[ "$BUILD_WEB" == "1" && ! -x "$(command -v "$NPM_CMD")" ]]; then
  echo "$NPM_CMD is required to build the EggsFM web UI (set BUILD_WEB=0 to skip)" >&2
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

if [[ "$BUILD_WEB" == "1" ]]; then
  echo "Building EggsFM web UI"
  if [[ -f "$ENV_FILE" ]]; then
    set -a
    # shellcheck disable=SC1090
    source "$ENV_FILE"
    set +a
  fi

  if [[ "$INSTALL_WEB_DEPS" == "1" || ! -d "$WEB_DIR/node_modules" ]]; then
    (cd "$WEB_DIR" && "$NPM_CMD" ci)
  fi

  (cd "$WEB_DIR" && "$NPM_CMD" run build)

  if [[ ! -f "$WEB_BUILD_DIR/index.html" ]]; then
    echo "Frontend build output not found at $WEB_BUILD_DIR (rerun with BUILD_WEB=1)" >&2
    exit 1
  fi
else
  if [[ ! -f "$WEB_BUILD_DIR/index.html" ]]; then
    echo "Frontend assets are missing at $WEB_BUILD_DIR. Run with BUILD_WEB=1 first so the binary can embed them." >&2
    exit 1
  fi
  echo "Skipping web build (using existing assets in $WEB_BUILD_DIR)"
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
