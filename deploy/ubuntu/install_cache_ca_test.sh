#!/usr/bin/env bash
# Tests for install-cache-ca.sh helpers (no chroot update-ca-certificates).
set -euo pipefail
root="$(cd "$(dirname "$0")" && pwd)"
TEMPERCI_CACHE_CA_LIB=1
# shellcheck disable=SC1091
source "$root/install-cache-ca.sh"

fail() { echo "FAIL: $*" >&2; exit 1; }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/root/home/runner" "$tmp/root/root" "$tmp/root/opt/actions-runner" \
  "$tmp/root/etc/sudoers.d" "$tmp/root/etc"
printf 'PATH="/usr/bin"\n' >"$tmp/root/etc/environment"
printf 'runner ALL=(ALL) NOPASSWD:ALL\n' >"$tmp/root/etc/sudoers.d/runner"
printf '%s\n' '-----BEGIN CERTIFICATE-----' 'TESTCA' '-----END CERTIFICATE-----' >"$tmp/ca.crt"

TEMPERCI_SKIP_UPDATE_CA=1 temperci_install_cache_ca "$tmp/root" "$tmp/ca.crt"

[[ -f "$tmp/root/usr/local/share/ca-certificates/temperci-cache.crt" ]] || fail "system CA missing"
grep -q TESTCA "$tmp/root/usr/local/share/ca-certificates/temperci-cache.crt" || fail "CA contents"
grep -q '^NODE_EXTRA_CA_CERTS=' "$tmp/root/etc/environment" || fail "environment NODE_EXTRA_CA_CERTS"
grep -q 'NODE_EXTRA_CA_CERTS' "$tmp/root/opt/actions-runner/.env" || fail "runner .env"
grep -q 'cafile=' "$tmp/root/home/runner/.npmrc" || fail "runner npmrc"
grep -q 'cafile=' "$tmp/root/root/.npmrc" || fail "root npmrc"
grep -q 'NODE_EXTRA_CA_CERTS' "$tmp/root/home/runner/.profile" || fail "runner profile"
grep -q 'NODE_EXTRA_CA_CERTS' "$tmp/root/etc/sudoers.d/temperci-cache-ca" || fail "sudoers env_keep"

echo "ok"
