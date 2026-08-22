import { useEffect, useState } from "react";

import { api } from "../api";
import { EmptyState } from "../components/empty-state";
import { PageHeader } from "../components/page-header";
import { LiveDot, StatusBadge } from "../components/status-badge";
import { useRealtime, type VMRow } from "../hooks/useRealtime";
import { Card } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { cn } from "@/lib/utils";

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
      <div className="mb-1 flex justify-between font-mono text-[10px] text-muted-foreground">
        <span>{label}</span>
        <span>
          {value.toFixed(1)}
          {unit}
          {max > 0 ? ` / ${max}${unit}` : ""}
        </span>
      </div>
      <Progress
        value={pct}
        className={cn(
          "h-1.5",
          pct >= 85 ? "*:data-[slot=progress-indicator]:bg-red-400" : pct >= 60 ? "*:data-[slot=progress-indicator]:bg-amber-400" : "",
        )}
      />
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

  if (err && vms.length === 0) return <p className="text-sm text-destructive">{err}</p>;

  return (
    <>
      <PageHeader
        kicker="/ MicroVMs"
        title="Live microVM usage"
        description={
          <span className="inline-flex flex-wrap items-center gap-2">
            Host-side Firecracker samples (CPU / RSS / disk).
            <LiveDot live={rt.connected} />
          </span>
        }
      />
      <Card className="py-2">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Agent</TableHead>
              <TableHead>VM</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Job</TableHead>
              <TableHead>CPU</TableHead>
              <TableHead>Memory (RSS)</TableHead>
              <TableHead>Disk</TableHead>
              <TableHead>PID</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {vms.length === 0 ? (
              <TableRow>
                <TableCell colSpan={8}>
                  <EmptyState title="No microVMs reported">
                    Warm pool VMs appear after the agent heartbeats. Ensure Firecracker is running.
                  </EmptyState>
                </TableCell>
              </TableRow>
            ) : (
              vms.map((v) => (
                <TableRow key={`${v.agent_id}-${v.id}`}>
                  <TableCell className="font-mono text-xs">{v.agent_id}</TableCell>
                  <TableCell>
                    <code className="font-mono text-xs">{v.id.slice(0, 12)}</code>
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={v.state} />
                  </TableCell>
                  <TableCell className="font-mono text-xs">{v.job_id || "—"}</TableCell>
                  <TableCell>
                    <UsageBar label="CPU" value={v.cpu_percent || 0} max={100} unit="%" />
                  </TableCell>
                  <TableCell>
                    <UsageBar label="RSS" value={v.rss_mib || 0} max={v.memory_mib || 0} unit="MiB" />
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {v.disk_mib != null ? `${v.disk_mib.toFixed(1)} MiB` : "—"}
                  </TableCell>
                  <TableCell className="font-mono text-xs">{v.pid || "—"}</TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>
    </>
  );
}
