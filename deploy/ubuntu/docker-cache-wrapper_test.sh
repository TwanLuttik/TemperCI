#!/usr/bin/env bash
# Unit tests for temperci_docker_rewrite (no docker binary required).
set -euo pipefail
root="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=docker-cache-wrapper.sh
source "$root/docker-cache-wrapper.sh"

fail() { echo "FAIL: $*" >&2; exit 1; }

expect_eq() {
  local got="$1" want="$2" msg="$3"
  if [[ "$got" != "$want" ]]; then
    fail "$msg"$'\n'"  got:  $got"$'\n'"  want: $want"
  fi
}

# build without existing cache-to → buildx build --load + cache flags
GITHUB_REPOSITORY=acme/app
got="$(temperci_docker_rewrite build -t x .)"
want="buildx build --load --cache-from type=registry,ref=ghcr.io/__temperci_cache/acme/app/buildkit --cache-to type=registry,ref=ghcr.io/__temperci_cache/acme/app/buildkit,mode=max -t x ."
expect_eq "$got" "$want" "plain docker build"

# buildx build
got="$(temperci_docker_rewrite buildx build -t x .)"
want="buildx build --cache-from type=registry,ref=ghcr.io/__temperci_cache/acme/app/buildkit --cache-to type=registry,ref=ghcr.io/__temperci_cache/acme/app/buildkit,mode=max -t x ."
expect_eq "$got" "$want" "docker buildx build"

# existing --cache-to is left alone
got="$(temperci_docker_rewrite buildx build --cache-to type=gha -t x .)"
want=""
expect_eq "$got" "$want" "existing cache-to"

# run is unchanged
got="$(temperci_docker_rewrite run postgres)"
want=""
expect_eq "$got" "$want" "docker run"

# no repo env
unset GITHUB_REPOSITORY
got="$(temperci_docker_rewrite build -t x .)"
want=""
expect_eq "$got" "$want" "missing GITHUB_REPOSITORY"

echo "ok"
