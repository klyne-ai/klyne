// Scenario: a decision is recorded mid-session.
//
// Expected outcome: row exists. KLYNE_SUMMARY may be "skip" (the
// model interprets a synthetic decision as hypothetical) or populated
// — both are valid signals. The non-negotiable: row written, no
// unauthorized claude --print spawned, no daemon-side LM call.
export default {
  name: "decision",
  prompts: [
    "We need to decide whether to ship feature X using approach A or approach B. Briefly explain why approach A is better and conclude with the decision in plain prose: 'DECISION: we will use approach A because ...'.",
  ],
  verify(rows) {
    const failures = [];
    if (rows.length < 1) {
      failures.push({ kind: "no-rows", detail: "no stop_summaries written" });
    }
    return {
      scenario: "decision",
      rows: rows.length,
      populated: rows.filter((r) => r.ai_drafted_summary && r.ai_drafted_summary.trim() !== "").length,
      failures,
    };
  },
};
