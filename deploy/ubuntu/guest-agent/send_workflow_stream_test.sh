#!/usr/bin/env bash
# Prove send_workflow_stream emits "wf <offset> <n>\n" + raw bytes (TCP frame).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
AGENT="${SCRIPT_DIR}/temperci-runner-agent.sh"
WORKDIR="$(mktemp -d /tmp/temperci-wf-tcp.XXXXXX)"
cleanup() { rm -rf "${WORKDIR}"; }
trap cleanup EXIT

printf '##[group]Run x\nhello\n' >"${WORKDIR}/workflow.log"
SINK="${WORKDIR}/pkt"
{
  echo "WORKDIR='${WORKDIR}'"
  echo "WF_SENT=0"
  echo "TEMPERCI_MAILBOX_SINK='${SINK}'"
  awk '/^workflow_pending\(\)/,/^}/' "${AGENT}"
  awk '/^send_workflow_stream\(\)/,/^}/' "${AGENT}"
  echo 'send_workflow_stream'
} >"${WORKDIR}/run.sh"

bash "${WORKDIR}/run.sh"

if [[ ! -s "${SINK}" ]]; then
  echo "FAIL: no stream frame" >&2
  exit 1
fi
header=$(head -n 1 "${SINK}")
read -r tag off n <<<"${header}"
if [[ "${tag}" != "wf" || "${off}" != "0" || -z "${n}" ]]; then
  echo "FAIL: bad header: ${header}" >&2
  exit 1
fi
body=$(tail -c +$(( ${#header} + 2 )) "${SINK}")
# header + newline is the first line; remainder is raw payload
body=$(tail -n +2 "${SINK}")
if [[ "${body}" != "$(cat "${WORKDIR}/workflow.log")" ]]; then
  echo "FAIL: body mismatch: ${body}" >&2
  exit 1
fi
if [[ "${n}" != "$(wc -c <"${WORKDIR}/workflow.log" | tr -d ' ')" ]]; then
  echo "FAIL: n=${n}" >&2
  exit 1
fi
echo "OK send_workflow_stream"
