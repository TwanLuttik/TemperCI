#!/usr/bin/env bash
# Install (or generate) the TemperCI Actions-cache CA into a mounted guest rootfs
# so every guest user (root + runner) and Node/npm trusts the host SNI intercept.
#
# Usage:
#   ./deploy/ubuntu/install-cache-ca.sh <mounted-rootfs> [ca.crt]
#
# If ca.crt is omitted, writes a new CA under the host data dir:
#   /var/lib/temperci/cache/ca/ca.crt  (create with openssl if missing)
set -euo pipefail

CA_REL="/usr/local/share/ca-certificates/temperci-cache.crt"
BUNDLE_REL="/etc/ssl/certs/ca-certificates.crt"

temperci_ensure_host_cache_ca() {
  local dir="${1:-${TEMPERCI_DATA_DIR:-/var/lib/temperci}/cache/ca}"
  mkdir -p "$dir"
  if [[ ! -f "$dir/ca.crt" || ! -f "$dir/ca.key" ]]; then
    openssl req -x509 -newkey rsa:2048 -sha256 -days 3650 -nodes \
      -subj "/CN=TemperCI Actions Cache CA" \
      -keyout "$dir/ca.key" -out "$dir/ca.crt"
  fi
  printf '%s\n' "$dir/ca.crt"
}

temperci_write_user_ca_env() {
  local home="$1"
  mkdir -p "$home"
  if [[ -f "$home/.profile" ]]; then
    grep -q 'NODE_EXTRA_CA_CERTS=' "$home/.profile" 2>/dev/null || \
      printf '\nexport NODE_EXTRA_CA_CERTS=%s\nexport SSL_CERT_FILE=%s\n' "$CA_REL" "$BUNDLE_REL" >>"$home/.profile"
  else
    printf 'export NODE_EXTRA_CA_CERTS=%s\nexport SSL_CERT_FILE=%s\n' "$CA_REL" "$BUNDLE_REL" >"$home/.profile"
  fi
  if [[ -f "$home/.bashrc" ]]; then
    grep -q 'NODE_EXTRA_CA_CERTS=' "$home/.bashrc" 2>/dev/null || \
      printf '\nexport NODE_EXTRA_CA_CERTS=%s\nexport SSL_CERT_FILE=%s\n' "$CA_REL" "$BUNDLE_REL" >>"$home/.bashrc"
  fi
  # cafile replaces npm/pnpm's default store. Point at the full system
  # bundle (which includes our CA after update-ca-certificates).
  printf 'cafile=%s\n' "$BUNDLE_REL" >"$home/.npmrc"
}

