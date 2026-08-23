import { useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";

import { api, formatDuration, killVM, type Job } from "../api";
import { EmptyState } from "../components/empty-state";
import { PageHeader } from "../components/page-header";
import { StatCard } from "../components/stat-card";
import { LiveDot, StatusBadge } from "../components/status-badge";
import { useNow } from "../hooks/useNow";
import { useRealtime } from "../hooks/useRealtime";
import { liveJobClockMs } from "../lib/job-duration";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

type VMDetail = {
  ok: boolean;
  agent_id: string;
  vm: {
    id: string;
    state: string;
    job_id?: string;
    vcpus: number;
    memory_mib: number;
    pid?: number;
    cpu_percent: number;
    rss_mib: number;
    disk_mib?: number;
    created_at?: string;
    sampled_at?: string;
    guest_ip?: string;
    host_ip?: string;
    tap?: string;
    shape?: string;
    console_tail?: string;
    agent_tail?: string;
  };
  job?: Job | null;
};

function fmt(ts?: string) {
  if (!ts) return "—";
  const d = new Date(ts);
  if (Number.isNaN(d.getTime()) || d.getTime() === 0) return "—";
  return d.toLocaleString();
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
      className="m-0 max-h-[32rem] overflow-auto rounded-lg border bg-background p-3 font-mono text-[11px] leading-relaxed whitespace-pre-wrap text-zinc-300"
    >
      {text}
      {live ? <span className="mt-2 block animate-pulse text-[10px] text-muted-foreground">● live</span> : null}
    </pre>
  );
}

export function VMDetailPage() {
  const { id } = useParams();
  const rt = useRealtime(true);
  const [data, setData] = useState<VMDetail | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [killing, setKilling] = useState(false);
  const [tab, setTab] = useState("console");
  const present = Boolean(data?.vm.id);
  const now = useNow(present);

  useEffect(() => {
    if (!id) return;
    let stop = false;
    const load = () => {
      api<VMDetail>(`/api/v1/vms/${encodeURIComponent(id)}`)
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

  if (err && !data) return <p className="text-sm text-destructive">{err}</p>;
  if (!data) return <p className="text-sm text-muted-foreground">Loading microVM…</p>;

  const v = data.vm;
  const job = data.job;
  const live = v.state !== "destroying";
  const cpu = v.cpu_percent || 0;
  const rss = v.rss_mib || 0;

  return (
    <>
      <PageHeader
        kicker={
          <>
            <Link to="/vms" className="text-primary">
              / MicroVMs
            </Link>{" "}
            · {v.id.slice(0, 12)}
          </>
        }
        title={v.id}
        description={
          <span className="inline-flex flex-wrap items-center gap-2">
            {data.agent_id} · {v.shape || `${v.vcpus} vCPU / ${v.memory_mib} MiB`}
            <LiveDot status={rt.status} />
          </span>
        }
        actions={
          v.state === "destroying" ? undefined : (
            <Button
              type="button"
              variant="outline"
              disabled={killing}
              onClick={() => {
                if (!window.confirm(`Kill VM ${v.id.slice(0, 12)}?`)) return;
                setKilling(true);
                killVM(v.id)
                  .catch((e: Error) => setErr(e.message))
                  .finally(() => setKilling(false));
              }}
            >
              {killing ? "Killing…" : "Kill VM"}
            </Button>
          )
        }
      />

      {err ? <p className="mb-4 text-sm text-destructive">{err}</p> : null}

      <div className="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        <StatCard label="State" value={<StatusBadge status={v.state} />} />
        <StatCard
          label="Job"
          value={
            v.job_id ? (
              <Link to={`/jobs/${v.job_id}`} className="font-mono text-sm text-primary">
                {job?.name || v.job_id}
              </Link>
            ) : (
              "idle"
            )
          }
          hint={job?.repo_full_name || (v.job_id ? "bound" : "warm pool")}
        />
        <StatCard
          label="Run"
          value={job ? formatDuration(liveJobClockMs(job, now)) : "—"}
          hint={job?.warm_bind ? "warm bind" : job ? "cold" : undefined}
        />
        <StatCard label="Guest IP" value={<span className="font-mono text-sm">{v.guest_ip || "—"}</span>} hint={v.tap || undefined} />
        <StatCard label="Host TAP" value={<span className="font-mono text-sm">{v.host_ip || "—"}</span>} />
        <StatCard
          label="CPU"
          value={`${cpu.toFixed(1)}%`}
          hint={
            <Progress value={Math.min(100, cpu)} className="mt-1 h-1.5" />
          }
        />
        <StatCard
          label="RSS"
          value={`${rss.toFixed(0)} MiB`}
          hint={v.memory_mib ? `of ${v.memory_mib} MiB` : undefined}
        />
        <StatCard label="Disk" value={v.disk_mib != null ? `${v.disk_mib.toFixed(0)} MiB` : "—"} />
        <StatCard label="PID" value={<span className="font-mono text-sm">{v.pid || "—"}</span>} />
        <StatCard label="Created" value={fmt(v.created_at)} hint={v.sampled_at ? `sampled ${fmt(v.sampled_at)}` : undefined} />
      </div>

      <Card>
        <CardContent>
          <Tabs value={tab} onValueChange={setTab}>
            <TabsList className="mb-4">
              <TabsTrigger value="console">Serial console{live ? " · live" : ""}</TabsTrigger>
              <TabsTrigger value="agent">Guest agent{live ? " · live" : ""}</TabsTrigger>
            </TabsList>
            <TabsContent value="console">
              {v.console_tail ? (
                <LiveLog text={v.console_tail} live={live} />
              ) : (
                <EmptyState title="No serial output yet">
                  Firecracker writes console=ttyS0 here after the guest boots. Refresh stays live while the VM is reported.
                </EmptyState>
              )}
            </TabsContent>
            <TabsContent value="agent">
              {v.agent_tail ? (
                <LiveLog text={v.agent_tail} live={live} />
              ) : (
                <EmptyState title="No guest-agent log yet">
                  The guest agent writes this after it mounts the inject disk and waits for JIT.
                </EmptyState>
              )}
            </TabsContent>
          </Tabs>
        </CardContent>
      </Card>
    </>
  );
}
