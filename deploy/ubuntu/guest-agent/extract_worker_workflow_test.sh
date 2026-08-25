#!/usr/bin/env bash
# extract-worker-workflow.sh must turn Worker _diag into ##[group] step text.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORKDIR="$(mktemp -d /tmp/temperci-wextract.XXXXXX)"
trap 'rm -rf "${WORKDIR}"' EXIT

cat >"${WORKDIR}/Worker_utc.log" <<'EOF'
[2026-08-25 02:52:32Z INFO HostContext] Runner is ready
[2026-08-25 02:52:40Z INFO StepsRunner] Processing step: DisplayName='Checkout code'
[2026-08-25 02:52:40Z INFO StepsRunner] Starting step.
[2026-08-25 02:52:41Z INFO ExecutionContext] ##[group]Run actions/checkout@v5
[2026-08-25 02:52:41Z INFO ExecutionContext] ##[command]git checkout
[2026-08-25 02:52:42Z INFO ExecutionContext] ##[group]Fetching the repository
[2026-08-25 02:52:42Z INFO ExecutionContext] From github.com
[2026-08-25 02:52:42Z INFO ExecutionContext] ##[endgroup]
[2026-08-25 02:52:42Z INFO ExecutionContext] Syncing repository
[2026-08-25 02:52:50Z INFO StepsRunner] Processing step: DisplayName='Install dependencies'
[2026-08-25 02:52:51Z INFO ExecutionContext] ##[group]Run pnpm install --frozen-lockfile
[2026-08-25 02:52:51Z INFO ExecutionContext] Lockfile is up to date
EOF

got="$(bash "${SCRIPT_DIR}/extract-worker-workflow.sh" "${WORKDIR}/Worker_utc.log")"
printf '%s\n' "$got" >"${WORKDIR}/out.txt"

need=(
  "##[group]Run Checkout code"
  "##[command]git checkout"
  "From github.com"
  "Syncing repository"
  "##[group]Run Install dependencies"
  "Lockfile is up to date"
)
for s in "${need[@]}"; do
  if ! grep -Fq "$s" "${WORKDIR}/out.txt"; then
    echo "FAIL: missing ${s}" >&2
    cat "${WORKDIR}/out.txt" >&2
    exit 1
  fi
done
if grep -q 'HostContext' "${WORKDIR}/out.txt"; then
  echo "FAIL: raw diag leaked" >&2
  exit 1
fi
# Nested checkout internals must not become their own step group.
if grep -Fq '##[group]Fetching the repository' "${WORKDIR}/out.txt"; then
  echo "FAIL: nested group leaked as a step" >&2
  cat "${WORKDIR}/out.txt" >&2
  exit 1
fi
# Closed steps emit endgroup so the UI can split siblings.
if ! grep -Fq '##[endgroup]' "${WORKDIR}/out.txt"; then
  echo "FAIL: expected endgroup after the first step" >&2
  cat "${WORKDIR}/out.txt" >&2
  exit 1
fi
# Live last step must stay open (no trailing endgroup) so the UI can keep tailing.
if tail -n 1 "${WORKDIR}/out.txt" | grep -q '##\[endgroup\]'; then
  echo "FAIL: last step should stay open for live tail" >&2
  exit 1
fi

# Official ##[group]Run lines must split even when Processing step is missing
# and nested groups have no endgroup (the live Worker log we actually see).
cat >"${WORKDIR}/Worker_groups_only.log" <<'EOF'
[2026-08-25 03:00:53Z INFO ExecutionContext] ##[group]Run actions/checkout@v5
[2026-08-25 03:00:53Z INFO ExecutionContext] ##[command]git checkout
[2026-08-25 03:00:54Z INFO ExecutionContext] ##[group]Fetching the repository
[2026-08-25 03:00:54Z INFO ExecutionContext] From github.com
[2026-08-25 03:00:55Z INFO ExecutionContext] ##[group]Run pnpm install --frozen-lockfile
[2026-08-25 03:00:55Z INFO ExecutionContext] Lockfile is up to date
EOF
got2="$(bash "${SCRIPT_DIR}/extract-worker-workflow.sh" "${WORKDIR}/Worker_groups_only.log")"
printf '%s\n' "$got2" >"${WORKDIR}/out2.txt"
need2=(
  "##[group]Run actions/checkout@v5"
  "##[command]git checkout"
  "From github.com"
  "##[endgroup]"
  "##[group]Run pnpm install --frozen-lockfile"
  "Lockfile is up to date"
)
for s in "${need2[@]}"; do
  if ! grep -Fq "$s" "${WORKDIR}/out2.txt"; then
    echo "FAIL groups-only: missing ${s}" >&2
    cat "${WORKDIR}/out2.txt" >&2
    exit 1
  fi
done
if grep -Fq '##[group]Fetching the repository' "${WORKDIR}/out2.txt"; then
  echo "FAIL groups-only: nested group leaked as a step" >&2
  cat "${WORKDIR}/out2.txt" >&2
  exit 1
fi
echo "extract_worker_workflow_test: OK"
