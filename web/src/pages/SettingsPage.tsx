import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  api,
  waitForServicesReady,
  type Overview,
  type RestartProgress,
  type RestartTarget,
  type RunnerShape,
  type SettingsConfig,
  type SettingsConfigSave,
  type SettingsShapes,
} from "../api";
import { GitHubAppGuide } from "../components/GitHubAppGuide";
import { PageHeader } from "../components/page-header";
import { RestartStatus } from "../components/RestartStatus";
import { ServicesPanel } from "../components/ServicesPanel";
import { StatusBadge } from "../components/status-badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

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
  cache_listen_addr: string;
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
    cache_listen_addr: "",
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
  f.cache_listen_addr = get("cache_listen_addr");
  // secrets stay blank (leave unchanged unless user types)
  f.github_webhook_secret = "";
  f.agent_token = "";
  f.github_app_private_key_pem = "";
  return f;
}

export function SettingsPage({ onOverview }: Props) {
  const navigate = useNavigate();
  const [o, setO] = useState<Overview | null>(null);
  const [cfg, setCfg] = useState<SettingsConfig | null>(null);
  const [shapes, setShapes] = useState<RunnerShape[]>([]);
  const [shapePath, setShapePath] = useState("");
  const [form, setForm] = useState<FormState>(emptyForm());
  const [msg, setMsg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [restartTarget, setRestartTarget] = useState<RestartTarget | null>(null);
  const [restartProgress, setRestartProgress] = useState<RestartProgress | null>(null);

  const reload = useCallback(async () => {
    const [overview, settings, shapeCfg] = await Promise.all([
      api<Overview>("/api/v1/overview"),
      api<SettingsConfig>("/api/v1/settings/config"),
      api<SettingsShapes>("/api/v1/settings/shapes"),
    ]);
    setO(overview);
    onOverview(overview);
    setCfg(settings);
    setForm(formFromConfig(settings));
    setShapes(shapeCfg.shapes?.length ? shapeCfg.shapes : [{ vcpu: 4, memory_mib: 8192, min_ready: 1 }]);
    setShapePath(shapeCfg.agent_path || "");
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
        cache_listen_addr: form.cache_listen_addr,
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
        setRestartTarget("all");
        setRestartProgress({
          control: "restarting",
          agent: "restarting",
          done: false,
        });
        const progress = await waitForServicesReady("all", setRestartProgress);
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

  if (err && !o) return <p className="text-sm text-destructive">{err}</p>;
  if (!o || !cfg) return <p className="text-sm text-muted-foreground">Loading settings…</p>;

  return (
    <>
      <PageHeader
        kicker="/ Settings"
        title="Control plane"
        description="View and edit configuration. Secrets stay blank unless you enter a new value. Save writes the TOML on this host."
        actions={
          <Button type="button" variant="outline" onClick={() => navigate("/setup")}>
            Open setup wizard
          </Button>
        }
      />

      <Card className="mb-4">
        <CardHeader className="flex-row items-center justify-between">
          <CardTitle>GitHub App setup</CardTitle>
          <span className="font-mono text-[11px] text-muted-foreground">
            org · {form.github_org || o.org || "—"}
          </span>
        </CardHeader>
        <CardContent>
          <GitHubAppGuide orgSlug={form.github_org || o.org} compact />
        </CardContent>
      </Card>

      <div className="mb-4">
        <ServicesPanel onRestarted={() => void reload()} />
      </div>

      <RunnerShapesCard
        shapes={shapes}
        path={shapePath}
        busy={busy}
        onChange={setShapes}
        onSave={async (restart) => {
          setBusy(true);
          setMsg(null);
          setErr(null);
          try {
            const res = await api<SettingsConfigSave>("/api/v1/settings/shapes", {
              method: "POST",
              body: JSON.stringify({ shapes, restart }),
            });
            if (res.reconnect) {
              setMsg("Shapes saved. Restarting agent…");
              setRestartTarget("agent");
              setRestartProgress({ control: "idle", agent: "restarting", done: false });
              const progress = await waitForServicesReady("agent", setRestartProgress);
              if (progress.done) {
                setMsg("Shapes saved. Agent restarted and will refill matching warm VMs.");
                await reload();
              } else {
                setErr(progress.error || "Shapes saved, but agent restart did not finish.");
              }
            } else {
              setMsg(res.note || "Shapes saved. Restart the agent to apply.");
              await reload();
            }
          } catch (e) {
            setErr((e as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      />

      <Card className="mb-4">
        <CardHeader className="flex-row items-center justify-between">
          <CardTitle>Runtime</CardTitle>
          <span className="font-mono text-[11px] text-muted-foreground">{o.org || "—"}</span>
        </CardHeader>
        <CardContent className="space-y-3">
        <div className="flex flex-wrap gap-2">
          <StatusBadge tone={o.fleet_ready ? "ok" : "warn"}>
            fleet {o.fleet_ready ? "ready" : "limited"}
          </StatusBadge>
          <StatusBadge tone={o.hostctl_configured ? "ok" : "warn"}>
            hostctl {o.hostctl_configured ? "available" : "missing"}
          </StatusBadge>
          <StatusBadge tone={cfg.missing_count === 0 ? "ok" : "warn"}>
            {cfg.missing_count === 0
              ? "config complete"
              : `${cfg.missing_count} field(s) missing`}
          </StatusBadge>
        </div>
        <RestartStatus
          active={Boolean(restartProgress)}
          target={restartTarget}
          progress={restartProgress}
        />
        {msg ? <p className="text-sm text-emerald-400">{msg}</p> : null}
        {err ? <p className="text-sm text-destructive">{err}</p> : null}
        </CardContent>
      </Card>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          void save(false);
        }}
      >
        <Card>
          <CardHeader className="flex-row items-center justify-between">
            <CardTitle>Configuration</CardTitle>
            <span className="font-mono text-[11px] text-muted-foreground">{cfg.config_path || "—"}</span>
          </CardHeader>
          <CardContent>
        <p className="mb-4 text-sm text-muted-foreground">
          Writes to <code className="font-mono text-xs">{cfg.config_path || "/etc/temperci/control.toml"}</code>. After changing
          GitHub App or agent token, use <strong>Save &amp; restart</strong> so services reload.
        </p>

        {groups.map((g) => (
          <div key={g.name} className="mb-6 last:mb-0">
            <div className="mb-2.5 font-mono text-[11px] tracking-wider text-primary uppercase">{g.name}</div>
            {g.fields.map((f) => (
              <div
                key={f.key}
                className="grid items-start gap-3 border-b border-border py-3 last:border-0 md:grid-cols-[minmax(200px,1fr)_minmax(220px,1.2fr)]"
              >
                <div>
                  <div className="mb-1 flex flex-wrap items-center gap-2 font-medium">
                    {f.label}{" "}
                    <StatusBadge
                      tone={f.status === "ok" ? "ok" : f.status === "warn" ? "warn" : "bad"}
                    >
                      {f.status === "ok" ? "set" : f.status === "warn" ? "check" : "missing"}
                    </StatusBadge>
                  </div>
                  {f.description ? <div className="mb-1 max-w-[42ch] text-xs text-muted-foreground">{f.description}</div> : null}
                  <div className="font-mono text-[10px] text-muted-foreground">{f.key}</div>
                </div>
                <div>
                  {!f.editable || f.input_type === "readonly" ? (
                    <div className="py-2 font-mono text-xs break-all">
                      {f.secret ? (
                        <span className={f.configured ? "text-emerald-400" : "text-destructive"}>
                          {f.value || (f.configured ? "set" : "not set")}
                        </span>
                      ) : (
                        f.value || "—"
                      )}
                    </div>
                  ) : f.input_type === "textarea" ? (
                    <Textarea
                      placeholder={
                        f.secret
                          ? "Leave blank to keep current PEM on disk"
                          : undefined
                      }
                      value={formValue(f.key)}
                      onChange={(e) => setFormKey(f.key, e.target.value)}
                      rows={5}
                      className="font-mono text-xs"
                    />
                  ) : f.input_type === "select" ? (
                    <Select value={formValue(f.key)} onValueChange={(v) => setFormKey(f.key, v)}>
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {(f.options || []).map((opt) => (
                          <SelectItem key={opt} value={opt}>
                            {opt}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  ) : (
                    <Input
                      type={
                        f.input_type === "password"
                          ? "password"
                          : f.input_type === "number"
                            ? "number"
                            : "text"
                      }
                      className={f.input_type === "password" ? undefined : "font-mono"}
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

        <div className="mt-4 flex flex-wrap gap-2">
          <Button type="submit" disabled={busy}>
            Save config
          </Button>
          <Button type="button" variant="outline" disabled={busy} onClick={() => void save(true)}>
            Save &amp; restart
          </Button>
          <Button
            type="button"
            variant="ghost"
            disabled={busy}
            onClick={() => void reload().catch((e: Error) => setErr(e.message))}
          >
            Reload
          </Button>
        </div>
        {msg ? <p className="mt-3 text-sm text-emerald-400">{msg}</p> : null}
        {err ? <p className="mt-3 text-sm text-destructive">{err}</p> : null}
          </CardContent>
        </Card>
      </form>
    </>
  );
}

function shapeLabel(s: RunnerShape): string {
  if (s.vcpu === 4 && s.memory_mib === 8192) return "temperci-4vcpu-ubuntu-2404";
  const g = Math.max(1, Math.round((s.memory_mib || 0) / 1024));
  return `temperci-${s.vcpu || 0}vcpu-${g}g-ubuntu-2404`;
}

function RunnerShapesCard({
  shapes,
  path,
  busy,
  onChange,
  onSave,
}: {
  shapes: RunnerShape[];
  path: string;
  busy: boolean;
  onChange: (next: RunnerShape[]) => void;
  onSave: (restart: boolean) => void;
}) {
  const patch = (i: number, part: Partial<RunnerShape>) => {
    onChange(shapes.map((s, idx) => (idx === i ? { ...s, ...part } : s)));
  };
  return (
    <Card className="mb-4">
      <CardHeader className="flex-row items-center justify-between">
        <CardTitle>Warm microVMs</CardTitle>
        <span className="font-mono text-[11px] text-muted-foreground">{path || "agent.toml"}</span>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-sm text-muted-foreground">
          Keep one or more sizes warm. Workflows pick a size with{" "}
          <code className="font-mono text-xs">runs-on: temperci-4vcpu-ubuntu-2404</code> or{" "}
          <code className="font-mono text-xs">temperci-2vcpu-4g-ubuntu-2404</code>. A matching warm VM
          is used when one exists; otherwise that size is cold-booted.
        </p>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left font-mono text-[10px] tracking-wider text-muted-foreground uppercase">
                <th className="pb-2 pr-3">vCPU</th>
                <th className="pb-2 pr-3">RAM (GiB)</th>
                <th className="pb-2 pr-3">Warm</th>
                <th className="pb-2 pr-3">runs-on label</th>
                <th className="pb-2" />
              </tr>
            </thead>
            <tbody>
              {shapes.map((s, i) => (
                <tr key={`${s.vcpu}-${s.memory_mib}-${i}`} className="border-t border-border">
                  <td className="py-2 pr-3">
                    <Input
                      type="number"
                      min={1}
                      className="w-20 font-mono"
                      value={s.vcpu || ""}
                      onChange={(e) => patch(i, { vcpu: Number(e.target.value) || 0 })}
                    />
                  </td>
                  <td className="py-2 pr-3">
                    <Input
                      type="number"
                      min={1}
                      className="w-20 font-mono"
                      value={s.memory_mib ? Math.round(s.memory_mib / 1024) : ""}
                      onChange={(e) =>
                        patch(i, { memory_mib: Math.max(1, Number(e.target.value) || 0) * 1024 })
                      }
                    />
                  </td>
                  <td className="py-2 pr-3">
                    <Input
                      type="number"
                      min={0}
                      className="w-20 font-mono"
                      value={s.min_ready}
                      onChange={(e) => patch(i, { min_ready: Math.max(0, Number(e.target.value) || 0) })}
                    />
                  </td>
                  <td className="py-2 pr-3 font-mono text-xs text-muted-foreground">
                    {shapeLabel({ ...s, label: undefined })}
                  </td>
                  <td className="py-2 text-right">
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      disabled={shapes.length <= 1}
                      onClick={() => onChange(shapes.filter((_, idx) => idx !== i))}
                    >
                      Remove
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            type="button"
            variant="outline"
            onClick={() => onChange([...shapes, { vcpu: 2, memory_mib: 4096, min_ready: 0 }])}
          >
            Add size
          </Button>
          <Button type="button" disabled={busy} onClick={() => onSave(true)}>
            Save &amp; restart agent
          </Button>
          <Button type="button" variant="ghost" disabled={busy} onClick={() => onSave(false)}>
            Save without restart
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
