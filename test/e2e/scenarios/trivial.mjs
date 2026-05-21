// Scenario: a trivial turn that should result in KLYNE_SUMMARY: skip.
// Expected outcome: a row exists, but ai_drafted_summary is empty
// (because "skip" maps to empty). recap_visible may be 0 due to
// the deterministic suppression rules — that's also fine.
export default {
  name: "trivial",
  prompts: [
    "Say the word 'hi' and nothing else. Do not make any file changes, run any commands, or do anything else.",
  ],
  verify(rows) {
    const failures = [];
    // A trivial turn may be fully suppressed by ShouldSuppress, in
    // which case no row at all is fine. If a row IS written, it
    // SHOULD have empty ai_drafted_summary (because the assistant
    // was instructed to emit "skip" for trivial turns).
    const populated = rows.filter((r) => r.ai_drafted_summary && r.ai_drafted_summary.trim() !== "");
    if (populated.length > 0) {
      failures.push({
        kind: "trivial-not-skipped",
        detail: "trivial turn produced a non-empty ai_drafted_summary — instruction may be ineffective or prompt was misclassified",
        sample: populated[0].ai_drafted_summary,
      });
    }
    return {
      scenario: "trivial",
      rows: rows.length,
      populated: populated.length,
      failures,
    };
  },
};
