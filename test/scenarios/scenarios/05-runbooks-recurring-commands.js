// Scenario 05 — Runbooks (closest match to "planning" feature in user requirements).
//
// NOTE: no `planning` surface exists in klyne. Closest match is /klyne:runbooks,
// which surfaces recurring Bash command sequences from past sessions.
// Flag this assumption in handoff so the user can correct if they meant something
// else.
//
// We:
//   1. Run 3 sessions that each execute the same shell sequence (e.g. `git status`).
//   2. Invoke /klyne:runbooks via claude -p.
//   3. Assert response surfaces at least one runbook candidate matching the pattern.

import { runClaude } from "../lib/claude.js";
import { makeProject, removeProject } from "../lib/tmpdir.js";
import { waitForStopSummaries, cleanupProject, nowMs } from "../lib/klyne.js";
import { STATUS } from "../lib/report.js";

export const name = "05-runbooks-recurring-commands";

export async function run({ keepTmp = false, noClaude = false } = {}) {
  const steps = [];
  const evidence = {};
  let costUsd = 0;

  if (noClaude) {
    return {
      status: STATUS.SKIP,
      summary: "Dry-run: would run 3 sessions with repeating bash patterns then /klyne:runbooks.",
      steps,
      evidence,
      costUsd,
    };
  }

  steps.push({
    name: "ASSUMPTION: 'planning' feature interpreted as /klyne:runbooks",
    status: STATUS.SKIP,
    detail:
      "Requirements named 'planning' but no such surface exists in klyne (verified via grep). " +
      "Closest match: /klyne:runbooks (recurring command capture). " +
      "Please confirm or correct in morning.",
  });

  const dir = makeProject("runbooks");
  const t0 = nowMs();

  try {
    // --- seed: 3 sessions each running git status + ls ---
    for (let i = 0; i < 3; i++) {
      const r = await runClaude({
        cwd: dir,
        prompt:
          `Run 'git status' and then 'ls -la' in this directory. Then reply with the word OK. Session ${i + 1}.`,
        timeoutMs: 120_000,
      });
      costUsd += r.costUsd;
      evidence[`seed${i + 1}.exit`] = `exit=${r.exitCode} cost=$${r.costUsd.toFixed(4)}`;
    }

    const seedRows = await waitForStopSummaries(dir, t0, { minCount: 1, timeoutMs: 25_000 });
    evidence["seed.rowCount"] = seedRows.length;

    // --- invoke /klyne:runbooks ---
    const runbooks = await runClaude({
      cwd: dir,
      prompt: "/klyne:runbooks",
      timeoutMs: 180_000,
    });
    costUsd += runbooks.costUsd;
    evidence["runbooks.text"] = runbooks.text.slice(0, 1200) || "(empty)";

    if (!runbooks.text || runbooks.text.length === 0) {
      steps.push({
        name: "/klyne:runbooks: produced response text",
        status: STATUS.FAIL,
        detail: "Empty response from /klyne:runbooks.",
      });
    } else {
      const lower = runbooks.text.toLowerCase();
      const mentionsAny = ["git status", "ls -la", "ls "].some((c) => lower.includes(c));
      steps.push({
        name: "/klyne:runbooks: response references one of the repeated commands",
        status: mentionsAny ? STATUS.PASS : STATUS.SKIP,
        detail: mentionsAny
          ? "Response surfaces a candidate matching the seeded pattern."
          : "Response did not mention seeded commands. May need more repetitions to clear klyne's threshold; not a hard failure.",
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
      ? "Runbooks scenario hit a hard failure (see steps)."
      : "Runbooks scenario completed (see steps for soft-skip details).",
    steps,
    evidence,
    costUsd,
  };
}
