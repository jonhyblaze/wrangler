#!/bin/sh
# Wrangler install script — detects architecture, downloads the correct binary.
# Usage: curl -fsSL https://raw.githubusercontent.com/jonhyblaze/wrangler/main/install.sh | sh

set -e

REPO="jonhyblaze/wrangler"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Detect architecture.
ARCH=$(uname -m)
case "$ARCH" in
  arm64|aarch64)
    ARCH_SUFFIX="darwin_arm64"
    ;;
  x86_64|amd64)
    ARCH_SUFFIX="darwin_amd64"
    ;;
  *)
    echo "ERROR: Unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

# Detect OS.
OS=$(uname -s)
if [ "$OS" != "Darwin" ]; then
  echo "ERROR: Wrangler currently supports macOS only." >&2
  exit 1
fi

echo "→ Fetching latest release info..."
LATEST_TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' \
  | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')

if [ -z "$LATEST_TAG" ]; then
  echo "ERROR: Could not determine latest release tag." >&2
  exit 1
fi

echo "→ Latest version: $LATEST_TAG"

ARCHIVE="wrangler_${LATEST_TAG}_${ARCH_SUFFIX}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${LATEST_TAG}/${ARCHIVE}"

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

echo "→ Downloading $ARCHIVE..."
curl -fsSL "$URL" -o "$TMP_DIR/$ARCHIVE"

echo "→ Extracting..."
tar xzf "$TMP_DIR/$ARCHIVE" -C "$TMP_DIR"

# Install binary.
if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP_DIR/wrangler" "$INSTALL_DIR/wrangler"
  chmod +x "$INSTALL_DIR/wrangler"
else
  echo "→ Installing to $INSTALL_DIR (requires sudo)..."
  sudo mv "$TMP_DIR/wrangler" "$INSTALL_DIR/wrangler"
  sudo chmod +x "$INSTALL_DIR/wrangler"
fi

echo "→ Verifying installation..."
"$INSTALL_DIR/wrangler" --version

echo ""
echo "✓ Wrangler installed successfully!"
echo "  Run: wrangler"
