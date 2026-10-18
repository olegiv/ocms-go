#!/bin/bash
# SessionStart hook: ensures Go version matches go.mod requirement.
# Downloads, verifies and installs the correct Go version if mismatched.
#
# Not wired into .claude/settings.json on purpose: it replaces /usr/local/go,
# which is a machine-wide change that an operator should opt into. Add it to
# SessionStart in .claude/settings.local.json (gitignored) on a machine where
# that is wanted; TestClaudeHooksAreWired keeps it out of settings.json.
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

# Extract into the target's own filesystem so the final step is a rename,
# not a copy that can fail half-way, and put the previous toolchain back if
# anything goes wrong after it was moved aside.
STAGE="/usr/local/.go.new.$$"
PREVIOUS="/usr/local/.go.previous.$$"
restore() {
  if [ -d "$PREVIOUS" ] && [ ! -d /usr/local/go ]; then
    mv "$PREVIOUS" /usr/local/go
  fi
  rm -rf "$STAGE" "$WORK"
}
trap restore EXIT
mkdir -p "$STAGE"
tar -C "$STAGE" -xzf "$WORK/$TARBALL"
INSTALLED=$("$STAGE/go/bin/go" version 2>/dev/null | sed -n 's/^go version go\([0-9.][0-9.]*\).*/\1/p' || echo "")
if [ "$INSTALLED" != "$REQUIRED_VERSION" ]; then
  echo "Go installation verification failed: got ${INSTALLED:-nothing}" >&2
  exit 2
fi
if [ -d /usr/local/go ]; then
  mv /usr/local/go "$PREVIOUS"
fi
mv "$STAGE/go" /usr/local/go
rm -rf "$PREVIOUS"
echo "Go ${REQUIRED_VERSION} installed successfully." >&2
