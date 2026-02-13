#!/bin/bash
# SessionStart hook: ensures Go version matches go.mod requirement.
# Downloads and installs the correct Go version if mismatched.
set -e

# Extract required version from go.mod
REQUIRED_VERSION=$(grep -oP '^go \K[0-9.]+' go.mod 2>/dev/null || echo "")
if [ -z "$REQUIRED_VERSION" ]; then
  exit 0
fi

# Check current version
CURRENT_VERSION=$(go version 2>/dev/null | grep -oP 'go\K[0-9.]+' || echo "")
if [ "$CURRENT_VERSION" = "$REQUIRED_VERSION" ]; then
  exit 0
fi

echo "Go version mismatch: have $CURRENT_VERSION, need $REQUIRED_VERSION. Installing..." >&2

# Detect platform
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
esac

TARBALL="go${REQUIRED_VERSION}.${OS}-${ARCH}.tar.gz"
URL="https://go.dev/dl/${TARBALL}"

curl -fsSL -o "/tmp/${TARBALL}" "$URL" || { echo "Failed to download Go ${REQUIRED_VERSION}" >&2; exit 2; }
rm -rf /usr/local/go
tar -C /usr/local -xzf "/tmp/${TARBALL}"
rm -f "/tmp/${TARBALL}"

# Verify
INSTALLED=$(/usr/local/go/bin/go version 2>/dev/null | grep -oP 'go\K[0-9.]+' || echo "")
if [ "$INSTALLED" = "$REQUIRED_VERSION" ]; then
  echo "Go ${REQUIRED_VERSION} installed successfully." >&2
else
  echo "Go installation verification failed: got ${INSTALLED}" >&2
  exit 2
fi
