import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api, formatBytes, formatDuration, type Overview } from "../api";
import { useRealtime } from "../hooks/useRealtime";

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

  if (err) return <div className="err">{err}</div>;
  if (!o) return <div className="loading">Loading overview…</div>;

  return (
    <>
      <div className="page-head">
        <p className="page-kicker">/ Console</p>
        <h1>GitHub Actions, on your hardware</h1>
        <p className="lead">
          Live capacity and job flow across TemperCI agents — spot stuck hosts and empty warm pools
          quickly.{" "}
          {rt.connected ? (
            <span className="badge ok">websocket live</span>
          ) : (
            <span className="badge warn">rest</span>
          )}
        </p>
      </div>
      <div className="grid">
        <div className="stat">
          <div className="label">Agents</div>
          <div className="value">{o.agents_registered}</div>
          <div className="hint">registered hosts</div>
        </div>
        <div className="stat">
          <div className="label">Warm</div>
          <div className="value">{o.warm}</div>
          <div className="hint">ready microVMs</div>
        </div>
        <div className="stat">
          <div className="label">Busy</div>
          <div className="value">{o.busy}</div>
          <div className="hint">running jobs</div>
        </div>
        <div className="stat">
          <div className="label">Queued</div>
          <div className="value">{o.jobs_pending}</div>
          <div className="hint">awaiting claim</div>
        </div>
        <div className="stat">
          <div className="label">Started</div>
          <div className="value">{o.jobs_started}</div>
          <div className="hint">in flight</div>
        </div>
        <div className="stat">
          <div className="label">Finished</div>
          <div className="value">{o.jobs_finished}</div>
          <div className="hint">completed in memory</div>
        </div>
        <div className="stat">
          <div className="label">Run p50</div>
          <div className="value">{formatDuration(o.run_p50_ms)}</div>
          <div className="hint">last 100 finished</div>
        </div>
        <div className="stat">
          <div className="label">Run p95</div>
          <div className="value">{formatDuration(o.run_p95_ms)}</div>
          <div className="hint">last 100 finished</div>
        </div>
        <div className="stat">
          <div className="label">Cache</div>
          <div className="value">
            {(o.cache_hits ?? 0) + (o.cache_misses ?? 0) === 0
              ? "—"
              : `${o.cache_hits ?? 0}/${(o.cache_hits ?? 0) + (o.cache_misses ?? 0)}`}
          </div>
          <div className="hint">
            hits / lookups
            {o.cache_bytes ? ` · ${formatBytes(o.cache_bytes)} on disk` : ""}
          </div>
        </div>
      </div>
      <div className="split">
        <div className="panel">
          <div className="panel-head">
            <h2>Fleet health</h2>
            <span className="meta">org · {o.org || "—"}</span>
          </div>
          <p style={{ margin: "0 0 12px", color: "var(--muted)" }}>
            {o.fleet_ready
              ? "Control plane is minting JIT configs and assigning work to agents."
              : "Setup incomplete or GitHub client not ready — finish wizard / config first."}
          </p>
          <div className="row">
            <span className={`badge ${o.fleet_ready ? "ok" : "warn"}`}>
              {o.fleet_ready ? "ready" : "limited"}
            </span>
            <span className={`badge ${o.hostctl_configured ? "ok" : ""}`}>
              hostctl {o.hostctl_configured ? "on" : "off"}
            </span>
            <span className="badge">{o.jobs_failed || 0} failed</span>
          </div>
        </div>
        <div className="panel">
          <div className="panel-head">
            <h2>Next actions</h2>
            <span className="meta">operator</span>
          </div>
          <div className="row">
            <button type="button" className="ghost" onClick={() => navigate("/hosts")}>
              View runners
            </button>
            <button type="button" className="ghost" onClick={() => navigate("/vms")}>
              MicroVM usage
            </button>
            <button type="button" className="ghost" onClick={() => navigate("/jobs")}>
              View jobs
            </button>
            <button type="button" className="ghost" onClick={() => navigate("/cache")}>
              View cache
            </button>
            <button type="button" className="secondary" onClick={() => navigate("/settings")}>
              Settings
            </button>
          </div>
        </div>
      </div>
    </>
  );
}
