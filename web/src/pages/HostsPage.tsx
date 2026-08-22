import { useEffect, useState } from "react";
import { api, type Host } from "../api";
import { useRealtime } from "../hooks/useRealtime";

function fmtMiB(mib?: number): string {
  if (mib == null || Number.isNaN(mib)) return "—";
  if (mib >= 1024) return `${(mib / 1024).toFixed(1)} GiB`;
  return `${mib} MiB`;
}

export function HostsPage() {
  const [hosts, setHosts] = useState<Host[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const rt = useRealtime(true);

  useEffect(() => {
    if (rt.last?.hosts) {
      setHosts(rt.last.hosts);
      return;
    }
    api<{ hosts: Host[] }>("/api/v1/hosts")
      .then((d) => setHosts(d.hosts || []))
      .catch((e: Error) => setErr(e.message));
  }, [rt.last]);

  if (err) return <div className="err">{err}</div>;

  return (
    <>
      <div className="page-head">
        <p className="page-kicker">/ Runners</p>
        <h1>Host agents</h1>
        <p className="lead">
          Capacity is leftover job slots. Max is clamped to host RAM/disk. CPU is informational.{" "}
          {rt.connected ? (
            <span className="badge ok">live</span>
          ) : (
            <span className="badge warn">rest</span>
          )}
        </p>
      </div>
      <div className="panel" style={{ overflow: "auto", padding: "8px 12px 4px" }}>
        <table>
          <thead>
            <tr>
              <th>Agent</th>
              <th>Free</th>
              <th>Max</th>
              <th>RAM</th>
              <th>Disk</th>
              <th>CPU</th>
              <th>Warm</th>
              <th>Busy</th>
              <th>Last seen</th>
            </tr>
          </thead>
          <tbody>
            {hosts.length === 0 ? (
              <tr>
                <td colSpan={9}>
                  <div className="empty">
                    <strong>No agents registered</strong>
                    Start temperci-agent and confirm agent_token matches control.
                  </div>
                </td>
              </tr>
            ) : (
              hosts.map((h) => {
                const r = h.resources;
                const configured = r?.configured_max_ready;
                const effective = r?.effective_max_ready;
                const showClamp =
                  configured != null && effective != null && configured !== effective;
                const lastAdmit = r?.last_admit_reason?.trim();
                return (
                  <tr key={h.agent_id}>
                    <td>
                      <code>{h.agent_id}</code>
                      {lastAdmit ? (
                        <div style={{ color: "var(--muted)" }}>refused: {lastAdmit}</div>
                      ) : null}
                    </td>
                    <td className="mono">{h.capacity ?? "—"}</td>
                    <td>
                      {showClamp ? (
                        <>
                          <span className="mono">
                            {effective}/{configured}
                          </span>
                          {r?.clamp_reason ? (
                            <div style={{ color: "var(--muted)" }}>{r.clamp_reason}</div>
                          ) : null}
                        </>
                      ) : (
                        <span className="mono">{h.max_capacity ?? "—"}</span>
                      )}
                    </td>
                    <td className="mono">{fmtMiB(r?.ram_avail_mib)}</td>
                    <td className="mono">{fmtMiB(r?.disk_free_mib)}</td>
                    <td className="mono">{r?.num_cpu ?? "—"}</td>
                    <td className="mono">{h.warm ?? 0}</td>
                    <td className="mono">{h.busy ?? 0}</td>
                    <td style={{ color: "var(--muted)" }}>
                      {h.last_seen_at ? new Date(h.last_seen_at).toLocaleString() : "—"}
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>
    </>
  );
}
