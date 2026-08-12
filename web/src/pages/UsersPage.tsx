import { useEffect, useState } from "react";
import { api, type User } from "../api";

export function UsersPage() {
  const [users, setUsers] = useState<User[]>([]);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState("viewer");
  const [err, setErr] = useState<string | null>(null);

  const load = () =>
    api<{ users: User[] }>("/api/v1/users")
      .then((d) => setUsers(d.users || []))
      .catch((e: Error) => setErr(e.message));

  useEffect(() => {
    void load();
  }, []);

  const create = async () => {
    setErr(null);
    try {
      await api("/api/v1/users", {
        method: "POST",
        body: JSON.stringify({
          email,
          password,
          role,
          must_change_password: true,
        }),
      });
      setEmail("");
      setPassword("");
      await load();
    } catch (e) {
      setErr((e as Error).message);
    }
  };

  return (
    <>
      <div className="page-head">
        <p className="page-kicker">/ Users</p>
        <h1>Team access</h1>
        <p className="lead">Local accounts only. Create credentials here — no invite emails are sent.</p>
      </div>
      <div className="split">
        <div className="panel" style={{ overflow: "auto", padding: "8px 12px 4px" }}>
          <table>
            <thead>
              <tr>
                <th>Email</th>
                <th>Role</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody>
              {users.length === 0 ? (
                <tr>
                  <td colSpan={3}>
                    <div className="empty">No users</div>
                  </td>
                </tr>
              ) : (
                users.map((u) => (
                  <tr key={u.id}>
                    <td>{u.email}</td>
                    <td>
                      <span className="badge">{u.role}</span>
                    </td>
                    <td style={{ color: "var(--muted)" }}>{u.created_at || ""}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        <div className="panel">
          <div className="panel-head">
            <h2>Create user</h2>
            <span className="meta">invite</span>
          </div>
          <label>Email</label>
          <input value={email} onChange={(e) => setEmail(e.target.value)} />
          <label>Temporary password</label>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <label>Role</label>
          <select value={role} onChange={(e) => setRole(e.target.value)}>
            <option value="viewer">viewer</option>
            <option value="admin">admin</option>
          </select>
          <div className="row" style={{ marginTop: 14 }}>
            <button type="button" onClick={() => void create()}>
              Create user
            </button>
          </div>
          {err ? <div className="err">{err}</div> : null}
        </div>
      </div>
    </>
  );
}
