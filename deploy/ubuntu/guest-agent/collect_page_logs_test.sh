#!/usr/bin/env bash
# collect-page-logs.sh must keep official step stdout across page upload/delete.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORKDIR="$(mktemp -d /tmp/temperci-pages.XXXXXX)"
trap 'rm -rf "${WORKDIR}"' EXIT
PAGES="${WORKDIR}/pages"
SNAP="${WORKDIR}/snap"
mkdir -p "$PAGES" "$SNAP"

# Same format the official PagingLogger writes (ISO timestamp + message).
printf '%s\n' \
  "2026-08-25T03:00:53.0435802Z ##[group]Run actions/checkout@v5" \
  "2026-08-25T03:00:53.0437303Z ##[command]git checkout" \
  "2026-08-25T03:00:54.0000000Z Syncing repository" \
  >"${PAGES}/aaaa_bbbb_1.log"
printf '%s\n' \
  "2026-08-25T03:01:10.0000000Z ##[group]Run pnpm install --frozen-lockfile" \
  "2026-08-25T03:01:10.1000000Z Lockfile is up to date" \
  >"${PAGES}/aaaa_cccc_1.log"

bash "${SCRIPT_DIR}/collect-page-logs.sh" "$PAGES" "$SNAP" "${WORKDIR}/out.txt"
if ! grep -Fq '##[group]Run actions/checkout@v5' "${WORKDIR}/out.txt"; then
  echo "FAIL: missing checkout page" >&2
  cat "${WORKDIR}/out.txt" >&2
  exit 1
fi
if ! grep -Fq 'Lockfile is up to date' "${WORKDIR}/out.txt"; then
  echo "FAIL: missing install page" >&2
  exit 1
fi
# Checkout must come before install (first-line timestamp order), not GUID order.
chk=$(grep -n 'Run actions/checkout' "${WORKDIR}/out.txt" | head -1 | cut -d: -f1)
ins=$(grep -n 'Run pnpm install' "${WORKDIR}/out.txt" | head -1 | cut -d: -f1)
if [ "$chk" -ge "$ins" ]; then
  echo "FAIL: pages not ordered by time (checkout=$chk install=$ins)" >&2
  exit 1
fi

# Growing current page is picked up on the next pass.
printf '%s\n' "2026-08-25T03:01:11.0000000Z Done in 30.9s" >>"${PAGES}/aaaa_cccc_1.log"
bash "${SCRIPT_DIR}/collect-page-logs.sh" "$PAGES" "$SNAP" "${WORKDIR}/out.txt"
if ! grep -Fq 'Done in 30.9s' "${WORKDIR}/out.txt"; then
  echo "FAIL: did not pick up grown page" >&2
  exit 1
fi

# Runner deletes pages after upload; snapshot must keep finished step output.
rm -f "${PAGES}/aaaa_bbbb_1.log" "${PAGES}/aaaa_cccc_1.log"
printf '%s\n' \
  "2026-08-25T03:02:00.0000000Z ##[group]Run pnpm build" \
  "2026-08-25T03:02:00.1000000Z Compiled successfully" \
  >"${PAGES}/aaaa_dddd_1.log"
bash "${SCRIPT_DIR}/collect-page-logs.sh" "$PAGES" "$SNAP" "${WORKDIR}/out.txt"
if ! grep -Fq 'Syncing repository' "${WORKDIR}/out.txt"; then
  echo "FAIL: lost finished checkout after page delete" >&2
  cat "${WORKDIR}/out.txt" >&2
  exit 1
fi
if ! grep -Fq 'Compiled successfully' "${WORKDIR}/out.txt"; then
  echo "FAIL: missing current step page" >&2
  exit 1
fi

# No pages and no snapshot → fail so the agent can fall back to Worker extract.
rm -rf "${WORKDIR}/empty" && mkdir -p "${WORKDIR}/empty" "${WORKDIR}/empty-snap"
if bash "${SCRIPT_DIR}/collect-page-logs.sh" "${WORKDIR}/empty" "${WORKDIR}/empty-snap" "${WORKDIR}/empty.out"; then
  echo "FAIL: expected failure when no page files exist" >&2
  exit 1
fi

echo "collect_page_logs_test: OK"
