export class ApiError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ApiError";
  }
}

export function jobIsActive(status?: string): boolean {
  const s = String(status || "").toLowerCase();
  return s === "minted" || s === "assigned" || s === "started" || s === "pending" || s === "queued";
}

export async function cancelJob(jobId: number | string): Promise<void> {
  await api(`/api/v1/jobs/${jobId}/cancel`, { method: "POST" });
}

export async function killVM(vmId: string): Promise<void> {
  await api(`/api/v1/vms/${encodeURIComponent(vmId)}/kill`, { method: "POST" });
}

export async function api<T = unknown>(
  path: string,
  opts: RequestInit = {},
): Promise<T> {
  const res = await fetch(path, {
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...(opts.headers || {}),
    },
    ...opts,
  });
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = { raw: text };
  }
  if (!res.ok) {
    const msg =
      (data as { error?: string; message?: string } | null)?.error ||
      (data as { message?: string } | null)?.message ||
      res.statusText ||
      "request failed";
    throw new ApiError(msg);
  }
  return data as T;
}

export type SetupCheck = {
  id: string;
  label: string;
  status: string;
  detail: string;
};

export type SetupValues = {
  auth_mode?: string;
  github_org?: string;
  github_app_id?: number;
  listen_addr?: string;
  cache_listen_addr?: string;
  webhook_set?: boolean;
  webhook_received?: boolean;
  webhook_last_event?: string;
  webhook_url?: string;
  pem_set?: boolean;
  agent_token_set?: boolean;
  admin_users?: number;
  agents_registered?: number;
  hostctl?: boolean;
  guest_image?: boolean;
  guest_kernel?: boolean;
};

export type SetupStatus = {
  ok: boolean;
  needs_setup: boolean;
  setup_completed: boolean;
  auth_mode: string;
  fleet_ready: boolean;
  org: string;
  listen_addr: string;
  steps?: SetupCheck[];
  values?: SetupValues;
  webhook?: WebhookStatus;
};

export type WebhookEndpoint = {
  kind: string;
  label?: string;
  url: string;
  public: boolean;
  detail?: string;
};

export type WebhookStatus = {
  received: boolean;
  last_at?: string;
  last_event?: string;
  last_delivery?: string;
  suggested_url?: string;
  suggested_kind?: string;
  suggested_public?: boolean;
  suggested_detail?: string;
  endpoints?: WebhookEndpoint[];
};

export type Me = {
  ok: boolean;
  auth_mode: string;
  admin: boolean;
  open: boolean;
  email?: string;
  role?: string;
};

export type VersionStatus = {
  ok: boolean;
  version: string;
  latest?: string;
  update_available?: boolean;
  release_url?: string;
  checked_at?: string;
  check_error?: string;
};

export type Overview = {
  ok: boolean;
  fleet_ready: boolean;
  setup_completed: boolean;
  org: string;
  agents_registered: number;
  warm: number;
  busy: number;
  jobs_pending: number;
  jobs_minted: number;
  jobs_assigned: number;
  jobs_started: number;
  jobs_finished: number;
  jobs_failed: number;
  hostctl_configured: boolean;
  webhook_received?: boolean;
  webhook_last_event?: string;
  webhook?: WebhookStatus;
  run_p50_ms?: number;
  run_p95_ms?: number;
  cache_hits?: number;
  cache_misses?: number;
  cache_bytes?: number;
  cache_max_bytes?: number;
};

export type CacheEntry = {
  key: string;
  version?: string;
  bytes: number;
  created?: string;
  last_access?: string;
};

export type CacheRepo = {
  repo: string;
  bytes: number;
  entries: number;
  last_access?: string;
  keys?: CacheEntry[];
};

export type CacheHost = {
  agent_id: string;
  last_seen_at?: string;
  bytes: number;
  max_bytes: number;
  entries: number;
  repos?: CacheRepo[];
};

export type CacheInventory = {
  ok: boolean;
  bytes: number;
  max_bytes: number;
  entries: number;
  repos: number;
  hosts: CacheHost[];
};

export type CacheClearResponse = {
  ok: boolean;
  queued: number;
  error?: string;
};

