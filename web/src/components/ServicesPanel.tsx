import { useCallback, useEffect, useRef, useState } from "react";
import {
  api,
  formatAge,
  waitForServicesReady,
  type Me,
  type RestartProgress,
  type RestartTarget,
  type ServiceSlice,
  type SystemStatus,
} from "../api";
import { RestartStatus } from "./RestartStatus";
import { StatusBadge } from "./status-badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Loader2Icon } from "lucide-react";

type Props = {
  onRestarted?: () => void;
};

const POLL_MS = 4000;

export function serviceBadgeClass(status?: string): string {
  switch (status) {
    case "running":
      return "ok";
    case "starting":
    case "stopping":
    case "unknown":
    case "not_installed":
      return "warn";
    case "stopped":
    case "failed":
      return "bad";
    default:
      return "";
  }
}

function overallLabel(status?: string): string {
  switch (status) {
    case "running":
      return "all running";
    case "starting":
      return "starting";
    case "stopping":
      return "stopping";
    case "stopped":
      return "stopped";
    case "failed":
      return "failed";
    case "unknown":
      return "unknown";
    case "not_installed":
      return "not installed";
    default:
      return status || "unknown";
  }
}

function serviceTitle(svc: ServiceSlice | undefined, fallback: string): string {
  return svc?.name || fallback;
}

function serviceDetail(svc: ServiceSlice | undefined): string {
  const bits: string[] = [];
  if (svc?.detail) bits.push(svc.detail);
  if (svc?.last_seen_at) bits.push(`last seen ${formatAge(svc.last_seen_at)}`);
  return bits.join(" · ");
}

function rowState(
  svc: ServiceSlice | undefined,
  restartingThis: boolean,
  restartState?: RestartProgress["control"],
): { status: string; label: string } {
  if (restartingThis) {
    if (restartState === "up") return { status: "running", label: "running" };
    if (restartState === "timeout" || restartState === "error") {
      return { status: "failed", label: restartState };
    }
    if (restartState === "down") return { status: "stopped", label: "down" };
    return { status: "starting", label: "restarting" };
  }
  const status = svc?.status || "unknown";
  return { status, label: status };
}