temperci_install_cache_ca() {
  local root="$1" ca_crt="$2"
  install -d -m 0755 "$root/usr/local/share/ca-certificates"
  install -m 0644 "$ca_crt" "$root$CA_REL"

  if [[ -z "${TEMPERCI_SKIP_UPDATE_CA:-}" ]] && \
     { [[ -x "$root/usr/sbin/update-ca-certificates" ]] || [[ -x /usr/sbin/update-ca-certificates ]]; }; then
    chroot "$root" /usr/sbin/update-ca-certificates || true
  fi

  install -d -m 0755 "$root/etc/profile.d"
  cat >"$root/etc/profile.d/temperci-cache-ca.sh" <<EOF
export NODE_EXTRA_CA_CERTS=${CA_REL}
export SSL_CERT_FILE=${BUNDLE_REL}
export REQUESTS_CA_BUNDLE=${BUNDLE_REL}
export CURL_CA_BUNDLE=${BUNDLE_REL}
export GIT_SSL_CAINFO=${BUNDLE_REL}
EOF
  chmod 0644 "$root/etc/profile.d/temperci-cache-ca.sh"

  mkdir -p "$root/etc"
  touch "$root/etc/environment"
  grep -q '^NODE_EXTRA_CA_CERTS=' "$root/etc/environment" || \
    echo "NODE_EXTRA_CA_CERTS=${CA_REL}" >>"$root/etc/environment"
  grep -q '^SSL_CERT_FILE=' "$root/etc/environment" || \
    echo "SSL_CERT_FILE=${BUNDLE_REL}" >>"$root/etc/environment"
  grep -q '^REQUESTS_CA_BUNDLE=' "$root/etc/environment" || \
    echo "REQUESTS_CA_BUNDLE=${BUNDLE_REL}" >>"$root/etc/environment"

  if [[ -d "$root/opt/actions-runner" ]]; then
    cat >"$root/opt/actions-runner/.env" <<EOF
NODE_EXTRA_CA_CERTS=${CA_REL}
SSL_CERT_FILE=${BUNDLE_REL}
REQUESTS_CA_BUNDLE=${BUNDLE_REL}
CURL_CA_BUNDLE=${BUNDLE_REL}
GIT_SSL_CAINFO=${BUNDLE_REL}
EOF
    chmod 0644 "$root/opt/actions-runner/.env"
  fi

  if [[ -d "$root/home/runner" ]]; then
    temperci_write_user_ca_env "$root/home/runner"
    # Keep ownership if runner already exists in the image.
    if [[ -f "$root/etc/passwd" ]] && grep -q '^runner:' "$root/etc/passwd"; then
      chown -R --reference="$root/home/runner" "$root/home/runner/.profile" "$root/home/runner/.npmrc" 2>/dev/null || true
    fi
  fi
  if [[ -d "$root/root" ]]; then
    temperci_write_user_ca_env "$root/root"
  fi

  printf 'cafile=%s\n' "$BUNDLE_REL" >"$root/etc/npmrc"
  chmod 0644 "$root/etc/npmrc"

  install -d -m 0755 "$root/etc/sudoers.d"
  cat >"$root/etc/sudoers.d/temperci-cache-ca" <<'EOF'
Defaults env_keep += "NODE_EXTRA_CA_CERTS SSL_CERT_FILE REQUESTS_CA_BUNDLE CURL_CA_BUNDLE GIT_SSL_CAINFO"
EOF
  chmod 0440 "$root/etc/sudoers.d/temperci-cache-ca"

  if [[ -f "$root/etc/hosts" ]] && ! grep -q 'tempercicache.blob.core.windows.net' "$root/etc/hosts"; then
    echo "10.231.255.254 tempercicache.blob.core.windows.net" >>"$root/etc/hosts"
  fi

  if [[ -f "$root/etc/systemd/system/temperci-runner-agent.service" ]]; then
    if ! grep -q 'NODE_EXTRA_CA_CERTS=' "$root/etc/systemd/system/temperci-runner-agent.service"; then
      awk -v ca="$CA_REL" -v bundle="$BUNDLE_REL" '
        /^ExecStart=/ && !seen {
          print "Environment=NODE_EXTRA_CA_CERTS=" ca
          print "Environment=SSL_CERT_FILE=" bundle
          print "Environment=REQUESTS_CA_BUNDLE=" bundle
          seen=1
        }
        { print }
      ' "$root/etc/systemd/system/temperci-runner-agent.service" >"$root/etc/systemd/system/temperci-runner-agent.service.tmp"
      mv "$root/etc/systemd/system/temperci-runner-agent.service.tmp" "$root/etc/systemd/system/temperci-runner-agent.service"
    fi
  fi
}

if [[ "${TEMPERCI_CACHE_CA_LIB:-}" == 1 ]]; then
  return 0 2>/dev/null || exit 0
fi

ROOT="${1:-}"
CA_CRT="${2:-}"
if [[ -z "$ROOT" || ! -d "$ROOT" ]]; then
  echo "usage: $0 <mounted-rootfs> [ca.crt]" >&2
  exit 2
fi

if [[ -z "$CA_CRT" ]]; then
  CA_CRT="$(temperci_ensure_host_cache_ca)"
fi
temperci_install_cache_ca "$ROOT" "$CA_CRT"
echo "installed TemperCI cache CA into $ROOT"
