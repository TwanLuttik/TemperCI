import type { ReactNode } from "react";
import { LoaderCircle } from "lucide-react";

import type { RealtimeStatus } from "../hooks/useRealtime";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

export type Tone = "ok" | "warn" | "bad" | "neutral" | "accent";

export function toneFor(status?: string): Tone {
  const s = String(status || "").toLowerCase();
  if (["finished", "success", "ok", "running", "ready", "warm", "set", "in_progress"].includes(s)) return "ok";
  if (["failed", "error", "failure", "timeout", "cancelled", "stopped", "missing"].includes(s)) {
    return "bad";
  }
  if (["skipped"].includes(s)) return "neutral";
  if (
    ["started", "assigned", "minted", "pending", "queued", "busy", "limited", "unknown", "starting", "stopping", "not_installed", "check"].includes(
      s,
    )
  ) {
    return "warn";
  }
  return "neutral";
}

const toneClass: Record<Tone, string> = {
  ok: "border-transparent bg-emerald-500/15 text-emerald-400",
  warn: "border-transparent bg-amber-500/15 text-amber-400",
  bad: "border-transparent bg-red-500/15 text-red-400",
  accent: "border-transparent bg-primary/15 text-orange-100",
  neutral: "",
};

export function StatusBadge({
  tone,
  status,
  className,
  children,
}: {
  tone?: Tone;
  status?: string;
  className?: string;
  children?: ReactNode;
}) {
  const t = tone ?? toneFor(status);
  return (
    <Badge variant="outline" className={cn("font-mono text-[11px] font-medium", toneClass[t], className)}>
      {children ?? status ?? "—"}
    </Badge>
  );
}

export function LiveDot({ live, status }: { live?: boolean; status?: RealtimeStatus }) {
  const s: RealtimeStatus = status ?? (live ? "live" : "rest");
  if (s === "connecting") {
    return (
      <StatusBadge tone="neutral">
        <LoaderCircle className="size-3 animate-spin text-muted-foreground" />
        loading
      </StatusBadge>
    );
  }
  return (
    <StatusBadge tone={s === "live" ? "ok" : "warn"}>
      <span className={cn("size-1.5 rounded-full", s === "live" ? "bg-emerald-400" : "bg-amber-400")} />
      {s === "live" ? "live" : "rest"}
    </StatusBadge>
  );
}
