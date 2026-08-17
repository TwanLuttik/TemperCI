import { useCallback, useEffect, useState } from "react";
import { api, formatBytes, type CacheClearResponse, type CacheInventory, type Me } from "../api";

function usagePct(bytes: number, max: number): number {
  if (max <= 0) return 0;
  return Math.min(100, (bytes / max) * 100);
}

function barColor(pct: number): string {
  if (pct >= 85) return "bg-bad";
  if (pct >= 60) return "bg-warn";
  return "bg-ok";
}

export function CachePage() {
  const [inv, setInv] = useState<CacheInventory | null>(null);
  const [me, setMe] = useState<Me | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
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
    const target = repo ? `${repo}${agentId ? ` on ${agentId}` : " on all hosts"}` : agentId ? `all cache on ${agentId}` : "ALL cache on every host";
    if (!window.confirm(`Clear ${target}? Jobs currently saving cache may fail. This cannot be undone.`)) {
      return;
    }
    setBusy(true);
    setMsg(null);
    setErr(null);
    try {
      const res = await api<CacheClearResponse>("/api/v1/cache/clear", {
        method: "POST",
        body: JSON.stringify({ agent_id: agentId || "", repo: repo || "" }),
      });
      setMsg(`Queued clear on ${res.queued} host${res.queued === 1 ? "" : "s"}. Agents apply it on the next heartbeat (~2s).`);
      await load();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  if (err && !inv) return <div className="err">{err}</div>;
  if (!inv) return <div className="loading">Loading cache…</div>;

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
      <div className="page-head">
        <p className="page-kicker">/ Cache</p>
        <h1>Actions cache</h1>
        <p className="lead">
          Host-local <code>actions/cache</code> storage. Blobs stay on the agent disk (never in the
          guest). Clear queues a purge the agent applies on its next heartbeat.
        </p>
      </div>

      {err ? <div className="err" style={{ marginBottom: 16 }}>{err}</div> : null}
      {msg ? (
        <div className="panel" style={{ marginBottom: 16, padding: "12px 16px", color: "var(--ok)" }}>
          {msg}
        </div>
      ) : null}

      <div className="grid" style={{ marginBottom: 18 }}>
        <div className="stat">
          <div className="label">Used</div>
          <div className="value">{formatBytes(inv.bytes)}</div>
          <div className="hint">{inv.max_bytes ? `of ${formatBytes(inv.max_bytes)} LRU cap` : "reported by agents"}</div>
        </div>
        <div className="stat">
          <div className="label">Entries</div>
          <div className="value">{inv.entries}</div>
          <div className="hint">finalized cache keys</div>
        </div>
        <div className="stat">
          <div className="label">Repos</div>
          <div className="value">{inv.repos}</div>
          <div className="hint">namespaces across hosts</div>
        </div>
        <div className="stat">
          <div className="label">Hosts</div>
          <div className="value">{inv.hosts.length}</div>
          <div className="hint">agents reporting inventory</div>
        </div>
      </div>

      {admin ? (
        <div className="row" style={{ marginBottom: 18 }}>
          <button type="button" className="secondary" disabled={busy || inv.hosts.length === 0} onClick={() => void clear()}>
            Clear all hosts
          </button>
        </div>
      ) : null}

      {inv.hosts.map((h) => {
        const pct = usagePct(h.bytes, h.max_bytes);
        return (
          <div className="panel" key={h.agent_id} style={{ marginBottom: 16, padding: "14px 16px" }}>
            <div className="panel-head">
              <h2 className="mono">{h.agent_id}</h2>
              <span className="meta">
                {h.last_seen_at ? new Date(h.last_seen_at).toLocaleString() : "never"}
              </span>
            </div>
            <div className="mb-2 flex items-center justify-between gap-4">
              <div className="min-w-[180px] flex-1">
                <div className="mb-1 flex justify-between font-mono text-[10px] text-dim">
                  <span>
                    {formatBytes(h.bytes)}
                    {h.max_bytes ? ` / ${formatBytes(h.max_bytes)}` : ""}
                  </span>
                  <span>{h.entries} entries</span>
                </div>
                <div className="h-1.5 overflow-hidden rounded-full bg-line-soft">
                  <div
                    className={`h-full rounded-full ${barColor(pct)}`}
                    style={{ width: `${pct}%` }}
                  />
                </div>
              </div>
              {admin ? (
                <button
                  type="button"
                  className="secondary"
                  disabled={busy}
                  onClick={() => void clear(h.agent_id)}
                >
                  Clear host
                </button>
              ) : null}
            </div>
            {(h.repos || []).length === 0 ? (
              <p className="text-muted" style={{ margin: "8px 0 0" }}>
                No repo namespaces on this host.
              </p>
            ) : null}
          </div>
        );
      })}

      <div className="panel" style={{ overflow: "auto", padding: "8px 12px 4px" }}>
        <table>
          <thead>
            <tr>
              <th>Repository</th>
              <th>Host</th>
              <th>Entries</th>
              <th>Size</th>
              <th>Last access</th>
              {admin ? <th /> : null}
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 ? (
              <tr>
                <td colSpan={admin ? 6 : 5}>
                  <div className="empty">
                    <strong>No cached repos</strong>
                    Enable <code>cache_listen_addr</code> on the agent and run a job that uses{" "}
                    <code>actions/cache</code>.
                  </div>
                </td>
              </tr>
            ) : (
              rows.map((r) => (
                <tr key={`${r.agent}:${r.repo}`}>
                  <td className="mono">{r.repo}</td>
                  <td className="mono">{r.agent}</td>
                  <td className="mono">{r.entries}</td>
                  <td className="mono">{formatBytes(r.bytes)}</td>
                  <td style={{ color: "var(--muted)" }}>
                    {r.last ? new Date(r.last).toLocaleString() : "—"}
                  </td>
                  {admin ? (
                    <td>
                      <button
                        type="button"
                        className="ghost"
                        disabled={busy}
                        onClick={() => void clear(r.agent, r.repo)}
                      >
                        Clear
                      </button>
                    </td>
                  ) : null}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </>
  );
}