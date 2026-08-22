import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";

import { api, formatDuration, type Job } from "../api";
import { WorkflowAnalyticsGrid } from "../components/job-analytics";
import { EmptyState } from "../components/empty-state";
import { PageHeader } from "../components/page-header";
import { StatCard } from "../components/stat-card";
import { useRealtime } from "../hooks/useRealtime";
import { workflowStats } from "../lib/job-analytics";
import { Card, CardContent } from "@/components/ui/card";

export function AnalyticsPage() {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [filter, setFilter] = useState("all");
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

  const rows = useMemo(() => workflowStats(jobs), [jobs]);
  const keys = rows.map((r) => r.key);
  const selected = filter !== "all" && !keys.includes(filter) ? "all" : filter;
  const shown = selected === "all" ? rows : rows.filter((r) => r.key === selected);
  const runs = shown.reduce((n, r) => n + r.n, 0);
  const fails = shown.reduce((n, r) => n + r.fail, 0);
  const p50 = shown.length ? shown.reduce((s, r) => s + r.p50 * r.n, 0) / Math.max(1, runs) : 0;
  const p95 = shown.length ? Math.max(...shown.map((r) => r.p95)) : 0;

  if (err) return <p className="text-sm text-destructive">{err}</p>;

  return (
    <>
      <PageHeader
        kicker="/ Analytics"
        title="Workflow analytics"
        description="Duration and success rate by workflow type. Built from recent TemperCI assignments."
      />

      {keys.length > 1 ? (
        <div className="mb-4 flex flex-wrap gap-1.5">
          <Chip active={selected === "all"} onClick={() => setFilter("all")}>
            all workflows
          </Chip>
          {keys.map((k) => (
            <Chip key={k} active={selected === k} onClick={() => setFilter(k)}>
              {k}
            </Chip>
          ))}
        </div>
      ) : null}

      <div className="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatCard label="Workflows" value={shown.length} />
        <StatCard label="Finished runs" value={runs} hint={fails ? `${fails} failed` : "all successful"} />
        <StatCard label="p50" value={runs ? formatDuration(p50) : "—"} hint="weighted by runs" />
        <StatCard label="p95" value={runs ? formatDuration(p95) : "—"} hint="slowest workflow p95" />
      </div>

      {jobs.length === 0 ? (
        <Card>
          <CardContent className="pt-6">
            <EmptyState title="No job history yet">
              Dispatch a workflow with <code>runs-on: temperci-…</code> and finished runs will appear here.
            </EmptyState>
          </CardContent>
        </Card>
      ) : (
        <WorkflowAnalyticsGrid jobs={jobs} selected={selected} />
      )}

      <p className="mt-4 text-xs text-muted-foreground">
        Grouped by GitHub workflow name, then job name. Open a job on the{" "}
        <Link to="/jobs" className="text-primary">
          jobs
        </Link>{" "}
        page to backfill titles for older assignments.
      </p>
    </>
  );
}

function Chip({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`rounded-full border px-2.5 py-0.5 font-mono text-[11px] ${
        active
          ? "border-primary/40 bg-primary/15 text-foreground"
          : "border-border text-muted-foreground hover:text-foreground"
      }`}
    >
      {children}
    </button>
  );
}
