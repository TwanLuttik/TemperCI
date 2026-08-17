import { useCallback, useEffect, useState } from "react";
import { Navigate, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { api, type Me, type Overview, type SetupStatus } from "./api";
import { Layout } from "./components/Layout";
import { HostsPage } from "./pages/HostsPage";
import { JobDetailPage } from "./pages/JobDetailPage";
import { JobsPage } from "./pages/JobsPage";
import { LoginPage } from "./pages/LoginPage";
import { OverviewPage } from "./pages/OverviewPage";
import { SettingsPage } from "./pages/SettingsPage";
import { SetupPage } from "./pages/SetupPage";
import { UsersPage } from "./pages/UsersPage";
import { VMsPage } from "./pages/VMsPage";

export default function App() {
  const [setup, setSetup] = useState<SetupStatus | null>(null);
  const [me, setMe] = useState<Me | null>(null);
  const [overview, setOverview] = useState<Overview | null>(null);
  const [bootError, setBootError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const location = useLocation();
  const navigate = useNavigate();

  const refresh = useCallback(async () => {
    setBootError(null);
    const st = await api<SetupStatus>("/api/v1/setup/status");
    setSetup(st);
    if (st.needs_setup) {
      setMe({ ok: true, auth_mode: st.auth_mode, admin: true, open: true });
      return;
    }
    if (st.auth_mode === "open") {
      setMe(await api<Me>("/api/v1/me"));
      return;
    }
    try {
      setMe(await api<Me>("/api/v1/me"));
    } catch {
      setMe(null);
    }
  }, []);

  useEffect(() => {
    refresh()
      .catch((e: Error) => setBootError(e.message))
      .finally(() => setLoading(false));
  }, [refresh]);

  useEffect(() => {
    if (!setup || setup.needs_setup || !me) return;
    api<Overview>("/api/v1/overview")
      .then(setOverview)
      .catch(() => setOverview(null));
  }, [setup, me, location.pathname]);

  if (loading) {
    return <div className="content loading">Loading console…</div>;
  }
  if (bootError) {
    return (
      <div className="content">
        <div className="err">Failed to load: {bootError}</div>
      </div>
    );
  }
  if (!setup) {
    return <div className="content err">No setup status</div>;
  }

  if (setup.needs_setup) {
    return (
      <Layout setup={setup} me={me} overview={overview} onLogout={async () => {}}>
        <Routes>
          <Route path="/setup" element={<SetupPage onDone={async () => { await refresh(); navigate("/"); }} />} />
          <Route path="*" element={<Navigate to="/setup" replace />} />
        </Routes>
      </Layout>
    );
  }

  if (setup.auth_mode === "password" && !me) {
    return (
      <Layout setup={setup} me={null} overview={null} onLogout={async () => {}}>
        <Routes>
          <Route
            path="/login"
            element={
              <LoginPage
                onDone={async () => {
                  await refresh();
                  navigate("/");
                }}
              />
            }
          />
          <Route path="*" element={<Navigate to="/login" replace />} />
        </Routes>
      </Layout>
    );
  }

  const logout = async () => {
    await api("/api/v1/auth/logout", { method: "POST", body: "{}" });
    setMe(null);
    navigate("/login");
  };

  return (
    <Layout setup={setup} me={me} overview={overview} onLogout={logout}>
      <Routes>
        <Route path="/" element={<OverviewPage onOverview={setOverview} />} />
        <Route path="/hosts" element={<HostsPage />} />
        <Route path="/vms" element={<VMsPage />} />
        <Route path="/jobs" element={<JobsPage />} />
        <Route path="/jobs/:id" element={<JobDetailPage />} />
        <Route path="/settings" element={<SettingsPage onOverview={setOverview} />} />
        {me?.admin && setup.auth_mode === "password" ? (
          <Route path="/users" element={<UsersPage />} />
        ) : null}
        <Route path="/login" element={<Navigate to="/" replace />} />
        <Route path="/setup" element={<Navigate to="/" replace />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Layout>
  );
}
