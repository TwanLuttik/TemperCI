import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { api, formatDuration, type Job } from "../api";
import { EmptyState } from "../components/empty-state";
import { PageHeader } from "../components/page-header";
import { StatusBadge } from "../components/status-badge";
import { useRealtime } from "../hooks/useRealtime";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

export function JobsPage() {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const rt = useRealtime(true);

  useEffect(() => {
    if (rt.last?.jobs) {
      setJobs(rt.last.jobs);
      return;
    }
    api<{ jobs: Job[] }>("/api/v1/jobs")
      .then((d) => setJobs(d.jobs || []))
      .catch((e: Error) => setErr(e.message));
  }, [rt.last]);

  if (err) return <p className="text-sm text-destructive">{err}</p>;

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
              <TableHead>Repository</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Agent</TableHead>
              <TableHead>Duration</TableHead>
              <TableHead>Labels</TableHead>
              <TableHead>Outcome</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {jobs.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7}>
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
                      <code className="font-mono text-xs">{j.job_id}</code>
                    </Link>
                  </TableCell>
                  <TableCell>{j.repo_full_name || "—"}</TableCell>
                  <TableCell>
                    <StatusBadge status={j.status} />
                  </TableCell>
                  <TableCell className="font-mono text-xs">{j.assigned_agent_id || "—"}</TableCell>
                  <TableCell
                    className="font-mono text-xs"
                    title={`queue ${formatDuration(j.queue_ms)} · bind ${formatDuration(j.bind_ms)}`}
                  >
                    {formatDuration(j.run_ms || j.total_ms)}
                  </TableCell>
                  <TableCell className="max-w-[220px] truncate text-muted-foreground">
                    {(j.labels || []).join(", ")}
                  </TableCell>
                  <TableCell>{j.outcome ? <StatusBadge status={j.outcome} /> : "—"}</TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>
    </>
  );
}
