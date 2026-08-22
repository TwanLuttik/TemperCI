import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";

import { api, formatBytes, formatDuration, type Overview } from "../api";
import { PageHeader } from "../components/page-header";
import { ServicesPanel } from "../components/ServicesPanel";
import { StatCard } from "../components/stat-card";
import { LiveDot, StatusBadge } from "../components/status-badge";
import { useRealtime } from "../hooks/useRealtime";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

type Props = { onOverview: (o: Overview) => void };

export function OverviewPage({ onOverview }: Props) {
  const [o, setO] = useState<Overview | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const navigate = useNavigate();
  const rt = useRealtime(true);

  useEffect(() => {
    if (rt.last?.overview) {
      const merged = { ok: true, ...rt.last.overview } as Overview;
      setO(merged);
      onOverview(merged);
      return;
    }
    api<Overview>("/api/v1/overview")
      .then((data) => {
        setO(data);
        onOverview(data);
      })
      .catch((e: Error) => setErr(e.message));
  }, [onOverview, rt.last]);

  if (err) return <p className="text-sm text-destructive">{err}</p>;
  if (!o) return <p className="text-sm text-muted-foreground">Loading overview…</p>;

  return (
    <>
      <PageHeader
        kicker="/ Console"
        title="GitHub Actions, on your hardware"
        description={
          <span className="inline-flex flex-wrap items-center gap-2">
            Live capacity and job flow across TemperCI agents.
            <LiveDot status={rt.status} />
          </span>
        }
      />

      <div className="mb-6">
        <ServicesPanel />
      </div>

      <div className="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        <StatCard label="Agents" value={o.agents_registered} hint="registered hosts" />
        <StatCard label="Warm" value={o.warm} hint="ready microVMs" />
        <StatCard label="Busy" value={o.busy} hint="running jobs" />
        <StatCard label="Queued" value={o.jobs_pending} hint="awaiting claim" />
        <StatCard label="Started" value={o.jobs_started} hint="in flight" />
        <StatCard label="Finished" value={o.jobs_finished} hint="completed in memory" />
        <StatCard label="Run p50" value={formatDuration(o.run_p50_ms)} hint="last 100 finished" />
        <StatCard label="Run p95" value={formatDuration(o.run_p95_ms)} hint="last 100 finished" />
        <StatCard
          label="Cache"
          value={
            (o.cache_hits ?? 0) + (o.cache_misses ?? 0) === 0
              ? "—"
              : `${o.cache_hits ?? 0}/${(o.cache_hits ?? 0) + (o.cache_misses ?? 0)}`
          }
          hint={`hits / lookups${o.cache_bytes ? ` · ${formatBytes(o.cache_bytes)} on disk` : ""}`}
        />
      </div>

      <div className="grid gap-3 lg:grid-cols-[1.4fr_1fr]">
        <Card>
          <CardHeader className="flex-row items-center justify-between">
            <CardTitle>Fleet health</CardTitle>
            <span className="font-mono text-[11px] text-muted-foreground">org · {o.org || "—"}</span>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-sm text-muted-foreground">
              {o.fleet_ready
                ? "Control plane is minting JIT configs and assigning work to agents."
                : "Setup incomplete or GitHub client not ready — finish wizard / config first."}
            </p>
            <div className="flex flex-wrap gap-2">
              <StatusBadge tone={o.fleet_ready ? "ok" : "warn"}>{o.fleet_ready ? "ready" : "limited"}</StatusBadge>
              <StatusBadge tone={o.hostctl_configured ? "ok" : "neutral"}>
                hostctl {o.hostctl_configured ? "on" : "off"}
              </StatusBadge>
              <StatusBadge>{o.jobs_failed || 0} failed</StatusBadge>
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Next actions</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-wrap gap-2">
            <Button variant="secondary" onClick={() => navigate("/hosts")}>
              View runners
            </Button>
            <Button variant="secondary" onClick={() => navigate("/vms")}>
              MicroVM usage
            </Button>
            <Button variant="secondary" onClick={() => navigate("/jobs")}>
              View jobs
            </Button>
            <Button variant="secondary" onClick={() => navigate("/analytics")}>
              Workflow analytics
            </Button>
            <Button variant="secondary" onClick={() => navigate("/cache")}>
              View cache
            </Button>
            <Button variant="outline" onClick={() => navigate("/settings")}>
              Settings
            </Button>
            <Button variant="outline" onClick={() => navigate("/setup")}>
              Setup wizard
            </Button>
          </CardContent>
        </Card>
      </div>
    </>
  );
}
