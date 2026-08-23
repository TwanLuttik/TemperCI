import { useEffect, useRef, useState } from "react";
import type { Host, Job, Overview } from "../api";

export type VMRow = {
  agent_id: string;
  id: string;
  state: string;
  job_id?: string;
  vcpus: number;
  memory_mib: number;
  pid?: number;
  cpu_percent: number;
  rss_mib: number;
  disk_mib?: number;
  created_at?: string;
};

export type RealtimeSnapshot = {
  type: string;
  time?: string;
  overview?: Partial<Overview> & { ws_clients?: number };
  hosts?: Host[];
  jobs?: Job[];
  vms?: VMRow[];
};

export type RealtimeStatus = "connecting" | "live" | "rest";

export type RealtimeState = {
  connected: boolean;
  status: RealtimeStatus;
  last?: RealtimeSnapshot;
  error?: string;
};

function wsURL(): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${window.location.host}/api/v1/ws`;
}

/**
 * Subscribe to control-plane WebSocket snapshots for live dashboard data.
 * Falls back silently when the socket cannot connect (pages still use REST).
 */
export function useRealtime(enabled = true): RealtimeState {
  const [state, setState] = useState<RealtimeState>({
    connected: false,
    status: enabled ? "connecting" : "rest",
  });
  const retryRef = useRef(0);
  const wsRef = useRef<WebSocket | null>(null);

  useEffect(() => {
    if (!enabled) {
      setState((s) => ({ ...s, connected: false, status: "rest" }));
      return;
    }
    setState((s) => ({ ...s, connected: false, status: "connecting" }));

    let closed = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const connect = () => {
      if (closed) return;
      try {
        const ws = new WebSocket(wsURL());
        wsRef.current = ws;

        ws.onopen = () => {
          retryRef.current = 0;
          setState((s) => ({ ...s, connected: true, status: "live", error: undefined }));
        };

        ws.onmessage = (ev) => {
          try {
            const data = JSON.parse(String(ev.data)) as RealtimeSnapshot;
            if (data.type === "snapshot" || data.type === "hello") {
              if (data.type === "snapshot") {
                setState({ connected: true, status: "live", last: data });
              }
            }
          } catch {
            /* ignore malformed */
          }
        };

        ws.onerror = () => {
          setState((s) => ({ ...s, connected: false, status: "connecting", error: "websocket error" }));
        };

        ws.onclose = () => {
          setState((s) => ({ ...s, connected: false, status: "connecting" }));
          wsRef.current = null;
          if (closed) return;
          const delay = Math.min(10_000, 500 * 2 ** retryRef.current);
          retryRef.current += 1;
          timer = setTimeout(connect, delay);
        };
      } catch (e) {
        setState({ connected: false, status: "connecting", error: (e as Error).message });
        timer = setTimeout(connect, 2000);
      }
    };

    connect();

    return () => {
      closed = true;
      if (timer) clearTimeout(timer);
      wsRef.current?.close();
      wsRef.current = null;
    };
  }, [enabled]);

  return state;
}
