import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";

import { api, formatBytes, type CacheClearResponse, type CacheInventory, type Me } from "../api";
import { EmptyState } from "../components/empty-state";
import { PageHeader } from "../components/page-header";
import { StatCard } from "../components/stat-card";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { cn } from "@/lib/utils";

function usagePct(bytes: number, max: number): number {
  if (max <= 0) return 0;
  return Math.min(100, (bytes / max) * 100);
}

export function CachePage() {
  const [inv, setInv] = useState<CacheInventory | null>(null);
  const [me, setMe] = useState<Me | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    const [c, who] = await Promise.all([
      api<CacheInventory>("/api/v1/cache"),
      api<Me>("/api/v1/me").catch(() => null),
    ]);
    setInv(c);
    setMe(who);
  }, []);

  useEffect(() => {
    let stop = false;
    const tick = () => {
      load()
        .then(() => {
          if (!stop) setErr(null);
        })
        .catch((e: Error) => {
          if (!stop) setErr(e.message);
        });
    };
    tick();
    const t = setInterval(tick, 4000);
    return () => {
      stop = true;
      clearInterval(t);
    };
  }, [load]);

  const admin = Boolean(me?.admin || me?.open);

  const clear = async (agentId?: string, repo?: string) => {
    const target = repo
      ? `${repo}${agentId ? ` on ${agentId}` : " on all hosts"}`
      : agentId
        ? `all cache on ${agentId}`
        : "ALL cache on every host";
    if (!window.confirm(`Clear ${target}? Jobs currently saving cache may fail. This cannot be undone.`)) {
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      const res = await api<CacheClearResponse>("/api/v1/cache/clear", {
        method: "POST",
        body: JSON.stringify({ agent_id: agentId || "", repo: repo || "" }),
      });
      toast.success(`Queued clear on ${res.queued} host${res.queued === 1 ? "" : "s"}.`);
      await load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  if (err && !inv) return <p className="text-sm text-destructive">{err}</p>;
  if (!inv) return <p className="text-sm text-muted-foreground">Loading cache…</p>;

  const rows = inv.hosts.flatMap((h) =>
    (h.repos || []).map((r) => ({
      agent: h.agent_id,
      repo: r.repo,
      bytes: r.bytes,
      entries: r.entries,
      last: r.last_access,
    })),
  );

  return (
    <>
      <PageHeader
        kicker="/ Cache"
        title="Actions cache"
        description={
          <>
            Host-local <code className="font-mono text-xs">actions/cache</code> storage. Blobs stay on
            the agent disk. Clear queues a purge the agent applies on its next heartbeat.
          </>
        }
        actions={
          admin ? (
            <Button variant="outline" disabled={busy || inv.hosts.length === 0} onClick={() => void clear()}>
              Clear all hosts
            </Button>
          ) : null
        }
      />

      {err ? <p className="mb-4 text-sm text-destructive">{err}</p> : null}

      <div className="mb-6 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatCard
          label="Used"
          value={formatBytes(inv.bytes)}
          hint={inv.max_bytes ? `of ${formatBytes(inv.max_bytes)} LRU cap` : "reported by agents"}
        />
        <StatCard label="Entries" value={inv.entries} hint="finalized cache keys" />
        <StatCard label="Repos" value={inv.repos} hint="namespaces across hosts" />
        <StatCard label="Hosts" value={inv.hosts.length} hint="agents reporting inventory" />
      </div>

      {inv.hosts.map((h) => {
        const pct = usagePct(h.bytes, h.max_bytes);
        return (
          <Card key={h.agent_id} className="mb-4">
            <CardHeader className="flex-row items-center justify-between">
              <CardTitle className="font-mono text-sm">{h.agent_id}</CardTitle>
              <span className="font-mono text-[11px] text-muted-foreground">
                {h.last_seen_at ? new Date(h.last_seen_at).toLocaleString() : "never"}
              </span>
            </CardHeader>
            <CardContent>
              <div className="flex items-center justify-between gap-4">
                <div className="min-w-[180px] flex-1">
                  <div className="mb-1 flex justify-between font-mono text-[10px] text-muted-foreground">
                    <span>
                      {formatBytes(h.bytes)}
                      {h.max_bytes ? ` / ${formatBytes(h.max_bytes)}` : ""}
                    </span>
                    <span>{h.entries} entries</span>
                  </div>
                  <Progress
                    value={pct}
                    className={cn(
                      "h-1.5",
                      pct >= 85
                        ? "*:data-[slot=progress-indicator]:bg-red-400"
                        : pct >= 60
                          ? "*:data-[slot=progress-indicator]:bg-amber-400"
                          : "",
                    )}
                  />
                </div>
                {admin ? (
                  <Button variant="outline" size="sm" disabled={busy} onClick={() => void clear(h.agent_id)}>
                    Clear host
                  </Button>
                ) : null}
              </div>
              {(h.repos || []).length === 0 ? (
                <p className="mt-3 text-sm text-muted-foreground">No repo namespaces on this host.</p>
              ) : null}
            </CardContent>
          </Card>
        );
      })}

      <Card className="py-2">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Repository</TableHead>
              <TableHead>Host</TableHead>
              <TableHead>Entries</TableHead>
              <TableHead>Size</TableHead>
              <TableHead>Last access</TableHead>
              {admin ? <TableHead /> : null}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={admin ? 6 : 5}>
                  <EmptyState title="No cached repos">
                    Enable <code className="font-mono text-xs">cache_listen_addr</code> and run a job
                    that uses <code className="font-mono text-xs">actions/cache</code>.
                  </EmptyState>
                </TableCell>
              </TableRow>
            ) : (
              rows.map((r) => (
                <TableRow key={`${r.agent}:${r.repo}`}>
                  <TableCell className="font-mono text-xs">{r.repo}</TableCell>
                  <TableCell className="font-mono text-xs">{r.agent}</TableCell>
                  <TableCell className="font-mono text-xs">{r.entries}</TableCell>
                  <TableCell className="font-mono text-xs">{formatBytes(r.bytes)}</TableCell>
                  <TableCell className="text-muted-foreground">
                    {r.last ? new Date(r.last).toLocaleString() : "—"}
                  </TableCell>
                  {admin ? (
                    <TableCell>
                      <Button variant="ghost" size="sm" disabled={busy} onClick={() => void clear(r.agent, r.repo)}>
                        Clear
                      </Button>
                    </TableCell>
                  ) : null}
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>
    </>
  );
}
