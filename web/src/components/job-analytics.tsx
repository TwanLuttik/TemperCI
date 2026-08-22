import { formatDuration, type Job } from "../api";
import { workflowKey, workflowStats, type SeriesPoint, type WorkflowStats } from "../lib/job-analytics";
import { Card, CardContent } from "@/components/ui/card";

export function WorkflowAnalyticsGrid({ jobs, selected }: { jobs: Job[]; selected?: string }) {
  const rows = workflowStats(jobs).filter((r) => !selected || selected === "all" || r.key === selected);
  if (rows.length === 0) {
    return <p className="text-sm text-muted-foreground">No finished runs to chart yet.</p>;
  }
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      {rows.map((row) => (
        <WorkflowCard key={row.key} row={row} />
      ))}
    </div>
  );
}

function WorkflowCard({ row }: { row: WorkflowStats }) {
  const rate = row.n ? Math.round((row.ok / row.n) * 100) : 0;
  return (
    <Card>
      <CardContent className="pt-5">
        <div className="mb-3 flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="truncate text-base font-semibold">{row.key}</div>
            <p className="mt-0.5 text-xs text-muted-foreground">
              {row.n} run{row.n === 1 ? "" : "s"} · {rate}% success
              {row.fail ? ` · ${row.fail} failed` : ""}
            </p>
          </div>
        </div>
        <DurationLine points={row.points} />
        <div className="mt-4 grid grid-cols-2 gap-2 sm:grid-cols-4">
          <MiniStat label="p50" value={formatDuration(row.p50)} />
          <MiniStat label="p95" value={formatDuration(row.p95)} />
          <MiniStat label="last" value={formatDuration(row.last)} />
          <MiniStat label="queue" value={formatDuration(row.queueAvg)} />
        </div>
      </CardContent>
    </Card>
  );
}

function MiniStat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-border px-2.5 py-2">
      <div className="font-mono text-[10px] tracking-wider text-muted-foreground uppercase">{label}</div>
      <div className="mt-1 font-mono text-sm tabular-nums">{value}</div>
    </div>
  );
}

function DurationLine({ points }: { points: SeriesPoint[] }) {
  const w = 640;
  const h = 140;
  const pad = { l: 8, r: 8, t: 12, b: 8 };
  if (points.length === 0) {
    return <div className="h-[140px] rounded-lg border border-dashed border-border" />;
  }
  const max = Math.max(...points.map((p) => p.ms), 1);
  const innerW = w - pad.l - pad.r;
  const innerH = h - pad.t - pad.b;
  const x = (i: number) =>
    points.length === 1 ? pad.l + innerW / 2 : pad.l + (i / (points.length - 1)) * innerW;
  const y = (ms: number) => pad.t + innerH - (ms / max) * innerH;
  const d = points
    .map((p, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(p.ms).toFixed(1)}`)
    .join(" ");
  const area = `${d} L${x(points.length - 1).toFixed(1)},${pad.t + innerH} L${x(0).toFixed(1)},${pad.t + innerH} Z`;
  return (
    <svg viewBox={`0 0 ${w} ${h}`} className="h-[140px] w-full" role="img" aria-label="Job run duration over time">
      <path d={area} fill="oklch(0.72 0.19 45 / 0.12)" />
      <path d={d} fill="none" stroke="oklch(0.72 0.19 45)" strokeWidth="2" />
      {points.map((p, i) => (
        <circle
          key={p.job.job_id}
          cx={x(i)}
          cy={y(p.ms)}
          r="3"
          fill={String(p.job.outcome || "").toLowerCase() === "success" ? "oklch(0.72 0.19 45)" : "oklch(0.7 0.19 22)"}
        >
          <title>
            {workflowKey(p.job)} · {p.job.name || p.job.job_id} · {formatDuration(p.ms)}
          </title>
        </circle>
      ))}
    </svg>
  );
}
