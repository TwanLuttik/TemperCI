import { Fragment, useCallback, useEffect, useState } from "react";
import { toast } from "sonner";

import { ChevronRight } from "lucide-react";

import { api, formatBytes, type CacheClearResponse, type CacheEntry, type CacheInventory, type Me } from "../api";
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
  const [open, setOpen] = useState<Record<string, boolean>>({});

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
      keys: r.keys || [],
    })),
  );

  const toggle = (id: string) => setOpen((cur) => ({ ...cur, [id]: !cur[id] }));

  return (
    <>
      <PageHeader
        kicker="/ Cache"
        title="Actions cache"
        description={
          <>
            Host-local <code className="font-mono text-xs">actions/cache</code> storage. Expand a repo
            to see how size is split across keys. Clear queues a purge the agent applies on its next
            heartbeat.
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
              rows.map((r) => {
                const id = `${r.agent}:${r.repo}`;
                const expanded = Boolean(open[id]);
                const canExpand = r.keys.length > 0;
                return (
                  <Fragment key={id}>
                    <TableRow>
                      <TableCell className="font-mono text-xs">
                        {canExpand ? (
                          <button
                            type="button"
                            className="inline-flex max-w-full items-center gap-1 text-left"
                            onClick={() => toggle(id)}
                            aria-expanded={expanded}
                          >
                            <ChevronRight
                              className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", expanded && "rotate-90")}
                            />
                            <span className="truncate">{r.repo}</span>
                          </button>
                        ) : (
                          r.repo
                        )}
                      </TableCell>
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
                    {expanded && canExpand ? (
                      <TableRow className="hover:bg-transparent">
                        <TableCell colSpan={admin ? 6 : 5} className="bg-muted/30 py-3">
                          <KeyBreakdown keys={r.keys} total={r.bytes} />
                        </TableCell>
                      </TableRow>
                    ) : null}
                  </Fragment>
                );
              })
            )}
          </TableBody>
        </Table>
      </Card>
    </>
  );
}

function shortVersion(v?: string): string {
  if (!v) return "";
  if (v.length <= 14) return v;
  return `${v.slice(0, 10)}…`;
}

function KeyBreakdown({ keys, total }: { keys: CacheEntry[]; total: number }) {
  return (
    <div className="space-y-2">
      <div className="font-mono text-[10px] tracking-wider text-muted-foreground uppercase">
        Size by cache key
      </div>
      {keys.map((k, i) => {
        const pct = usagePct(k.bytes, total);
        return (
          <div key={`${k.key}:${k.version || ""}:${i}`} className="grid items-center gap-2 sm:grid-cols-[minmax(0,1fr)_88px_64px]">
            <div className="min-w-0">
              <div className="truncate font-mono text-xs text-foreground" title={k.key}>
                {k.key}
              </div>
              <div className="mt-0.5 flex items-center gap-2">
                <Progress value={pct} className="h-1" />
                <span className="shrink-0 font-mono text-[10px] text-muted-foreground">{Math.round(pct)}%</span>
              </div>
            </div>
            <div className="font-mono text-[11px] text-muted-foreground" title={k.version || undefined}>
              {k.version ? shortVersion(k.version) : "—"}
            </div>
            <div className="text-right font-mono text-xs">{formatBytes(k.bytes)}</div>
          </div>
        );
      })}
    </div>
  );
}
