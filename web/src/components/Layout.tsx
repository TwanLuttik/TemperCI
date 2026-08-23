import { useEffect, useState, type ReactNode } from "react";
import { NavLink } from "react-router-dom";
import {
  Activity,
  Boxes,
  HardDrive,
  LayoutDashboard,
  Menu,
  Server,
  Settings,
  Users,
  ChartNoAxesCombined,
  Workflow,
} from "lucide-react";

import { api, type Me, type Overview, type SetupStatus, type SystemStatus } from "../api";
import { serviceBadgeClass } from "./ServicesPanel";
import { StatusBadge } from "./status-badge";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { Sheet, SheetContent, SheetTrigger } from "@/components/ui/sheet";
import { cn } from "@/lib/utils";

type Props = {
  setup: SetupStatus;
  me: Me | null;
  overview: Overview | null;
  onLogout: () => void | Promise<void>;
  children: ReactNode;
};

const navClass = ({ isActive }: { isActive: boolean }) =>
  cn(
    "flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13.5px] font-medium no-underline transition-colors",
    isActive
      ? "bg-sidebar-accent text-sidebar-accent-foreground"
      : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-foreground",
  );

export function Layout({ setup, me, overview, onLogout, children }: Props) {
  const showUsers = Boolean(me?.admin && setup.auth_mode === "password" && !setup.needs_setup);
  const showLogout = Boolean(setup.auth_mode === "password" && me && !me.open);
  const [mobileOpen, setMobileOpen] = useState(false);

  const nav = (
    <NavLinks setup={setup} showUsers={showUsers} onNavigate={() => setMobileOpen(false)} />
  );

  return (
    <div className="flex min-h-full bg-[radial-gradient(900px_400px_at_10%_-10%,oklch(0.72_0.19_45_/_0.08),transparent_55%)]">
      <aside className="sticky top-0 hidden h-svh w-60 shrink-0 flex-col border-r border-sidebar-border bg-sidebar md:flex">
        <Brand />
        <div className="px-4 pt-4 pb-2 font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
          Operate
        </div>
        {nav}
        <div className="mt-auto space-y-2 border-t border-sidebar-border px-4 py-4 text-xs text-muted-foreground">
          <div className="inline-flex items-center gap-1.5 rounded-full border border-border bg-card px-2 py-1 font-mono text-[11px]">
            <span className="size-1.5 rounded-full bg-ok" />
            self-hosted
          </div>
          <p className="leading-snug">Fleet control for your runners — not a SaaS tenant.</p>
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-10 flex items-center justify-between gap-3 border-b border-border/80 bg-background/75 px-4 py-3 backdrop-blur-md md:px-7">
          <div className="flex items-center gap-2">
            <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
              <SheetTrigger asChild>
                <Button variant="ghost" size="icon" className="md:hidden" aria-label="Open menu">
                  <Menu />
                </Button>
              </SheetTrigger>
              <SheetContent side="left" className="w-64 bg-sidebar p-0">
                <Brand />
                <div className="px-4 pt-4 pb-2 font-mono text-[10px] tracking-widest text-muted-foreground uppercase">
                  Operate
                </div>
                {nav}
              </SheetContent>
            </Sheet>
            <p className="font-mono text-xs text-muted-foreground">
              / Console ·{" "}
              <strong className="font-medium text-foreground">
                {setup.needs_setup ? "Setup" : setup.org || "TemperCI"}
              </strong>
            </p>
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2">
            {!setup.needs_setup && me ? <TopbarServiceBadges /> : null}
            {overview ? (
              <>
                <StatusBadge tone={overview.fleet_ready ? "ok" : "warn"}>
                  {overview.fleet_ready ? "fleet ready" : "limited"}
                </StatusBadge>
                <StatusBadge tone={overview.webhook_received ? "ok" : "warn"}>
                  {overview.webhook_received ? "webhook" : "waiting for a job"}
                </StatusBadge>
                {overview.org ? <StatusBadge tone="accent">{overview.org}</StatusBadge> : null}
              </>
            ) : null}
            {showLogout ? (
              <Button type="button" variant="outline" size="sm" onClick={() => void onLogout()}>
                Sign out
              </Button>
            ) : null}
          </div>
        </header>
        <main className="mx-auto w-full max-w-[1200px] flex-1 px-4 py-6 md:px-7">{children}</main>
      </div>
    </div>
  );
}

