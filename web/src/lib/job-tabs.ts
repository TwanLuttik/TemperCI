import type { Job, JobLogs } from "../api";

export function suggestJobTab(job: Pick<Job, "status">, logs: JobLogs): "events" | "agent" | "runner" | "console" {
  const st = String(job.status || "").toLowerCase();
  const wf = (logs.workflow_log || "").trim();
  const hasSteps = wf.includes("##[group]") || wf.includes("##[command]") || wf.includes("##[error]");
  const runner = logs.runner_log || "";
  const agent = (logs.agent_log || "").trim();
  if (st === "minted" || st === "assigned") {
    return agent ? "agent" : "events";
  }
  if (st === "started") {
    if (hasSteps || /Running job:/i.test(runner)) {
      return "runner";
    }
    if (agent) {
      return "agent";
    }
    return "events";
  }
  if (st === "finished" || st === "failed") {
    if (hasSteps) {
      return "runner";
    }
    return "events";
  }
  return "events";
}
