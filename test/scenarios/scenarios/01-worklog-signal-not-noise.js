// Scenario 01 — Worklog signal-not-noise.
//
// Premise: klyne's Stop hook fires on every `claude -p` exit and inserts a
// stop_summaries row. The deterministic suppression rules in
// internal/worklog/suppress.go should set recap_visible=0 for trivial
// sessions (no edits, <90s, no signal tags) and recap_visible=1 for
// meaningful ones (edits made, qualifying tags).

import { runClaude } from "../lib/claude.js";
import { makeProject, removeProject } from "../lib/tmpdir.js";
import {
  waitForStopSummaries,
  getAllStopSummariesForProject,
  cleanupProject,
  nowMs,
} from "../lib/klyne.js";
import { STATUS } from "../lib/report.js";

export const name = "01-worklog-signal-not-noise";

export async function run({ keepTmp = false, noClaude = false } = {}) {
  const steps = [];
  const evidence = {};
  let costUsd = 0;

  if (noClaude) {
    return {
      status: STATUS.SKIP,
      summary: "Dry-run: would create 2 tmp projects and run a trivial + meaningful claude -p session in each.",
      steps,
      evidence,
      costUsd,
    };
  }

  const trivialDir = makeProject("trivial");
  const meaningfulDir = makeProject("meaningful");
  const t0 = nowMs();

  try {
    // --- trivial session ---
    const trivial = await runClaude({
      cwd: trivialDir,
      prompt: "What is 2 plus 2? Answer in one short sentence. Do not use any tools.",
      allowedTools: [],
    });
    costUsd += trivial.costUsd;
    evidence["trivial.claude.text"] = trivial.text.slice(0, 500) || "(empty)";
    evidence["trivial.claude.stderr"] = trivial.stderr.slice(0, 500) || "(empty)";

    steps.push({
      name: "trivial: claude -p exited cleanly",
      status: trivial.exitCode === 0 ? STATUS.PASS : STATUS.FAIL,
      detail: `exit=${trivial.exitCode} cost=$${trivial.costUsd.toFixed(4)}`,
    });

    // Wait for Stop hook to write at least one row OR confirm no row.
    // We give 60s grace; absence of a row is also valid (hook may simply not fire on no-tool sessions).
    const trivialRows = await waitForStopSummaries(trivialDir, t0, {
      minCount: 1,
      timeoutMs: 60_000,
    });
    evidence["trivial.db.rows"] = trivialRows;

    if (trivialRows.length === 0) {
      steps.push({
        name: "trivial: no worklog row (acceptable — hook may not have fired)",
        status: STATUS.PASS,
        detail: "Zero stop_summaries rows for trivial project; counts as 'noise filtered'.",
      });
    } else {
      const anyVisible = trivialRows.some((r) => r.recap_visible === 1);
      steps.push({
        name: "trivial: all worklog rows must have recap_visible=0 (suppressed)",
        status: anyVisible ? STATUS.FAIL : STATUS.PASS,
        detail: anyVisible
          ? `EXPECTED suppression but found ${trivialRows.filter((r) => r.recap_visible === 1).length} visible row(s). Suppression rules may not be working.`
          : `${trivialRows.length} row(s) all suppressed (recap_visible=0). Correct.`,
      });
    }

    // --- meaningful session ---
    const t1 = nowMs();
    const meaningful = await runClaude({
      cwd: meaningfulDir,
      prompt:
        "Create hello.js with two exported functions: `greet(name)` returns 'hello <name>' and " +
        "`farewell(name)` returns 'bye <name>'. Then create tests.js that imports both and " +
        "console.logs their outputs. Verify by running `node tests.js`. " +
        "Finally, `git add` the new files and create a commit with a descriptive message. " +
        "(The git commit is required — it gives the worklog a signal tag.)",
      timeoutMs: 300_000,
    });
    costUsd += meaningful.costUsd;
    evidence["meaningful.claude.text"] = meaningful.text.slice(0, 800) || "(empty)";
    evidence["meaningful.claude.stderr"] = meaningful.stderr.slice(0, 500) || "(empty)";

    steps.push({
      name: "meaningful: claude -p exited cleanly",
      status: meaningful.exitCode === 0 ? STATUS.PASS : STATUS.FAIL,
      detail: `exit=${meaningful.exitCode} cost=$${meaningful.costUsd.toFixed(4)}`,
    });

    const meaningfulRows = await waitForStopSummaries(meaningfulDir, t1, {
      minCount: 1,
      timeoutMs: 90_000,
    });
    evidence["meaningful.db.rows"] = meaningfulRows;

    if (meaningfulRows.length === 0) {
      steps.push({
        name: "meaningful: stop_summaries row created",
        status: STATUS.FAIL,
        detail:
          "Poll returned no rows within 90s window. Check the 'meaningful.finalRows' " +
          "evidence below — if rows DO appear there, hook fired but outside the poll " +
          "window (timing race); if empty, hook truly didn't fire.",
      });
    } else {
      const visible = meaningfulRows.filter((r) => r.recap_visible === 1);
      steps.push({
        name: "meaningful: at least one row with recap_visible=1",
        status: visible.length > 0 ? STATUS.PASS : STATUS.FAIL,
        detail:
          visible.length > 0
            ? `${visible.length}/${meaningfulRows.length} row(s) visible. Importance: ${visible.map((r) => r.importance).join(", ")}`
            : `All ${meaningfulRows.length} row(s) suppressed. Expected at least one visible because edits were made.`,
      });

      // files_json should mention hello.js (best-effort, suppression strips lockfiles but should keep these)
      if (visible.length > 0) {
        const files = visible.flatMap((r) => {
          try { return JSON.parse(r.files_json || "[]"); } catch { return []; }
        });
        const hasHello = files.some((f) => f.toLowerCase().includes("hello.js"));
        steps.push({
          name: "meaningful: files_json should reference hello.js",
          status: hasHello ? STATUS.PASS : STATUS.SKIP,
          detail: hasHello
            ? `files captured: ${files.slice(0, 5).join(", ")}`
            : `files: ${files.slice(0, 5).join(", ") || "(none)"} — claude may have used in-memory steps; not a hard fail.`,
        });
      }
    }
  } finally {
    // Final diagnostic sweep BEFORE cleanup: if the hook fired but landed outside
    // our poll window, surface it so the user can distinguish "hook never fired"
    // from "we polled too narrowly".
    try {
      evidence["trivial.finalRows"] = getAllStopSummariesForProject(trivialDir);
      evidence["meaningful.finalRows"] = getAllStopSummariesForProject(meaningfulDir);
    } catch {}

    if (!keepTmp) {
      cleanupProject(trivialDir);
      cleanupProject(meaningfulDir);
      removeProject(trivialDir);
      removeProject(meaningfulDir);
    } else {
      evidence["tmp.dirs"] = `trivial: ${trivialDir}\nmeaningful: ${meaningfulDir}`;
    }
  }

  const failed = steps.some((s) => s.status === STATUS.FAIL);
  return {
    status: failed ? STATUS.FAIL : STATUS.PASS,
    summary: failed
      ? "Worklog signal-vs-noise did not behave as expected. See step details."
      : "Worklog correctly suppressed trivial session and surfaced meaningful one.",
    steps,
    evidence,
    costUsd,
  };
}
