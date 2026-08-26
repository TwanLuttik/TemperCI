import { applyJobLogsFrame, applyWorkflowDelta, mergeJobLogs } from "./job-log-live";

if (applyWorkflowDelta("ab", 2, "c") !== "abc") throw new Error("append at end");
if (applyWorkflowDelta("ab", 4, "c") !== "ab") throw new Error("gap must keep cur");
if (applyWorkflowDelta("abcd", 2, "CDEF") !== "abCDEF") throw new Error("overlap extend");

const deltaMap = applyJobLogsFrame({}, 7, { workflow_offset: 0, workflow_append: "##[group]a\n" });
const deltaMap2 = applyJobLogsFrame(deltaMap, 7, { workflow_offset: "##[group]a\n".length, workflow_append: "b\n" });
if (deltaMap2["7"]?.workflow_log !== "##[group]a\nb\n") throw new Error(`delta map = ${deltaMap2["7"]?.workflow_log}`);


const a = mergeJobLogs({ workflow_log: "##[group]a\n" }, { workflow_log: "##[group]a\nb\n" });
if (a.workflow_log !== "##[group]a\nb\n") throw new Error(`merge grow = ${a.workflow_log}`);

const b = mergeJobLogs({ workflow_log: "##[group]a\nb\n" }, { workflow_log: "##[group]a\n" });
if (b.workflow_log !== "##[group]a\nb\n") throw new Error("stale shorter REST must not win");

const c = mergeJobLogs({ runner_log: "old", workflow_log: "w" }, { agent_log: "ag" });
if (c.runner_log !== "old" || c.agent_log !== "ag" || c.workflow_log !== "w") {
  throw new Error(`partial merge = ${JSON.stringify(c)}`);
}

const map = applyJobLogsFrame({}, 99, { workflow_log: "one" });
const map2 = applyJobLogsFrame(map, 99, { workflow_log: "one\ntwo" });
if (map2["99"]?.workflow_log !== "one\ntwo") throw new Error("frame map");
if (map["99"]?.workflow_log !== "one") throw new Error("previous map must stay");
