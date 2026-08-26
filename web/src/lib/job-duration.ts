import { jobIsActive, type Job } from "../api";
import { parseStepTime } from "./job-steps";

export type LiveJobTimings = {
  queue?: number;
  bind?: number;
  run?: number;
  total?: number;
};

function elapsed(start?: number, end?: number): number | undefined {
  if (start == null || end == null) return undefined;
  return Math.max(0, end - start);
}

/** Queue / bind / run / total, ticking with `now` while a phase is open. */
export function liveJobTimings(job: Job | undefined, now: number): LiveJobTimings {
  if (!job) return {};
  const created = parseStepTime(job.created_at);
  const assigned = parseStepTime(job.assigned_at);
  const started = parseStepTime(job.started_at);
  const finished = parseStepTime(job.finished_at);
  return {
    queue: elapsed(created, assigned ?? now) ?? job.queue_ms,
    bind: elapsed(assigned, started ?? now) ?? job.bind_ms,
    run: elapsed(started, finished ?? now) ?? job.run_ms,
    total: elapsed(created, finished ?? now) ?? job.total_ms,
  };
}

/** Primary clock shown in job lists: run if started, otherwise total. */
export function liveJobClockMs(job: Job | undefined, now: number): number | undefined {
  if (!job) return undefined;
  const t = liveJobTimings(job, now);
  if (t.run != null && (parseStepTime(job.started_at) != null || (job.run_ms ?? 0) > 0)) {
    return t.run;
  }
  return t.total ?? t.run ?? job.run_ms ?? job.total_ms;
}

export function jobsNeedLiveClock(jobs: Array<Pick<Job, "status">>): boolean {
  return jobs.some((j) => jobIsActive(j.status));
}

/** Prefer incoming fields, but keep previously fetched steps/timestamps if a snapshot omitted them. */
export function mergeJobRow(old: Job, incoming: Job): Job {
  const oldDone = ["finished", "failed"].includes(String(old.status || "").toLowerCase());
  const incDone = ["finished", "failed"].includes(String(incoming.status || "").toLowerCase());
  return {
    ...old,
    ...incoming,
    status: oldDone && !incDone ? old.status : incoming.status,
    outcome: incoming.outcome || old.outcome,
    steps: incoming.steps && incoming.steps.length > 0 ? incoming.steps : old.steps,
    assigned_at: incoming.assigned_at || old.assigned_at,
    started_at: incoming.started_at || old.started_at,
    finished_at: incoming.finished_at || old.finished_at,
  };
}

export function mergeJobSnapshots(incoming: Job[], prev: Job[]): Job[] {
  const prevById = new Map(prev.map((j) => [j.job_id, j]));
  return incoming.map((j) => {
    const old = prevById.get(j.job_id);
    if (!old) return j;
    return mergeJobRow(old, j);
  });
}

/** How often JobDetail should REST-poll. null = WebSocket owns updates. */
export function jobDetailPollMs(wsLive: boolean, jobDone: boolean): number | null {
  if (jobDone || wsLive) return null;
  return 2000;
}
