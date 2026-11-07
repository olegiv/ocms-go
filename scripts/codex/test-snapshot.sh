#!/usr/bin/env bash
# Run the project's gate from a fresh copy of a trusted candidate tree.
set -euo pipefail

tree=$1
quality=${2:-false}
root=$(git rev-parse --show-toplevel)
objects=$(git rev-parse --path-format=absolute --git-path objects)
shared=$(git rev-parse "$tree:.claude/shared")
shared_root="$root/.claude/shared"
if [[ ! -e "$shared_root/.git" ]]; then
  echo 'Initialize .claude/shared at the recorded revision before preparing a commit.' >&2
  exit 2
fi
shared_objects=$(git -C "$shared_root" rev-parse --path-format=absolute --git-path objects)

snapshot=$(mktemp -d "${TMPDIR:-/tmp}/ocms-test-tree.XXXXXXXX")
trap 'rm -rf -- "$snapshot"' EXIT
git archive "$tree" | tar -xf - -C "$snapshot"

(
  # Clear repository-local overrides before initializing the snapshot.
  git_vars=$(git rev-parse --local-env-vars)
  unset $git_vars
  export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
  cd "$snapshot"
  git init -q --template=
  printf '%s\n' "$objects" > .git/objects/info/alternates
  git read-tree "$tree"

  git init -q --template= .claude/shared
  printf '%s\n' "$shared_objects" > .claude/shared/.git/objects/info/alternates
  git -C .claude/shared switch --quiet --detach "$shared"

  check_tree() {
    if [[ $(git write-tree) != "$tree" || $(git -C .claude/shared rev-parse HEAD) != "$shared" ]] ||
      ! git diff --exit-code --no-ext-diff --no-textconv --ignore-submodules=all ||
      [[ -n $(git -C .claude/shared status --porcelain) ]]; then
      echo 'The snapshot changed tracked content or the shared tooling revision.' >&2
      exit 2
    fi
  }

  export GOWORK=off GOENV=off GOFLAGS= OCMS_REQUIRE_SHARED_TOOLING=1
  unset OCMS_SESSION_SECRET
  # A local replacement can import code from outside the snapshot.
  go mod edit -json | node -e '
    const mod = JSON.parse(require("fs").readFileSync(0, "utf8"));
    if ((mod.Replace || []).some(replacement => !replacement.New.Version)) {
      console.error("The snapshot gate requires versioned Go dependencies.");
      process.exit(2);
    }
  '
  check_tree
  CI=true ./scripts/build-assets.sh
  check_tree
  if [[ "$quality" == true ]]; then
    golangci-lint run ./...
    check_tree
  fi
  ./scripts/test-gate.sh
  check_tree
)
