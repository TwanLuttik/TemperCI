#!/usr/bin/env bash
# Snapshot official actions/runner page logs and emit GitHub-style workflow.log.
#
# The runner writes the same step stdout GitHub shows to
#   _diag/pages/{timelineId}_{recordId}_{page}.log
# and deletes each file after upload. We keep a growing snapshot so finished
# steps stay visible while the current page is still open.
#
# Usage: collect-page-logs.sh <pages-src-dir> <snapshot-dir> [workflow.log]
set -euo pipefail
src="${1:?usage: $0 <pages-src-dir> <snapshot-dir> [workflow.log]}"
snap="${2:?}"
out="${3:-/dev/stdout}"
mkdir -p "$snap"

copy_grown() {
  local f="$1" dest="$2" src_sz dest_sz
  src_sz=$(wc -c <"$f" 2>/dev/null | tr -d ' ' || echo 0)
  [ "${src_sz:-0}" -gt 0 ] || return 0
  dest_sz=0
  if [ -f "$dest" ]; then
    dest_sz=$(wc -c <"$dest" 2>/dev/null | tr -d ' ' || echo 0)
  fi
  if [ ! -f "$dest" ] || [ "$src_sz" -gt "$dest_sz" ]; then
    cp -f "$f" "$dest" 2>/dev/null || true
  fi
}

if [ -d "$src" ]; then
  for f in "$src"/*.log; do
    [ -f "$f" ] || continue
    copy_grown "$f" "$snap/$(basename "$f")"
  done
fi

# Order: first-line timestamp, then page number (filename ends with _N.log).
index="$(mktemp "${snap}/.index.XXXXXX")"
trap 'rm -f "$index"' EXIT
found=0
for f in "$snap"/*.log; do
  [ -f "$f" ] || continue
  found=1
  first=$(head -n 1 "$f" 2>/dev/null | tr -d '\r' || true)
  first="${first#$'\xEF\xBB\xBF'}"
  ts="${first%% *}"
  case "$ts" in
    [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*) ;;
    *) ts="0000-00-00T00:00:00Z" ;;
  esac
  base=$(basename "$f")
  page="${base##*_}"
  page="${page%.log}"
  case "$page" in
    ''|*[!0-9]*) page=1 ;;
  esac
  printf '%s\t%09d\t%s\n' "$ts" "$page" "$f"
done >"$index"

if [ "$found" -eq 0 ]; then
  exit 1
fi

tmp="$(mktemp "${snap}/.out.XXXXXX")"
sort "$index" | while IFS= read -r line; do
  f="${line##*$'\t'}"
  [ -f "$f" ] || continue
  cat "$f"
done >"$tmp"

if [ ! -s "$tmp" ]; then
  rm -f "$tmp"
  exit 1
fi
if [ "$out" = "/dev/stdout" ]; then
  cat "$tmp"
  rm -f "$tmp"
else
  mv -f "$tmp" "$out"
fi
