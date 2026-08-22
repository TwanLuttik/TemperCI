import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";

import { api, formatDuration, type JobDetail } from "../api";
import { EmptyState } from "../components/empty-state";
import { PageHeader } from "../components/page-header";
import { StatCard } from "../components/stat-card";
import { StatusBadge } from "../components/status-badge";
import { Card, CardContent } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

function fmt(ts?: string) {
  if (!ts) return "—";
  const d = new Date(ts);
  if (Number.isNaN(d.getTime()) || d.getTime() === 0) return "—";
  return d.toLocaleString();
}

export function JobDetailPage() {
  const { id } = useParams();
  const [data, setData] = useState<JobDetail | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [tab, setTab] = useState("events");

  useEffect(() => {
    let stop = false;
    const load = () => {
      api<JobDetail>(`/api/v1/jobs/${id}`)
        .then((d) => {
          if (!stop) {
            setData(d);
            setErr(null);
          }
        })
        .catch((e: Error) => {
          if (!stop) setErr(e.message);
        });
    };
    load();
    const t = setInterval(load, 2000);
    return () => {
      stop = true;
      clearInterval(t);
    };
  }, [id]);

  if (err) return <p className="text-sm text-destructive">{err}</p>;
  if (!data) return <p className="text-sm text-muted-foreground">Loading job…</p>;

  const j = data.job;
  const logs = data.logs || {};
  const events = logs.events || [];
  const running = !["finished", "failed"].includes(String(j.status || "").toLowerCase());
  const gh =
    j.repo_full_name && j.run_id
      ? `https://github.com/${j.repo_full_name}/actions/runs/${j.run_id}`
      : null;

  return (
    <>
      <PageHeader
        kicker={
          <>
            <Link to="/jobs" className="text-primary">
              / Jobs
            </Link>{" "}
            · {j.job_id}
          </>
        }
        title={`Job ${j.job_id}`}
        description={`${j.repo_full_name || "unknown repo"} · ${j.runner_name || "runner"}${
          running ? " · refreshing every 2s" : ""
        }`}
      />

      <div className="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        <StatCard label="Status" value={<StatusBadge status={j.status} />} />
        <StatCard
          label="Outcome"
          value={j.outcome ? <StatusBadge status={j.outcome} /> : "—"}
        />
        <StatCard
          label="Agent / VM"
          value={<span className="font-mono text-sm">{j.assigned_agent_id || "—"}</span>}
          hint={j.vm_id}
        />
        <StatCard label="Queue" value={formatDuration(j.queue_ms)} hint="created → assigned" />
        <StatCard label="Bind" value={formatDuration(j.bind_ms)} hint="assigned → started" />
        <StatCard label="Run" value={formatDuration(j.run_ms)} hint="started → finished" />
        <StatCard label="Total" value={formatDuration(j.total_ms)} hint="created → finished" />
        <StatCard
          label="Cache"
          value={
            (j.cache_hits ?? 0) + (j.cache_misses ?? 0) === 0
              ? "—"
              : `${j.cache_hits ?? 0} / ${j.cache_misses ?? 0}`
          }
          hint={
            j.cache_bytes_in || j.cache_bytes_out
              ? `${Math.round(((j.cache_bytes_in ?? 0) + (j.cache_bytes_out ?? 0)) / 1024)} KiB`
              : "local actions/cache"
          }
        />
        <StatCard
          label="GitHub"
          value={
            gh ? (
              <a href={gh} target="_blank" rel="noreferrer" className="text-sm text-primary">
                run {j.run_id}
              </a>
            ) : (
              "—"
            )
          }
        />
      </div>

      {j.error ? <p className="mb-4 text-sm text-destructive">{j.error}</p> : null}

      <Card>
        <CardContent>
          <Tabs value={tab} onValueChange={setTab}>
            <TabsList className="mb-4">
              <TabsTrigger value="events">Timeline ({events.length})</TabsTrigger>
              <TabsTrigger value="agent">Guest agent</TabsTrigger>
              <TabsTrigger value="runner">actions/runner</TabsTrigger>
              <TabsTrigger value="console">Serial console</TabsTrigger>
            </TabsList>
            <TabsContent value="events">
              {events.length === 0 ? (
                <EmptyState title="No events yet">
                  Dispatch a workflow with runs-on: temperci-… then this timeline fills as the job
                  progresses.
                </EmptyState>
              ) : (
                <ol className="m-0 flex list-none flex-col gap-2 p-0">
                  {events.map((e, i) => (
                    <li
                      key={`${e.time}-${i}`}
                      className="flex flex-wrap items-baseline gap-2.5 border-b border-border py-1.5 text-[13px] last:border-0"
                    >
                      <span className="font-mono text-xs text-muted-foreground">{fmt(e.time)}</span>
                      <StatusBadge tone={e.level === "error" || e.level === "warn" ? "bad" : "neutral"}>
                        {e.source}
                      </StatusBadge>
                      <span>{e.message}</span>
                    </li>
                  ))}
                </ol>
              )}
            </TabsContent>
            {(["agent", "runner", "console"] as const).map((key) => {
              const pane =
                key === "agent" ? logs.agent_log : key === "runner" ? logs.runner_log : logs.console_log;
              return (
                <TabsContent key={key} value={key}>
                  {pane ? (
                    <pre className="m-0 max-h-[28rem] overflow-auto rounded-lg border bg-background p-3 font-mono text-[11px] leading-relaxed whitespace-pre-wrap text-zinc-300">
                      {pane}
                    </pre>
                  ) : (
                    <EmptyState title={`No ${key} log yet`}>
                      Logs upload when the guest runner exits. Keep this page open.
                    </EmptyState>
                  )}
                </TabsContent>
              );
            })}
          </Tabs>
        </CardContent>
      </Card>

      <p className="mt-4 text-xs text-muted-foreground">
        Created {fmt(j.created_at)} · started {fmt(j.started_at)} · finished {fmt(j.finished_at)}
        {j.warm_bind ? " · warm bind" : ""}
        {j.labels?.length ? ` · ${(j.labels || []).join(", ")}` : ""}
      </p>
    </>
  );
}
