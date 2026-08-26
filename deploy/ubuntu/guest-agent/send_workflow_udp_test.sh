#!/usr/bin/env bash
# Prove send_workflow_udp emits one "wf <offset> <total> <b64>" line the host parser accepts.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
AGENT="${SCRIPT_DIR}/temperci-runner-agent.sh"
if [[ ! -f "${AGENT}" ]]; then
  echo "FAIL: missing ${AGENT}" >&2
  exit 1
fi

WORKDIR="$(mktemp -d /tmp/temperci-wf-udp.XXXXXX)"
cleanup() { rm -rf "${WORKDIR}"; }
trap cleanup EXIT

printf '##[group]Run x\nhello\n' >"${WORKDIR}/workflow.log"
SINK="${WORKDIR}/pkt"

# Extract the function and run it with a file sink (no /dev/udp).
{
  echo 'host_ip() { echo 127.0.0.1; }'
  echo "MAILBOX_PORT=9876"
  echo "WORKDIR='${WORKDIR}'"
  echo "WF_SENT=0"
  echo "TEMPERCI_MAILBOX_SINK='${SINK}'"
  awk '/^workflow_pending\(\)/,/^}/' "${AGENT}"
  awk '/^send_workflow_udp\(\)/,/^}/' "${AGENT}"
  echo 'send_workflow_udp'
} >"${WORKDIR}/run.sh"

bash "${WORKDIR}/run.sh"

if [[ ! -s "${SINK}" ]]; then
  echo "FAIL: no mailbox packet" >&2
  exit 1
fi

read -r tag off total b64 <"${SINK}"
if [[ "${tag}" != "wf" || "${off}" != "0" || -z "${b64}" ]]; then
  echo "FAIL: bad packet: $(cat "${SINK}")" >&2
  exit 1
fi
if [[ "${total}" != "$(wc -c <"${WORKDIR}/workflow.log" | tr -d ' ')" ]]; then
  echo "FAIL: total=${total}" >&2
  exit 1
fi

decoded=$(printf '%s' "${b64}" | base64 --decode 2>/dev/null || printf '%s' "${b64}" | base64 -D)
if [[ "${decoded}" != "$(cat "${WORKDIR}/workflow.log")" ]]; then
  echo "FAIL: decoded mismatch: ${decoded}" >&2
  exit 1
fi

echo "OK send_workflow_udp"
