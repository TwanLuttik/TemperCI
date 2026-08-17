import { useEffect, useState } from "react";
import { api } from "../api";
import { useRealtime, type VMRow } from "../hooks/useRealtime";

function barColor(pct: number): string {
  if (pct >= 85) return "bg-bad";
  if (pct >= 60) return "bg-warn";
  return "bg-ok";
}

function UsageBar({
  label,
  value,
  max,
  unit,
}: {
  label: string;
  value: number;
  max: number;
  unit: string;
}) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  return (
    <div className="min-w-[120px]">
      <div className="mb-1 flex justify-between font-mono text-[10px] text-dim">
        <span>{label}</span>
        <span>
          {value.toFixed(1)}
          {unit}
          {max > 0 ? ` / ${max}${unit}` : ""}
        </span>
      </div>
      <div className="h-1.5 overflow-hidden rounded-full bg-line-soft">
        <div
          className={`h-full rounded-full transition-all duration-500 ${barColor(pct)}`}
          style={{ width: `${pct}%` }}
        />
      </div>
    </div>
  );
}

export function VMsPage() {
  const rt = useRealtime(true);
  const [vms, setVms] = useState<VMRow[]>([]);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (rt.last?.vms) {
      setVms(rt.last.vms);
      return;
    }
    api<{ vms: VMRow[] }>("/api/v1/vms")
      .then((d) => setVms(d.vms || []))
      .catch((e: Error) => setErr(e.message));
  }, [rt.last]);

  if (err && vms.length === 0) return <div className="err">{err}</div>;

  return (
    <>
      <div className="page-head">
        <p className="page-kicker">/ MicroVMs</p>
        <h1>Live microVM usage</h1>
        <p className="lead">
          Host-side Firecracker process samples (CPU / RSS / instance disk). Updates over WebSocket
          when connected
          {rt.connected ? (
            <span className="badge ok ml-2">live</span>
          ) : (
            <span className="badge warn ml-2">polling REST</span>
          )}
          .
        </p>
      </div>

      <div className="panel" style={{ overflow: "auto", padding: "8px 12px 4px" }}>
        <table>
          <thead>
            <tr>
              <th>Agent</th>
              <th>VM</th>
              <th>State</th>
              <th>Job</th>
              <th>CPU</th>
              <th>Memory (RSS)</th>
              <th>Disk</th>
              <th>PID</th>
            </tr>
          </thead>
          <tbody>
            {vms.length === 0 ? (
              <tr>
                <td colSpan={8}>
                  <div className="empty">
                    <strong>No microVMs reported</strong>
                    Warm pool VMs appear after the agent heartbeats (every ~2s). Ensure the agent is
                    running with Firecracker instances.
                  </div>
                </td>
              </tr>
            ) : (
              vms.map((v) => (
                <tr key={`${v.agent_id}-${v.id}`}>
                  <td className="mono">{v.agent_id}</td>
                  <td>
                    <code>{v.id.slice(0, 12)}</code>
                  </td>
                  <td>
                    <span
                      className={`badge ${
                        v.state === "busy" ? "warn" : v.state === "warm" ? "ok" : ""
                      }`}
                    >
                      {v.state}
                    </span>
                  </td>
                  <td className="mono">{v.job_id || "—"}</td>
                  <td>
                    <UsageBar label="CPU" value={v.cpu_percent || 0} max={100} unit="%" />
                  </td>
                  <td>
                    <UsageBar
                      label="RSS"
                      value={v.rss_mib || 0}
                      max={v.memory_mib || 0}
                      unit="MiB"
                    />
                  </td>
                  <td className="mono">
                    {v.disk_mib != null ? `${v.disk_mib.toFixed(1)} MiB` : "—"}
                  </td>
                  <td className="mono">{v.pid || "—"}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </>
  );
}
