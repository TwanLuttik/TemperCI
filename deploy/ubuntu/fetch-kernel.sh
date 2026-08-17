#!/usr/bin/env bash
# Download a Firecracker-compatible uncompressed kernel (vmlinux).
#
# Usage:
#   sudo ./deploy/ubuntu/fetch-kernel.sh [dest-path]
#
# Env:
#   TEMPERCI_IMAGES_DIR   default /var/lib/temperci/images
#   TEMPERCI_KERNEL_URL   override download URL (skip pin + discovery)
#   TEMPERCI_KERNEL_ARCH  x86_64 or aarch64 (default: uname -m)
#
# Default pin: Firecracker CI v1.11 Linux 6.1.102 (getting-started compatible).
# See deploy/ubuntu/guest-image.md.
set -euo pipefail

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "fetch-kernel.sh is for Linux hosts (got $(uname -s))" >&2
  exit 1
fi

S3_BASE="https://s3.amazonaws.com/spec.ccfc.min"
# Pinned CI artifacts from https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.11/
PINNED_X86_64="${S3_BASE}/firecracker-ci/v1.11/x86_64/vmlinux-6.1.102"
PINNED_AARCH64="${S3_BASE}/firecracker-ci/v1.11/aarch64/vmlinux-6.1.102"

IMAGES_DIR="${TEMPERCI_IMAGES_DIR:-/var/lib/temperci/images}"
DEST="${1:-${IMAGES_DIR}/vmlinux}"
ARCH="${TEMPERCI_KERNEL_ARCH:-$(uname -m)}"

pinned_url() {
  case "$ARCH" in
    x86_64|amd64) echo "$PINNED_X86_64" ;;
    aarch64|arm64) echo "$PINNED_AARCH64" ;;
    *) return 1 ;;
  esac
}

# Latest dated firecracker-ci/YYYYMMDD-… artifact (same listing as getting-started.md).
discover_kernel_url() {
  local prefix key arch_key
  case "$ARCH" in
    x86_64|amd64) arch_key="x86_64" ;;
    aarch64|arm64) arch_key="aarch64" ;;
    *) return 1 ;;
  esac
  prefix="$(curl -fsSL "${S3_BASE}?list-type=2&prefix=firecracker-ci/&delimiter=/" \
    | grep -oE 'firecracker-ci/[0-9]{8}-[^/<]+/' \
    | sort \
    | tail -1 || true)"
  if [[ -z "$prefix" ]]; then
    return 1
  fi
  key="$(curl -fsSL "${S3_BASE}?list-type=2&prefix=${prefix}${arch_key}/vmlinux-" \
    | grep -oE "${prefix}${arch_key}/vmlinux-[0-9]+\.[0-9]+\.[0-9]+" \
    | grep -vE 'config|no-acpi' \
    | sort -V \
    | tail -1 || true)"
  if [[ -z "$key" ]]; then
    return 1
  fi
  echo "${S3_BASE}/${key}"
}

download_url() {
  local url="$1"
  local tmp="$2"
  echo "fetch-kernel: downloading ${url}"
  curl -fL --retry 3 --retry-delay 2 -o "$tmp" "$url"
}

mkdir -p "$(dirname "$DEST")"

tmp="$(mktemp "${DEST}.XXXXXX")"
cleanup() {
  rm -f "$tmp"
}
trap cleanup EXIT

ok=0
urls=()
if [[ -n "${TEMPERCI_KERNEL_URL:-}" ]]; then
  urls+=("${TEMPERCI_KERNEL_URL}")
else
  if pin="$(pinned_url)"; then
    urls+=("$pin")
  fi
  if disc="$(discover_kernel_url)"; then
    if [[ ${#urls[@]} -eq 0 || "$disc" != "${urls[0]}" ]]; then
      urls+=("$disc")
    fi
  fi
fi

if [[ ${#urls[@]} -eq 0 ]]; then
  echo "fetch-kernel: no kernel URL for arch ${ARCH}." >&2
  echo "Set TEMPERCI_KERNEL_URL to a Firecracker-compatible vmlinux and retry." >&2
  exit 1
fi

err=""
for url in "${urls[@]}"; do
  if download_url "$url" "$tmp"; then
    ok=1
    break
  fi
  err="download failed: ${url}"
  echo "fetch-kernel: ${err}" >&2
done

if [[ "$ok" -ne 1 ]]; then
  echo "fetch-kernel: could not download a Firecracker kernel." >&2
  echo "Tried:" >&2
  for url in "${urls[@]}"; do
    echo "  - ${url}" >&2
  done
  echo "Set TEMPERCI_KERNEL_URL to a working vmlinux URL, or copy a kernel to:" >&2
  echo "  ${DEST}" >&2
  echo "Docs: https://github.com/firecracker-microvm/firecracker/blob/main/docs/getting-started.md" >&2
  exit 1
fi

sz="$(stat -c%s "$tmp")"
if [[ "$sz" -lt 1000000 ]]; then
  echo "fetch-kernel: downloaded file is too small (${sz} bytes); not a kernel." >&2
  exit 1
fi

chmod 0644 "$tmp"
mv -f "$tmp" "$DEST"
trap - EXIT
echo "fetch-kernel: wrote ${DEST} (${sz} bytes)"
