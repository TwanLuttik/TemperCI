import type { ReactNode } from "react";

import { Card, CardContent } from "@/components/ui/card";
import { cn } from "@/lib/utils";

export function StatCard({
  label,
  value,
  hint,
  className,
}: {
  label: string;
  value: ReactNode;
  hint?: ReactNode;
  className?: string;
}) {
  return (
    <Card className={cn("relative overflow-hidden py-0 shadow-none", className)}>
      <div className="absolute inset-y-0 left-0 w-0.5 bg-primary/70" />
      <CardContent className="px-4 py-3.5">
        <div className="font-mono text-[10px] tracking-widest text-muted-foreground uppercase">{label}</div>
        <div className="mt-1.5 text-[1.65rem] leading-none font-semibold tracking-tight tabular-nums">{value}</div>
        {hint ? <div className="mt-1.5 text-xs text-muted-foreground">{hint}</div> : null}
      </CardContent>
    </Card>
  );
}
