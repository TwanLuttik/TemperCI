import { useState } from "react";
import { api } from "../api";

type Props = { onDone: () => void | Promise<void> };

export function LoginPage({ onDone }: Props) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setBusy(true);
    setErr(null);
    try {
      await api("/api/v1/auth/login", {
        method: "POST",
        body: JSON.stringify({ email, password }),
      });
      await onDone();
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="page-head">
        <p className="page-kicker">/ Access</p>
        <h1>Sign in to console</h1>
        <p className="lead">Password mode for this self-hosted TemperCI control plane.</p>
      </div>
      <div className="panel" style={{ maxWidth: 420 }}>
        <label>Email</label>
        <input
          type="email"
          autoComplete="username"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
        />
        <label>Password</label>
        <input
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") void submit();
          }}
        />
        <div className="row" style={{ marginTop: 16 }}>
          <button type="button" disabled={busy} onClick={() => void submit()}>
            Continue
          </button>
        </div>
        {err ? <div className="err">{err}</div> : null}
      </div>
    </>
  );
}
