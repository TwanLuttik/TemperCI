import type { ReactNode } from "react";

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

export type Tone = "ok" | "warn" | "bad" | "neutral" | "accent";

export function toneFor(status?: string): Tone {
  const s = String(status || "").toLowerCase();
  if (["finished", "success", "ok", "running", "ready", "warm", "set"].includes(s)) return "ok";
  if (["failed", "error", "failure", "timeout", "cancelled", "stopped", "missing"].includes(s)) {
    return "bad";
  }
  if (
    ["started", "assigned", "minted", "pending", "busy", "limited", "unknown", "starting", "stopping", "not_installed", "check"].includes(
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

export function LiveDot({ live }: { live: boolean }) {
  return (
    <StatusBadge tone={live ? "ok" : "warn"}>
      <span className={cn("size-1.5 rounded-full", live ? "bg-emerald-400" : "bg-amber-400")} />
      {live ? "live" : "rest"}
    </StatusBadge>
  );
}
