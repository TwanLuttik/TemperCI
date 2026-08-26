import type { Host, HostResources } from "../api";

export type MemorySegment = {
  key: string;
  label: string;
  mib: number;
  kind: "guest" | "reserve" | "free";
};

export type MemoryBreakdown = {
  totalMiB: number;
  allocatedMiB: number;
  reserveMiB: number;
  leftoverMiB: number;
  availMiB: number;
  exclusiveBusy: boolean;
  lastAdmit?: string;
  segments: MemorySegment[];
};

export function fmtMiB(mib?: number): string {
  if (mib == null || Number.isNaN(mib)) return "—";
  if (Math.abs(mib) >= 1024) return `${(mib / 1024).toFixed(1)} GiB`;
  return `${Math.round(mib)} MiB`;
}

export function hostMemoryBreakdown(host: Host): MemoryBreakdown | null {
  const r = host.resources;
  const total = r?.ram_total_mib ?? 0;
  if (total <= 0) return null;
  const vms = host.vms || [];
  const fromVMs = vms.reduce((n, v) => n + (v.memory_mib || 0), 0);
  const allocated = r?.allocated_ram_mib && r.allocated_ram_mib > 0 ? r.allocated_ram_mib : fromVMs;
  const reserve = r?.reserve_ram_mib ?? 0;
  const leftover = Math.max(0, total - allocated - reserve);
  const segments: MemorySegment[] = [];
  vms.forEach((v, i) => {
    const mib = v.memory_mib || 0;
    if (mib <= 0) return;
    const g = Math.round(mib / 1024);
    segments.push({
      key: v.id || `vm-${i}`,
      label: `${g}g ${v.state || "guest"}`,
      mib,
      kind: "guest",
    });
  });
  if (segments.length === 0 && allocated > 0) {
    segments.push({ key: "allocated", label: `${fmtMiB(allocated)} guests`, mib: allocated, kind: "guest" });
  }
  if (reserve > 0) {
    segments.push({ key: "reserve", label: `${fmtMiB(reserve)} host reserve`, mib: reserve, kind: "reserve" });
  }
  if (leftover > 0) {
    segments.push({ key: "free", label: `${fmtMiB(leftover)} leftover`, mib: leftover, kind: "free" });
  }
  return {
    totalMiB: total,
    allocatedMiB: allocated,
    reserveMiB: reserve,
    leftoverMiB: leftover,
    availMiB: r?.ram_avail_mib ?? 0,
    exclusiveBusy: false,
    lastAdmit: r?.last_admit_reason?.trim() || undefined,
    segments,
  };
}

export function jobWaitHint(
  status: string,
  vmId: string | undefined,
  resources?: HostResources,
  vms?: { memory_mib?: number; state?: string }[],
): string | null {
  if (status !== "assigned" || vmId) return null;
  const reason = resources?.last_admit_reason || "";
  if (reason === "ram_committed" || reason === "ram_available") {
    return "Waiting: not enough host RAM for another guest";
  }
  if (reason) {
    return `Waiting: host refused create (${reason})`;
  }
  void vms;
  return "Waiting for a microVM";
}
