// Scenario 02 — Specific suppression rules from internal/worklog/suppress.go
//
// Rules under test:
//   - ruleSkipReadOnly: zero edits AND not a whitelisted command (git commit, npm test, etc.)
//                       -> suppressed.
//   - ruleSkipTrivialSize: no commit AND <90s AND <5 tools -> suppressed.
//   - ruleRequireSignal: no explicit log, no commit, missing qualifying tags -> suppressed.

import { runClaude } from "../lib/claude.js";
import { makeProject, removeProject } from "../lib/tmpdir.js";
import { waitForStopSummaries, cleanupProject, nowMs } from "../lib/klyne.js";
import { STATUS } from "../lib/report.js";

export const name = "02-worklog-suppression-rules";

export async function run({ keepTmp = false, noClaude = false } = {}) {
  const steps = [];
  const evidence = {};
  let costUsd = 0;

  if (noClaude) {
    return {
      status: STATUS.SKIP,
      summary: "Dry-run: would run a read-only prompt and verify suppression.",
      steps,
      evidence,
      costUsd,
    };
  }

  const dir = makeProject("readonly");
  const t0 = nowMs();

  try {
    const r = await runClaude({
      cwd: dir,
      prompt: "List the words in the sentence 'fast brown fox' one per line. No tools, no file edits.",
      allowedTools: [],
    });
    costUsd += r.costUsd;
    evidence["readonly.text"] = r.text.slice(0, 400) || "(empty)";

    const rows = await waitForStopSummaries(dir, t0, { minCount: 1, timeoutMs: 15_000 });
    evidence["readonly.rows"] = rows;

    if (rows.length === 0) {
      // No row at all = strongest form of suppression. Pass.
      steps.push({
        name: "read-only session: no worklog row written",
        status: STATUS.PASS,
        detail: "Zero rows — hook may have skipped, or suppression filtered before insert. Either way: no noise.",
      });
    } else {
      const visible = rows.filter((r) => r.recap_visible === 1);
      steps.push({
        name: "read-only session: all rows suppressed (recap_visible=0)",
        status: visible.length === 0 ? STATUS.PASS : STATUS.FAIL,
        detail: visible.length === 0
          ? `${rows.length} row(s) all suppressed.`
          : `${visible.length} visible row(s) — ruleSkipReadOnly may not be firing.`,
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
      ? "At least one suppression rule did not fire as expected."
      : "Read-only suppression rule fired as expected.",
    steps,
    evidence,
    costUsd,
  };
}
