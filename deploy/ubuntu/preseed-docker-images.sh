#!/usr/bin/env bash
# Load operator-supplied Docker image refs into a mounted guest rootfs.
# Usage:
#   sudo ./preseed-docker-images.sh /path/to/mounted-rootfs [image ...]
#   TEMPERCI_PRESEED_IMAGES=$'img:1\nimg:2' sudo ./preseed-docker-images.sh /mnt
#   TEMPERCI_PRESEED_IMAGES_FILE=images.txt sudo ./preseed-docker-images.sh /mnt
#
# Tests set TEMPERCI_PRESEED_LOADER to a stub docker. Live bake starts the
# guest dockerd in the chroot and docker-pulls each ref into /var/lib/docker.
set -euo pipefail

ROOT="${1:-}"
if [[ -z "$ROOT" || "$ROOT" == -* ]]; then
  echo "usage: $0 /path/to/mounted-rootfs [image ...]" >&2
  exit 1
fi
shift
if [[ ! -d "$ROOT" ]]; then
  echo "preseed-docker-images: not a directory: $ROOT" >&2
  exit 1
fi

collect_images() {
  local img file
  IMAGES=()
  if [[ "$#" -gt 0 ]]; then
    IMAGES=("$@")
    return
  fi
  if [[ -n "${TEMPERCI_PRESEED_IMAGES:-}" ]]; then
    while IFS= read -r img; do
      [[ -z "$img" || "$img" == \#* ]] && continue
      IMAGES+=("$img")
    done <<<"${TEMPERCI_PRESEED_IMAGES}"
    return
  fi
  file="${TEMPERCI_PRESEED_IMAGES_FILE:-}"
  if [[ -n "$file" && -f "$file" ]]; then
    while IFS= read -r img; do
      [[ -z "$img" || "$img" == \#* ]] && continue
      IMAGES+=("$img")
    done <"$file"
  fi
}

collect_images "$@"

if [[ "${#IMAGES[@]}" -eq 0 ]]; then
  echo "preseed-docker-images: no images configured (pass refs, TEMPERCI_PRESEED_IMAGES, or TEMPERCI_PRESEED_IMAGES_FILE)"
  exit 0
fi

docker_cmd() {
  if [[ -n "${TEMPERCI_PRESEED_LOADER:-}" ]]; then
    "${TEMPERCI_PRESEED_LOADER}" "$@"
    return
  fi
  chroot "$ROOT" /usr/bin/docker "$@"
}

write_list() {
  mkdir -p "$ROOT/etc/temperci"
  printf '%s\n' "${IMAGES[@]}" >"$ROOT/etc/temperci/preseeded-images.txt"
}

if [[ -n "${TEMPERCI_PRESEED_LOADER:-}" ]]; then
  for img in "${IMAGES[@]}"; do
    docker_cmd pull "$img"
  done
  write_list
  echo "preseed-docker-images: stub-loaded ${#IMAGES[@]} images into $ROOT"
  exit 0
fi

if [[ "${EUID}" -ne 0 ]]; then
  echo "preseed-docker-images: live bake requires root" >&2
  exit 1
fi
if [[ ! -x "$ROOT/usr/bin/dockerd" || ! -x "$ROOT/usr/bin/docker" ]]; then
  echo "preseed-docker-images: guest docker missing under $ROOT" >&2
  exit 1
fi

cleanup() {
  if [[ -n "${DOCKERD_PID:-}" ]]; then
    kill "$DOCKERD_PID" 2>/dev/null || true
    wait "$DOCKERD_PID" 2>/dev/null || true
    DOCKERD_PID=""
  fi
  if [[ -n "${DID_MOUNTS:-}" ]]; then
    umount -l "$ROOT/dev/pts" 2>/dev/null || true
    umount -l "$ROOT/dev" 2>/dev/null || true
    umount -l "$ROOT/sys" 2>/dev/null || true
    umount -l "$ROOT/proc" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

mkdir -p "$ROOT/proc" "$ROOT/sys" "$ROOT/dev" "$ROOT/dev/pts" "$ROOT/var/run" "$ROOT/var/lib/docker" "$ROOT/tmp"
if ! mountpoint -q "$ROOT/proc"; then
  mount -t proc proc "$ROOT/proc"
  mount -t sysfs sysfs "$ROOT/sys"
  mount --bind /dev "$ROOT/dev"
  mount -t devpts devpts "$ROOT/dev/pts" 2>/dev/null || true
  DID_MOUNTS=1
fi
if [[ ! -s "$ROOT/etc/resolv.conf" ]]; then
  printf 'nameserver 8.8.8.8\nnameserver 1.1.1.1\n' >"$ROOT/etc/resolv.conf"
fi

# Dedicated daemon.json so flags do not collide with the guest's /etc/docker/daemon.json.
cat >"$ROOT/tmp/temperci-preseed-daemon.json" <<'EOF'
{
  "storage-driver": "overlay2",
  "iptables": false,
  "ip6tables": false,
  "live-restore": false
}
EOF

chroot "$ROOT" env DOCKER_INSECURE_NO_IPTABLES_RAW=1 /usr/bin/dockerd \
  --config-file=/tmp/temperci-preseed-daemon.json \
  --host=unix:///var/run/docker.sock \
  --pidfile=/var/run/docker.pid \
  --bridge=none \
  >/tmp/temperci-preseed-dockerd.log 2>&1 &
DOCKERD_PID=$!

ok=0
for _ in $(seq 1 40); do
  if docker_cmd info >/dev/null 2>&1; then
    ok=1
    break
  fi
  if ! kill -0 "$DOCKERD_PID" 2>/dev/null; then
    echo "preseed-docker-images: dockerd exited; log:" >&2
    tail -c 2000 /tmp/temperci-preseed-dockerd.log >&2 || true
    exit 1
  fi
  sleep 0.5
done
if [[ "$ok" -ne 1 ]]; then
  echo "preseed-docker-images: dockerd did not become ready" >&2
  tail -c 2000 /tmp/temperci-preseed-dockerd.log >&2 || true
  exit 1
fi

for img in "${IMAGES[@]}"; do
  echo "preseed-docker-images: pulling $img"
  docker_cmd pull "$img"
done
write_list
rm -f "$ROOT/tmp/temperci-preseed-daemon.json"
echo "preseed-docker-images: loaded ${#IMAGES[@]} images into $ROOT/var/lib/docker"
