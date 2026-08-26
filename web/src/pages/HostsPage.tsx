import { useEffect, useState } from "react";

import { api, type Host } from "../api";
import { EmptyState } from "../components/empty-state";
import { PageHeader } from "../components/page-header";
import { LiveDot } from "../components/status-badge";
import { useRealtime } from "../hooks/useRealtime";
import { fmtMiB, hostMemoryBreakdown, type MemorySegment } from "../lib/host-memory";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { cn } from "@/lib/utils";

function segmentClass(kind: MemorySegment["kind"]): string {
  if (kind === "guest") return "bg-sky-500/80";
  if (kind === "reserve") return "bg-amber-500/70";
  return "bg-muted-foreground/25";
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

  if (err) return <p className="text-sm text-destructive">{err}</p>;

  return (
    <>
      <PageHeader
        kicker="/ Runners"
        title="Host agents"
        description={
          <span className="inline-flex flex-wrap items-center gap-2">
            Capacity is leftover job slots. Max is clamped to host RAM/disk. CPU is informational.
            <LiveDot status={rt.status} />
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
              <TableHead>RAM</TableHead>
              <TableHead>Disk</TableHead>
              <TableHead>CPU</TableHead>
              <TableHead>Warm</TableHead>
              <TableHead>Busy</TableHead>
              <TableHead>Last seen</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {hosts.length === 0 ? (
              <TableRow>
                <TableCell colSpan={9}>
                  <EmptyState title="No agents registered">
                    Start temperci-agent and confirm agent_token matches control.
                  </EmptyState>
                </TableCell>
              </TableRow>
            ) : (
              hosts.map((h) => {
                const r = h.resources;
                const configured = r?.configured_max_ready;
                const effective = r?.effective_max_ready;
                const showClamp =
                  configured != null && effective != null && configured !== effective;
                const lastAdmit = r?.last_admit_reason?.trim();
                return (
                  <TableRow key={h.agent_id}>
                    <TableCell>
                      <code className="font-mono text-xs">{h.agent_id}</code>
                      {lastAdmit ? (
                        <div className="text-xs text-muted-foreground">refused: {lastAdmit}</div>
                      ) : null}
                    </TableCell>
                    <TableCell className="font-mono text-xs">{h.capacity ?? "—"}</TableCell>
                    <TableCell>
                      {showClamp ? (
                        <>
                          <span className="font-mono text-xs">
                            {effective}/{configured}
                          </span>
                          {r?.clamp_reason ? (
                            <div className="text-xs text-muted-foreground">{r.clamp_reason}</div>
                          ) : null}
                        </>
                      ) : (
                        <span className="font-mono text-xs">{h.max_capacity ?? "—"}</span>
                      )}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {r?.ram_total_mib ? (
                        <div>
                          <div>
                            {fmtMiB(r.ram_avail_mib)}{" "}
                            <span className="text-muted-foreground">avail</span>
                          </div>
                          <div className="text-[11px] text-muted-foreground">
                            {fmtMiB(r.allocated_ram_mib)} of {fmtMiB(r.ram_total_mib)} committed
                          </div>
                        </div>
                      ) : (
                        fmtMiB(r?.ram_avail_mib)
                      )}
                    </TableCell>
                    <TableCell className="font-mono text-xs">{fmtMiB(r?.disk_free_mib)}</TableCell>
                    <TableCell className="font-mono text-xs">{r?.num_cpu ?? "—"}</TableCell>
                    <TableCell className="font-mono text-xs">{h.warm ?? 0}</TableCell>
                    <TableCell className="font-mono text-xs">{h.busy ?? 0}</TableCell>
                    <TableCell className="text-muted-foreground">
                      {h.last_seen_at ? new Date(h.last_seen_at).toLocaleString() : "—"}
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </Card>

      {hosts.map((h) => {
        const mem = hostMemoryBreakdown(h);
        if (!mem) return null;
        return (
          <Card key={`${h.agent_id}-mem`} className="mt-3">
            <CardHeader className="pb-2">
              <CardTitle className="text-sm font-medium">
                Memory on <code className="font-mono text-xs">{h.agent_id}</code>
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="flex h-3 overflow-hidden rounded-full bg-muted">
                {mem.segments.map((s) => (
                  <div
                    key={s.key}
                    title={`${s.label} · ${fmtMiB(s.mib)}`}
                    className={cn("h-full min-w-[2px]", segmentClass(s.kind))}
                    style={{ width: `${(s.mib / mem.totalMiB) * 100}%` }}
                  />
                ))}
              </div>
              <ul className="grid gap-1 text-xs text-muted-foreground sm:grid-cols-2">
                {mem.segments.map((s) => (
                  <li key={s.key} className="flex items-center gap-2">
                    <span className={cn("inline-block size-2 rounded-sm", segmentClass(s.kind))} />
                    <span>{s.label}</span>
                    <span className="ml-auto font-mono tabular-nums text-foreground">{fmtMiB(s.mib)}</span>
                  </li>
                ))}
              </ul>
              <p className="text-xs text-muted-foreground">
                Linux reports {fmtMiB(mem.availMiB)} unused (includes page cache). Another guest starts
                when leftover RAM covers it; otherwise Bind waits.
              </p>
              {mem.lastAdmit ? (
                <p className="text-xs text-amber-500">Waiting on host: {mem.lastAdmit}</p>
              ) : null}
            </CardContent>
          </Card>
        );
      })}
    </>
  );
}