export type HostResources = {
  ram_total_mib?: number;
  ram_avail_mib?: number;
  allocated_ram_mib?: number;
  reserve_ram_mib?: number;
  disk_total_mib?: number;
  disk_free_mib?: number;
  num_cpu?: number;
  configured_max_ready?: number;
  effective_max_ready?: number;
  clamp_reason?: string;
  last_admit_reason?: string;
  exclusive_busy?: boolean;
};

export type Host = {
  agent_id: string;
  capacity?: number;
  max_capacity?: number;
  warm?: number;
  busy?: number;
  last_seen_at?: string;
  labels?: string[];
  resources?: HostResources;
  vms?: {
    id: string;
    state: string;
    cpu_percent?: number;
    rss_mib?: number;
    memory_mib?: number;
  }[];
};

export type JobStep = {
  name: string;
  status: string;
  conclusion?: string;
  number: number;
  started_at?: string;
  completed_at?: string;
};

export type Job = {
  job_id: number;
  run_id?: number;
  org?: string;
  repo_full_name?: string;
  name?: string;
  workflow_name?: string;
  steps?: JobStep[];
  labels?: string[];
  status: string;
  assigned_agent_id?: string;
  vm_id?: string;
  warm_bind?: boolean;
  outcome?: string;
  error?: string;
  created_at?: string;
  assigned_at?: string;
  started_at?: string;
  finished_at?: string;
  runner_name?: string;
  runner_id?: number;
  queue_ms?: number;
  bind_ms?: number;
  run_ms?: number;
  total_ms?: number;
  cache_hits?: number;
  cache_misses?: number;
  cache_bytes_in?: number;
  cache_bytes_out?: number;
};

