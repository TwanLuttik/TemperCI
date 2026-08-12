import { useCallback, useEffect, useMemo, useState } from "react";
import {
  api,
  waitForServicesReady,
  type Overview,
  type RestartProgress,
  type RestartTarget,
  type SettingsConfig,
  type SettingsConfigSave,
} from "../api";
import { GitHubAppGuide } from "../components/GitHubAppGuide";
import { RestartStatus } from "../components/RestartStatus";

type Props = { onOverview: (o: Overview) => void };

type FormState = {
  listen_addr: string;
  github_app_id: string;
  github_org: string;
  github_webhook_secret: string;
  github_app_private_key_path: string;
  github_app_private_key_pem: string;
  label_prefix: string;
  runner_group_id: string;
  agent_token: string;
  auth_mode: string;
  setup_completed: string;
  sqlite_path: string;
  data_dir: string;
  hostctl_path: string;
};

function emptyForm(): FormState {
  return {
    listen_addr: "",
    github_app_id: "",
    github_org: "",
    github_webhook_secret: "",
    github_app_private_key_path: "",
    github_app_private_key_pem: "",
    label_prefix: "",
    runner_group_id: "",
    agent_token: "",
    auth_mode: "open",
    setup_completed: "true",
    sqlite_path: "",
    data_dir: "",
    hostctl_path: "",
  };
}

function formFromConfig(cfg: SettingsConfig): FormState {
  const f = emptyForm();
  const byKey = new Map(cfg.fields.map((x) => [x.key, x]));
  const get = (k: string) => byKey.get(k)?.value ?? "";
  f.listen_addr = get("listen_addr");
  f.github_app_id = get("github_app_id") === "0" ? "" : get("github_app_id");
  f.github_org = get("github_org");
  f.github_app_private_key_path = get("github_app_private_key_path");
  f.label_prefix = get("label_prefix");
  f.runner_group_id = get("runner_group_id");
  f.auth_mode = get("auth_mode") || "open";
  f.setup_completed = get("setup_completed") === "true" ? "true" : "false";
  f.sqlite_path = get("sqlite_path");
  f.data_dir = get("data_dir");
  f.hostctl_path = get("hostctl_path");
  // secrets stay blank (leave unchanged unless user types)
  f.github_webhook_secret = "";
  f.agent_token = "";
  f.github_app_private_key_pem = "";
  return f;
}

