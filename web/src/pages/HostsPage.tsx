import { useEffect, useState } from "react";

import { api, type Host } from "../api";
import { EmptyState } from "../components/empty-state";
import { PageHeader } from "../components/page-header";
import { LiveDot } from "../components/status-badge";
import { useRealtime } from "../hooks/useRealtime";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

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

  if (err) return <p className="text-sm text-destructive">{err}</p>;

  return (
    <>
      <PageHeader
        kicker="/ Runners"
        title="Host agents"
        description={
          <span className="inline-flex flex-wrap items-center gap-2">
            Capacity-aware hosts. Warm pool size is the main lever for pickup latency.
            <LiveDot live={rt.connected} />
          </span>
        }
      />
      <Card className="py-2">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Agent</TableHead>
              <TableHead>Free</TableHead>
              <TableHead>Max</TableHead>
              <TableHead>Warm</TableHead>
              <TableHead>Busy</TableHead>
              <TableHead>Last seen</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {hosts.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6}>
                  <EmptyState title="No agents registered">
                    Start temperci-agent and confirm agent_token matches control.
                  </EmptyState>
                </TableCell>
              </TableRow>
            ) : (
              hosts.map((h) => (
                <TableRow key={h.agent_id}>
                  <TableCell>
                    <code className="font-mono text-xs">{h.agent_id}</code>
                  </TableCell>
                  <TableCell className="font-mono text-xs">{h.capacity ?? "—"}</TableCell>
                  <TableCell className="font-mono text-xs">{h.max_capacity ?? "—"}</TableCell>
                  <TableCell className="font-mono text-xs">{h.warm ?? 0}</TableCell>
                  <TableCell className="font-mono text-xs">{h.busy ?? 0}</TableCell>
                  <TableCell className="text-muted-foreground">
                    {h.last_seen_at ? new Date(h.last_seen_at).toLocaleString() : "—"}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>
    </>
  );
}
