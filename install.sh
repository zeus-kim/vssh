#!/bin/bash
# vssh - AI-native remote execution daemon (drop-in ssh replacement)
# Install:  curl -fsSL https://raw.githubusercontent.com/zeus-kim/vssh/main/install.sh | bash
# Pin:      curl -fsSL .../install.sh | VSSH_VERSION=0.7.36 bash
# Dir:      curl -fsSL .../install.sh | INSTALL_DIR=/usr/local/bin bash

set -euo pipefail

REPO="zeus-kim/vssh"
BINARY="vssh"
INSTALL_DIR="${INSTALL_DIR:-$HOME/bin}"
VSSH_VERSION="${VSSH_VERSION:-latest}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64)          ARCH="amd64" ;;
    aarch64|arm64)   ARCH="arm64" ;;
    armv7l|armv6l|arm) ARCH="arm" ;;
    i686|i386)       ARCH="386" ;;
    riscv64)         ARCH="riscv64" ;;
    ppc64le)         ARCH="ppc64le" ;;
    s390x)           ARCH="s390x" ;;
    *) echo "vssh: unsupported architecture: $ARCH" >&2; exit 1 ;;
esac
case "$OS" in
    linux)  PLATFORM="linux" ;;
    darwin) PLATFORM="darwin" ;;
    *) echo "vssh: unsupported OS: $OS" >&2; exit 1 ;;
esac

ASSET="${BINARY}-${PLATFORM}-${ARCH}"
if [ "$VSSH_VERSION" = "latest" ]; then
    BASE="https://github.com/$REPO/releases/latest/download"
else
    BASE="https://github.com/$REPO/releases/download/v${VSSH_VERSION#v}"
fi

echo "vssh: installing ${ASSET} (${VSSH_VERSION}) -> ${INSTALL_DIR}"
mkdir -p "$INSTALL_DIR"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fetch() { # url dest
    if ! curl -fsSL "$1" -o "$2"; then
        echo "vssh: download failed: $1" >&2
        echo "vssh: build locally instead: GOOS=$PLATFORM GOARCH=$ARCH go build -o $INSTALL_DIR/$BINARY ./cmd/vssh" >&2
        exit 1
    fi
}

fetch "$BASE/$ASSET" "$TMP/$ASSET"

# Checksum verification (fail-closed). checksums.txt lists: "<sha256>  <asset>"
if fetch "$BASE/checksums.txt" "$TMP/checksums.txt" 2>/dev/null; then
    EXPECTED="$(grep " ${ASSET}\$" "$TMP/checksums.txt" | awk '{print $1}' | head -1)"
    if [ -z "$EXPECTED" ]; then
        echo "vssh: no checksum entry for $ASSET in checksums.txt" >&2; exit 1
    fi
    if command -v sha256sum >/dev/null 2>&1; then
        ACTUAL="$(sha256sum "$TMP/$ASSET" | awk '{print $1}')"
    else
        ACTUAL="$(shasum -a 256 "$TMP/$ASSET" | awk '{print $1}')"
    fi
    if [ "$EXPECTED" != "$ACTUAL" ]; then
        echo "vssh: CHECKSUM MISMATCH for $ASSET" >&2
        echo "  expected $EXPECTED" >&2
        echo "  actual   $ACTUAL" >&2
        exit 1
    fi
    echo "vssh: checksum verified (sha256 ${ACTUAL:0:12}...)"
else
    echo "vssh: WARNING - checksums.txt unavailable, skipping verification" >&2
fi

chmod +x "$TMP/$ASSET"
mv -f "$TMP/$ASSET" "$INSTALL_DIR/$BINARY"

echo ""
echo "vssh installed: $("$INSTALL_DIR/$BINARY" --version 2>/dev/null || echo "$INSTALL_DIR/$BINARY")"
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo "Add to PATH:  export PATH=\"\$PATH:$INSTALL_DIR\""
fi
echo ""
echo "Next:"
echo "  vssh status        # fleet dashboard"
echo "  vssh server        # run the daemon (key-only auth; see ~/.vssh/authorized_keys)"
echo "  vssh <node>        # shell into a node"
echo "  vssh mcp           # MCP server for AI agents"
