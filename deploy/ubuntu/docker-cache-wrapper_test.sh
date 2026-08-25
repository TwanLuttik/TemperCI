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

# build without existing cache-to → buildx build --load + cache flags (mode=min)
export TEMPERCI_FORCE_BUILDX=1
GITHUB_REPOSITORY=acme/app
got="$(temperci_docker_rewrite build -t x .)"
want="buildx build --load --cache-from type=registry,ref=ghcr.io/__temperci_cache/acme/app/buildkit --cache-to type=registry,ref=ghcr.io/__temperci_cache/acme/app/buildkit,mode=min -t x ."
expect_eq "$got" "$want" "plain docker build"

# buildx build
got="$(temperci_docker_rewrite buildx build -t x .)"
want="buildx build --cache-from type=registry,ref=ghcr.io/__temperci_cache/acme/app/buildkit --cache-to type=registry,ref=ghcr.io/__temperci_cache/acme/app/buildkit,mode=min -t x ."
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

# without buildx, do not rewrite (job must still build)
unset TEMPERCI_FORCE_BUILDX
export TEMPERCI_DISABLE_BUILDX=1
GITHUB_REPOSITORY=acme/app
got="$(temperci_docker_rewrite build -t x .)"
want=""
expect_eq "$got" "$want" "no buildx leaves docker build alone"

# have_buildx must never invoke `docker` on PATH (that is this wrapper).
unset TEMPERCI_DISABLE_BUILDX TEMPERCI_FORCE_BUILDX
hit="$(mktemp)"
trap 'rm -f "$hit"' EXIT
fakebin="$(mktemp -d)"
cat >"${fakebin}/docker" <<EOF
#!/usr/bin/env bash
echo invoked >>"$hit"
exit 0
EOF
chmod +x "${fakebin}/docker"
PATH="${fakebin}:$PATH" temperci_have_buildx || true
if [[ -s "$hit" ]]; then
  fail "temperci_have_buildx invoked PATH docker (would recurse as the wrapper)"
fi

echo "ok"