export function ServicesPanel({ onRestarted }: Props) {
  const [status, setStatus] = useState<SystemStatus | null>(null);
  const [me, setMe] = useState<Me | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [restarting, setRestarting] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [restartTarget, setRestartTarget] = useState<RestartTarget | null>(null);
  const [restartProgress, setRestartProgress] = useState<RestartProgress | null>(null);
  const restartingRef = useRef(false);

  const load = useCallback(async () => {
    const [st, who] = await Promise.all([
      api<SystemStatus>("/api/v1/system/status"),
      api<Me>("/api/v1/me"),
    ]);
    setStatus(st);
    setMe(who);
  }, []);

  useEffect(() => {
    load().catch((e: Error) => setErr(e.message));
    const id = window.setInterval(() => {
      if (restartingRef.current) return;
      api<SystemStatus>("/api/v1/system/status")
        .then(setStatus)
        .catch(() => {
          /* keep last snapshot */
        });
    }, POLL_MS);
    return () => window.clearInterval(id);
  }, [load]);

  const busy = restarting || installing;
  const canRestart = Boolean(me?.admin) && Boolean(status?.hostctl) && !busy;
  const restartHint = !me?.admin
    ? "Admin required to restart"
    : !status?.hostctl
      ? "Install temperci-hostctl to restart from the dashboard"
      : undefined;

  const agentMissing = status?.agent?.installed === false || status?.agent?.status === "not_installed";
  const canInstall =
    Boolean(me?.admin) && Boolean(status?.agent?.installable) && !busy;
  const installHint = !me?.admin
    ? "Admin required to install the agent"
    : status?.agent?.install_hint || restartHint;

  const restart = async (target: RestartTarget) => {
    setMsg(null);
    setErr(null);
    setRestarting(true);
    restartingRef.current = true;
    setRestartTarget(target);
    setRestartProgress({
      control: target === "agent" ? "idle" : "restarting",
      agent: target === "control" ? "idle" : "restarting",
      done: false,
    });
    try {
      await api<{ reconnect?: boolean }>("/api/v1/system/restart", {
        method: "POST",
        body: JSON.stringify({ target }),
      });
      const progress = await waitForServicesReady(target, setRestartProgress);
      if (progress.done) {
        setMsg(
          target === "all"
            ? "Control and agent restarted successfully."
            : target === "control"
              ? "Control restarted successfully."
              : "Agent restarted successfully.",
        );
        await load();
        onRestarted?.();
      } else {
        setErr(progress.error || "Restart did not complete cleanly.");
      }
    } catch (e) {
      setErr((e as Error).message);
      setRestartProgress((p) =>
        p
          ? {
              ...p,
              control: p.control === "restarting" ? "error" : p.control,
              agent: p.agent === "restarting" ? "error" : p.agent,
              error: (e as Error).message,
            }
          : p,
      );
    } finally {
      setRestarting(false);
      restartingRef.current = false;
    }
  };

  const installAgent = async () => {
    setMsg(null);
    setErr(null);
    setInstalling(true);
    restartingRef.current = true;
    setRestartTarget("agent");
    setRestartProgress({
      control: "idle",
      agent: "restarting",
      done: false,
    });
    try {
      await api("/api/v1/system/install", {
        method: "POST",
        body: JSON.stringify({ target: "agent" }),
      });
      setMsg("Agent installed. Waiting for it to come up…");
      const progress = await waitForServicesReady("agent", setRestartProgress);
      if (progress.done) {
        setMsg("Agent installed and registered.");
        await load();
        onRestarted?.();
      } else {
        setErr(progress.error || "Agent installed, but it did not become ready.");
        await load();
      }
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setInstalling(false);
      restartingRef.current = false;
    }
  };

  if (err && !status) return <p className="text-sm text-destructive">{err}</p>;
  if (!status) return <p className="text-sm text-muted-foreground">Loading services…</p>;

  const controlBusy = restarting && (restartTarget === "all" || restartTarget === "control");
  const agentBusy = restarting && (restartTarget === "all" || restartTarget === "agent");
  const control = rowState(status.control, controlBusy, restartProgress?.control);
  const agent = rowState(status.agent, agentBusy, restartProgress?.agent);
  const overall = restarting ? "starting" : status.overall || "unknown";

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between">
        <CardTitle>Services</CardTitle>
        <div className="flex flex-wrap gap-2">
          <StatusBadge tone={toneFromClass(serviceBadgeClass(overall))}>
            <span className="size-1.5 rounded-full bg-current" aria-hidden />
            {restarting ? "restarting" : overallLabel(overall)}
          </StatusBadge>
          <StatusBadge tone={status.hostctl ? "ok" : "warn"}>
            hostctl {status.hostctl ? "available" : "missing"}
          </StatusBadge>
        </div>
      </CardHeader>
      <CardContent>
      <div className="flex flex-col">
        <ServiceRow
          title={serviceTitle(status.control, "temperci-control.service")}
          detail={serviceDetail(status.control)}
          status={control.status}
          label={control.label}
          restarting={controlBusy && restartProgress?.control === "restarting"}
          canRestart={canRestart}
          restartHint={restartHint}
          onRestart={() => void restart("control")}
        />
        <ServiceRow
          title={serviceTitle(status.agent, "temperci-agent.service")}
          detail={serviceDetail(status.agent)}
          status={agent.status}
          label={agentMissing ? "not installed" : agent.label}
          restarting={
            (agentBusy && restartProgress?.agent === "restarting") || installing
          }
          canRestart={agentMissing ? canInstall : canRestart}
          restartHint={agentMissing ? installHint : restartHint}
          actionLabel={agentMissing ? (installing ? "Installing" : "Install") : undefined}
          onRestart={() => void (agentMissing ? installAgent() : restart("agent"))}
        />
      </div>

      <div className="mt-3 flex flex-wrap items-center gap-3 border-t pt-3.5">
        {agentMissing ? (
          <Button type="button" disabled={!canInstall} title={installHint} onClick={() => void installAgent()}>
            {installing ? (
              <>
                <Loader2Icon className="size-3 animate-spin" /> Installing agent…
              </>
            ) : (
              "Install agent"
            )}
          </Button>
        ) : null}
        <Button
          type="button"
          variant="destructive"
          disabled={!canRestart}
          title={restartHint}
          onClick={() => void restart("all")}
        >
          {restarting && restartTarget === "all" ? (
            <>
              <Loader2Icon className="size-3 animate-spin" /> Restarting…
            </>
          ) : (
            "Restart all"
          )}
        </Button>
        {!status.hostctl ? (
          <span className="text-xs text-muted-foreground">
            Restart from the host:{" "}
            <code className="font-mono text-xs">systemctl restart temperci-control temperci-agent</code>
          </span>
        ) : null}
      </div>

      <RestartStatus
        active={Boolean(restartProgress)}
        target={restartTarget}
        progress={restartProgress}
      />
      {msg ? <p className="mt-3 text-sm text-emerald-400">{msg}</p> : null}
      {err ? <p className="mt-3 text-sm text-destructive">{err}</p> : null}
      </CardContent>
    </Card>
  );
}

function ServiceRow({
  title,
  detail,
  status,
  label,
  restarting,
  canRestart,
  restartHint,
  actionLabel,
  onRestart,
}: {
  title: string;
  detail: string;
  status: string;
  label: string;
  restarting: boolean;
  canRestart: boolean;
  restartHint?: string;
  actionLabel?: string;
  onRestart: () => void;
}) {
  const btn = actionLabel || (restarting ? "Restarting" : "Restart");
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-t py-3 first:border-t-0 first:pt-0 sm:grid-cols-[minmax(0,1fr)_auto_auto]">
      <div>
        <div className="font-mono text-[13px] font-semibold">{title}</div>
        {detail ? <div className="mt-0.5 text-xs text-muted-foreground">{detail}</div> : null}
      </div>
      <StatusBadge tone={toneFromClass(serviceBadgeClass(status))} className="min-w-[7.5rem] justify-start">
        {restarting ? (
          <Loader2Icon className="size-3 animate-spin" />
        ) : (
          <span className="size-1.5 rounded-full bg-current" aria-hidden />
        )}
        {label}
      </StatusBadge>
      <Button
        type="button"
        variant={actionLabel && !restarting ? "default" : "secondary"}
        size="sm"
        disabled={!canRestart}
        title={restartHint}
        onClick={onRestart}
        className="col-span-2 justify-self-start sm:col-span-1"
      >
        {restarting ? (
          <>
            <Loader2Icon className="size-3 animate-spin" /> {actionLabel || "Restarting"}
          </>
        ) : (
          btn
        )}
      </Button>
    </div>
  );
}

function toneFromClass(cls: string): "ok" | "warn" | "bad" | "neutral" {
  if (cls === "ok") return "ok";
  if (cls === "warn") return "warn";
  if (cls === "bad") return "bad";
  return "neutral";
}