export function formatBytes(n?: number): string {
  if (n == null || n < 0) return "—";
  if (n < 1024) return `${Math.round(n)} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export function formatAge(iso?: string): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const sec = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (sec < 5) return "just now";
  if (sec < 60) return `${sec}s ago`;
  if (sec < 3600) return `${Math.round(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.round(sec / 3600)}h ago`;
  return `${Math.round(sec / 86400)}d ago`;
}

export function formatDuration(ms?: number): string {
  if (ms == null || ms < 0) return "—";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const sec = ms / 1000;
  if (sec < 60) return `${sec < 10 ? sec.toFixed(1) : Math.round(sec)}s`;
  const m = Math.floor(sec / 60);
  const s = Math.round(sec % 60);
  if (m < 60) return s ? `${m}m ${s}s` : `${m}m`;
  const h = Math.floor(m / 60);
  const rm = m % 60;
  return rm ? `${h}h ${rm}m` : `${h}h`;
}

export type JobEvent = {
  time: string;
  source: string;
  level?: string;
  message: string;
};

export type JobLogs = {
  job_id?: number;
  runner_log?: string;
  agent_log?: string;
  console_log?: string;
  workflow_log?: string;
  events?: JobEvent[];
  updated_at?: string;
};

export type JobDetail = {
  ok: boolean;
  job: Job;
  logs: JobLogs;
};

export type User = {
  id: number;
  email: string;
  role: string;
  created_at?: string;
};

export type ConfigField = {
  key: string;
  label: string;
  group: string;
  value?: string;
  configured: boolean;
  secret: boolean;
  editable?: boolean;
  input_type?: string;
  options?: string[];
  status: "ok" | "missing" | "warn" | string;
  description?: string;
};

export type SettingsConfig = {
  ok: boolean;
  config_path: string;
  fleet_ready: boolean;
  setup_required: boolean;
  missing_count: number;
  fields: ConfigField[];
  webhook?: WebhookStatus;
};

export type RunnerShape = {
  label?: string;
  vcpu: number;
  memory_mib: number;
  min_ready: number;
};

export type SettingsShapes = {
  ok: boolean;
  agent_path?: string;
  shapes: RunnerShape[];
};

export type SettingsConfigSave = {
  ok: boolean;
  config_path?: string;
  restart?: boolean;
  reconnect?: boolean;
  note?: string;
};

export type ServiceStatus =
  | "running"
  | "starting"
  | "stopping"
  | "stopped"
  | "failed"
  | "unknown"
  | "not_installed";

export type ServiceSlice = {
  name?: string;
  label?: string;
  unit?: string;
  status?: ServiceStatus | string;
  detail?: string;
  healthy?: boolean;
  ready?: boolean;
  registered?: boolean;
  registered_ids?: string[];
  last_seen_at?: string;
  installed?: boolean;
  installable?: boolean;
  install_hint?: string;
  binary?: boolean;
};

export type SystemStatus = {
  ok: boolean;
  control: ServiceSlice;
  agent: ServiceSlice;
  overall?: ServiceStatus | string;
  hostctl: boolean;
  time?: string;
};

export async function waitForHealth(attempts = 40, delayMs = 500): Promise<void> {
  for (let i = 0; i < attempts; i++) {
    await new Promise((r) => setTimeout(r, delayMs));
    try {
      const res = await fetch("/healthz", { credentials: "same-origin" });
      if (res.ok) return;
    } catch {
      /* retry */
    }
  }
}

export type RestartTarget = "all" | "control" | "agent";

export type RestartProgress = {
  control: "idle" | "restarting" | "up" | "down" | "timeout" | "error";
  agent: "idle" | "restarting" | "up" | "down" | "timeout" | "error";
  done: boolean;
  error?: string;
};

/**
 * Poll /healthz + /api/v1/system/status until requested services are ready.
 * Calls onUpdate on each tick for UI spinners/labels.
 */
export async function waitForServicesReady(
  target: RestartTarget,
  onUpdate: (p: RestartProgress) => void,
  attempts = 60,
  delayMs = 500,
): Promise<RestartProgress> {
  const needControl = target === "all" || target === "control";
  const needAgent = target === "all" || target === "agent";

  let progress: RestartProgress = {
    control: needControl ? "restarting" : "idle",
    agent: needAgent ? "restarting" : "idle",
    done: false,
  };
  onUpdate(progress);

  // Brief pause so systemd can stop units before we probe.
  await new Promise((r) => setTimeout(r, 400));

  for (let i = 0; i < attempts; i++) {
    await new Promise((r) => setTimeout(r, delayMs));

    let controlUp = !needControl;
    let agentUp = !needAgent;

    if (needControl) {
      try {
        const res = await fetch("/healthz", { credentials: "same-origin" });
        controlUp = res.ok;
      } catch {
        controlUp = false;
      }
    }

    if (needAgent && (controlUp || !needControl)) {
      try {
        // Status requires control; only query once control answers.
        if (!needControl) {
          const st = await api<SystemStatus>("/api/v1/system/status");
          agentUp = Boolean(st.agent?.ready);
        } else if (controlUp) {
          const st = await api<SystemStatus>("/api/v1/system/status");
          agentUp = Boolean(st.agent?.ready);
          // If unit is active but not registered yet, keep waiting.
          if (st.agent?.unit === "active" && !st.agent?.registered) {
            agentUp = false;
          }
          if (st.agent?.unit === "inactive" || st.agent?.unit === "failed") {
            // Still early in restart loop; only mark down near the end.
            if (i > attempts - 5) {
              progress = {
                ...progress,
                control: controlUp ? "up" : "restarting",
                agent: "down",
              };
              onUpdate(progress);
            }
          }
        }
      } catch {
        agentUp = false;
      }
    }

    progress = {
      control: needControl ? (controlUp ? "up" : "restarting") : "idle",
      agent: needAgent ? (agentUp ? "up" : "restarting") : "idle",
      done: false,
    };
    onUpdate(progress);

    if (controlUp && agentUp) {
      progress = { ...progress, done: true };
      onUpdate(progress);
      return progress;
    }
  }

  progress = {
    control: needControl
      ? progress.control === "up"
        ? "up"
        : "timeout"
      : "idle",
    agent: needAgent ? (progress.agent === "up" ? "up" : "timeout") : "idle",
    done: false,
    error: "Timed out waiting for services to come back",
  };
  onUpdate(progress);
  return progress;
}
