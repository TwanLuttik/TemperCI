#!/usr/bin/env bash
# Unit tests for install.sh helpers (no apt, Firecracker, or debootstrap).
set -euo pipefail
root="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=install.sh
TEMPERCI_INSTALL_LIB=1
# shellcheck disable=SC1091
source "$root/install.sh"

fail() { echo "FAIL: $*" >&2; exit 1; }

expect_eq() {
  local got="$1" want="$2" msg="$3"
  if [[ "$got" != "$want" ]]; then
    fail "$msg"$'\n'"  got:  $got"$'\n'"  want: $want"
  fi
}

expect_ok() {
  if ! "$@"; then
    fail "expected success: $*"
  fi
}

expect_fail() {
  if "$@" >/dev/null 2>&1; then
    fail "expected failure: $*"
  fi
}

# --- progress ---
got="$(temperci_step_line 3 8 "Installing Firecracker" ok)"
expect_eq "$got" "[3/8] Installing Firecracker ...................... ok" "step ok line"

got="$(temperci_step_line 8 8 "Preparing guest image" skip)"
expect_eq "$got" "[8/8] Preparing guest image ....................... skip" "step skip line"

# --- os parse ---
expect_ok temperci_os_supported $'NAME="Ubuntu"\nVERSION_ID="24.04"\nID=ubuntu'
expect_ok temperci_os_supported $'NAME="Ubuntu"\nVERSION_ID="22.04"\nID=ubuntu'
expect_fail temperci_os_supported $'NAME="Ubuntu"\nVERSION_ID="20.04"\nID=ubuntu'
expect_ok temperci_os_supported $'NAME="Debian GNU/Linux"\nVERSION_ID="12"\nID=debian'
expect_ok temperci_os_supported $'NAME="Debian GNU/Linux"\nVERSION_ID="13"\nID=debian'
expect_fail temperci_os_supported $'NAME="Debian GNU/Linux"\nVERSION_ID="11"\nID=debian'
expect_fail temperci_os_supported $'NAME="Fedora Linux"\nVERSION_ID="41"\nID=fedora'

pkgs="$(TEMPERCI_SKIP_QEMU_KVM=1 temperci_apt_packages | tr '\n' ' ')"
echo "$pkgs" | grep -q qemu-kvm && fail "qemu-kvm must not be installed when skipped"
echo "$pkgs" | grep -q debootstrap || fail "debootstrap required"

# control runs as User=temperci with ProtectSystem=strict; wizard writes
# github-app.pem and control.toml under /etc/temperci.
unit="$root/../systemd/temperci-control.service"
grep -E '^ReadWritePaths=.*\/etc\/temperci' "$unit" >/dev/null || fail "control unit must ReadWritePaths=/etc/temperci (wizard PEM write)"

# --- write-once TOML ---
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# fetch+install must replace dest via install(1), not curl -o dest
rep="$tmp/replace-dest"
echo old >"$rep"
chmod 0755 "$rep"
srcf="$tmp/replace-src"
echo newbin >"$srcf"
install -m 0755 "$srcf" "$rep"
expect_eq "$(cat "$rep")" "newbin" "install replaces dest inode"

ctl="$tmp/control.toml"
agent="$tmp/agent.toml"
token="aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"

temperci_write_control_toml "$ctl" "$token"
expect_ok test -f "$ctl"
mode="$(stat -c '%a' "$ctl" 2>/dev/null || stat -f '%OLp' "$ctl")"
expect_eq "$mode" "600" "control.toml mode"
grep -q 'setup_completed = false' "$ctl" || fail "setup_completed missing"
grep -q "agent_token = \"$token\"" "$ctl" || fail "control token"
first="$(cat "$ctl")"
echo "mutated" >"$ctl"
temperci_write_control_toml "$ctl" "other-token-should-not-apply"
got="$(cat "$ctl")"
expect_eq "$got" "mutated" "control.toml write-once"

temperci_write_agent_toml "$agent" "$token" "/var/lib/temperci"
grep -q 'vmm_backend = "firecracker"' "$agent" || fail "agent vmm"
grep -q 'cache_listen_addr = "127.0.0.1:8743"' "$agent" || fail "agent cache"
grep -q "agent_token = \"$token\"" "$agent" || fail "agent token"
echo "keep" >"$agent"
temperci_write_agent_toml "$agent" "nope" "/var/lib/temperci"
expect_eq "$(cat "$agent")" "keep" "agent.toml write-once"

# --- wizard URL ---
got="$(temperci_wizard_url $'127.0.0.1\n192.168.1.10\n10.0.0.50')"
expect_eq "$got" "http://192.168.1.10:8080/" "prefer first non-loopback"

got="$(temperci_wizard_url $'127.0.0.1')"
expect_eq "$got" "http://127.0.0.1:8080/" "loopback fallback"

# --- prepare-guest-image (stubbed fetch/build) ---
img="$tmp/images"
mkdir -p "$img" "$tmp/bin"
cat >"$tmp/bin/fetch" <<'EOF'
#!/bin/sh
echo fetched >"$1"
EOF
cat >"$tmp/bin/build" <<EOF
#!/bin/sh
echo rootfs >"${img}/ubuntu-2404-runner.ext4"
EOF
chmod +x "$tmp/bin/fetch" "$tmp/bin/build"
TEMPERCI_IMAGES_DIR="$img" TEMPERCI_FETCH_KERNEL="$tmp/bin/fetch" TEMPERCI_BUILD_GUEST="$tmp/bin/build" \
  bash "$root/prepare-guest-image.sh"
[[ -s "${img}/.ready" ]] || fail "stamp not written"
[[ -s "${img}/vmlinux" ]] || fail "kernel not written"
[[ -s "${img}/ubuntu-2404-runner.ext4" ]] || fail "rootfs not written"
# second run is a no-op (does not rewrite stamp content via missing files)
stamp="$(cat "${img}/.ready")"
TEMPERCI_IMAGES_DIR="$img" TEMPERCI_FETCH_KERNEL="$tmp/bin/fetch" TEMPERCI_BUILD_GUEST="$tmp/bin/build" \
  bash "$root/prepare-guest-image.sh"
expect_eq "$(cat "${img}/.ready")" "$stamp" "prepare is idempotent when ready"

echo "ok"
