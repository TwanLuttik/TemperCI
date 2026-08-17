import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api, formatDuration, type JobDetail } from "../api";

function statusBadge(status?: string) {
  const s = String(status || "").toLowerCase();
  if (["finished", "success", "ok"].includes(s)) return "ok";
  if (["failed", "error", "failure", "timeout", "cancelled"].includes(s)) return "bad";
  if (["started", "assigned", "minted", "pending"].includes(s)) return "warn";
  return "";
}

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
  const [tab, setTab] = useState<"events" | "agent" | "runner" | "console">("events");

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

  if (err) return <div className="err">{err}</div>;
  if (!data) return <div className="loading">Loading job…</div>;

  const j = data.job;
  const logs = data.logs || {};
  const events = logs.events || [];
  const running = !["finished", "failed"].includes(String(j.status || "").toLowerCase());
  const gh =
    j.repo_full_name && j.run_id
      ? `https://github.com/${j.repo_full_name}/actions/runs/${j.run_id}`
      : null;

  const pane =
    tab === "agent"
      ? logs.agent_log
      : tab === "runner"
        ? logs.runner_log
        : tab === "console"
          ? logs.console_log
          : "";

  return (
    <>
      <div className="page-head">
        <p className="page-kicker">
          <Link to="/jobs">/ Jobs</Link> · {j.job_id}
        </p>
        <h1>Job {j.job_id}</h1>
        <p className="lead">
          {j.repo_full_name || "unknown repo"} · {j.runner_name || "runner"}
          {running ? " · refreshing every 2s" : ""}
        </p>
      </div>

      <div className="grid" style={{ marginBottom: 18 }}>
        <div className="stat">
          <div className="label">Status</div>
          <div className="hint" style={{ marginTop: 10 }}>
            <span className={`badge ${statusBadge(j.status)}`}>{j.status}</span>
          </div>
        </div>
        <div className="stat">
          <div className="label">Outcome</div>
          <div className="hint" style={{ marginTop: 10 }}>
            {j.outcome ? <span className={`badge ${statusBadge(j.outcome)}`}>{j.outcome}</span> : "—"}
          </div>
        </div>
        <div className="stat">
          <div className="label">Agent / VM</div>
          <div className="hint mono">
            {j.assigned_agent_id || "—"}
            {j.vm_id ? ` / ${j.vm_id}` : ""}
          </div>
        </div>
        <div className="stat">
          <div className="label">Queue</div>
          <div className="value">{formatDuration(j.queue_ms)}</div>
          <div className="hint">created → assigned</div>
        </div>
        <div className="stat">
          <div className="label">Bind</div>
          <div className="value">{formatDuration(j.bind_ms)}</div>
          <div className="hint">assigned → started</div>
        </div>
        <div className="stat">
          <div className="label">Run</div>
          <div className="value">{formatDuration(j.run_ms)}</div>
          <div className="hint">started → finished</div>
        </div>
        <div className="stat">
          <div className="label">Total</div>
          <div className="value">{formatDuration(j.total_ms)}</div>
          <div className="hint">created → finished</div>
        </div>
        <div className="stat">
          <div className="label">Cache</div>
          <div className="value">
            {(j.cache_hits ?? 0) + (j.cache_misses ?? 0) === 0
              ? "—"
              : `${j.cache_hits ?? 0} hit / ${j.cache_misses ?? 0} miss`}
          </div>
          <div className="hint">
            {j.cache_bytes_in || j.cache_bytes_out
              ? `${Math.round(((j.cache_bytes_in ?? 0) + (j.cache_bytes_out ?? 0)) / 1024)} KiB`
              : "local actions/cache"}
          </div>
        </div>
        <div className="stat">
          <div className="label">GitHub</div>
          <div className="hint">
            {gh ? (
              <a href={gh} target="_blank" rel="noreferrer">
                actions run {j.run_id}
              </a>
            ) : (
              "—"
            )}
          </div>
        </div>
      </div>

      {j.error ? <div className="err" style={{ marginBottom: 16 }}>{j.error}</div> : null}

      <div className="panel" style={{ padding: "14px 16px 18px" }}>
        <div className="log-tabs">
          <button type="button" className={tab === "events" ? "" : "secondary"} onClick={() => setTab("events")}>
            Timeline ({events.length})
          </button>
          <button type="button" className={tab === "agent" ? "" : "secondary"} onClick={() => setTab("agent")}>
            Guest agent
          </button>
          <button type="button" className={tab === "runner" ? "" : "secondary"} onClick={() => setTab("runner")}>
            actions/runner
          </button>
          <button type="button" className={tab === "console" ? "" : "secondary"} onClick={() => setTab("console")}>
            Serial console
          </button>
        </div>

        {tab === "events" ? (
          events.length === 0 ? (
            <div className="empty">
              <strong>No events yet</strong>
              Dispatch a workflow with runs-on: temperci-… then this timeline fills as the webhook,
              claim, bind, and runner complete.
            </div>
          ) : (
            <ol className="event-list">
              {events.map((e, i) => (
                <li key={`${e.time}-${i}`}>
                  <span className="mono dim">{fmt(e.time)}</span>
                  <span className={`badge ${e.level === "error" || e.level === "warn" ? "bad" : ""}`}>
                    {e.source}
                  </span>
                  <span>{e.message}</span>
                </li>
              ))}
            </ol>
          )
        ) : pane ? (
          <pre className="log-pre">{pane}</pre>
        ) : (
          <div className="empty">
            <strong>No {tab} log yet</strong>
            Logs upload when the guest runner exits (or if the job fails). Keep this page open.
          </div>
        )}
      </div>

      <p className="lead" style={{ marginTop: 16, fontSize: 12 }}>
        Created {fmt(j.created_at)} · started {fmt(j.started_at)} · finished {fmt(j.finished_at)}
        {j.warm_bind ? " · warm bind" : ""}
        {j.labels?.length ? ` · ${(j.labels || []).join(", ")}` : ""}
      </p>
    </>
  );
}
