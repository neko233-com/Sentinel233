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
    BIN_URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY}-${OS}-${ARCH}${EXT}"
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
  echo "  ${BINARY}                              # Start on :23390"
  echo "  ${BINARY} -addr :8080                  # Custom port"
  echo "  ${BINARY} -config sentinel233.yaml     # With config file"
  echo "  ${BINARY} -version                     # Show version"
  echo ""
  echo "Enable systemd autostart (Linux):"
  echo "  sudo systemctl enable --now sentinel233-server"
  echo ""

  case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
      echo "Add to PATH:"
      echo "  export PATH=\"\$PATH:$INSTALL_DIR\""
      ;;
  esac
}

main
