import { displayLogLine, parseWorkflowGroups, stepLogsByNumber } from "./job-step-logs";
import type { JobStep } from "../api";

const sample = [
  "2026-08-25T01:08:53.0589213Z Current runner version: '2.336.0'",
  "2026-08-25T01:08:53.1000000Z ##[group]GITHUB_TOKEN Permissions",
  "2026-08-25T01:08:53.1000001Z Actions: write",
  "2026-08-25T01:08:53.1000002Z ##[endgroup]",
  "2026-08-25T01:09:08.0000000Z ##[group]Run actions/checkout@v5",
  "2026-08-25T01:09:08.0000001Z ##[command]git checkout",
  "2026-08-25T01:09:08.0000002Z ##[group]Fetching the repository",
  "2026-08-25T01:09:08.0000003Z From github.com",
  "2026-08-25T01:09:08.0000004Z ##[endgroup]",
  "2026-08-25T01:09:08.0000005Z Syncing repository",
  "2026-08-25T01:09:08.0000006Z ##[endgroup]",
  "2026-08-25T01:10:14.0000000Z ##[group]Run pnpm install --frozen-lockfile",
  "2026-08-25T01:10:14.0000001Z Lockfile is up to date",
  "2026-08-25T01:10:44.0000000Z Done in 30.9s",
  "2026-08-25T01:10:44.0000001Z ##[endgroup]",
  "2026-08-25T01:10:45.0000000Z ##[group]Run pnpm build",
  "2026-08-25T01:10:45.0000001Z $ next build",
  "2026-08-25T01:11:44.0000000Z \u2713 Compiled successfully in 59s",
].join("\n");

if (displayLogLine("2026-08-25T01:10:45.0000001Z ##[command]git checkout") !== "$ git checkout") {
  throw new Error("command line should render like GitHub $ …");
}

const groups = parseWorkflowGroups(sample);
if (groups.length !== 4) throw new Error(`groups=${groups.length} want 4: ${groups.map((g) => g.title).join(" | ")}`);
if (groups[0].title !== "Set up job") throw new Error(groups[0].title);
if (!groups[0].body.includes("Actions: write")) throw new Error("setup should keep GITHUB_TOKEN group");
if (groups[1].title !== "Run actions/checkout@v5") throw new Error(groups[1].title);
if (!groups[1].body.includes("$ git checkout")) throw new Error("checkout body");
if (!groups[1].body.includes("From github.com")) throw new Error("nested checkout group must stay inside checkout");
if (groups[3].title !== "Run pnpm build") throw new Error("incomplete last group");
if (!groups[3].body.includes("Compiled successfully")) throw new Error("open group must keep tail");

const steps: JobStep[] = [
  { name: "Set up job", status: "completed", conclusion: "success", number: 1 },
  { name: "Checkout code", status: "completed", conclusion: "success", number: 2 },
  { name: "Install dependencies", status: "completed", conclusion: "success", number: 8 },
  { name: "Start Next.js (background)", status: "in_progress", number: 14 },
  { name: "Run E2E tests", status: "pending", number: 17 },
  { name: "Wait for Next.js", status: "completed", conclusion: "skipped", number: 15 },
];
const byNum = stepLogsByNumber(steps, sample);
if (!byNum[1]?.includes("Actions: write")) throw new Error("set up job log");
if (!byNum[2]?.includes("From github.com")) throw new Error("checkout should include nested fetch");
if (!byNum[8]?.includes("Lockfile is up to date")) {
  throw new Error("install step should get pnpm install group");
}
if (!byNum[14]?.includes("Compiled successfully")) {
  throw new Error("in-progress step should get the open pnpm build group");
}
if (byNum[17]) throw new Error("pending step should not have a log");
if (byNum[15]) throw new Error("skipped step should not steal a log");

// Live extract without endgroups: each ##[group]Run is its own step.
const liveUnclosed = [
  "##[group]Run actions/checkout@v5",
  "##[command]git checkout",
  "##[group]Fetching the repository",
  "From github.com",
  "##[group]Run pnpm install --frozen-lockfile",
  "Lockfile is up to date",
  "##[group]Run pnpm build",
  "Compiled successfully",
].join("\n");
const liveSteps: JobStep[] = [
  { name: "Checkout code", status: "completed", conclusion: "success", number: 2 },
  { name: "Install dependencies", status: "completed", conclusion: "success", number: 8 },
  { name: "Start Next.js (background)", status: "in_progress", number: 14 },
];
const liveBy = stepLogsByNumber(liveSteps, liveUnclosed);
if (!liveBy[2]?.includes("From github.com")) throw new Error("unclosed checkout should stay on checkout");
if (!liveBy[8]?.includes("Lockfile is up to date")) throw new Error("unclosed install group must not nest in checkout");
if (!liveBy[14]?.includes("Compiled successfully")) throw new Error("current step should get the open last group");
if (liveBy[2]?.includes("Lockfile is up to date")) throw new Error("checkout must not swallow later Run groups");

// A single "Run (live)" blob belongs on the in-progress step.
const blob = "##[group]Run (live)\ntelemetry leftover\npnpm install";
const blobSteps: JobStep[] = [
  { name: "Checkout code", status: "completed", conclusion: "success", number: 2 },
  { name: "Install dependencies", status: "in_progress", number: 8 },
];
const blobBy = stepLogsByNumber(blobSteps, blob);
if (blobBy[2]) throw new Error("completed step should not steal the live blob");
if (!blobBy[8]?.includes("pnpm install")) throw new Error("in-progress step should get the live blob");

console.log("job-step-logs.test.ts ok");
