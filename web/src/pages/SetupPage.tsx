import { useState } from "react";
import { api, waitForHealth } from "../api";
import { GitHubAppGuide } from "../components/GitHubAppGuide";

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
      <div className="page-head">
        <p className="page-kicker">/ Setup</p>
        <h1>Get your fleet online</h1>
        <p className="lead">
          Same product shape as managed runner platforms — on hardware you own.
        </p>
      </div>
      <div className="wizard-steps">
        {STEPS.map((s, i) => (
          <span key={s} className={i === step ? "on" : ""}>
            {String(i + 1).padStart(2, "0")} {s}
          </span>
        ))}
      </div>
      <div className="panel">
        {step === 0 ? (
          <>
            <label>Auth mode</label>
            <select
              value={data.auth_mode}
              onChange={(e) => patch({ auth_mode: e.target.value })}
            >
              <option value="password">Password — local users</option>
              <option value="open">Open — private network only</option>
            </select>
            {data.auth_mode === "password" ? (
              <>
                <label>Admin email</label>
                <input
                  value={data.admin_email}
                  onChange={(e) => patch({ admin_email: e.target.value })}
                />
                <label>Admin password</label>
                <input
                  type="password"
                  value={data.admin_password}
                  onChange={(e) => patch({ admin_password: e.target.value })}
                />
                <p className="lead" style={{ marginTop: 12 }}>
                  Invite teammates later by creating accounts in Users (no email sending).
                </p>
              </>
            ) : null}
          </>
        ) : null}
        {step === 1 ? (
          <>
            <GitHubAppGuide orgSlug={data.github_org} />
            <label>GitHub organization login</label>
            <input
              placeholder="e.g. coatcheckapp (from github.com/orgs/…)"
              value={data.github_org}
              onChange={(e) => patch({ github_org: e.target.value })}
            />
            <p className="lead" style={{ marginTop: 8 }}>
              Enter the org login first so the links above point at the right organization.
            </p>
            <label>GitHub App ID</label>
            <input
              value={data.github_app_id}
              onChange={(e) => patch({ github_app_id: e.target.value })}
            />
            <label>Webhook secret</label>
            <input
              value={data.github_webhook_secret}
              onChange={(e) => patch({ github_webhook_secret: e.target.value })}
            />
            <label>App private key (PEM)</label>
            <textarea
              placeholder="-----BEGIN RSA PRIVATE KEY-----"
              value={data.github_app_private_key_pem}
              onChange={(e) => patch({ github_app_private_key_pem: e.target.value })}
            />
          </>
        ) : null}
        {step === 2 ? (
          <>
            <label>
              Agent token <span style={{ color: "var(--dim)" }}>(blank = auto-generate)</span>
            </label>
            <input
              className="mono"
              value={data.agent_token}
              onChange={(e) => patch({ agent_token: e.target.value })}
            />
            <label>Listen address</label>
            <input
              className="mono"
              value={data.listen_addr}
              onChange={(e) => patch({ listen_addr: e.target.value })}
            />
            <p className="lead" style={{ marginTop: 12 }}>
              Use the same <code>agent_token</code> in every host <code>agent.toml</code>.
            </p>
          </>
        ) : null}
        {step === 3 ? (
          <>
            <div className="grid" style={{ margin: 0 }}>
              <div className="stat">
                <div className="label">Auth</div>
                <div className="value" style={{ fontSize: 18 }}>
                  {data.auth_mode}
                </div>
              </div>
              <div className="stat">
                <div className="label">Org</div>
                <div className="value" style={{ fontSize: 18 }}>
                  {data.github_org || "—"}
                </div>
              </div>
              <div className="stat">
                <div className="label">App ID</div>
                <div className="value" style={{ fontSize: 18 }}>
                  {data.github_app_id || "—"}
                </div>
              </div>
              <div className="stat">
                <div className="label">Listen</div>
                <div className="value" style={{ fontSize: 16, fontFamily: "var(--mono)" }}>
                  {data.listen_addr}
                </div>
              </div>
            </div>
            <p className="lead" style={{ marginTop: 16 }}>
              Apply writes config + PEM, then restarts services when hostctl is installed.
            </p>
          </>
        ) : null}
        <div className="row" style={{ marginTop: 18 }}>
          {step > 0 ? (
            <button type="button" className="secondary" onClick={() => setStep((s) => s - 1)}>
              Back
            </button>
          ) : null}
          {step < 3 ? (
            <button type="button" onClick={() => setStep((s) => s + 1)}>
              Continue
            </button>
          ) : (
            <button type="button" disabled={busy} onClick={() => void apply()}>
              Apply &amp; restart
            </button>
          )}
        </div>
        {msg ? <div className="okmsg">{msg}</div> : null}
        {err ? <div className="err">{err}</div> : null}
      </div>
    </>
  );
}
