#!/usr/bin/env bash
# preseed-docker-images.sh must pull only the refs it is given.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORKDIR="$(mktemp -d /tmp/temperci-preseed.XXXXXX)"
trap 'rm -rf "${WORKDIR}"' EXIT

ROOT="${WORKDIR}/rootfs"
mkdir -p "${ROOT}/usr/bin" "${ROOT}/var/lib/docker"

cat >"${WORKDIR}/loader.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "info" ]]; then
  exit 0
fi
if [[ "${1:-}" == "pull" && -n "${2:-}" ]]; then
  echo "$2" >>"${TEMPERCI_PRESEED_LOG}"
  exit 0
fi
echo "stub docker: unexpected $*" >&2
exit 1
EOF
chmod +x "${WORKDIR}/loader.sh"

export TEMPERCI_PRESEED_LOADER="${WORKDIR}/loader.sh"
export TEMPERCI_PRESEED_LOG="${WORKDIR}/pulled.txt"
: >"${TEMPERCI_PRESEED_LOG}"

# No images configured → no-op, no list file.
bash "${SCRIPT_DIR}/preseed-docker-images.sh" "${ROOT}"
if [[ -s "${TEMPERCI_PRESEED_LOG}" ]]; then
  echo "FAIL: pulled images with empty list:" >&2
  cat "${TEMPERCI_PRESEED_LOG}" >&2
  exit 1
fi
if [[ -f "${ROOT}/etc/temperci/preseeded-images.txt" ]]; then
  echo "FAIL: wrote list file with empty image list" >&2
  exit 1
fi

: >"${TEMPERCI_PRESEED_LOG}"
want=(example.com/db:1 example.com/cache:2)
bash "${SCRIPT_DIR}/preseed-docker-images.sh" "${ROOT}" "${want[@]}"

for img in "${want[@]}"; do
  if ! grep -Fxq "${img}" "${TEMPERCI_PRESEED_LOG}"; then
    echo "FAIL: loader did not pull ${img}" >&2
    cat "${TEMPERCI_PRESEED_LOG}" >&2
    exit 1
  fi
done
if [[ "$(wc -l <"${TEMPERCI_PRESEED_LOG}")" -ne "${#want[@]}" ]]; then
  echo "FAIL: unexpected extra pulls:" >&2
  cat "${TEMPERCI_PRESEED_LOG}" >&2
  exit 1
fi

list="${ROOT}/etc/temperci/preseeded-images.txt"
if [[ ! -f "${list}" ]]; then
  echo "FAIL: missing ${list}" >&2
  exit 1
fi
for img in "${want[@]}"; do
  if ! grep -Fxq "${img}" "${list}"; then
    echo "FAIL: ${list} missing ${img}" >&2
    cat "${list}" >&2
    exit 1
  fi
done

# Env list is used when no CLI images are given.
: >"${TEMPERCI_PRESEED_LOG}"
export TEMPERCI_PRESEED_IMAGES="env.example/one:a"$'\n'"env.example/two:b"
bash "${SCRIPT_DIR}/preseed-docker-images.sh" "${ROOT}"
if ! grep -Fxq "env.example/one:a" "${TEMPERCI_PRESEED_LOG}" || \
   ! grep -Fxq "env.example/two:b" "${TEMPERCI_PRESEED_LOG}"; then
  echo "FAIL: TEMPERCI_PRESEED_IMAGES not used:" >&2
  cat "${TEMPERCI_PRESEED_LOG}" >&2
  exit 1
fi

echo "preseed_docker_images_test: OK"
