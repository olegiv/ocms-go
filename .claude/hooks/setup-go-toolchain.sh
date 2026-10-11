#!/bin/bash
# SessionStart hook: ensures Go version matches go.mod requirement.
# Downloads, verifies and installs the correct Go version if mismatched.
#
# Not wired into .claude/settings.json on purpose: it replaces /usr/local/go,
# which is a machine-wide change that an operator should opt into. Add it to
# the SessionStart hooks of a checkout where that is wanted.
set -euo pipefail

# Extract required version from go.mod
REQUIRED_VERSION=$(sed -n 's/^go \([0-9.][0-9.]*\).*/\1/p' go.mod 2>/dev/null | head -n1 || echo "")
if [ -z "$REQUIRED_VERSION" ]; then
  exit 0
fi

# Check current version
CURRENT_VERSION=$(go version 2>/dev/null | sed -n 's/^go version go\([0-9.][0-9.]*\).*/\1/p' || echo "")
if [ "$CURRENT_VERSION" = "$REQUIRED_VERSION" ]; then
  exit 0
fi

echo "Go version mismatch: have ${CURRENT_VERSION:-none}, need $REQUIRED_VERSION. Installing..." >&2

# Detect platform
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
esac

TARBALL="go${REQUIRED_VERSION}.${OS}-${ARCH}.tar.gz"
URL="https://go.dev/dl/${TARBALL}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# Download the archive and its published checksum, and verify before touching
# the existing installation.
curl -fsSL -o "$WORK/$TARBALL" "$URL" || { echo "Failed to download Go ${REQUIRED_VERSION}" >&2; exit 2; }
curl -fsSL -o "$WORK/$TARBALL.sha256" "$URL.sha256" || { echo "Failed to download the checksum for Go ${REQUIRED_VERSION}" >&2; exit 2; }
EXPECTED=$(tr -d '[:space:]' < "$WORK/$TARBALL.sha256")
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL=$(sha256sum "$WORK/$TARBALL" | cut -d' ' -f1)
else
  ACTUAL=$(shasum -a 256 "$WORK/$TARBALL" | cut -d' ' -f1)
fi
if [ "$EXPECTED" != "$ACTUAL" ]; then
  echo "Checksum mismatch for ${TARBALL}: expected ${EXPECTED}, got ${ACTUAL}" >&2
  exit 2
fi

# Extract next to the target and swap only after a successful extraction, so
# a failed download never leaves the machine without a Go toolchain.
mkdir -p "$WORK/extract"
tar -C "$WORK/extract" -xzf "$WORK/$TARBALL"
INSTALLED=$("$WORK/extract/go/bin/go" version 2>/dev/null | sed -n 's/^go version go\([0-9.][0-9.]*\).*/\1/p' || echo "")
if [ "$INSTALLED" != "$REQUIRED_VERSION" ]; then
  echo "Go installation verification failed: got ${INSTALLED:-nothing}" >&2
  exit 2
fi
rm -rf /usr/local/go.previous
if [ -d /usr/local/go ]; then
  mv /usr/local/go /usr/local/go.previous
fi
mv "$WORK/extract/go" /usr/local/go
rm -rf /usr/local/go.previous
echo "Go ${REQUIRED_VERSION} installed successfully." >&2
