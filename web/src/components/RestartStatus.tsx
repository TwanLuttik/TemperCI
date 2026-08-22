import { Loader2Icon } from "lucide-react";

import type { RestartProgress, RestartTarget } from "../api";
import { StatusBadge, type Tone } from "./status-badge";

type Props = {
  target: RestartTarget | null;
  progress: RestartProgress | null;
  active: boolean;
};

function ServiceRow({
  name,
  state,
  show,
}: {
  name: string;
  state: RestartProgress["control"];
  show: boolean;
}) {
  if (!show) return null;

  const label =
    state === "restarting"
      ? "restarting…"
      : state === "up"
        ? "restarted successfully"
        : state === "down"
          ? "down"
          : state === "timeout"
            ? "timeout"
            : state === "error"
              ? "error"
              : "idle";

  const tone: Tone =
    state === "up" ? "ok" : state === "restarting" ? "warn" : state === "idle" ? "neutral" : "bad";

  return (
    <div className="flex items-center justify-between gap-3 border-t border-border py-2">
      <span className="font-mono text-xs text-muted-foreground">{name}</span>
      <StatusBadge tone={tone} className="min-w-[11rem] justify-start">
        {state === "restarting" ? <Loader2Icon className="size-3 animate-spin" /> : null}
        {label}
      </StatusBadge>
    </div>
  );
}

export function RestartStatus({ target, progress, active }: Props) {
  if (!active || !progress || !target) return null;

  const showControl = target === "all" || target === "control";
  const showAgent = target === "all" || target === "agent";
  const bothUp =
    (!showControl || progress.control === "up") && (!showAgent || progress.agent === "up");

  return (
    <div className="mt-4 rounded-lg border bg-card px-3.5 py-3" role="status" aria-live="polite">
      <div className="mb-2.5">
        {bothUp && progress.done ? (
          <StatusBadge tone="ok">All requested services are back</StatusBadge>
        ) : (
          <StatusBadge tone="warn">
            <Loader2Icon className="size-3 animate-spin" />
            Waiting for services…
          </StatusBadge>
        )}
      </div>
      <ServiceRow name="temperci-control" state={progress.control} show={showControl} />
      <ServiceRow name="temperci-agent" state={progress.agent} show={showAgent} />
      {progress.error ? <p className="mt-2 text-sm text-destructive">{progress.error}</p> : null}
    </div>
  );
}
