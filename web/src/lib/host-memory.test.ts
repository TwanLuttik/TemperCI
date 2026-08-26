import { fmtMiB, hostMemoryBreakdown, jobWaitHint } from "./host-memory";
import type { Host } from "../api";

if (fmtMiB(12288) !== "12.0 GiB") throw new Error(`fmtMiB 12g = ${fmtMiB(12288)}`);
if (fmtMiB(512) !== "512 MiB") throw new Error(`fmtMiB 512 = ${fmtMiB(512)}`);

const host: Host = {
  agent_id: "pve",
  resources: {
    ram_total_mib: 32072,
    ram_avail_mib: 19439,
    allocated_ram_mib: 12288,
    reserve_ram_mib: 2048,
    exclusive_busy: true,
  },
  vms: [{ id: "vm-e2e", state: "busy", memory_mib: 12288 }],
};

const b = hostMemoryBreakdown(host);
if (!b) throw new Error("expected breakdown");
if (b.allocatedMiB !== 12288) throw new Error(`allocated=${b.allocatedMiB}`);
if (b.reserveMiB !== 2048) throw new Error(`reserve=${b.reserveMiB}`);
if (b.leftoverMiB !== 32072 - 12288 - 2048) throw new Error(`leftover=${b.leftoverMiB}`);
if (b.exclusiveBusy) throw new Error("exclusive packing is gone");
if (!b.segments.some((s) => s.kind === "guest" && s.mib === 12288)) {
  throw new Error(`segments=${JSON.stringify(b.segments)}`);
}

if (
  jobWaitHint("assigned", "", { last_admit_reason: "ram_committed" }) !==
  "Waiting: not enough host RAM for another guest"
) {
  throw new Error("ram wait hint");
}
if (jobWaitHint("started", "vm-1", { exclusive_busy: true }) !== null) {
  throw new Error("started job should not wait");
}

console.log("host-memory.test.ts ok");
