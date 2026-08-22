import type { Job } from "../api";

export function workflowKey(j: Job): string {
  return (j.workflow_name || j.name || "other").trim() || "other";
}

export function finishedRunMS(j: Job): number | undefined {
  const st = String(j.status || "").toLowerCase();
  if (st !== "finished" && st !== "failed") return undefined;
  if (j.run_ms != null && j.run_ms >= 0) return j.run_ms;
  if (j.total_ms != null && j.total_ms >= 0) return j.total_ms;
  return undefined;
}

export type SeriesPoint = { job: Job; ms: number; t: number; queue: number };

export type WorkflowStats = {
  key: string;
  n: number;
  ok: number;
  fail: number;
  sum: number;
  max: number;
  p50: number;
  p95: number;
  last: number;
  queueAvg: number;
  points: SeriesPoint[];
};

export function jobTime(j: Job): number {
  return Date.parse(j.finished_at || j.started_at || j.created_at || "") || 0;
}

export function percentile(values: number[], p: number): number {
  if (values.length === 0) return 0;
  const s = [...values].sort((a, b) => a - b);
  const i = Math.min(s.length - 1, Math.max(0, Math.ceil(p * s.length) - 1));
  return s[i];
}

export function workflowStats(jobs: Job[]): WorkflowStats[] {
  const map = new Map<string, Job[]>();
  for (const j of jobs) {
    if (finishedRunMS(j) == null) continue;
    const k = workflowKey(j);
    const list = map.get(k) || [];
    list.push(j);
    map.set(k, list);
  }
  return [...map.entries()]
    .map(([key, list]) => {
      const points: SeriesPoint[] = list
        .map((job) => ({
          job,
          ms: finishedRunMS(job) || 0,
          t: jobTime(job),
          queue: job.queue_ms || 0,
        }))
        .sort((a, b) => a.t - b.t);
      const values = points.map((p) => p.ms);
      const ok = list.filter((j) => String(j.outcome || "").toLowerCase() === "success").length;
      const fail = list.length - ok;
      const sum = values.reduce((a, b) => a + b, 0);
      const queueAvg = points.reduce((a, p) => a + p.queue, 0) / Math.max(1, points.length);
      return {
        key,
        n: list.length,
        ok,
        fail,
        sum,
        max: Math.max(...values, 0),
        p50: percentile(values, 0.5),
        p95: percentile(values, 0.95),
        last: values[values.length - 1] || 0,
        queueAvg,
        points,
      };
    })
    .sort((a, b) => b.n - a.n || a.key.localeCompare(b.key));
}
