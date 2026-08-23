#!/usr/bin/env bash
# TemperCI one-command installer for a single Ubuntu/KVM host.
#
#   curl -fsSL https://github.com/TwanLuttik/TemperCI/releases/latest/download/install.sh | bash
#   TEMPERCI_BIN_DIR=./bin ./deploy/ubuntu/install.sh
# Must run as root (no sudo required if you are already root).
#
# The operator then opens the printed URL and finishes the setup wizard.
set -euo pipefail

FIRECRACKER_VERSION="${FIRECRACKER_VERSION:-v1.9.1}"
TEMPERCI_REPO="${TEMPERCI_REPO:-TwanLuttik/TemperCI}"
TEMPERCI_VERSION="${TEMPERCI_VERSION:-latest}"
TEMPERCI_RELEASE_URL="${TEMPERCI_RELEASE_URL:-}"
TEMPERCI_BIN_DIR="${TEMPERCI_BIN_DIR:-}"
DESTDIR="${DESTDIR:-${TEMPERCI_ROOT:-}}"

temperci_root() {
  printf '%s%s' "${DESTDIR:-}" "$1"
}

temperci_step_line() {
  local n="$1" total="$2" name="$3" status="$4"
  local field="$name "
  while [[ ${#field} -lt 45 ]]; do
    field="${field}."
  done
  printf '[%s/%s] %s %s' "$n" "$total" "$field" "$status"
}

temperci_step() {
  echo "$(temperci_step_line "$@")" >&2
}

temperci_ubuntu_supported() {
  local text="$1"
  echo "$text" | grep -q '^ID=ubuntu' || return 1
  echo "$text" | grep -Eq '^VERSION_ID="?(22.04|24.04)"?' || return 1
}

temperci_write_control_toml() {
  local path="$1" token="$2"
  if [[ -e "$path" ]]; then
    return 0
  fi
  mkdir -p "$(dirname "$path")"
  umask 077
  cat >"$path" <<EOF
listen_addr = "0.0.0.0:8080"
github_app_private_key_path = "/etc/temperci/github-app.pem"
github_org = ""
label_prefix = "temperci-"
runner_group_id = 1
agent_token = "${token}"
auth_mode = "open"
setup_completed = false
sqlite_path = "/var/lib/temperci/control.db"
hostctl_path = "/usr/local/bin/temperci-hostctl"
data_dir = "/var/lib/temperci"
EOF
  chmod 0600 "$path"
}

temperci_write_agent_toml() {
  local path="$1" token="$2" data_dir="$3"
  if [[ -e "$path" ]]; then
    return 0
  fi
  mkdir -p "$(dirname "$path")"
  umask 077
  cat >"$path" <<EOF
control_url = "http://127.0.0.1:8080"
agent_token = "${token}"
data_dir = "${data_dir}"
image_path = "${data_dir}/images/ubuntu-2404-runner.ext4"
kernel_path = "${data_dir}/images/vmlinux"
vmm_backend = "firecracker"
min_ready = 1
max_ready = 2
job_simulate_seconds = 0
vcpu = 4
memory_mib = 6144
cache_listen_addr = "127.0.0.1:8743"
EOF
  chmod 0600 "$path"
}

temperci_wizard_url() {
  local ips="$1" ip
  while IFS= read -r ip; do
    [[ -z "$ip" || "$ip" == "127.0.0.1" ]] && continue
    printf 'http://%s:8080/\n' "$ip"
    return 0
  done <<<"$ips"
  printf 'http://127.0.0.1:8080/\n'
}

temperci_list_ipv4() {
  if command -v hostname >/dev/null 2>&1; then
    hostname -I 2>/dev/null | tr ' ' '\n' || true
  fi
  if command -v ip >/dev/null 2>&1; then
    ip -4 -o addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1 || true
  fi
}

if [[ "${TEMPERCI_INSTALL_LIB:-}" == 1 ]]; then
  return 0 2>/dev/null || exit 0
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || true)"
TOTAL=8

die() {
  echo "install.sh: $*" >&2
  exit 1
}

step_fail() {
  local n="$1" name="$2"
  shift 2
  temperci_step "$n" "$TOTAL" "$name" fail
  echo "$*" >&2
  exit 1
}

release_base() {
  if [[ -n "$TEMPERCI_RELEASE_URL" ]]; then
    echo "${TEMPERCI_RELEASE_URL%/}"
    return
  fi
  if [[ "$TEMPERCI_VERSION" == "latest" ]]; then
    echo "https://github.com/${TEMPERCI_REPO}/releases/latest/download"
  else
    echo "https://github.com/${TEMPERCI_REPO}/releases/download/${TEMPERCI_VERSION}"
  fi
}

repo_src_dir() {
  if [[ -n "${TEMPERCI_SRC_DIR:-}" && -d "$TEMPERCI_SRC_DIR" ]]; then
    echo "$TEMPERCI_SRC_DIR"
    return
  fi
  if [[ -n "${SCRIPT_DIR:-}" && -f "${SCRIPT_DIR}/build-guest-image.sh" ]]; then
    echo "$SCRIPT_DIR"
    return
  fi
  echo ""
}

ensure_src_tree() {
  local src
  src="$(repo_src_dir)"
  if [[ -n "$src" ]]; then
    echo "$src"
    return
  fi
  local dest share archive url
  dest="$(temperci_root /usr/local/lib/temperci/src)"
  if [[ -f "${dest}/deploy/ubuntu/build-guest-image.sh" ]]; then
    echo "${dest}/deploy/ubuntu"
    return
  fi
  mkdir -p "$dest"
  if [[ "$TEMPERCI_VERSION" == "latest" ]]; then
    url="https://github.com/${TEMPERCI_REPO}/archive/refs/heads/main.tar.gz"
  else
    url="https://github.com/${TEMPERCI_REPO}/archive/refs/tags/${TEMPERCI_VERSION}.tar.gz"
  fi
  archive="$(mktemp)"
  curl -fsSL "$url" -o "$archive"
  tar -xzf "$archive" -C "$dest" --strip-components=1
  rm -f "$archive"
  echo "${dest}/deploy/ubuntu"
}

install_firecracker() {
  local dest
  dest="$(temperci_root /usr/local/bin/firecracker)"
  if command -v firecracker >/dev/null 2>&1 && firecracker --version >/dev/null 2>&1; then
    return 1
  fi
  if [[ -x "$dest" ]] && "$dest" --version >/dev/null 2>&1; then
    return 1
  fi
  local arch tgz work bin
  arch="$(uname -m)"
  case "$arch" in
    x86_64 | amd64) arch="x86_64" ;;
    aarch64 | arm64) arch="aarch64" ;;
    *) die "unsupported arch ${arch}" ;;
  esac
  tgz="$(mktemp)"
  work="$(mktemp -d)"
  curl -fsSL -o "$tgz" \
    "https://github.com/firecracker-microvm/firecracker/releases/download/${FIRECRACKER_VERSION}/firecracker-${FIRECRACKER_VERSION}-${arch}.tgz"
  tar -xzf "$tgz" -C "$work"
  bin="$(find "$work" -type f -name "firecracker-${FIRECRACKER_VERSION}-${arch}" | head -1)"
  [[ -n "$bin" ]] || die "firecracker binary missing from archive"
  mkdir -p "$(dirname "$dest")"
  install -m 0755 "$bin" "$dest"
  rm -rf "$tgz" "$work"
  return 0
}

