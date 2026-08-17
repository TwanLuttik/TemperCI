import type { ReactNode } from "react";
import { NavLink } from "react-router-dom";
import type { Me, Overview, SetupStatus } from "../api";

type Props = {
  setup: SetupStatus;
  me: Me | null;
  overview: Overview | null;
  onLogout: () => void | Promise<void>;
  children: ReactNode;
};

export function Layout({ setup, me, overview, onLogout, children }: Props) {
  const showUsers = Boolean(me?.admin && setup.auth_mode === "password" && !setup.needs_setup);
  const showLogout = Boolean(setup.auth_mode === "password" && me && !me.open);

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">T</div>
          <div>
            <div className="brand-text">TemperCI</div>
            <div className="brand-sub">/ Console</div>
          </div>
        </div>
        <div className="side-section">Operate</div>
        <nav className="side-nav">
          {setup.needs_setup ? (
            <NavLink to="/setup" className={({ isActive }) => (isActive ? "active" : "")}>
              <span className="dot" />
              Setup wizard
            </NavLink>
          ) : (
            <>
              <NavLink to="/" end className={({ isActive }) => (isActive ? "active" : "")}>
                <span className="dot" />
                Overview
              </NavLink>
              <NavLink to="/hosts" className={({ isActive }) => (isActive ? "active" : "")}>
                <span className="dot" />
                Runners
              </NavLink>
              <NavLink to="/vms" className={({ isActive }) => (isActive ? "active" : "")}>
                <span className="dot" />
                MicroVMs
              </NavLink>
              <NavLink to="/jobs" className={({ isActive }) => (isActive ? "active" : "")}>
                <span className="dot" />
                Jobs
              </NavLink>
              <NavLink to="/settings" className={({ isActive }) => (isActive ? "active" : "")}>
                <span className="dot" />
                Settings
              </NavLink>
              {showUsers ? (
                <NavLink to="/users" className={({ isActive }) => (isActive ? "active" : "")}>
                  <span className="dot" />
                  Users
                </NavLink>
              ) : null}
            </>
          )}
        </nav>
        <div className="side-footer">
          <div className="pill">
            <span className="live" /> self-hosted
          </div>
          <div className="mt-2.5 leading-snug">
            Fleet control for your runners — not a SaaS tenant.
          </div>
        </div>
      </aside>
      <div className="main">
        <div className="topbar">
          <div className="crumb">
            / Console · <strong>{setup.needs_setup ? "Setup" : setup.org || "TemperCI"}</strong>
          </div>
          <div className="topbar-actions">
            {overview ? (
              <>
                <span className={`badge ${overview.fleet_ready ? "ok" : "warn"}`}>
                  {overview.fleet_ready ? "fleet ready" : "limited"}
                </span>
                {overview.org ? <span className="badge accent">{overview.org}</span> : null}
              </>
            ) : null}
            {showLogout ? (
              <button type="button" className="secondary" onClick={() => void onLogout()}>
                Sign out
              </button>
            ) : null}
          </div>
        </div>
        <div className="content">{children}</div>
      </div>
    </div>
  );
}
