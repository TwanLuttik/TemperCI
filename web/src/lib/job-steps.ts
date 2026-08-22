import type { JobStep } from "../api";

export type JobStepProgress = {
  total: number;
  done: number;
  current?: JobStep;
  index: number;
};

function norm(s?: string): string {
  return String(s || "").toLowerCase();
}

/** Close leftover in_progress steps once the TemperCI job itself is done. */
export function settleSteps(
  steps: JobStep[] | undefined,
  job: { status?: string; outcome?: string; finished_at?: string },
): JobStep[] {
  const list = steps || [];
  const st = norm(job.status);
  if (st !== "finished" && st !== "failed") return list;
  const conclusion =
    st === "failed" || ["failure", "cancelled", "timeout", "error"].includes(norm(job.outcome))
      ? job.outcome || "failure"
      : "success";
  return list.map((s) => {
    if (norm(s.status) !== "in_progress") return s;
    return {
      ...s,
      status: "completed",
      conclusion: s.conclusion || conclusion,
      completed_at: s.completed_at || job.finished_at,
    };
  });
}

export function jobStepProgress(steps?: JobStep[] | null): JobStepProgress {
  const list = steps || [];
  const total = list.length;
  const done = list.filter((s) => norm(s.status) === "completed").length;
  const running = list.find((s) => norm(s.status) === "in_progress");
  const failed = list.find(
    (s) => norm(s.status) === "completed" && ["failure", "cancelled"].includes(norm(s.conclusion)),
  );
  const current = running || (done < total ? list[done] : failed);
  const index = current?.number || (total > 0 && done === total ? total : done);
  return { total, done, current, index };
}

export function lastWorkflowGroup(log?: string): string | undefined {
  if (!log) return undefined;
  let last: string | undefined;
  const re = /##\[group\]([^\n]+)/g;
  for (let m = re.exec(log); m; m = re.exec(log)) {
    const name = m[1].trim();
    if (name) last = name;
  }
  return last;
}

export function parseStepTime(iso?: string): number | undefined {
  if (!iso) return undefined;
  const t = Date.parse(iso);
  return Number.isNaN(t) ? undefined : t;
}

/** Elapsed time for a step. Live steps use `now`; completed steps use GitHub timestamps. */
export function stepElapsedMs(step: JobStep, now: number, fallbackStart?: number): number | undefined {
  const start = parseStepTime(step.started_at) ?? fallbackStart;
  if (start == null) return undefined;
  const status = norm(step.status);
  if (status === "completed") {
    const end = parseStepTime(step.completed_at) ?? now;
    return Math.max(0, end - start);
  }
  if (status === "in_progress") {
    return Math.max(0, now - start);
  }
  return undefined;
}

/** Whole-second clock, e.g. 12s, 1m 04s, 1h 02m 03s. */
export function formatStepClock(ms?: number): string {
  if (ms == null || ms < 0) return "—";
  const total = Math.floor(ms / 1000);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const ss = s.toString().padStart(2, "0");
  if (h > 0) return `${h}h ${m}m ${ss}s`;
  if (m > 0) return `${m}m ${ss}s`;
  return `${total}s`;
}
