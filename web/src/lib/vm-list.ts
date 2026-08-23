import type { VMRow } from "../hooks/useRealtime";

export function vmKey(v: Pick<VMRow, "agent_id" | "id">): string {
  return `${v.agent_id}\0${v.id}`;
}

export function compareVMByBoot(a: VMRow, b: VMRow): number {
  const ta = Date.parse(a.created_at || "") || 0;
  const tb = Date.parse(b.created_at || "") || 0;
  if (ta !== tb) {
    if (ta === 0) return 1;
    if (tb === 0) return -1;
    return ta - tb;
  }
  const aa = vmKey(a);
  const bb = vmKey(b);
  if (aa < bb) return -1;
  if (aa > bb) return 1;
  return 0;
}

/** Keep the previous row order; new VMs append (oldest-boot first among newcomers). */
export function orderVMs(prev: VMRow[], next: VMRow[]): VMRow[] {
  if (!next.length) return [];
  if (!prev.length) {
    return [...next].sort(compareVMByBoot);
  }
  const nextByKey = new Map(next.map((v) => [vmKey(v), v]));
  const kept: VMRow[] = [];
  for (const v of prev) {
    const fresh = nextByKey.get(vmKey(v));
    if (fresh) {
      kept.push(fresh);
      nextByKey.delete(vmKey(v));
    }
  }
  const added = [...nextByKey.values()].sort(compareVMByBoot);
  return [...kept, ...added];
}
