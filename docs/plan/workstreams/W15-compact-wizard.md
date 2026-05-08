# W15 · Compact Recovery + Wizard + Restore-Context

> **Wave:** 3 · **Effort:** L · **Depends on:** W4, W7, W11, W14 · **Recommended skills:** `general-purpose` + `regex-vs-llm-structured-text` + `frontend-patterns`

> Cross-cutting feature slice. Touches both backend and frontend, but **owns disjoint files** from W11 / W12 / W14.

---

## Universal preamble

You are working on the klyne repo. Spec at `compass_artifact_wf-d189e421-ff1d-443c-95b8-19b52fcd59b4_text_markdown.md`. §18 decisions are LOCKED. TDD-first; ≥80% coverage. After every change >30 lines run `make ci`. Use `superpowers:verification-before-completion` before claiming done.

**Single-writer rule:** modify only files under "Owned paths". You import `MessageBubble`, `CostPanel`, etc. from W14 read-only. If a W14 component needs a change, open a PR back to W14 — don't edit it.

---

## Goal

Implement the **killer demo** flow C (`/compact` recovery) plus the first-run wizard (flow A) and the "Open in CLI" command builder.

---

## Spec sections

- §6 flow A (wizard) and flow C (`/compact` recovery)
- §8 (wizard model picker — selector reasons + per-task overrides)
- §13 risk #4 (heuristic compact detection)

---

## Owned paths

### Backend
```
internal/api/handlers/restore.go               (GET /sessions/:id/restore — returns Markdown + last messages + resume command)
internal/api/handlers/restore_test.go
internal/api/handlers/wizard.go                (GET /wizard/detect, POST /wizard/complete)
internal/api/handlers/wizard_test.go
internal/connectors/claude/compact.go          (heuristic: synthetic summary message + token-count drop signal)
internal/connectors/claude/compact_test.go
internal/resume/builder.go                     (claude --resume / codex resume command builders, OS-quoted)
internal/resume/builder_test.go
```

### Frontend
```
ui/src/lib/components/RestoreContext.svelte
ui/src/lib/components/RestoreContext.test.ts
ui/src/lib/components/ModelPicker.svelte
ui/src/lib/components/ModelPicker.test.ts
ui/src/lib/components/Wizard/Welcome.svelte
ui/src/lib/components/Wizard/Detection.svelte
ui/src/lib/components/Wizard/ModelPick.svelte
ui/src/lib/components/Wizard/Done.svelte
ui/src/lib/components/Wizard/*.test.ts
ui/src/routes/wizard/+page.svelte
ui/src/routes/settings/+page.svelte
```

---

## Inputs

- W4 Claude connector — your `compact.go` lives in `internal/connectors/claude/` but is a separate file the connector imports.
- W7 router — register your handlers via `RegisterMounter` (do **not** edit W7's files).
- W11 selector — call `Pick()` to populate "recommended" badges in the wizard.
- W14 UI components — `import` them into Wizard and RestoreContext, never modify.
- W6 config loader — POST `/wizard/complete` writes settings via `config.Save()`.

---

## Compact detection heuristic (`compact.go`)

Claude Code does not (as of May 2026) emit a clean "compaction event" line. Detect by:
1. A synthetic summary message appearing in the JSONL (look for the system/summary marker observed in the W0 fixture).
2. A token-count drop: next assistant message reports a much smaller `tokens_in` than the trailing message before the marker.

When both conditions met → emit a `CompactDetected{session_id, ts}` event via the same channel the connector uses, plus write a row to `compact_events` table.

Use the `regex-vs-llm-structured-text` skill — this is structured detection, no LLM.

---

## Restore endpoint (`GET /sessions/:id/restore`)

Returns:
```json
{
  "session_id": "...",
  "markdown": "...rolling summary + last 20 messages, formatted as a single Markdown block...",
  "resume_command": "claude --resume 0193abc...",
  "last_messages": [...]
}
```

The Markdown block is what the user pastes into Claude Code to recover context. It must include:
- Latest summary (from `session_summaries`).
- Last 20 raw messages.
- A "Decisions made" section if extractable from summaries.

---

## Wizard flow (4 screens, spec §6 flow A)

1. **Welcome.** Static copy from spec §6.
2. **Detection.** GET `/wizard/detect` returns: JSONL roots present (Claude/Codex), env keys (Anthropic/OpenAI/Gemini/Ollama). Display green check / grey dash per item.
3. **ModelPick.** For each task (Summarize, Title), show selector's recommended model + reason. User can accept all or override per-task via `ModelPicker`.
4. **Done.** POST `/wizard/complete` writes config; redirects to dashboard.

---

## Resume command builder (`internal/resume/builder.go`)

```go
func ClaudeCmd(sessionID, projectPath string) string  // → "claude --resume 0193abc..."
func CodexCmd(sessionID, projectPath string) string   // → "codex resume --last" (Codex resumes last by default; document)
```

OS-aware quoting:
- Unix: shell-quote `projectPath`.
- Windows: backslash-quote and wrap in `"..."` if it contains spaces.

---

## Acceptance criteria

- [ ] **Synthetic fixture with `/compact` event** triggers detection; emits `CompactDetected` SSE; writes `compact_events` row.
- [ ] **Restore endpoint returns Markdown bundle exactly matching golden file.** (Use a golden file: `internal/api/handlers/testdata/restore_golden.md`.)
- [ ] **"Open in CLI"** produces correctly-quoted commands per OS for both `claude --resume` and `codex resume`. Test on Windows with backslash paths.
- [ ] **Wizard happy path** end-to-end: Welcome → Detection → ModelPick → Done → config.toml written → redirect.
- [ ] Wizard correctly displays the "I only have Claude Pro" path from spec §8 (recommends Gemini API or Ollama).
- [ ] 80%+ coverage on new code (backend + frontend).

---

## Testing requirements (TDD-first)

Backend:
1. `TestCompact_DetectedFromFixture` — load fixture with `/compact` marker; assert detection.
2. `TestCompact_NotTriggeredWithoutMarker`.
3. `TestCompact_NotTriggeredOnTokenDropWithoutMarker` — token drop alone is not enough.
4. `TestRestoreEndpoint_GoldenFile`.
5. `TestWizardDetect_AllCombinations` — env permutations.
6. `TestWizardComplete_WritesConfig`.
7. `TestResumeBuilder_PerOS` — table-driven across `runtime.GOOS`.

Frontend:
8. `Wizard/*.test.ts` — each screen renders, advances correctly.
9. `RestoreContext.test.ts` — opens, fetches, copies to clipboard.
10. `ModelPicker.test.ts` — change dropdown, emits change event.

Manual: walk through flow C in a browser using a synthetic compact fixture.

---

## Hard boundaries

- Do **NOT** modify W14's components — `import` them.
- Do **NOT** edit W7's router files — register via `RegisterMounter`.
- Do **NOT** edit W4's connector files — your `compact.go` is a separate file in the same package, imported by `claude.go`. (Coordinate with W4: the connector imports `compact.Detect()` from your file.)
- Do **NOT** modify W11's selector — call it.

---

## Done

When the killer demo works end-to-end on a fresh machine: a real Claude Code `/compact` event triggers a notification in klyne, the user clicks "Restore context", a Markdown block lands on their clipboard, and pasting it into Claude Code recovers the conversation.