function Brand() {
  return (
    <div className="flex items-center gap-2.5 border-b border-sidebar-border px-[18px] py-5">
      <div className="grid size-7 place-items-center rounded-lg bg-linear-to-br from-orange-400 to-orange-600 text-[13px] font-bold text-zinc-900">
        T
      </div>
      <div>
        <div className="text-[15px] font-semibold tracking-tight">TemperCI</div>
        <div className="font-mono text-[10px] tracking-wider text-muted-foreground uppercase">/ Console</div>
      </div>
    </div>
  );
}

function NavLinks({
  setup,
  showUsers,
  onNavigate,
}: {
  setup: SetupStatus;
  showUsers: boolean;
  onNavigate: () => void;
}) {
  if (setup.needs_setup) {
    return (
      <nav className="flex flex-col gap-0.5 px-2.5">
        <NavLink to="/setup" className={navClass} onClick={onNavigate}>
          <Settings className="size-4" />
          Setup wizard
        </NavLink>
      </nav>
    );
  }
  return (
    <nav className="flex flex-col gap-0.5 px-2.5">
      <NavLink to="/" end className={navClass} onClick={onNavigate}>
        <LayoutDashboard className="size-4" />
        Overview
      </NavLink>
      <NavLink to="/hosts" className={navClass} onClick={onNavigate}>
        <Server className="size-4" />
        Runners
      </NavLink>
      <NavLink to="/vms" className={navClass} onClick={onNavigate}>
        <Boxes className="size-4" />
        MicroVMs
      </NavLink>
      <NavLink to="/jobs" className={navClass} onClick={onNavigate}>
        <Workflow className="size-4" />
        Jobs
      </NavLink>
      <NavLink to="/analytics" className={navClass} onClick={onNavigate}>
        <ChartNoAxesCombined className="size-4" />
        Analytics
      </NavLink>
      <NavLink to="/cache" className={navClass} onClick={onNavigate}>
        <HardDrive className="size-4" />
        Cache
      </NavLink>
      <Separator className="my-2" />
      <NavLink to="/settings" className={navClass} onClick={onNavigate}>
        <Settings className="size-4" />
        Settings
      </NavLink>
      {showUsers ? (
        <NavLink to="/users" className={navClass} onClick={onNavigate}>
          <Users className="size-4" />
          Users
        </NavLink>
      ) : null}
    </nav>
  );
}

function TopbarServiceBadges() {
  const [st, setSt] = useState<SystemStatus | null>(null);

  useEffect(() => {
    let stop = false;
    const tick = () => {
      api<SystemStatus>("/api/v1/system/status")
        .then((s) => {
          if (!stop) setSt(s);
        })
        .catch(() => {
          /* keep last snapshot */
        });
    };
    tick();
    const id = window.setInterval(tick, 5000);
    return () => {
      stop = true;
      window.clearInterval(id);
    };
  }, []);

  if (!st) return null;

  return (
    <>
      <StatusBadge tone={toneFromSvc(st.control?.status)}>
        <Activity className="size-3" />
        control {(st.control?.status || "unknown").replaceAll("_", " ")}
      </StatusBadge>
      <StatusBadge tone={toneFromSvc(st.agent?.status)}>
        <Server className="size-3" />
        agent {(st.agent?.status || "unknown").replaceAll("_", " ")}
      </StatusBadge>
    </>
  );
}

function toneFromSvc(status?: string) {
  const cls = serviceBadgeClass(status);
  if (cls === "ok") return "ok" as const;
  if (cls === "warn") return "warn" as const;
  if (cls === "bad") return "bad" as const;
  return "neutral" as const;
}