install_temperci_bins() {
  local dest_dir names n src dest
  dest_dir="$(temperci_root /usr/local/bin)"
  mkdir -p "$dest_dir"
  names=(temperci-control temperci-agent temperci-hostctl)
  if [[ -n "$TEMPERCI_BIN_DIR" ]]; then
    for n in "${names[@]}"; do
      src=""
      if [[ -f "${TEMPERCI_BIN_DIR}/${n}" ]]; then
        src="${TEMPERCI_BIN_DIR}/${n}"
      elif [[ -f "${TEMPERCI_BIN_DIR}/${n}-linux-amd64" ]]; then
        src="${TEMPERCI_BIN_DIR}/${n}-linux-amd64"
      else
        die "missing ${n} in TEMPERCI_BIN_DIR=${TEMPERCI_BIN_DIR}"
      fi
      install -m 0755 "$src" "${dest_dir}/${n}"
    done
    return 0
  fi
  local base
  base="$(release_base)"
  for n in "${names[@]}"; do
    dest="${dest_dir}/${n}"
    curl -fsSL "${base}/${n}-linux-amd64" -o "$dest"
    chmod 0755 "$dest"
  done
}

copy_support_files() {
  local ubuntu_src dest_lib dest_unit
  ubuntu_src="$(ensure_src_tree)"
  dest_lib="$(temperci_root /usr/local/lib/temperci)"
  dest_unit="$(temperci_root /etc/systemd/system)"
  mkdir -p "${dest_lib}/ubuntu" "$dest_unit"
  cp -a "${ubuntu_src}/." "${dest_lib}/ubuntu/"
  install -m 0755 "${ubuntu_src}/prepare-guest-image.sh" "${dest_lib}/prepare-guest-image.sh"
  local repo_root
  repo_root="$(cd "${ubuntu_src}/../.." && pwd)"
  if [[ -d "${repo_root}/deploy/systemd" ]]; then
    install -m 0644 "${repo_root}/deploy/systemd/temperci-control.service" "${dest_unit}/temperci-control.service"
    install -m 0644 "${repo_root}/deploy/systemd/temperci-agent.service" "${dest_unit}/temperci-agent.service"
    install -m 0644 "${repo_root}/deploy/systemd/temperci-guest-image.service" "${dest_unit}/temperci-guest-image.service"
  fi
}

