# Context Rescue — eval plan

Companion to [context-rescue-strategy.md](./context-rescue-strategy.md). The
strategy doc commits klyne to answering four questions in under 10 seconds
on a real session:

1. Is this session still healthy?
2. What is bloating the context?
3. Should I continue, compact, or start fresh?
4. Can I start fresh without manually reconstructing everything?

Without an eval set, those answers are vibes. This doc defines the dataset,
labelling rubric, metrics, and pass criteria the Context Health classifier
must clear before Phase 1 ships.

## Goal

Prevent the most painful UX failure: telling the user to start fresh when the
session is actually healthy. Conversely, prevent the silent failure: telling
the user "continue" when they're hours into a session that has already lost
the plot.

Optimise primarily for **low false-rescue rate** (don't yank a user out of
flow), secondarily for **high true-rescue recall** (catch the bad ones).

## Dataset

### Size

20 sessions minimum for v1 sign-off. 50 for steady state. Distribution target:

| State        | v1 count | Steady-state count |
|--------------|----------|--------------------|
| Healthy      | 6        | 15                 |
| Drifting     | 5        | 12                 |
| Risky        | 5        | 13                 |
| Rescue now   | 4        | 10                 |

### Source

Real captured Claude Code + Codex sessions from `~/.claude/projects` and
`~/.codex/sessions`, chosen to span:

- Both CLIs (Claude + Codex), at least 30% each.
- Token range from <10K to >800K context fill.
- At least 3 sessions per state with no tool calls (chat-only).
- At least 3 sessions per state with heavy tool-call/tool-result loops.
- At least 2 long-idle-then-resumed sessions (topic-shift edge case).

### Capture mechanism

A small `cmd/eval-capture` tool that:

1. Reads the JSONL transcript.
2. Snapshots message count, token totals, top tool-call frequencies, and
   project path at a chosen "evaluation point" (the message at which we
   want the classifier to make its call).
3. Writes a fixture: `docs/eval/context-health/<session>.fixture.json`.
4. Stores the human label and reasoning in
   `docs/eval/context-health/<session>.label.yaml`.

Fixtures are checked in; raw transcripts are NOT (PII, project secrets).
The fixture format must be reproducible from the same JSONL, so the runner
can regenerate without the original file when needed.

## Labelling rubric

For each session at the evaluation point, the human labeller answers in this
exact order. The first match wins.

1. **Rescue now** — at least one of:
   - Context fill >75%.
   - The same file (>5KB content) was read 5+ times in the last 20 messages.
   - Test/build command produced the same failure 4+ times in the last 20 messages.
   - Topic of the most recent 5 user messages is unrelated to the topic of the first 5 user messages, AND fill >40%.

2. **Risky** — none of the rescue triggers fire, but at least one of:
   - Context fill 50–75%.
   - Same file read 3–4 times recently.
   - Hidden tool/system messages outnumber visible assistant messages 3:1 or worse.

3. **Drifting** — none of the above, but at least one of:
   - Context fill 30–50%.
   - User has changed task at least once in the session (heuristic: a new
     imperative verb / new file path appears in a user message after a
     previous task was completed).

4. **Healthy** — default when no other rubric matches.

The labeller writes a one-sentence reason per label. Reasons are NOT input to
the classifier; they exist for review when the classifier disagrees.

### Inter-rater check

Two labellers independently label the first 10 fixtures. Cohen's kappa must
be ≥0.7 before the rest of the dataset is labelled by a single person.
Disagreements get a third tiebreaker and the rubric is tightened to remove
the ambiguity.

## Metrics

Computed per-state and aggregate:

| Metric                 | Definition                                              | v1 target            |
|------------------------|---------------------------------------------------------|----------------------|
| Per-state precision    | TP / (TP + FP) for each of the 4 states                 | ≥0.80 each           |
| Per-state recall       | TP / (TP + FN) for each of the 4 states                 | ≥0.75 each           |
| **False-rescue rate**  | "Rescue now" predicted on a Healthy/Drifting label      | **≤0.05**            |
| **Missed-rescue rate** | "Healthy" or "Drifting" predicted on a Rescue label     | ≤0.15                |
| Adjacent-state errors  | Off-by-one (e.g. Drifting predicted as Risky) — counted but not a fail by themselves | tracked, not gated |
| Latency p95            | `/sessions/{id}/context-health` end-to-end on a 5K-msg session | ≤200 ms (no AI), ≤2.5 s (AI-polished) |

False-rescue is gated tighter than anything else — the cost of a wrong rescue
is the user losing trust in the panel forever.

### Anti-target

If the classifier achieves the targets above by always returning "Drifting"
on ambiguous sessions, that satisfies the metrics but produces a useless
product. Add a **distribution check**: predicted state distribution must be
within ±20 percentage points of the labelled distribution per CLI.

## Runner

```
go test ./internal/contexthealth -run TestEvalContextHealth -v
```

The runner:

1. Loads every `*.fixture.json` + `*.label.yaml` under `docs/eval/context-health/`.
2. Calls the classifier function under test (NOT the HTTP handler — keep
   network out of the eval loop).
3. Computes the table above.
4. Writes a Markdown report to `docs/eval/context-health/last-run.md`
   (gitignored; CI uploads as an artifact).
5. Fails the test when any v1 target is missed.

The runner runs in CI on every PR that touches `internal/contexthealth/` or
the eval fixtures themselves. CI publishes the report as a PR comment.

## Pass criteria for Phase 1 launch

All of the following:

- [ ] Dataset has at least the v1 counts per state.
- [ ] Inter-rater kappa ≥0.7 on the calibration subset.
- [ ] All v1 metric targets met on the full dataset.
- [ ] Latency p95 met on a synthetic 5K-message fixture.
- [ ] Distribution check passes (per-CLI).
- [ ] Eval runner is wired into CI and currently green on `main`.

If any box is unchecked, Phase 1 does not ship — even if the demo looks
good.

## Regression discipline

- Every reported "wrong rescue" or "missed rescue" from a real user gets
  added to the dataset (anonymised) within one week.
- The classifier may not be tuned to fit a single new fixture; it must
  improve aggregate metrics or stay neutral.
- Removing a fixture from the dataset requires a one-line justification in
  the same PR.

## Open questions

- Do we ship a "self-eval" mode where users can run the eval against their
  own sessions and see how the classifier rates them? (Yes-leaning — it's a
  trust-builder. Defer to Phase 2.)
- How do we handle Codex sessions where tool-call shape differs from
  Claude? (Likely two parallel rubric branches; capture script should
  normalise where possible.)
- What's the cost ceiling per eval run when AI polish lands in Phase 3?
  (Budget item, not a v1 blocker.)
