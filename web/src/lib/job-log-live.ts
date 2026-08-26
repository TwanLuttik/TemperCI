import type { JobLogs } from "../api";

const logKeys = ["workflow_log", "runner_log", "agent_log", "console_log"] as const;

export type JobLogFrame = JobLogs & {
  workflow_offset?: number;
  workflow_append?: string;
};

/** Apply an append at offset. Gaps and older duplicates leave cur unchanged. */
export function applyWorkflowDelta(cur: string, offset: number, data: string): string {
  if (!data) return cur;
  if (offset === cur.length) return cur + data;
  if (offset < cur.length && offset + data.length > cur.length) {
    return cur.slice(0, offset) + data;
  }
  return cur;
}

/** Prefer the longer body for each stream so a stale REST poll cannot clobber WS. */
export function mergeJobLogs(cur: JobLogs, incoming: JobLogs): JobLogs {
  const next: JobLogs = { ...cur };
  for (const k of logKeys) {
    const a = cur[k] || "";
    const b = incoming[k] || "";
    if (b.length >= a.length) {
      next[k] = b;
    }
  }
  if (incoming.events && incoming.events.length >= (cur.events?.length || 0)) {
    next.events = incoming.events;
  }
  if (incoming.job_id) next.job_id = incoming.job_id;
  if (incoming.updated_at) next.updated_at = incoming.updated_at;
  return next;
}

export function applyJobLogsFrame(
  byJob: Record<string, JobLogs>,
  jobID: number | string,
  incoming: JobLogFrame,
): Record<string, JobLogs> {
  const key = String(jobID);
  const cur = byJob[key] || {};
  let frame: JobLogs = incoming;
  if (incoming.workflow_append) {
    const wf = applyWorkflowDelta(cur.workflow_log || "", incoming.workflow_offset ?? 0, incoming.workflow_append);
    frame = { ...incoming, workflow_log: wf };
  }
  return { ...byJob, [key]: mergeJobLogs(cur, frame) };
}
