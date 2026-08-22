import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export function PageHeader({
  kicker,
  title,
  description,
  actions,
  className,
}: {
  kicker?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("mb-6 flex flex-wrap items-start justify-between gap-4", className)}>
      <div className="min-w-0 space-y-1.5">
        {kicker ? (
          <p className="font-mono text-[11px] tracking-wider text-primary uppercase">{kicker}</p>
        ) : null}
        <h1 className="text-[1.65rem] font-semibold tracking-tight">{title}</h1>
        {description ? (
          <div className="max-w-2xl text-sm text-muted-foreground">{description}</div>
        ) : null}
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  );
}
