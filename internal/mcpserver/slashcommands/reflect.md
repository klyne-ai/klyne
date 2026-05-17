---
description: Synthesize a weekly reflection from the project's pending worklog entries
---

Use your own context window to synthesize a weekly reflection over this project's pending worklog entries. The work happens in three steps:

1. **Call `mcp__klyne__propose_reflection`** with `project_path` = the absolute path to the current project (use the cwd). The response includes:
   - `entries`: the pending worklog entries written since the last reflection (each with `session_id`, `cli`, `recap_topic`, `last_user`, `importance`, etc.)
   - `reason`: why synthesis is being proposed now (importance-sum threshold crossed / Sunday-evening cron / user-invoked)
   - `markdown`: a verbatim-renderable summary of the above

2. **Synthesize 3–5 insights** from those entries. Each insight is a short sentence about a pattern, decision, or theme that runs across multiple entries. **Every insight MUST cite at least one `session_id` from the entries list as evidence** — this is the citation invariant the system enforces. Insights without evidence are rejected.

3. **Call `mcp__klyne__record_reflection`** with `project_path` and your `insights` array, where each insight is `{text: "...", evidence: ["<session_id>", "<session_id>", ...]}`. The tool returns the persisted reflection id.

If `propose_reflection` returns zero entries, tell the user "nothing pending since the last reflection" and stop — don't fabricate insights.

After `record_reflection` succeeds, confirm the reflection id and the evidence count back to the user. The reflection will surface in the next `/klyne:bootstrap` brief automatically.
