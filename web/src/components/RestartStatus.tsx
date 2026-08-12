import type { RestartProgress, RestartTarget } from "../api";

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

  const badge =
    state === "up"
      ? "ok"
      : state === "restarting"
        ? "warn"
        : state === "idle"
          ? ""
          : "bad";

  return (
    <div className="restart-row">
      <span className="restart-name mono">{name}</span>
      <span className={`badge ${badge} restart-badge`}>
        {state === "restarting" ? <span className="spinner" aria-hidden /> : null}
        {state === "up" ? <span className="check" aria-hidden /> : null}
        {label}
      </span>
    </div>
  );
}

export function RestartStatus({ target, progress, active }: Props) {
  if (!active || !progress || !target) return null;

  const showControl = target === "all" || target === "control";
  const showAgent = target === "all" || target === "agent";
  const bothUp =
    (!showControl || progress.control === "up") &&
    (!showAgent || progress.agent === "up");

  return (
    <div className="restart-status" role="status" aria-live="polite">
      <div className="restart-status-head">
        {bothUp && progress.done ? (
          <span className="badge ok">All requested services are back</span>
        ) : (
          <span className="badge warn">
            <span className="spinner" aria-hidden />
            Waiting for services…
          </span>
        )}
      </div>
      <ServiceRow name="temperci-control" state={progress.control} show={showControl} />
      <ServiceRow name="temperci-agent" state={progress.agent} show={showAgent} />
      {progress.error ? <div className="err">{progress.error}</div> : null}
    </div>
  );
}
