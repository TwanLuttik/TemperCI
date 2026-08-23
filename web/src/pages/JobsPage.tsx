import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { api, cancelJob, formatDuration, jobIsActive, type Job } from "../api";
import { Button } from "@/components/ui/button";
import { EmptyState } from "../components/empty-state";
import { PageHeader } from "../components/page-header";
import { StatusBadge } from "../components/status-badge";
import { useNow } from "../hooks/useNow";
import { useRealtime } from "../hooks/useRealtime";
import { jobsNeedLiveClock, liveJobClockMs, liveJobTimings, mergeJobSnapshots } from "../lib/job-duration";
import { jobStepProgress, settleSteps } from "../lib/job-steps";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

export function JobsPage() {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<number | null>(null);
  const rt = useRealtime(true);
  const live = jobsNeedLiveClock(jobs);
  const now = useNow(live);

  useEffect(() => {
    if (rt.last?.jobs) {
      setJobs((prev) => mergeJobSnapshots(rt.last?.jobs || [], prev));
    }
  }, [rt.last]);

  useEffect(() => {
    let stop = false;
    const load = () => {
      api<{ jobs: Job[] }>("/api/v1/jobs")
        .then((d) => {
          if (!stop) {
            setJobs((prev) => mergeJobSnapshots(d.jobs || [], prev));
            setErr(null);
          }
        })
        .catch((e: Error) => {
          if (!stop) setErr(e.message);
        });
    };
    load();
    const t = setInterval(load, live ? 1000 : 8000);
    return () => {
      stop = true;
      clearInterval(t);
    };
  }, [live]);

  if (err && jobs.length === 0) return <p className="text-sm text-destructive">{err}</p>;

  return (
    <>
      <PageHeader
        kicker="/ Jobs"
        title="Recent assignments"
        description="Assignments persist across control restarts. Open a job to inspect the timeline and logs after you dispatch a workflow."
      />
      <Card className="py-2">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Job</TableHead>
              <TableHead>Workflow</TableHead>
              <TableHead>Repository</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Step</TableHead>
              <TableHead>Agent</TableHead>
              <TableHead>Duration</TableHead>
              <TableHead>Labels</TableHead>
              <TableHead>Outcome</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {jobs.length === 0 ? (
              <TableRow>
                <TableCell colSpan={10}>
                  <EmptyState title="No jobs in memory">
                    Dispatch a workflow with <code>runs-on: temperci-…</code>
                  </EmptyState>
                </TableCell>
              </TableRow>
            ) : (
              jobs.map((j) => (
                <TableRow key={j.job_id}>
                  <TableCell>
                    <Link to={`/jobs/${j.job_id}`} className="text-primary">
                      <div className="font-medium">{j.name || `Job ${j.job_id}`}</div>
                      <code className="font-mono text-[11px] text-muted-foreground">{j.job_id}</code>
                    </Link>
                  </TableCell>
                  <TableCell>
                    <div>{j.workflow_name || "—"}</div>
                    {j.workflow_name && j.name && j.workflow_name !== j.name ? (
                      <div className="text-[11px] text-muted-foreground">{j.name}</div>
                    ) : null}
                  </TableCell>
                  <TableCell>{j.repo_full_name || "—"}</TableCell>
                  <TableCell>
                    <StatusBadge status={j.status} />
                  </TableCell>
                  <TableCell>
                    <JobStepCell job={j} />
                  </TableCell>
                  <TableCell className="font-mono text-xs">{j.assigned_agent_id || "—"}</TableCell>
                  <TableCell className="font-mono text-xs tabular-nums">
                    <JobDurationCell job={j} now={now} />
                  </TableCell>
                  <TableCell className="max-w-[220px] truncate text-muted-foreground">
                    {(j.labels || []).join(", ")}
                  </TableCell>
                  <TableCell>{j.outcome ? <StatusBadge status={j.outcome} /> : "—"}</TableCell>
                  <TableCell className="text-right">
                    {jobIsActive(j.status) ? (
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={busyId === j.job_id}
                        onClick={() => {
                          if (!window.confirm(`Cancel job ${j.job_id}? This kills the microVM if one is bound.`)) return;
                          setBusyId(j.job_id);
                          cancelJob(j.job_id)
                            .catch((e: Error) => setErr(e.message))
                            .finally(() => setBusyId(null));
                        }}
                      >
                        Cancel
                      </Button>
                    ) : null}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>
    </>
  );
}

function JobDurationCell({ job, now }: { job: Job; now: number }) {
  const timings = liveJobTimings(job, now);
  return (
    <span title={`queue ${formatDuration(timings.queue)} · bind ${formatDuration(timings.bind)}`}>
      {formatDuration(liveJobClockMs(job, now))}
    </span>
  );
}

function JobStepCell({ job }: { job: Job }) {
  const steps = settleSteps(job.steps, job);
  const progress = jobStepProgress(steps);
  if (!progress.total) {
    return <span className="text-muted-foreground">—</span>;
  }
  if (!progress.current) {
    return (
      <div>
        <div className="text-muted-foreground">{progress.done} / {progress.total}</div>
      </div>
    );
  }
  return (
    <div className="max-w-[220px]">
      <div className="truncate">{progress.current.name}</div>
      <div className="text-[11px] text-muted-foreground">
        {progress.index} / {progress.total}
      </div>
    </div>
  );
}
