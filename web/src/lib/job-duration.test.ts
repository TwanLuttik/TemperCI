import { jobDetailPollMs, mergeJobRow } from "./job-duration";
import type { Job } from "../api";

if (jobDetailPollMs(true, false) !== null) throw new Error("ws live: no REST poll");
if (jobDetailPollMs(true, true) !== null) throw new Error("done+ws: no REST poll");
if (jobDetailPollMs(false, true) !== null) throw new Error("done: no REST poll");
if (jobDetailPollMs(false, false) !== 2000) throw new Error("ws down: 2s REST");

const a: Job = { job_id: 1, status: "started", steps: [{ name: "x", status: "in_progress", number: 1 }] };
const b: Job = { job_id: 1, status: "finished", outcome: "success" };
const m = mergeJobRow(a, b);
if (m.status !== "finished" || m.outcome !== "success") throw new Error("merge status");
if (!m.steps || m.steps.length !== 1) throw new Error("keep previous steps");
const kept = mergeJobRow({ job_id: 1, status: "finished", outcome: "success" }, { job_id: 1, status: "started" });
if (kept.status !== "finished" || kept.outcome !== "success") throw new Error("do not regress terminal");
