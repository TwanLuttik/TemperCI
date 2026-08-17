import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, formatDuration, type Job } from "../api";
import { useRealtime } from "../hooks/useRealtime";

function statusBadge(status?: string) {
  const s = String(status || "").toLowerCase();
  if (["finished", "success", "ok"].includes(s)) return "ok";
  if (["failed", "error", "failure"].includes(s)) return "bad";
  if (["started", "assigned", "minted", "pending"].includes(s)) return "warn";
  return "";
}

export function JobsPage() {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const rt = useRealtime(true);

  useEffect(() => {
    if (rt.last?.jobs) {
      setJobs(rt.last.jobs);
      return;
    }
    api<{ jobs: Job[] }>("/api/v1/jobs")
      .then((d) => setJobs(d.jobs || []))
      .catch((e: Error) => setErr(e.message));
  }, [rt.last]);

  if (err) return <div className="err">{err}</div>;

  return (
    <>
      <div className="page-head">
        <p className="page-kicker">/ Jobs</p>
        <h1>Recent assignments</h1>
        <p className="lead">
          Assignments persist across control restarts. Open a job to inspect the event timeline,
          guest agent log, and official runner log after you dispatch a GitHub Actions workflow.
        </p>
      </div>
      <div className="panel" style={{ overflow: "auto", padding: "8px 12px 4px" }}>
        <table>
          <thead>
            <tr>
              <th>Job</th>
              <th>Repository</th>
              <th>Status</th>
              <th>Agent</th>
              <th>Duration</th>
              <th>Labels</th>
              <th>Outcome</th>
            </tr>
          </thead>
          <tbody>
            {jobs.length === 0 ? (
              <tr>
                <td colSpan={7}>
                  <div className="empty">
                    <strong>No jobs in memory</strong>
                    Dispatch a workflow with runs-on: temperci-…
                  </div>
                </td>
              </tr>
            ) : (
              jobs.map((j) => (
                <tr key={j.job_id}>
                  <td>
                    <Link to={`/jobs/${j.job_id}`}>
                      <code>{j.job_id}</code>
                    </Link>
                  </td>
                  <td>{j.repo_full_name || "—"}</td>
                  <td>
                    <span className={`badge ${statusBadge(j.status)}`}>{j.status}</span>
                  </td>
                  <td className="mono">{j.assigned_agent_id || "—"}</td>
                  <td className="mono" title={`queue ${formatDuration(j.queue_ms)} · bind ${formatDuration(j.bind_ms)}`}>
                    {formatDuration(j.run_ms || j.total_ms)}
                  </td>
                  <td
                    style={{
                      color: "var(--muted)",
                      maxWidth: 220,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {(j.labels || []).join(", ")}
                  </td>
                  <td>
                    {j.outcome ? (
                      <span className={`badge ${statusBadge(j.outcome)}`}>{j.outcome}</span>
                    ) : (
                      "—"
                    )}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </>
  );
}
