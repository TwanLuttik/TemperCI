import { useEffect, useRef, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";

import { api, cancelJob, formatDuration, jobIsActive, type Job, type JobDetail, type JobStep } from "../api";
import { Button } from "@/components/ui/button";
import { useNow } from "../hooks/useNow";
import { liveJobTimings } from "../lib/job-duration";
import { formatStepClock, jobStepProgress, lastWorkflowGroup, parseStepTime, settleSteps, stepElapsedMs } from "../lib/job-steps";
import { suggestJobTab } from "../lib/job-tabs";
import { EmptyState } from "../components/empty-state";
import { PageHeader } from "../components/page-header";
import { StatCard } from "../components/stat-card";
import { StatusBadge } from "../components/status-badge";
import { Card, CardContent } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Check, Circle, LoaderCircle, Minus, X } from "lucide-react";

function fmt(ts?: string) {
  if (!ts) return "—";
  const d = new Date(ts);
  if (Number.isNaN(d.getTime()) || d.getTime() === 0) return "—";
  return d.toLocaleString();
}

const jobTabs = new Set(["events", "agent", "runner", "console"]);

export function JobDetailPage() {
  const { id } = useParams();
  const [params, setParams] = useSearchParams();
  const [data, setData] = useState<JobDetail | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [cancelling, setCancelling] = useState(false);
  const tabRaw = params.get("tab") || "events";
  const tab = jobTabs.has(tabRaw) ? tabRaw : "events";
  const [follow, setFollow] = useState(() => !params.get("tab"));
  const setTab = (next: string, pin = true) => {
    if (pin) setFollow(false);
    const p = new URLSearchParams(params);
    if (next === "events") {
      p.delete("tab");
    } else {
      p.set("tab", next);
    }
    setParams(p, { replace: true });
  };

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
    const t = setInterval(load, 1000);
    return () => {
      stop = true;
      clearInterval(t);
    };
  }, [id]);

  useEffect(() => {
    if (!follow || !data) return;
    const next = suggestJobTab(data.job, data.logs || {});
    if (next !== tab) setTab(next, false);
  }, [follow, data, tab]);

  const jobStatus = String(data?.job.status || "").toLowerCase();
  const jobDone = jobStatus === "finished" || jobStatus === "failed";
  const now = useNow(!jobDone && Boolean(data));

  if (err) return <p className="text-sm text-destructive">{err}</p>;
  if (!data) return <p className="text-sm text-muted-foreground">Loading job…</p>;

  const j = { ...data.job, steps: settleSteps(data.job.steps, data.job) };
  const logs = data.logs || {};
  const events = logs.events || [];
  const running = !["finished", "failed"].includes(String(j.status || "").toLowerCase());
  const progress = jobStepProgress(j.steps);
  const timings = liveJobTimings(j, now);
  const currentMs = progress.current ? stepElapsedMs(progress.current, now, parseStepTime(progress.current.started_at)) : undefined;
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
        title={j.name ? j.name : `Job ${j.job_id}`}
        description={`${j.workflow_name ? `${j.workflow_name} · ` : ""}${j.repo_full_name || "unknown repo"} · ${j.runner_name || "runner"}${
          j.name ? ` · ${j.job_id}` : ""
        }${running ? (follow ? " · live follow" : " · live (tab pinned)") : ""}`}
        actions={
          jobIsActive(j.status) ? (
            <Button
              type="button"
              variant="outline"
              disabled={cancelling}
              onClick={() => {
                if (!window.confirm(`Cancel job ${j.job_id} and kill its microVM?`)) return;
                setCancelling(true);
                cancelJob(j.job_id)
                  .catch((e: Error) => setErr(e.message))
                  .finally(() => setCancelling(false));
              }}
            >
              {cancelling ? "Cancelling…" : "Cancel job"}
            </Button>
          ) : undefined
        }
      />

      <div className="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        <StatCard label="Status" value={<StatusBadge status={j.status} />} />
        <StatCard
          label="Step"
          value={
            progress.total
              ? `${progress.index} / ${progress.total}`
              : "—"
          }
          hint={
            progress.current
              ? `${progress.current.name}${currentMs != null ? ` · ${formatStepClock(currentMs)}` : ""}`
              : lastWorkflowGroup(logs.workflow_log) ||
                (progress.total && progress.done === progress.total
                  ? "all steps complete"
                  : running
                    ? "waiting for GitHub steps"
                    : undefined)
          }
        />
        <StatCard
          label="Outcome"
          value={j.outcome ? <StatusBadge status={j.outcome} /> : "—"}
        />
        <StatCard
          label="Agent / VM"
          value={<span className="font-mono text-sm">{j.assigned_agent_id || "—"}</span>}
          hint={j.vm_id}
        />
        <StatCard label="Queue" value={formatDuration(timings.queue)} hint="created → assigned" />
        <StatCard label="Bind" value={formatDuration(timings.bind)} hint="assigned → started" />
        <StatCard label="Run" value={formatDuration(timings.run)} hint="started → finished" />
        <StatCard label="Total" value={formatDuration(timings.total)} hint="created → finished" />
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

      {progress.total > 0 ? <WorkflowSteps job={j} running={running} now={now} /> : null}

      <Card>
        <CardContent>
          <Tabs value={tab} onValueChange={(v) => setTab(v, true)}>
            <TabsList className="mb-4">
              <TabsTrigger value="events">
                Timeline ({events.length}){running && tab === "events" ? " · live" : ""}
              </TabsTrigger>
              <TabsTrigger value="agent">
                Guest agent{running && tab === "agent" ? " · live" : ""}
              </TabsTrigger>
              <TabsTrigger value="runner">
                Workflow log{running && tab === "runner" ? " · live" : ""}
              </TabsTrigger>
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
                key === "agent"
                  ? logs.agent_log
                  : key === "runner"
                    ? logs.workflow_log
                    : logs.console_log;
              return (
                <TabsContent key={key} value={key}>
                  {pane ? (
                    <LiveLog text={pane} live={running} />
                  ) : (
                    <EmptyState title={key === "runner" ? "No workflow log yet" : `No ${key} log yet`}>
                      {key === "runner"
                        ? running
                          ? "Waiting for GitHub step output (checkout, npm, compose…). Runner _diag is not shown here."
                          : "GitHub step logs appear after the job finishes (same text as the Actions run)."
                        : running
                          ? "Streaming from the guest as soon as this log appears."
                          : "Logs upload when the guest runner exits. Keep this page open."}
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

function WorkflowSteps({ job, running, now }: { job: Job; running: boolean; now: number }) {
  const steps = job.steps || [];
  const progress = jobStepProgress(steps);
  const pct = progress.total ? Math.round((progress.done / progress.total) * 100) : 0;
  const seenStart = useRef<Map<number, number>>(new Map());
  for (const step of steps) {
    if (String(step.status || "").toLowerCase() !== "in_progress") continue;
    if (seenStart.current.has(step.number)) continue;
    seenStart.current.set(step.number, parseStepTime(step.started_at) ?? now);
  }
  const currentMs = progress.current
    ? stepElapsedMs(progress.current, now, seenStart.current.get(progress.current.number))
    : undefined;
  return (
    <Card className="mb-6">
      <CardContent className="pt-5">
        <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
          <div>
            <div className="font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
              Workflow steps
            </div>
            <p className="mt-1 text-sm">
              {progress.current ? (
                <>
                  <span className="text-foreground">{progress.current.name}</span>
                  <span className="text-muted-foreground">
                    {" "}
                    · {progress.index} of {progress.total}
                    {running ? " · live" : ""}
                    {currentMs != null ? ` · ${formatStepClock(currentMs)}` : ""}
                  </span>
                </>
              ) : (
                <span className="text-muted-foreground">
                  {progress.done} of {progress.total} complete
                </span>
              )}
            </p>
          </div>
          <span className="font-mono text-xs text-muted-foreground">{pct}%</span>
        </div>
        <Progress value={pct} className="mb-4 h-1.5" />
        <ol className="m-0 flex list-none flex-col p-0">
          {steps.map((step) => (
            <StepRow
              key={`${step.number}-${step.name}`}
              step={step}
              now={now}
              fallbackStart={seenStart.current.get(step.number)}
            />
          ))}
        </ol>
      </CardContent>
    </Card>
  );
}

function StepRow({
  step,
  now,
  fallbackStart,
}: {
  step: JobStep;
  now: number;
  fallbackStart?: number;
}) {
  const status = String(step.status || "").toLowerCase();
  const conclusion = String(step.conclusion || "").toLowerCase();
  const active = status === "in_progress";
  const label = status === "completed" ? conclusion || "completed" : status || "queued";
  const elapsed = stepElapsedMs(step, now, fallbackStart);
  return (
    <li
      className={`flex items-center gap-2.5 border-b border-border py-2 text-sm last:border-0 ${
        active ? "text-foreground" : status === "completed" ? "text-foreground/90" : "text-muted-foreground"
      }`}
    >
      <StepMark status={status} conclusion={conclusion} />
      <span className={`min-w-0 flex-1 truncate ${active ? "font-medium" : ""}`}>{step.name}</span>
      <span
        className={`w-[4.75rem] shrink-0 text-right font-mono text-xs tabular-nums ${
          active ? "text-emerald-400" : "text-muted-foreground"
        }`}
      >
        {status === "pending" || status === "queued" ? "" : formatStepClock(elapsed)}
      </span>
      <StatusBadge status={label}>{label.replaceAll("_", " ")}</StatusBadge>
    </li>
  );
}

function StepMark({ status, conclusion }: { status: string; conclusion: string }) {
  if (status === "in_progress") {
    return <LoaderCircle className="size-3.5 shrink-0 animate-spin text-emerald-400" />;
  }
  if (status === "completed" && conclusion === "success") {
    return <Check className="size-3.5 shrink-0 text-emerald-400" />;
  }
  if (status === "completed" && (conclusion === "failure" || conclusion === "cancelled")) {
    return <X className="size-3.5 shrink-0 text-red-400" />;
  }
  if (status === "completed") {
    return <Minus className="size-3.5 shrink-0 text-muted-foreground" />;
  }
  return <Circle className="size-3 shrink-0 text-muted-foreground/70" />;
}

function LiveLog({ text, live }: { text: string; live: boolean }) {
  const ref = useRef<HTMLPreElement>(null);
  const stick = useRef(true);
  useEffect(() => {
    const el = ref.current;
    if (!el || !stick.current) return;
    el.scrollTop = el.scrollHeight;
  }, [text]);
  return (
    <pre
      ref={ref}
      onScroll={() => {
        const el = ref.current;
        if (!el) return;
        stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 48;
      }}
      className="m-0 max-h-[28rem] overflow-auto rounded-lg border bg-background p-3 font-mono text-[11px] leading-relaxed whitespace-pre-wrap text-zinc-300"
    >
      {text}
      {live ? <span className="mt-2 block animate-pulse text-[10px] text-muted-foreground">● live</span> : null}
    </pre>
  );
}
