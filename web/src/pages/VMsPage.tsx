import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { api, formatDuration, killVM, type Job } from "../api";
import { Button } from "@/components/ui/button";
import { EmptyState } from "../components/empty-state";
import { PageHeader } from "../components/page-header";
import { LiveDot, StatusBadge } from "../components/status-badge";
import { useRealtime, type VMRow } from "../hooks/useRealtime";
import { findJobForVM } from "../lib/vm-job";
import { orderVMs } from "../lib/vm-list";
import { Card } from "@/components/ui/card";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card";
import { Progress } from "@/components/ui/progress";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { cn } from "@/lib/utils";

function UsageBar({
  label,
  value,
  max,
  unit,
}: {
  label: string;
  value: number;
  max: number;
  unit: string;
}) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  return (
    <div className="min-w-[120px]">
      <div className="mb-1 flex justify-between font-mono text-[10px] text-muted-foreground">
        <span>{label}</span>
        <span>
          {value.toFixed(1)}
          {unit}
          {max > 0 ? ` / ${max}${unit}` : ""}
        </span>
      </div>
      <Progress
        value={pct}
        className={cn(
          "h-1.5",
          pct >= 85 ? "*:data-[slot=progress-indicator]:bg-red-400" : pct >= 60 ? "*:data-[slot=progress-indicator]:bg-amber-400" : "",
        )}
      />
    </div>
  );
}

function VMJobCell({ jobId, job }: { jobId?: string; job?: Job }) {
  if (!jobId) {
    return <span className="font-mono text-xs text-muted-foreground">—</span>;
  }
  const title = job?.name || `Job ${jobId}`;
  return (
    <HoverCard openDelay={180} closeDelay={80}>
      <HoverCardTrigger asChild>
        <Link
          to={`/jobs/${jobId}`}
          className="font-mono text-xs text-primary underline-offset-4 hover:underline"
        >
          {jobId}
        </Link>
      </HoverCardTrigger>
      <HoverCardContent side="top">
        <div className="space-y-2">
          <div>
            <div className="font-medium leading-snug">{title}</div>
            <div className="mt-0.5 font-mono text-[11px] text-muted-foreground">{jobId}</div>
          </div>
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-[12px]">
            <dt className="text-muted-foreground">Repo</dt>
            <dd className="truncate">{job?.repo_full_name || "—"}</dd>
            <dt className="text-muted-foreground">Workflow</dt>
            <dd className="truncate">{job?.workflow_name || "—"}</dd>
            <dt className="text-muted-foreground">Status</dt>
            <dd className="flex flex-wrap items-center gap-1">
              <StatusBadge status={job?.status || "unknown"} />
              {job?.outcome ? <StatusBadge status={job.outcome} /> : null}
            </dd>
            <dt className="text-muted-foreground">Run</dt>
            <dd className="font-mono text-[11px]">{formatDuration(job?.run_ms || job?.total_ms)}</dd>
            <dt className="text-muted-foreground">Bind</dt>
            <dd>{job?.warm_bind ? "warm pool" : job ? "cold" : "—"}</dd>
            {job?.labels && job.labels.length > 0 ? (
              <>
                <dt className="text-muted-foreground">Labels</dt>
                <dd className="truncate text-muted-foreground">{job.labels.join(", ")}</dd>
              </>
            ) : null}
          </dl>
          <div className="text-[11px] text-muted-foreground">Open job details</div>
        </div>
      </HoverCardContent>
    </HoverCard>
  );
}

export function VMsPage() {
  const rt = useRealtime(true);
  const [vms, setVms] = useState<VMRow[]>([]);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [killing, setKilling] = useState<string | null>(null);

  useEffect(() => {
    if (rt.last?.vms) {
      setVms((prev) => orderVMs(prev, rt.last?.vms || []));
      if (rt.last.jobs) setJobs(rt.last.jobs);
      return;
    }
    Promise.all([api<{ vms: VMRow[] }>("/api/v1/vms"), api<{ jobs: Job[] }>("/api/v1/jobs")])
      .then(([v, j]) => {
        setVms((prev) => orderVMs(prev, v.vms || []));
        setJobs(j.jobs || []);
      })
      .catch((e: Error) => setErr(e.message));
  }, [rt.last]);

  if (err && vms.length === 0) return <p className="text-sm text-destructive">{err}</p>;

  return (
    <>
      <PageHeader
        kicker="/ MicroVMs"
        title="Live microVM usage"
        description={
          <span className="inline-flex flex-wrap items-center gap-2">
            Host-side Firecracker samples (CPU / RSS / disk).
            <LiveDot status={rt.status} />
          </span>
        }
      />
      <Card className="py-2">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Agent</TableHead>
              <TableHead>VM</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Job</TableHead>
              <TableHead>CPU</TableHead>
              <TableHead>Memory (RSS)</TableHead>
              <TableHead>Disk</TableHead>
              <TableHead>PID</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {vms.length === 0 ? (
              <TableRow>
                <TableCell colSpan={9}>
                  <EmptyState title="No microVMs reported">
                    Warm pool VMs appear after the agent heartbeats. Ensure Firecracker is running.
                  </EmptyState>
                </TableCell>
              </TableRow>
            ) : (
              vms.map((v) => (
                <TableRow key={`${v.agent_id}-${v.id}`}>
                  <TableCell className="font-mono text-xs">{v.agent_id}</TableCell>
                  <TableCell>
                    <code className="font-mono text-xs">{v.id.slice(0, 12)}</code>
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={v.state} />
                  </TableCell>
                  <TableCell>
                    <VMJobCell jobId={v.job_id} job={findJobForVM(jobs, v.job_id)} />
                  </TableCell>
                  <TableCell>
                    <UsageBar label="CPU" value={v.cpu_percent || 0} max={100} unit="%" />
                  </TableCell>
                  <TableCell>
                    <UsageBar label="RSS" value={v.rss_mib || 0} max={v.memory_mib || 0} unit="MiB" />
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {v.disk_mib != null ? `${v.disk_mib.toFixed(1)} MiB` : "—"}
                  </TableCell>
                  <TableCell className="font-mono text-xs">{v.pid || "—"}</TableCell>
                  <TableCell className="text-right">
                    {v.state === "destroying" ? null : (
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={killing === v.id}
                        onClick={() => {
                          const label = v.job_id ? `VM ${v.id.slice(0, 12)} (job ${v.job_id})` : `VM ${v.id.slice(0, 12)}`;
                          if (!window.confirm(`Kill ${label}?`)) return;
                          setKilling(v.id);
                          killVM(v.id)
                            .catch((e: Error) => setErr(e.message))
                            .finally(() => setKilling(null));
                        }}
                      >
                        Kill
                      </Button>
                    )}
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