export function SettingsPage({ onOverview }: Props) {
  const [o, setO] = useState<Overview | null>(null);
  const [cfg, setCfg] = useState<SettingsConfig | null>(null);
  const [form, setForm] = useState<FormState>(emptyForm());
  const [msg, setMsg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [restartTarget, setRestartTarget] = useState<RestartTarget | null>(null);
  const [restartProgress, setRestartProgress] = useState<RestartProgress | null>(null);

  const reload = useCallback(async () => {
    const [overview, settings] = await Promise.all([
      api<Overview>("/api/v1/overview"),
      api<SettingsConfig>("/api/v1/settings/config"),
    ]);
    setO(overview);
    onOverview(overview);
    setCfg(settings);
    setForm(formFromConfig(settings));
  }, [onOverview]);

  useEffect(() => {
    reload().catch((e: Error) => setErr(e.message));
  }, [reload]);

  const groups = useMemo(() => {
    if (!cfg?.fields) return [] as { name: string; fields: SettingsConfig["fields"] }[];
    const order: string[] = [];
    const map = new Map<string, SettingsConfig["fields"]>();
    for (const f of cfg.fields) {
      if (!map.has(f.group)) {
        map.set(f.group, []);
        order.push(f.group);
      }
      map.get(f.group)!.push(f);
    }
    return order.map((name) => ({ name, fields: map.get(name)! }));
  }, [cfg]);

  const patch = (key: keyof FormState, value: string) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  };

  const restart = async (target: RestartTarget) => {
    setMsg(null);
    setErr(null);
    setRestarting(true);
    setRestartTarget(target);
    setRestartProgress({
      control: target === "agent" ? "idle" : "restarting",
      agent: target === "control" ? "idle" : "restarting",
      done: false,
    });
    try {
      await api<{ reconnect?: boolean }>("/api/v1/system/restart", {
        method: "POST",
        body: JSON.stringify({ target }),
      });
      setMsg(
        target === "all"
          ? "Restart requested for control + agent…"
          : `Restart requested for ${target}…`,
      );
      const progress = await waitForServicesReady(target, setRestartProgress);
      if (progress.done) {
        setMsg(
          target === "all"
            ? "Control and agent restarted successfully."
            : target === "control"
              ? "Control restarted successfully."
              : "Agent restarted successfully.",
        );
        await reload();
      } else {
        setErr(progress.error || "Restart did not complete cleanly.");
      }
    } catch (e) {
      setErr((e as Error).message);
      setRestartProgress((p) =>
        p
          ? {
              ...p,
              control: p.control === "restarting" ? "error" : p.control,
              agent: p.agent === "restarting" ? "error" : p.agent,
              error: (e as Error).message,
            }
          : p,
      );
    } finally {
      setRestarting(false);
    }
  };

  const save = async (withRestart: boolean) => {
    setBusy(true);
    setMsg(null);
    setErr(null);
    try {
      const body: Record<string, unknown> = {
        listen_addr: form.listen_addr,
        github_org: form.github_org,
        github_app_private_key_path: form.github_app_private_key_path,
        label_prefix: form.label_prefix,
        auth_mode: form.auth_mode,
        setup_completed: form.setup_completed === "true",
        sqlite_path: form.sqlite_path,
        data_dir: form.data_dir,
        hostctl_path: form.hostctl_path,
        restart: withRestart,
      };
      if (form.github_app_id.trim() !== "") {
        body.github_app_id = Number(form.github_app_id);
      }
      if (form.runner_group_id.trim() !== "") {
        body.runner_group_id = Number(form.runner_group_id);
      }
      if (form.github_webhook_secret.trim() !== "") {
        body.github_webhook_secret = form.github_webhook_secret;
      }
      if (form.agent_token.trim() !== "") {
        body.agent_token = form.agent_token;
      }
      if (form.github_app_private_key_pem.trim() !== "") {
        body.github_app_private_key_pem = form.github_app_private_key_pem;
      }

      const res = await api<SettingsConfigSave>("/api/v1/settings/config", {
        method: "POST",
        body: JSON.stringify(body),
      });
      if (res.reconnect) {
        setMsg("Config saved. Restarting control + agent…");
        setRestarting(true);
        setRestartTarget("all");
        setRestartProgress({
          control: "restarting",
          agent: "restarting",
          done: false,
        });
        const progress = await waitForServicesReady("all", setRestartProgress);
        setRestarting(false);
        if (progress.done) {
          setMsg("Config saved. Control and agent restarted successfully.");
          await reload();
        } else {
          setErr(progress.error || "Config saved, but restart did not finish.");
        }
      } else {
        setMsg(res.note || "Config saved. Restart control to fully reload the GitHub client.");
        await reload();
      }
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const formValue = (key: string): string => {
    if (key in form) return form[key as keyof FormState];
    return "";
  };

  const setFormKey = (key: string, value: string) => {
    if (key in form) patch(key as keyof FormState, value);
  };

  if (err && !o) return <div className="err">{err}</div>;
  if (!o || !cfg) return <div className="loading">Loading settings…</div>;

  return (
    <>
      <div className="page-head">
        <p className="page-kicker">/ Settings</p>
        <h1>Control plane</h1>
        <p className="lead">
          View and edit control-plane configuration. Secrets stay blank unless you enter a new
          value. Save writes the TOML on this host.
        </p>
      </div>

      <div className="panel" style={{ marginBottom: 14 }}>
        <div className="panel-head">
          <h2>GitHub App setup</h2>
          <span className="meta">org · {form.github_org || o.org || "—"}</span>
        </div>
        <GitHubAppGuide orgSlug={form.github_org || o.org} compact />
      </div>

      <div className="panel" style={{ marginBottom: 14 }}>
        <div className="panel-head">
          <h2>Runtime</h2>
          <span className="meta">{o.org || "—"}</span>
        </div>
        <div className="row" style={{ marginBottom: 16 }}>
          <span className={`badge ${o.fleet_ready ? "ok" : "warn"}`}>
            fleet {o.fleet_ready ? "ready" : "limited"}
          </span>
          <span className={`badge ${o.hostctl_configured ? "ok" : "warn"}`}>
            hostctl {o.hostctl_configured ? "available" : "missing"}
          </span>
          <span className={`badge ${cfg.missing_count === 0 ? "ok" : "warn"}`}>
            {cfg.missing_count === 0
              ? "config complete"
              : `${cfg.missing_count} field(s) missing`}
          </span>
        </div>
        <div className="row">
          <button
            type="button"
            className="danger"
            disabled={restarting || busy}
            onClick={() => void restart("all")}
          >
            {restarting && restartTarget === "all" ? (
              <>
                <span className="spinner" aria-hidden /> Restarting…
              </>
            ) : (
              "Restart control + agent"
            )}
          </button>
          <button
            type="button"
            className="secondary"
            disabled={restarting || busy}
            onClick={() => void restart("control")}
          >
            Control only
          </button>
          <button
            type="button"
            className="ghost"
            disabled={restarting || busy}
            onClick={() => void restart("agent")}
          >
            Agent only
          </button>
        </div>
        <RestartStatus
          active={Boolean(restartProgress)}
          target={restartTarget}
          progress={restartProgress}
        />
        {msg ? <div className="okmsg">{msg}</div> : null}
        {err ? <div className="err">{err}</div> : null}
      </div>

      <form
        className="panel"
        style={{ marginBottom: 14 }}
        onSubmit={(e) => {
          e.preventDefault();
          void save(false);
        }}
      >
        <div className="panel-head">
          <h2>Configuration</h2>
          <span className="meta mono">{cfg.config_path || "—"}</span>
        </div>
        <p style={{ margin: "0 0 14px", color: "var(--muted)", fontSize: 13 }}>
          Writes to <code>{cfg.config_path || "/etc/temperci/control.toml"}</code>. After changing
          GitHub App or agent token, use <strong>Save &amp; restart</strong> so services reload.
        </p>

        {groups.map((g) => (
          <div key={g.name} className="config-group">
            <div className="config-group-title">{g.name}</div>
            {g.fields.map((f) => (
              <div key={f.key} className="config-field">
                <div className="config-field-meta">
                  <div className="config-label">
                    {f.label}{" "}
                    <span
                      className={`badge ${
                        f.status === "ok" ? "ok" : f.status === "warn" ? "warn" : "bad"
                      }`}
                    >
                      {f.status === "ok" ? "set" : f.status === "warn" ? "check" : "missing"}
                    </span>
                  </div>
                  {f.description ? <div className="config-desc">{f.description}</div> : null}
                  <div className="config-key mono">{f.key}</div>
                </div>
                <div className="config-field-input">
                  {!f.editable || f.input_type === "readonly" ? (
                    <div className="mono config-value readonly">
                      {f.secret ? (
                        <span className={f.configured ? "secret-set" : "secret-missing"}>
                          {f.value || (f.configured ? "set" : "not set")}
                        </span>
                      ) : (
                        f.value || "—"
                      )}
                    </div>
                  ) : f.input_type === "textarea" ? (
                    <textarea
                      placeholder={
                        f.secret
                          ? "Leave blank to keep current PEM on disk"
                          : undefined
                      }
                      value={formValue(f.key)}
                      onChange={(e) => setFormKey(f.key, e.target.value)}
                      rows={5}
                    />
                  ) : f.input_type === "select" ? (
                    <select
                      value={formValue(f.key)}
                      onChange={(e) => setFormKey(f.key, e.target.value)}
                    >
                      {(f.options || []).map((opt) => (
                        <option key={opt} value={opt}>
                          {opt}
                        </option>
                      ))}
                    </select>
                  ) : (
                    <input
                      type={
                        f.input_type === "password"
                          ? "password"
                          : f.input_type === "number"
                            ? "number"
                            : "text"
                      }
                      className={f.input_type === "password" ? undefined : "mono"}
                      autoComplete="off"
                      placeholder={
                        f.secret
                          ? f.configured
                            ? "Leave blank to keep current"
                            : "Enter value"
                          : undefined
                      }
                      value={formValue(f.key)}
                      onChange={(e) => setFormKey(f.key, e.target.value)}
                    />
                  )}
                </div>
              </div>
            ))}
          </div>
        ))}

        <div className="row" style={{ marginTop: 8 }}>
          <button type="submit" disabled={busy}>
            Save config
          </button>
          <button
            type="button"
            className="secondary"
            disabled={busy}
            onClick={() => void save(true)}
          >
            Save &amp; restart
          </button>
          <button
            type="button"
            className="ghost"
            disabled={busy}
            onClick={() => void reload().catch((e: Error) => setErr(e.message))}
          >
            Reload
          </button>
        </div>
        {msg ? <div className="okmsg">{msg}</div> : null}
        {err ? <div className="err">{err}</div> : null}
      </form>
    </>
  );
}
