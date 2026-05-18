// Scenario 04 — Reflection synthesis.
//
// klyne's reflection pipeline (internal/worklog/reflection_*.go):
//   propose_reflection MCP tool loads pending stop_summaries entries -> claude
//   synthesizes 3-5 insights -> record_reflection MCP tool persists to
//   worklog_reflections (citation invariant: evidence_entry_ids_json != '[]').
//
// We:
//   1. Run 2-3 meaningful sessions to seed visible stop_summaries entries.
//   2. Invoke /klyne:reflect via claude -p.
//   3. Verify a worklog_reflections row appears with non-empty evidence IDs.

import { runClaude } from "../lib/claude.js";
import { makeProject, removeProject } from "../lib/tmpdir.js";
import {
  waitForStopSummaries,
  waitForReflections,
  getStopSummariesForProject,
  cleanupProject,
  nowMs,
} from "../lib/klyne.js";
import { STATUS } from "../lib/report.js";

export const name = "04-reflection-synthesis";

const SEED_PROMPTS = [
  "Create utils.js with a function `slugify(str)` that lowercases and replaces non-alphanumerics with hyphens. Write a quick sanity check in a separate test.js file that calls it.",
  "Create config.js exporting an object with API_BASE, TIMEOUT_MS, MAX_RETRIES (defaults: localhost, 5000, 3). Then update test.js to also log a config field.",
  "Refactor utils.js to also export a `truncate(str, n)` function. Update test.js to exercise it.",
];

export async function run({ keepTmp = false, noClaude = false } = {}) {
  const steps = [];
  const evidence = {};
  let costUsd = 0;

  if (noClaude) {
    return {
      status: STATUS.SKIP,
      summary: "Dry-run: would run 3 seed sessions then /klyne:reflect.",
      steps,
      evidence,
      costUsd,
    };
  }

  const dir = makeProject("reflect");
  const t0 = nowMs();

  try {
    // --- seed entries ---
    for (let i = 0; i < SEED_PROMPTS.length; i++) {
      const r = await runClaude({
        cwd: dir,
        prompt: SEED_PROMPTS[i],
        timeoutMs: 240_000,
      });
      costUsd += r.costUsd;
      evidence[`seed${i + 1}.exit`] = `exit=${r.exitCode} cost=$${r.costUsd.toFixed(4)}`;
    }

    // Wait for the visible-entry rows to appear.
    const seedRows = await waitForStopSummaries(dir, t0, { minCount: 2, timeoutMs: 30_000 });
    const visibleSeeds = seedRows.filter((r) => r.recap_visible === 1);
    evidence["seed.rows"] = seedRows;

    steps.push({
      name: "seed: at least 2 visible worklog entries created",
      status: visibleSeeds.length >= 2 ? STATUS.PASS : STATUS.SKIP,
      detail: `visible=${visibleSeeds.length}/${seedRows.length}.`,
    });

    if (visibleSeeds.length < 2) {
      // Not enough seed entries for reflection to be meaningful — skip rest.
      return {
        status: STATUS.SKIP,
        summary: "Could not seed enough visible worklog entries; reflection step skipped.",
        steps,
        evidence,
        costUsd,
      };
    }

    // --- invoke reflect ---
    const tReflect = nowMs();
    const reflect = await runClaude({
      cwd: dir,
      prompt: "/klyne:reflect",
      timeoutMs: 240_000,
    });
    costUsd += reflect.costUsd;
    evidence["reflect.text"] = reflect.text.slice(0, 1200) || "(empty)";

    const reflections = await waitForReflections(dir, tReflect, { timeoutMs: 30_000 });
    evidence["reflect.rows"] = reflections;

    if (reflections.length === 0) {
      steps.push({
        name: "/klyne:reflect: at least one worklog_reflections row written",
        status: STATUS.FAIL,
        detail:
          "No reflection row appeared. Possible causes: /klyne:reflect skill not registered, " +
          "MCP server not loaded, or reflection threshold not met.",
      });
    } else {
      steps.push({
        name: "/klyne:reflect: worklog_reflections row created",
        status: STATUS.PASS,
        detail: `${reflections.length} row(s) created.`,
      });

      // Citation invariant.
      const violators = reflections.filter((r) => {
        try {
          const ids = JSON.parse(r.evidence_entry_ids_json || "[]");
          return !Array.isArray(ids) || ids.length === 0;
        } catch {
          return true;
        }
      });
      steps.push({
        name: "citation invariant: every reflection cites ≥1 evidence entry",
        status: violators.length === 0 ? STATUS.PASS : STATUS.FAIL,
        detail: violators.length === 0
          ? "All reflections cite evidence."
          : `${violators.length} reflection(s) with empty evidence_entry_ids_json.`,
      });

      // Body sanity.
      const tooShort = reflections.filter((r) => (r.body_md || "").length < 50);
      steps.push({
        name: "body sanity: each reflection body ≥ 50 chars",
        status: tooShort.length === 0 ? STATUS.PASS : STATUS.FAIL,
        detail: tooShort.length === 0
          ? "All bodies are non-trivial."
          : `${tooShort.length} reflection(s) with body_md < 50 chars.`,
      });
    }
  } finally {
    if (!keepTmp) {
      cleanupProject(dir);
      removeProject(dir);
    } else {
      evidence["tmp.dir"] = dir;
    }
  }

  const failed = steps.some((s) => s.status === STATUS.FAIL);
  return {
    status: failed ? STATUS.FAIL : STATUS.PASS,
    summary: failed
      ? "Reflection synthesis did not produce expected DB state."
      : "/klyne:reflect produced a properly-cited reflection row.",
    steps,
    evidence,
    costUsd,
  };
}