wait_healthz() {
  local i
  for i in $(seq 1 20); do
    if curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

maybe_systemctl() {
  if [[ -n "${DESTDIR:-}" ]]; then
    return 0
  fi
  command -v systemctl >/dev/null 2>&1 || die "systemctl not found"
  "$@"
}

main() {
  local os_rel
  temperci_step 1 "$TOTAL" "Checking host" running
  [[ "$(uname -s)" == "Linux" ]] || step_fail 1 "Checking host" "Linux required (got $(uname -s))"
  [[ "$(id -u)" -eq 0 ]] || step_fail 1 "Checking host" "must run as root"
  [[ "$(uname -m)" == "x86_64" ]] || step_fail 1 "Checking host" "x86_64 required (got $(uname -m))"
  [[ -e /dev/kvm ]] || step_fail 1 "Checking host" "/dev/kvm missing — enable KVM"
  if [[ -f /etc/os-release ]]; then
    os_rel="$(cat /etc/os-release)"
  else
    os_rel=""
  fi
  temperci_ubuntu_supported "$os_rel" || step_fail 1 "Checking host" "Ubuntu 22.04 or 24.04 required"
  temperci_step 1 "$TOTAL" "Checking host" ok

  temperci_step 2 "$TOTAL" "Installing packages" running
  if [[ -n "${DESTDIR:-}" ]]; then
    temperci_step 2 "$TOTAL" "Installing packages" skip
  else
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y \
      qemu-kvm bridge-utils iproute2 e2fsprogs debootstrap iptables curl ca-certificates
    temperci_step 2 "$TOTAL" "Installing packages" ok
  fi

  temperci_step 3 "$TOTAL" "Installing Firecracker" running
  if [[ -n "${DESTDIR:-}" ]]; then
    temperci_step 3 "$TOTAL" "Installing Firecracker" skip
  elif install_firecracker; then
    temperci_step 3 "$TOTAL" "Installing Firecracker" ok
  else
    temperci_step 3 "$TOTAL" "Installing Firecracker" skip
  fi

  temperci_step 4 "$TOTAL" "Installing TemperCI binaries" running
  install_temperci_bins
  temperci_step 4 "$TOTAL" "Installing TemperCI binaries" ok

  temperci_step 5 "$TOTAL" "Writing config + systemd" running
  mkdir -p \
    "$(temperci_root /etc/temperci)" \
    "$(temperci_root /var/lib/temperci/images)" \
    "$(temperci_root /var/lib/temperci/instances)" \
    "$(temperci_root /var/log/temperci)"
  if ! getent passwd temperci >/dev/null 2>&1; then
    if [[ -z "${DESTDIR:-}" ]]; then
      useradd --system --home /var/lib/temperci --shell /usr/sbin/nologin temperci || true
      getent group kvm >/dev/null 2>&1 && usermod -aG kvm temperci || true
    fi
  fi
  local token
  token="$(openssl rand -hex 32 2>/dev/null || python3 -c 'import secrets; print(secrets.token_hex(32))')"
  temperci_write_control_toml "$(temperci_root /etc/temperci/control.toml)" "$token"
  temperci_write_agent_toml "$(temperci_root /etc/temperci/agent.toml)" "$token" "/var/lib/temperci"
  copy_support_files
  if [[ -z "${DESTDIR:-}" ]]; then
    chgrp temperci "$(temperci_root /etc/temperci)" "$(temperci_root /etc/temperci)"/*.toml 2>/dev/null || true
    chmod 0750 "$(temperci_root /etc/temperci)" || true
    chmod 0640 "$(temperci_root /etc/temperci)"/*.toml || true
    chown -R temperci:temperci "$(temperci_root /var/lib/temperci)" 2>/dev/null || true
    maybe_systemctl systemctl daemon-reload
  fi
  temperci_step 5 "$TOTAL" "Writing config + systemd" ok

  temperci_step 6 "$TOTAL" "Starting control plane" running
  if [[ -n "${DESTDIR:-}" ]]; then
    temperci_step 6 "$TOTAL" "Starting control plane" skip
  else
    maybe_systemctl systemctl enable --now temperci-control
    if ! wait_healthz; then
      journalctl -u temperci-control -n 50 --no-pager >&2 || true
      step_fail 6 "Starting control plane" "control did not become healthy on :8080"
    fi
    local url
    url="$(temperci_wizard_url "$(temperci_list_ipv4)")"
    echo "  Setup wizard: ${url}" >&2
    echo "  This host is reachable on the LAN with auth_mode=open until you finish the wizard." >&2
    temperci_step 6 "$TOTAL" "Starting control plane" ok
  fi

  temperci_step 7 "$TOTAL" "Starting host agent" running
  if [[ -n "${DESTDIR:-}" ]]; then
    temperci_step 7 "$TOTAL" "Starting host agent" skip
  else
    maybe_systemctl systemctl enable --now temperci-agent
    temperci_step 7 "$TOTAL" "Starting host agent" ok
  fi

  temperci_step 8 "$TOTAL" "Preparing guest image" running
  if [[ -n "${DESTDIR:-}" ]]; then
    temperci_step 8 "$TOTAL" "Preparing guest image" skip
  else
    maybe_systemctl systemctl enable temperci-guest-image
    maybe_systemctl systemctl start temperci-guest-image --no-block || maybe_systemctl systemctl start temperci-guest-image
    if [[ -t 1 ]]; then
      echo "  Building guest image (this can take several minutes)…" >&2
      echo "  journalctl -u temperci-guest-image -f" >&2
      local deadline now
      deadline=$((SECONDS + 2700))
      while ((SECONDS < deadline)); do
        if [[ -f /var/lib/temperci/images/.ready ]]; then
          temperci_step 8 "$TOTAL" "Preparing guest image" ok
          echo "  Open the setup wizard and finish GitHub App + auth." >&2
          return 0
        fi
        if systemctl is-failed temperci-guest-image >/dev/null 2>&1; then
          journalctl -u temperci-guest-image -n 80 --no-pager >&2 || true
          step_fail 8 "Preparing guest image" "guest image unit failed — control is still up"
        fi
        sleep 5
      done
      step_fail 8 "Preparing guest image" "timed out waiting for guest image (45m)"
    else
      echo "  Guest image is building in the background." >&2
      echo "  Follow: journalctl -u temperci-guest-image -f" >&2
      temperci_step 8 "$TOTAL" "Preparing guest image" running
    fi
  fi
}

main "$@"
