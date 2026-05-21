// Scenario: shipping a small feature.
// Expected outcome: at least one stop_summaries row with importance >= 7
// (commit_landed event tag elevates the score), ai_drafted_summary
// non-empty (assistant emitted KLYNE_SUMMARY).
export default {
  name: "feature",
  prompts: [
    "Create a file README.md containing a single line: '# klyne test feature'. Stage and commit it with message 'feat: add readme'.",
    "Append the line 'Status: ready' to README.md and amend the same commit (--amend, no edit). Verify with `git log --oneline -1`.",
  ],
  verify(rows) {
    const failures = [];
    if (rows.length < 1) {
      failures.push({ kind: "no-rows", detail: "no stop_summaries written" });
    }
    const populated = rows.filter((r) => r.ai_drafted_summary && r.ai_drafted_summary.trim() !== "");
    if (populated.length === 0) {
      failures.push({
        kind: "no-ai-drafted-summary",
        detail: "every row has empty ai_drafted_summary — instruction not followed?",
      });
    }
    const important = rows.filter((r) => Number(r.importance) >= 7);
    if (important.length === 0) {
      failures.push({
        kind: "no-important-row",
        detail: "no row reached importance >= 7 despite a commit — event-tag derivation may be broken",
      });
    }
    return {
      scenario: "feature",
      rows: rows.length,
      populated: populated.length,
      important: important.length,
      failures,
    };
  },
};
