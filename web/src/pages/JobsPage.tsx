import { useEffect, useState } from "react";
import { api, type Job } from "../api";

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

  useEffect(() => {
    api<{ jobs: Job[] }>("/api/v1/jobs")
      .then((d) => setJobs(d.jobs || []))
      .catch((e: Error) => setErr(e.message));
  }, []);

  if (err) return <div className="err">{err}</div>;

  return (
    <>
      <div className="page-head">
        <p className="page-kicker">/ Jobs</p>
        <h1>Recent assignments</h1>
        <p className="lead">
          In-memory control-plane view (resets on restart). Spot failing and stuck jobs across the
          fleet.
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
              <th>Labels</th>
              <th>Outcome</th>
            </tr>
          </thead>
          <tbody>
            {jobs.length === 0 ? (
              <tr>
                <td colSpan={6}>
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
                    <code>{j.job_id}</code>
                  </td>
                  <td>{j.repo_full_name || "—"}</td>
                  <td>
                    <span className={`badge ${statusBadge(j.status)}`}>{j.status}</span>
                  </td>
                  <td className="mono">{j.assigned_agent_id || "—"}</td>
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
