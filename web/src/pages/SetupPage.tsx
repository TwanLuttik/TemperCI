import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";

import { api, waitForHealth, type SetupStatus } from "../api";
import { GitHubAppGuide } from "../components/GitHubAppGuide";
import { WebhookStatus } from "../components/WebhookStatus";
import { PageHeader } from "../components/page-header";
import { StatCard } from "../components/stat-card";
import { StatusBadge } from "../components/status-badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

type Wizard = {
  auth_mode: string;
  admin_email: string;
  admin_password: string;
  github_app_id: string;
  github_org: string;
  github_webhook_secret: string;
  github_app_private_key_pem: string;
  agent_token: string;
  listen_addr: string;
  cache_listen_addr: string;
};

type Props = { onDone: () => void | Promise<void> };

const STEPS = [
  { id: "access", label: "Access" },
  { id: "github", label: "GitHub App" },
  { id: "agent", label: "Agent" },
  { id: "services", label: "Services" },
  { id: "review", label: "Review" },
];

const DRAFT_KEY = "temperci-setup-draft";

function parseAppID(raw: string): number {
  const n = Number(String(raw).trim());
  return Number.isFinite(n) && n > 0 ? Math.trunc(n) : 0;
}

function githubFieldsReady(
  data: Wizard,
  webhookSet: boolean,
  pemSet: boolean,
): boolean {
  return Boolean(
    data.github_org.trim() &&
      parseAppID(data.github_app_id) &&
      (data.github_webhook_secret.trim() || webhookSet) &&
      (data.github_app_private_key_pem.trim() || pemSet),
  );
}

