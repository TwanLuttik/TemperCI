import type { ReactNode } from "react";

export function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="px-4 py-10 text-center text-sm text-muted-foreground">
      <strong className="mb-1.5 block font-medium text-foreground">{title}</strong>
      {children}
    </div>
  );
}
