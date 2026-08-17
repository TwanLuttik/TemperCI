import { useEffect, useState } from "react";
import { api, type Host } from "../api";
import { useRealtime } from "../hooks/useRealtime";

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
          Capacity-aware hosts in your fleet. Warm pool size is the main lever for pickup latency.{" "}
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
              <th>Warm</th>
              <th>Busy</th>
              <th>Last seen</th>
            </tr>
          </thead>
          <tbody>
            {hosts.length === 0 ? (
              <tr>
                <td colSpan={6}>
                  <div className="empty">
                    <strong>No agents registered</strong>
                    Start temperci-agent and confirm agent_token matches control.
                  </div>
                </td>
              </tr>
            ) : (
              hosts.map((h) => (
                <tr key={h.agent_id}>
                  <td>
                    <code>{h.agent_id}</code>
                  </td>
                  <td className="mono">{h.capacity ?? "—"}</td>
                  <td className="mono">{h.max_capacity ?? "—"}</td>
                  <td className="mono">{h.warm ?? 0}</td>
                  <td className="mono">{h.busy ?? 0}</td>
                  <td style={{ color: "var(--muted)" }}>
                    {h.last_seen_at ? new Date(h.last_seen_at).toLocaleString() : "—"}
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