export function SetupPage({ onDone }: Props) {
  const navigate = useNavigate();
  const [step, setStep] = useState(0);
  const [status, setStatus] = useState<SetupStatus | null>(null);
  const [data, setData] = useState<Wizard>(() => {
    const empty: Wizard = {
      auth_mode: "password",
      admin_email: "",
      admin_password: "",
      github_app_id: "",
      github_org: "",
      github_webhook_secret: "",
      github_app_private_key_pem: "",
      agent_token: "",
      listen_addr: "0.0.0.0:8080",
      cache_listen_addr: "",
    };
    try {
      const raw = sessionStorage.getItem(DRAFT_KEY);
      if (!raw) return empty;
      return { ...empty, ...(JSON.parse(raw) as Partial<Wizard>) };
    } catch {
      return empty;
    }
  });
  const [msg, setMsg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const review = Boolean(status?.setup_completed && !status.needs_setup);

  useEffect(() => {
    api<SetupStatus>("/api/v1/setup/status")
      .then((st) => {
        setStatus(st);
        const v = st.values || {};
        setData((d) => ({
          ...d,
          auth_mode: v.auth_mode || d.auth_mode,
          github_org: v.github_org || d.github_org,
          github_app_id: v.github_app_id ? String(v.github_app_id) : d.github_app_id,
          listen_addr: v.listen_addr || d.listen_addr,
          cache_listen_addr: v.cache_listen_addr ?? d.cache_listen_addr,
        }));
      })
      .catch((e: Error) => setErr(e.message));
  }, []);

  const imageReady = Boolean(status?.values?.guest_image && status?.values?.guest_kernel);
  const webhookReceived = Boolean(status?.webhook?.received || status?.values?.webhook_received);
  useEffect(() => {
    if (imageReady && webhookReceived) {
      return;
    }
    const id = window.setInterval(() => {
      api<SetupStatus>("/api/v1/setup/status")
        .then(setStatus)
        .catch(() => {});
    }, 4000);
    return () => window.clearInterval(id);
  }, [imageReady, webhookReceived]);

  const webhookSet = Boolean(status?.values?.webhook_set);
  const pemSet = Boolean(status?.values?.pem_set);
  const tokenSet = Boolean(status?.values?.agent_token_set);

  const patch = (p: Partial<Wizard>) => {
    setData((d) => {
      const next = { ...d, ...p };
      try {
        sessionStorage.setItem(DRAFT_KEY, JSON.stringify(next));
      } catch {
        /* ignore quota */
      }
      return next;
    });
  };
  const check = (id: string) => {
    const st = status?.steps?.find((s) => s.id === id);
    if (id === "github" && githubFieldsReady(data, webhookSet, pemSet) && st?.status !== "ok") {
      return {
        id: "github",
        label: "GitHub App",
        status: "ok",
        detail: `${data.github_org} · app ${data.github_app_id}`,
      };
    }
    return st;
  };

  const payload = () => ({
    auth_mode: data.auth_mode,
    admin_email: data.admin_email,
    admin_password: data.admin_password,
    github_app_id: parseAppID(data.github_app_id),
    github_org: data.github_org.trim(),
    github_webhook_secret: data.github_webhook_secret,
    github_app_private_key_pem: data.github_app_private_key_pem,
    agent_token: data.agent_token,
    listen_addr: data.listen_addr,
    cache_listen_addr: data.cache_listen_addr,
  });

  const saveDraft = async () => {
    await api("/api/v1/setup/apply", {
      method: "POST",
      body: JSON.stringify({ ...payload(), draft: true, restart: false }),
    });
    const st = await api<SetupStatus>("/api/v1/setup/status");
    setStatus(st);
  };

  const continueNext = async () => {
    if (step === 1 && !githubFieldsReady(data, webhookSet, pemSet)) {
      setErr("Fill organization, App ID, webhook secret, and the private key PEM before continuing.");
      return;
    }
    setErr(null);
    setBusy(true);
    try {
      await saveDraft();
      setStep((s) => s + 1);
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const apply = async () => {
    setBusy(true);
    setMsg(review ? "Saving without resetting existing secrets…" : "Applying configuration…");
    setErr(null);
    try {
      const res = await api<{ agent_token?: string; reconnect?: boolean }>("/api/v1/setup/apply", {
        method: "POST",
        body: JSON.stringify({
          ...payload(),
          restart: true,
        }),
      });
      if (res.agent_token && !review) {
        setMsg(`Config written. Agent token: ${res.agent_token}`);
      }
      try {
        sessionStorage.removeItem(DRAFT_KEY);
      } catch {
        /* ignore */
      }
      if (res.reconnect) {
        setMsg("Config written. Reconnecting after restart…");
        await waitForHealth();
        await onDone();
        return;
      }
      setMsg("Config written. Restart control manually if needed.");
      await onDone();
    } catch (e) {
      setErr((e as Error).message);
      setMsg(null);
    } finally {
      setBusy(false);
    }
  };

  const users = status?.values?.admin_users ?? 0;

  return (
    <div className="min-h-svh bg-[radial-gradient(900px_400px_at_10%_-10%,oklch(0.72_0.19_45_/_0.08),transparent_55%)]">
      <div className="mx-auto flex min-h-svh w-full max-w-3xl flex-col px-4 py-8 md:px-8">
        <div className="mb-8 flex items-center justify-between gap-3">
          <div className="flex items-center gap-2.5">
            <div className="grid size-7 place-items-center rounded-lg bg-linear-to-br from-orange-400 to-orange-600 text-[13px] font-bold text-zinc-900">
              T
            </div>
            <div>
              <div className="text-[15px] font-semibold tracking-tight">TemperCI</div>
              <div className="font-mono text-[10px] tracking-wider text-muted-foreground uppercase">
                / Setup
              </div>
            </div>
          </div>
          {review ? (
            <Button type="button" variant="outline" onClick={() => navigate("/settings")}>
              Exit
            </Button>
          ) : null}
        </div>
      <PageHeader
        title={review ? "Review fleet setup" : "Get your fleet online"}
        description={
          review
            ? "Existing config is kept. Blank secrets stay as they are. Each step shows what is already installed."
            : "Same product shape as managed runner platforms — on hardware you own."
        }
      />
      <div className="mb-4 flex flex-wrap gap-2">
        {STEPS.map((s, i) => {
          const st = check(s.id);
          return (
            <button
              key={s.id}
              type="button"
              onClick={() => setStep(i)}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 font-mono text-[11px]",
                i === step
                  ? "border-transparent bg-primary/15 text-orange-100"
                  : "border-border text-muted-foreground",
              )}
            >
              {String(i + 1).padStart(2, "0")} {s.label}
              {st ? (
                <StatusBadge
                  status={st.status}
                  className="px-1.5 py-0 text-[10px]"
                >
                  {st.status === "ok" ? "set" : st.status === "warn" ? "check" : "todo"}
                </StatusBadge>
              ) : null}
            </button>
          );
        })}
      </div>
      <Card>
        <CardContent className="space-y-4">
        {step === 0 ? (
          <>
            <StepStatus check={check("access")} />
            <div className="space-y-2">
              <Label>Auth mode</Label>
              <Select value={data.auth_mode} onValueChange={(v) => patch({ auth_mode: v })}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="password">Password — local users</SelectItem>
                  <SelectItem value="open">Open — private network only</SelectItem>
                </SelectContent>
              </Select>
            </div>
            {data.auth_mode === "password" ? (
              <>
                {users > 0 ? (
                  <p className="text-sm text-muted-foreground">
                    {users} user(s) already exist. Leave the fields below blank to keep them.
                  </p>
                ) : null}
                <div className="space-y-2">
                  <Label>Admin email</Label>
                  <Input
                    value={data.admin_email}
                    onChange={(e) => patch({ admin_email: e.target.value })}
                    placeholder={users > 0 ? "Leave blank to keep current users" : undefined}
                  />
                </div>
                <div className="space-y-2">
                  <Label>Admin password</Label>
                  <Input
                    type="password"
                    value={data.admin_password}
                    onChange={(e) => patch({ admin_password: e.target.value })}
                    placeholder={users > 0 ? "Leave blank to keep current users" : undefined}
                  />
                </div>
              </>
            ) : null}
          </>
        ) : null}
        {step === 1 ? (
          <>
            <StepStatus check={check("github")} />
            <GitHubAppGuide orgSlug={data.github_org} webhookURL={status?.webhook?.suggested_url} />
            <div className="rounded-lg border border-border bg-muted/20 p-3">
              <div className="mb-2 text-sm font-semibold tracking-tight">GitHub webhook</div>
              <WebhookStatus webhook={status?.webhook} />
            </div>
            <div className="space-y-2">
              <Label>GitHub organization login</Label>
              <Input
                placeholder="e.g. coatcheckapp (from github.com/orgs/…)"
                value={data.github_org}
                onChange={(e) => patch({ github_org: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label>GitHub App ID</Label>
              <Input value={data.github_app_id} onChange={(e) => patch({ github_app_id: e.target.value })} />
            </div>
            <div className="space-y-2">
              <Label>Webhook secret</Label>
              <Input
                type="password"
                value={data.github_webhook_secret}
                onChange={(e) => patch({ github_webhook_secret: e.target.value })}
                placeholder={webhookSet ? "Leave blank to keep current" : undefined}
              />
            </div>
            <p className="text-sm text-muted-foreground">
              Continue writes these values to the server. Apply on Review finishes setup and restarts services.
            </p>
            <div className="space-y-2">
              <Label>App private key (PEM)</Label>
              <Textarea
                placeholder={
                  pemSet ? "Leave blank to keep the PEM already on disk" : "-----BEGIN RSA PRIVATE KEY-----"
                }
                value={data.github_app_private_key_pem}
                onChange={(e) => patch({ github_app_private_key_pem: e.target.value })}
                className="font-mono text-xs"
              />
            </div>
          </>
        ) : null}
        {step === 2 ? (
          <>
            <StepStatus check={check("agent")} />
            <div className="space-y-2">
              <Label>
                Agent token{" "}
                <span className="text-muted-foreground">
                  {tokenSet ? "(blank = keep current)" : "(blank = auto-generate)"}
                </span>
              </Label>
              <Input
                className="font-mono"
                type="password"
                value={data.agent_token}
                onChange={(e) => patch({ agent_token: e.target.value })}
                placeholder={tokenSet ? "Leave blank to keep current" : undefined}
              />
            </div>
            <div className="space-y-2">
              <Label>Listen address</Label>
              <Input
                className="font-mono"
                value={data.listen_addr}
                onChange={(e) => patch({ listen_addr: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label>
                Cache listen address <span className="text-muted-foreground">(blank = disabled)</span>
              </Label>
              <Input
                className="font-mono"
                placeholder="e.g. 127.0.0.1:8743"
                value={data.cache_listen_addr}
                onChange={(e) => patch({ cache_listen_addr: e.target.value })}
              />
            </div>
            <p className="text-sm text-muted-foreground">
              Use the same <code className="font-mono text-xs">agent_token</code> in every host{" "}
              <code className="font-mono text-xs">agent.toml</code>.
            </p>
          </>
        ) : null}
        {step === 3 ? (
          <>
            <StepStatus check={check("services")} />
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <InstallCheck ok={Boolean(status?.values?.hostctl)} label="hostctl" detail="restart helper" />
              <InstallCheck
                ok={(status?.values?.agents_registered ?? 0) > 0}
                label="Agent registered"
                detail={`${status?.values?.agents_registered ?? 0} host(s)`}
              />
              <InstallCheck ok={Boolean(status?.values?.guest_image)} label="Guest image" detail="ubuntu runner rootfs" />
              <InstallCheck ok={Boolean(status?.values?.guest_kernel)} label="Guest kernel" detail="Firecracker vmlinux" />
              <InstallCheck
                ok={webhookReceived}
                label="GitHub webhook"
                detail={
                  webhookReceived
                    ? `received ${status?.webhook?.last_event === "workflow_job" ? "job" : status?.webhook?.last_event || "delivery"}`
                    : "waiting for first TemperCI job"
                }
              />
            </div>
            <p className="text-sm text-muted-foreground">
              These are read-only checks. Missing pieces stay as they are — the wizard does not wipe them.
            </p>
          </>
        ) : null}
        {step === 4 ? (
          <>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
              <StatCard label="Auth" value={<span className="text-lg">{data.auth_mode}</span>} />
              <StatCard label="Org" value={<span className="text-lg">{data.github_org || "—"}</span>} />
              <StatCard label="App ID" value={<span className="text-lg">{data.github_app_id || "—"}</span>} />
              <StatCard
                label="Listen"
                value={<span className="font-mono text-base">{data.listen_addr}</span>}
              />
              <StatCard
                label="Cache"
                value={<span className="font-mono text-base">{data.cache_listen_addr || "disabled"}</span>}
              />
              <StatCard label="Secrets" value={<span className="text-lg">{review ? "kept" : "new"}</span>} />
              <StatCard
                label="Webhook"
                value={<span className="text-lg">{webhookReceived ? "received" : "waiting"}</span>}
                hint={
                  webhookReceived
                    ? "job webhook reached this host"
                    : status?.webhook?.suggested_url || "dispatch a temperci- job to verify"
                }
              />
            </div>
            <p className="text-sm text-muted-foreground">
              {review
                ? "Apply writes only the fields you changed. Existing webhook secret, PEM, and agent token stay unless you typed replacements."
                : "Apply writes config + PEM, then restarts services when hostctl is installed."}
            </p>
          </>
        ) : null}
        <div className="flex flex-wrap gap-2 pt-2">
          {step > 0 ? (
            <Button type="button" variant="outline" onClick={() => setStep((s) => s - 1)}>
              Back
            </Button>
          ) : null}
          {step < 4 ? (
            <Button type="button" disabled={busy} onClick={() => void continueNext()}>
              Continue
            </Button>
          ) : (
            <Button type="button" disabled={busy} onClick={() => void apply()}>
              {review ? "Save & restart" : "Apply & restart"}
            </Button>
          )}
        </div>
        {msg ? <p className="text-sm text-emerald-400">{msg}</p> : null}
        {err ? <p className="text-sm text-destructive">{err}</p> : null}
        </CardContent>
      </Card>
      </div>
    </div>
  );
}

function StepStatus({ check }: { check?: { status: string; detail: string } }) {
  if (!check) return null;
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-muted/30 px-3 py-2 text-sm">
      <StatusBadge status={check.status}>
        {check.status === "ok" ? "already set" : check.status === "warn" ? "partial" : "not set"}
      </StatusBadge>
      <span className="text-muted-foreground">{check.detail}</span>
    </div>
  );
}

function InstallCheck({ ok, label, detail }: { ok: boolean; label: string; detail: string }) {
  return (
    <div className="rounded-lg border border-border px-3 py-3">
      <div className="mb-1 flex items-center justify-between gap-2">
        <span className="font-medium">{label}</span>
        <StatusBadge status={ok ? "ok" : "missing"}>{ok ? "installed" : "missing"}</StatusBadge>
      </div>
      <p className="text-xs text-muted-foreground">{detail}</p>
    </div>
  );
}
