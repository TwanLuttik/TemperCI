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
};

export type RealtimeSnapshot = {
  type: string;
  time?: string;
  overview?: Partial<Overview> & { ws_clients?: number };
  hosts?: Host[];
  jobs?: Job[];
  vms?: VMRow[];
};

export type RealtimeState = {
  connected: boolean;
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
  const [state, setState] = useState<RealtimeState>({ connected: false });
  const retryRef = useRef(0);
  const wsRef = useRef<WebSocket | null>(null);

  useEffect(() => {
    if (!enabled) return;

    let closed = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const connect = () => {
      if (closed) return;
      try {
        const ws = new WebSocket(wsURL());
        wsRef.current = ws;

        ws.onopen = () => {
          retryRef.current = 0;
          setState((s) => ({ ...s, connected: true, error: undefined }));
        };

        ws.onmessage = (ev) => {
          try {
            const data = JSON.parse(String(ev.data)) as RealtimeSnapshot;
            if (data.type === "snapshot" || data.type === "hello") {
              if (data.type === "snapshot") {
                setState({ connected: true, last: data });
              }
            }
          } catch {
            /* ignore malformed */
          }
        };

        ws.onerror = () => {
          setState((s) => ({ ...s, connected: false, error: "websocket error" }));
        };

        ws.onclose = () => {
          setState((s) => ({ ...s, connected: false }));
          wsRef.current = null;
          if (closed) return;
          const delay = Math.min(10_000, 500 * 2 ** retryRef.current);
          retryRef.current += 1;
          timer = setTimeout(connect, delay);
        };
      } catch (e) {
        setState({ connected: false, error: (e as Error).message });
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
