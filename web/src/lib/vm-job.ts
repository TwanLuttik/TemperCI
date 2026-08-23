import type { Job } from "../api";

export function findJobForVM(jobs: Job[] | undefined, jobId?: string): Job | undefined {
  const id = String(jobId || "").trim();
  if (!id) return undefined;
  return (jobs || []).find((j) => String(j.job_id) === id);
}
