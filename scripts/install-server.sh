#!/usr/bin/env bash
# Sentinel233 Server one-click installer for Linux/macOS/Windows-MSYS
# Usage: curl -fsSL https://raw.githubusercontent.com/neko233-com/Sentinel233/main/scripts/install-server.sh | bash
set -euo pipefail

REPO="neko233-com/Sentinel233"
BINARY="sentinel233-server"
VERSION="${1:-latest}"

detect_os() {
  case "$(uname -s)" in
    Linux*)  echo "linux" ;;
    Darwin*) echo "darwin" ;;
    MINGW*|MSYS*|CYGWIN*) echo "windows" ;;
    *)       echo "unknown" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)  echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *)             echo "amd64" ;;
  esac
}

get_latest_version() {
  curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/' || echo "v0.1.0"
}

main() {
  OS=$(detect_os)
  ARCH=$(detect_arch)

  if [ "$VERSION" = "latest" ]; then
    VERSION=$(get_latest_version)
  fi

  VER_NUM="${VERSION#[vV]}"

  if [ -n "${SENTINEL233_SERVER_INSTALL:-}" ]; then
    INSTALL_DIR="$SENTINEL233_SERVER_INSTALL"
  elif [ "$OS" = "windows" ]; then
    INSTALL_DIR="${LOCALAPPDATA:-$HOME/AppData/Local}/sentinel233"
  else
    INSTALL_DIR="/usr/local/bin"
  fi
  DATA_DIR="${SENTINEL233_DATA:-/var/lib/sentinel233}"

  if [ "$OS" = "windows" ]; then
    EXT=".exe"
  else
    EXT=""
  fi

  TMPDIR=$(mktemp -d)
  trap 'rm -rf "$TMPDIR"' EXIT

  echo "Installing ${BINARY} server ${VERSION} for ${OS}/${ARCH}..."

  ARCHIVE="${BINARY}-${VER_NUM}-${OS}-${ARCH}.tar.gz"
  URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"

  if curl -fsSL "$URL" -o "$TMPDIR/$ARCHIVE" 2>/dev/null; then
    tar -xzf "$TMPDIR/$ARCHIVE" -C "$TMPDIR"
    BIN_PATH=$(find "$TMPDIR" -name "${BINARY}${EXT}" -type f | head -1)
  else
    echo "Archive not found, trying direct binary..."
    BIN_URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY}-${VER_NUM}-${OS}-${ARCH}${EXT}"
    curl -fsSL "$BIN_URL" -o "$TMPDIR/${BINARY}${EXT}"
    BIN_PATH="$TMPDIR/${BINARY}${EXT}"
  fi

  if [ ! -f "$BIN_PATH" ]; then
    echo "Error: binary not found"
    exit 1
  fi

  chmod +x "$BIN_PATH"

  if [ ! -w "$INSTALL_DIR" ]; then
    echo "Need sudo to install to $INSTALL_DIR"
    sudo mkdir -p "$INSTALL_DIR"
    sudo cp "$BIN_PATH" "$INSTALL_DIR/${BINARY}${EXT}"
  else
    mkdir -p "$INSTALL_DIR"
    cp "$BIN_PATH" "$INSTALL_DIR/${BINARY}${EXT}"
  fi

  echo ""
  echo "${BINARY} server ${VERSION} installed to ${INSTALL_DIR}/${BINARY}${EXT}"
  echo ""
  echo "Quick Start:"
  echo "  ${BINARY} -data ${DATA_DIR}            # Start on :23390"
  echo "  ${BINARY} -addr :8080 -data ${DATA_DIR} # Custom port"
  echo "  ${BINARY} -config sentinel233.yaml -data ${DATA_DIR}"
  echo "  ${BINARY} -version                     # Show version"
  echo ""
  if [ "${SENTINEL233_INSTALL_SERVICE:-0}" = "1" ] && [ "$OS" = "linux" ] && command -v systemctl >/dev/null 2>&1; then
    echo "Installing systemd service..."
    sudo mkdir -p "$DATA_DIR"
    sudo tee /etc/systemd/system/sentinel233-server.service >/dev/null <<EOF
[Unit]
Description=Sentinel233 local TSDB and monitoring server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/${BINARY} -data ${DATA_DIR}
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF
    sudo systemctl daemon-reload
    sudo systemctl enable --now sentinel233-server
    echo "systemd service sentinel233-server is running."
    echo ""
  else
    echo "Enable systemd autostart (Linux):"
    echo "  SENTINEL233_INSTALL_SERVICE=1 curl -fsSL https://raw.githubusercontent.com/${REPO}/main/scripts/install-server.sh | bash"
    echo ""
  fi

  case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
      echo "Add to PATH:"
      echo "  export PATH=\"\$PATH:$INSTALL_DIR\""
      ;;
  esac
}

main
