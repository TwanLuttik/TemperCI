import { useState } from "react";
import { api, waitForHealth } from "../api";
import { GitHubAppGuide } from "../components/GitHubAppGuide";
import { PageHeader } from "../components/page-header";
import { StatCard } from "../components/stat-card";
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

const STEPS = ["Access", "GitHub App", "Agent token", "Review"];

export function SetupPage({ onDone }: Props) {
  const [step, setStep] = useState(0);
  const [data, setData] = useState<Wizard>({
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
  });
  const [msg, setMsg] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const patch = (p: Partial<Wizard>) => setData((d) => ({ ...d, ...p }));

  const apply = async () => {
    setBusy(true);
    setMsg("Applying configuration…");
    setErr(null);
    try {
      const res = await api<{ agent_token?: string; reconnect?: boolean }>("/api/v1/setup/apply", {
        method: "POST",
        body: JSON.stringify({
          auth_mode: data.auth_mode,
          admin_email: data.admin_email,
          admin_password: data.admin_password,
          github_app_id: Number(data.github_app_id),
          github_org: data.github_org,
          github_webhook_secret: data.github_webhook_secret,
          github_app_private_key_pem: data.github_app_private_key_pem,
          agent_token: data.agent_token,
          listen_addr: data.listen_addr,
          cache_listen_addr: data.cache_listen_addr,
          restart: true,
        }),
      });
      if (res.agent_token) {
        setMsg(`Config written. Agent token: ${res.agent_token}`);
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

  return (
    <>
      <PageHeader
        kicker="/ Setup"
        title="Get your fleet online"
        description="Same product shape as managed runner platforms — on hardware you own."
      />
      <div className="mb-4 flex flex-wrap gap-2">
        {STEPS.map((s, i) => (
          <span
            key={s}
            className={cn(
              "rounded-full border px-2.5 py-1 font-mono text-[11px]",
              i === step
                ? "border-transparent bg-primary/15 text-orange-100"
                : "border-border text-muted-foreground",
            )}
          >
            {String(i + 1).padStart(2, "0")} {s}
          </span>
        ))}
      </div>
      <Card>
        <CardContent className="space-y-4">
        {step === 0 ? (
          <>
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
                <div className="space-y-2">
                  <Label>Admin email</Label>
                  <Input value={data.admin_email} onChange={(e) => patch({ admin_email: e.target.value })} />
                </div>
                <div className="space-y-2">
                  <Label>Admin password</Label>
                  <Input
                    type="password"
                    value={data.admin_password}
                    onChange={(e) => patch({ admin_password: e.target.value })}
                  />
                </div>
                <p className="text-sm text-muted-foreground">
                  Invite teammates later by creating accounts in Users (no email sending).
                </p>
              </>
            ) : null}
          </>
        ) : null}
        {step === 1 ? (
          <>
            <GitHubAppGuide orgSlug={data.github_org} />
            <div className="space-y-2">
              <Label>GitHub organization login</Label>
              <Input
                placeholder="e.g. coatcheckapp (from github.com/orgs/…)"
                value={data.github_org}
                onChange={(e) => patch({ github_org: e.target.value })}
              />
            </div>
            <p className="text-sm text-muted-foreground">
              Enter the org login first so the links above point at the right organization.
            </p>
            <div className="space-y-2">
              <Label>GitHub App ID</Label>
              <Input value={data.github_app_id} onChange={(e) => patch({ github_app_id: e.target.value })} />
            </div>
            <div className="space-y-2">
              <Label>Webhook secret</Label>
              <Input
                value={data.github_webhook_secret}
                onChange={(e) => patch({ github_webhook_secret: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label>App private key (PEM)</Label>
              <Textarea
                placeholder="-----BEGIN RSA PRIVATE KEY-----"
                value={data.github_app_private_key_pem}
                onChange={(e) => patch({ github_app_private_key_pem: e.target.value })}
                className="font-mono text-xs"
              />
            </div>
          </>
        ) : null}
        {step === 2 ? (
          <>
            <div className="space-y-2">
              <Label>
                Agent token <span className="text-muted-foreground">(blank = auto-generate)</span>
              </Label>
              <Input
                className="font-mono"
                value={data.agent_token}
                onChange={(e) => patch({ agent_token: e.target.value })}
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
              <code className="font-mono text-xs">agent.toml</code>. Leave cache listen empty unless you
              want the host-local Actions cache gateway.
            </p>
          </>
        ) : null}
        {step === 3 ? (
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
            </div>
            <p className="text-sm text-muted-foreground">
              Apply writes config + PEM, then restarts services when hostctl is installed.
            </p>
          </>
        ) : null}
        <div className="flex flex-wrap gap-2 pt-2">
          {step > 0 ? (
            <Button type="button" variant="outline" onClick={() => setStep((s) => s - 1)}>
              Back
            </Button>
          ) : null}
          {step < 3 ? (
            <Button type="button" onClick={() => setStep((s) => s + 1)}>
              Continue
            </Button>
          ) : (
            <Button type="button" disabled={busy} onClick={() => void apply()}>
              Apply &amp; restart
            </Button>
          )}
        </div>
        {msg ? <p className="text-sm text-emerald-400">{msg}</p> : null}
        {err ? <p className="text-sm text-destructive">{err}</p> : null}
        </CardContent>
      </Card>
    </>
  );
}
