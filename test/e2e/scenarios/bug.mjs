// Scenario: bug-finding session (no commit, prose-heavy).
//
// Expected outcome: at least one row written. KLYNE_SUMMARY is allowed
// to be either "skip" (the model judged the synthetic prompt as not
// worth logging) OR populated (real finding worth recording). What
// MUST hold is: the row exists with importance recorded, and no
// unauthorized claude --print subprocess spawned.
export default {
  name: "bug",
  prompts: [
    "Look at the file `cmd/klyne/doctor.go` in this repo (it does not exist — that's fine; just respond about what you'd look for). Identify what a missing-import bug would look like and describe how you'd catch it. Do NOT make any file changes.",
  ],
  verify(rows) {
    const failures = [];
    if (rows.length < 1) {
      failures.push({ kind: "no-rows", detail: "no stop_summaries written" });
    }
    // bug-finding without a commit may or may not produce a populated
    // ai_drafted_summary depending on whether the model judges the
    // turn substantive. Both outcomes are correct — we just verify
    // the row was written and the system didn't crash.
    return {
      scenario: "bug",
      rows: rows.length,
      populated: rows.filter((r) => r.ai_drafted_summary && r.ai_drafted_summary.trim() !== "").length,
      failures,
    };
  },
};
