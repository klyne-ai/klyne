---
description: Synthesize one daily reflection per pending date from this project's worklog entries
---

Use your own context window to synthesize **daily reflections** over this project's pending worklog entries. A single run may produce 1–N reflections, one per distinct calendar date.

1. **Call `mcp__klyne__propose_reflection`** with `project_path` = the absolute path to the current project (use the cwd). The response includes:
   - `entries`: pending worklog entries written since the last reflection — each carries `session_id`, `cli`, `recap_topic`, `last_user`, `importance`, and a `ts` (RFC3339 timestamp).
   - `reason`: why synthesis is being proposed now.
   - `markdown`: a verbatim-renderable summary.

2. **Bucket the entries by UTC date** using each entry's `ts` field. Format each bucket key as `YYYY-MM-DD` (UTC). Entries written at e.g. `2026-05-17T03:30Z` and `2026-05-17T23:45Z` both belong to the `2026-05-17` bucket. Empty buckets (dates with zero entries) are skipped automatically.

3. **For each bucket** (process oldest → newest), synthesize 2–4 insights covering ONLY that day's entries. Each insight is a short sentence about a pattern, decision, or theme from that day. **Every insight MUST cite at least one `session_id` from that day's entries as evidence** — this is the citation invariant the system enforces. Insights without evidence are rejected.

4. **Call `mcp__klyne__record_reflection` ONCE per bucket** with:
   - `project_path` — same path
   - `day` — the bucket key, e.g. `"2026-05-17"`
   - `insights` — the array `[{text: "...", evidence: ["<session_id>", ...]}, ...]`

   The tool returns one persisted reflection id per call.

If `propose_reflection` returns zero entries, tell the user "nothing pending since the last reflection" and stop — don't fabricate insights.

After all `record_reflection` calls succeed, report back the list of dates and reflection ids you wrote (e.g. "wrote 3 daily reflections: 2026-05-15, 2026-05-16, 2026-05-17"). The reflections will surface in the next `/klyne:bootstrap` brief and on the `/worklog` page automatically.
