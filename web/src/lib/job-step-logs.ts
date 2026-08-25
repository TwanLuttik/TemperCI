import type { JobStep } from "../api";

export type WorkflowGroup = { title: string; body: string };

function norm(s?: string): string {
  return String(s || "").toLowerCase();
}

/** Drop GitHub's leading timestamp so the pane looks like the Actions UI. */
export function displayLogLine(raw: string): string {
  let line = raw.replace(/^\uFEFF/, "").replace(/^\d{4}-\d{2}-\d{2}T[\d:.]+Z /, "");
  if (line.startsWith("##[command]")) return "$ " + line.slice("##[command]".length);
  if (line.startsWith("##[error]")) return "Error: " + line.slice("##[error]".length);
  if (line.startsWith("##[warning]")) return "Warning: " + line.slice("##[warning]".length);
  if (line.startsWith("##[notice]")) return line.slice("##[notice]".length);
  if (line.startsWith("##[debug]")) return line.slice("##[debug]".length);
  return line;
}

function isStepGroupTitle(title: string): boolean {
  return /^(run|post)\b/i.test(title.trim());
}

/** Split an official job log into one section per workflow step.
 *  Nested ##[group]s (checkout internals, “Environment details”) stay inside
 *  the parent. Groups that are not Run/Post (GITHUB_TOKEN Permissions) fold
 *  into Set up job or the previous step. */
export function parseWorkflowGroups(log?: string): WorkflowGroup[] {
  if (!log) return [];
  const lines = log.replace(/^\uFEFF/, "").split(/\r?\n/);
  const raw: WorkflowGroup[] = [];
  let cur: { title: string; lines: string[]; depth: number } | null = null;
  const flush = () => {
    if (!cur) return;
    raw.push({ title: cur.title, body: cur.lines.join("\n").replace(/\s+$/, "") });
    cur = null;
  };
  for (const rawLine of lines) {
    const line = displayLogLine(rawLine);
    if (line.startsWith("##[group]")) {
      const title = line.slice("##[group]".length).trim();
      // Live guest extract often omits ##[endgroup], so a later Run/Post
      // would otherwise nest inside the first step. Always promote those.
      if (isStepGroupTitle(title) || !cur || cur.depth <= 0) {
        flush();
        cur = { title, lines: title ? [title] : [], depth: 1 };
        continue;
      }
      cur.depth++;
      cur.lines.push(line);
      continue;
    }
    if (line === "##[endgroup]") {
      if (!cur) continue;
      cur.depth--;
      if (cur.depth <= 0) {
        flush();
      } else {
        cur.lines.push(line);
      }
      continue;
    }
    if (!cur) {
      // Preamble before the first group (runner version, machine name).
      cur = { title: "Set up job", lines: line ? [line] : [], depth: 0 };
      continue;
    }
    cur.lines.push(line);
  }
  flush();

  const out: WorkflowGroup[] = [];
  let setup: string[] = [];
  const pushSetup = () => {
    if (!setup.length) return;
    out.push({ title: "Set up job", body: setup.join("\n").replace(/\s+$/, "") });
    setup = [];
  };
  for (const g of raw) {
    if (isStepGroupTitle(g.title)) {
      pushSetup();
      out.push(g);
      continue;
    }
    if (out.length === 0) {
      setup.push(g.body);
    } else {
      out[out.length - 1] = {
        title: out[out.length - 1].title,
        body: (out[out.length - 1].body + "\n" + g.body).replace(/\s+$/, ""),
      };
    }
  }
  pushSetup();
  return out;
}

function groupTitleKey(title: string): string {
  return title.replace(/^run\s+/i, "").trim().toLowerCase();
}

function namesAlign(groupTitle: string, stepName: string): boolean {
  const g = groupTitleKey(groupTitle);
  const n = norm(stepName).trim();
  if (!g || !n) return false;
  if (g === n) return true;
  if (g.startsWith(n) || n.startsWith(g)) return true;
  if (n.length >= 8 && g.includes(n)) return true;
  if (g.length >= 8 && n.includes(g)) return true;
  const action = g.match(/actions\/([a-z0-9-]+)/);
  if (action && n.includes(action[1].replace(/-/g, " "))) return true;
  return false;
}

function stepHasLog(step: JobStep): boolean {
  const st = norm(step.status);
  if (st === "in_progress") return true;
  if (st !== "completed") return false;
  return norm(step.conclusion) !== "skipped";
}

/** Map GitHub step numbers to the matching log body from a job workflow log. */
export function stepLogsByNumber(steps: JobStep[] | undefined, log?: string): Record<number, string> {
  const groups = parseWorkflowGroups(log);
  const out: Record<number, string> = {};
  if (!steps?.length || !groups.length) return out;
  const used = new Set<number>();
  const started = steps.filter(stepHasLog);

  for (const step of started) {
    const i = groups.findIndex((g, gi) => !used.has(gi) && namesAlign(g.title, step.name));
    if (i >= 0) {
      out[step.number] = groups[i].body;
      used.add(i);
    }
  }
  const leftover = groups.filter((_, i) => !used.has(i));
  const live = started.find((s) => norm(s.status) === "in_progress");
  // A single "Run (live)" blob from the guest extractor belongs on the
  // current step, not the first completed one.
  const liveBlobs = leftover.filter((g) => groupTitleKey(g.title) === "(live)");
  const rest = leftover.filter((g) => groupTitleKey(g.title) !== "(live)");
  if (live && liveBlobs.length && out[live.number] == null) {
    out[live.number] = liveBlobs.map((g) => g.body).join("\n");
  }
  let li = 0;
  for (const step of started) {
    if (out[step.number] != null) continue;
    if (li < rest.length) out[step.number] = rest[li++].body;
  }
  return out;
}
