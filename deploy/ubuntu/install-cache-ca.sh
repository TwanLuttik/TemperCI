#!/usr/bin/env bash
# Install (or generate) the TemperCI Actions-cache CA into a mounted guest rootfs
# so the guest trusts the host SNI intercept for results/blob hosts.
#
# Usage:
#   ./deploy/ubuntu/install-cache-ca.sh <mounted-rootfs> [ca.crt]
#
# If ca.crt is omitted, writes a new CA under the host data dir:
#   /var/lib/temperci/cache/ca/ca.crt  (create with openssl if missing)
set -euo pipefail

ROOT="${1:-}"
CA_CRT="${2:-}"
if [[ -z "$ROOT" || ! -d "$ROOT" ]]; then
  echo "usage: $0 <mounted-rootfs> [ca.crt]" >&2
  exit 2
fi

if [[ -z "$CA_CRT" ]]; then
  HOST_CA_DIR="${TEMPERCI_DATA_DIR:-/var/lib/temperci}/cache/ca"
  mkdir -p "$HOST_CA_DIR"
  CA_CRT="$HOST_CA_DIR/ca.crt"
  CA_KEY="$HOST_CA_DIR/ca.key"
  if [[ ! -f "$CA_CRT" || ! -f "$CA_KEY" ]]; then
    openssl req -x509 -newkey rsa:2048 -sha256 -days 3650 -nodes \
      -subj "/CN=TemperCI Actions Cache CA" \
      -keyout "$CA_KEY" -out "$CA_CRT"
  fi
fi

install -d -m 0755 "$ROOT/usr/local/share/ca-certificates"
install -m 0644 "$CA_CRT" "$ROOT/usr/local/share/ca-certificates/temperci-cache.crt"
if [[ -x "$ROOT/usr/sbin/update-ca-certificates" ]] || [[ -x /usr/sbin/update-ca-certificates ]]; then
  chroot "$ROOT" /usr/sbin/update-ca-certificates || true
fi
echo "installed TemperCI cache CA into $ROOT"
