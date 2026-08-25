#!/usr/bin/env bash
# Turn an official actions/runner Worker _diag log into ##[group] step text
# so TemperCI can stream live step output before GitHub publishes job logs.
#
# Usage: extract-worker-workflow.sh [Worker_*.log] > workflow.log
set -euo pipefail
src="${1:-/dev/stdin}"
awk '
  function emit_end() {
    if (in_group) {
      print "##[endgroup]"
      in_group = 0
      depth = 0
    }
  }
  function emit_group(title) {
    if (title == last_title) return
    emit_end()
    print "##[group]" title
    in_group = 1
    last_title = title
    official = 0
    depth = 1
  }
  function is_step_title(t) {
    return t ~ /^(Run|Post) /
  }
  {
    line = $0
    if (match(line, /Processing step: DisplayName=['\''\"]/)) {
      rest = substr(line, RSTART + RLENGTH)
      sub(/['\''\"].*$/, "", rest)
      if (rest != "") emit_group("Run " rest)
      next
    }
    ctx = index(line, " ExecutionContext] ")
    if (ctx > 0) {
      msg = substr(line, ctx + length(" ExecutionContext] "))
      if (msg ~ /^##\[group\]/) {
        title = msg
        sub(/^##\[group\]/, "", title)
        if (is_step_title(title)) {
          # Processing-step group: first Run/Post is the same step. Later ones are new.
          if (!in_group || official) emit_group(title)
          official = 1
        } else if (in_group) {
          depth++
        }
        next
      }
      if (msg ~ /^##\[endgroup\]/) {
        if (in_group) {
          depth--
          if (depth <= 0) emit_end()
        }
        next
      }
      if (msg ~ /Publish step telemetry/) next
      if (msg ~ /Write event payload/) next
      if (msg ~ /Reserve record order/) next
      if (msg ~ /Try to append .* web console/) next
      if (!in_group) emit_group("Run (live)")
      print msg
      next
    }
  }
' "$src"
