package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/contexthealth"
	"github.com/klyne-ai/klyne/internal/mcpserver"
)

// adviseTimeout caps how long Advise spends per invocation. The
// UserPromptSubmit hook must not delay the user's prompt — anything
// taking longer than this is treated as "no advisory this time."
const adviseTimeout = 2 * time.Second

// fiveHourCacheMs is how long a previously-aggregated 5-hour summary
// is reused before recomputing. 60 seconds is short enough that the
// number feels live, long enough that quick follow-up prompts don't
// re-walk every JSONL on disk.
const adviseFiveHourCacheMs int64 = 60_000

// klyneSummaryInstruction is appended to every UserPromptSubmit hook
// payload. It asks the assistant to emit a one-line per-turn summary
// at the end of its response so klyne can capture a real prose record
// of what happened, WITHOUT the daemon ever calling an LM itself.
//
// The Stop hook (sessionend.go) then scans the assistant's last
// message for a line matching `KLYNE_SUMMARY: ...` and writes the
// captured text into stop_summaries.ai_drafted_summary.
//
// The instruction is kept short and unambiguous — additionalContext is
// guidance, not a binding mandate, so we lean on a clear, single rule.
const klyneSummaryInstruction = "At the end of your reply, on its own line, " +
	"emit exactly one of:\n" +
	"  KLYNE_SUMMARY: <a 1-2 sentence, ≤100-word factual summary of completed work and any unfinished, blocked, deferred, or review-stage work; include literal ticket, branch, commit, and PR references when present>\n" +
	"  KLYNE_SUMMARY: skip\n" +
	"Use `skip` only when the turn was trivial and created no actionable state (no edits, commits, decisions, findings, pending requests, blockers, or review work). " +
	"Do not surround the line with code fences or quotes. " +
	"Do not omit this line."

// adviseHookInput is the JSON the UserPromptSubmit hook puts on stdin.
type adviseHookInput struct {
	CWD       string `json:"cwd,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// adviseHookOutput is the JSON shape Claude Code expects back on
// stdout when the UserPromptSubmit hook wants to inject context.
type adviseHookOutput struct {
	HookSpecificOutput adviseHookSpecificOutput `json:"hookSpecificOutput"`
}

type adviseHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// Advise is the daemon-side implementation of `klyne advise`. The
// UserPromptSubmit hook calls it on every user prompt; the handler
// resolves the active session, aggregates the 5-hour-window spend,
// runs the deterministic advisor triggers, and emits a hook JSON
// payload only when an advisory should fire.
//
// Cwd is the cwd of the calling Claude Code process (forwarded by the
// stub). Empty stdin / unparseable JSON is fine — the handler still
// resolves the session from cwd alone. Any error path returns an empty
// stdout so the user prompt proceeds.
func Advise(ctx context.Context, stdin io.Reader, cwd string) Result {
	ctx, cancel := context.WithTimeout(ctx, adviseTimeout)
	defer cancel()

	out, err := computeAdvisory(ctx, stdin, cwd)
	if err != nil {
		var errb bytes.Buffer
		fmt.Fprintf(&errb, "klyne advise: %v\n", err)
		return Result{Stderr: errb.Bytes()}
	}
	if out == "" {
		return Result{}
	}
	return Result{Stdout: []byte(out + "\n")}
}

// computeAdvisory is the side-effect-free synthesis. Returns the JSON
// string to print (empty when no advisory should fire) or an error.
//
// Mirrors cmd/klyne/advise.go::computeAdvisory; the daemon-side copy
// uses the forwarded cwd from the stub instead of os.Getwd so it
// resolves the user's project, not the daemon's launch directory.
func computeAdvisory(ctx context.Context, stdin io.Reader, fallbackCwd string) (string, error) {
	in := readAdviseInput(stdin)

	cwd := in.CWD
	if cwd == "" {
		cwd = fallbackCwd
	}
	if cwd == "" {
		// Last-resort: daemon's own cwd. Unlikely to match a real
		// session but cheaper than failing.
		w, _ := os.Getwd()
		cwd = w
	}

	path, err := resolveSessionPath(in.SessionID, cwd)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil
	}

	snap, err := mcpserver.LoadSnapshot(ctx, path)
	if err != nil {
		return "", fmt.Errorf("load snapshot: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		cfg = config.Defaults()
	}
	if cfg.Advisor.Disabled {
		return "", nil
	}

	statePath, err := contexthealth.DefaultStatePath()
	if err != nil {
		return "", err
	}
	state := contexthealth.LoadState(statePath)
	state.GarbageCollect(time.Now().UnixMilli(), int64(7*24*time.Hour/time.Millisecond))

	now := time.Now().UnixMilli()
	summary := fiveHourSummary(ctx, cfg, state, now)
	state.FiveHour.LastPct = summary.PctUsed
	state.FiveHour.LastMeasuredMs = summary.MeasuredAtMs

	advisory := contexthealth.RenderAdvisor(contexthealth.AdvisorInput{
		SessionID:      snap.SessionID,
		Messages:       snap.Messages,
		ContextFillPct: snap.ContextFillPct,
		FiveHour:       summary,
		State:          state,
		NowMs:          now,
	})

	if err := contexthealth.SaveState(statePath, advisory.State); err != nil {
		// Logged but not fatal — same behavior as the cobra path.
		fmt.Fprintf(os.Stderr, "klyne advise: state save failed: %v\n", err)
	}

	// Always emit the KLYNE_SUMMARY instruction so every turn produces
	// a one-line summary in the assistant's reply. The advisor line,
	// when present, is prepended above it. The combined payload is the
	// daemon's only contribution to context — pure deterministic text,
	// no LM call.
	context := klyneSummaryInstruction
	if advisory.Line != "" {
		context = advisory.Line + "\n\n" + context
	}
	body, err := json.Marshal(adviseHookOutput{
		HookSpecificOutput: adviseHookSpecificOutput{
			HookEventName:     "UserPromptSubmit",
			AdditionalContext: context,
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshal hook output: %w", err)
	}
	return string(body), nil
}

// readAdviseInput parses the optional stdin JSON. Empty / malformed
// stdin returns a zero-value struct rather than an error — the hook
// must still work when invoked manually.
func readAdviseInput(r io.Reader) adviseHookInput {
	if r == nil {
		return adviseHookInput{}
	}
	body, err := io.ReadAll(io.LimitReader(r, 64*1024))
	if err != nil || len(body) == 0 {
		return adviseHookInput{}
	}
	var in adviseHookInput
	if err := json.Unmarshal(body, &in); err != nil {
		return adviseHookInput{}
	}
	return in
}

// resolveSessionPath turns the optional session_id + cwd into a JSONL
// path. Returns ("", nil) when no session matches — caller treats
// that as "silent, nothing to advise on."
func resolveSessionPath(sessionID, cwd string) (string, error) {
	if sessionID != "" {
		return mcpserver.FindSessionByID(sessionID)
	}
	cands, err := mcpserver.ListSessionsForCWD(cwd)
	if err != nil {
		return "", err
	}
	if pick, ok := mcpserver.PickActiveSession(cands); ok {
		return pick.Path, nil
	}
	if len(cands) == 1 {
		// Single candidate — advise even when it's not strictly
		// "active" so freshly-spawned sessions still get a verdict.
		return cands[0].Path, nil
	}
	return "", nil
}

// fiveHourSummary returns the cached 5-hour summary when it's still
// fresh, otherwise re-aggregates. The cache lives in the advisor
// state file so it survives across daemon hook calls and across
// process restarts.
func fiveHourSummary(_ context.Context, cfg *config.Config, state contexthealth.AdvisorState, nowMs int64) contexthealth.FiveHourSummary {
	cap := cfg.Plan.FiveHourCap()
	if cap <= 0 {
		// User hasn't configured a plan; silently skip the trigger.
		return contexthealth.FiveHourSummary{Cap: 0, MeasuredAtMs: nowMs}
	}
	if state.FiveHour.LastMeasuredMs > 0 &&
		nowMs-state.FiveHour.LastMeasuredMs < adviseFiveHourCacheMs {
		return contexthealth.FiveHourSummary{
			TotalEffective: int64(state.FiveHour.LastPct / 100 * float64(cap)),
			Cap:            cap,
			PctUsed:        state.FiveHour.LastPct,
			MeasuredAtMs:   state.FiveHour.LastMeasuredMs,
		}
	}
	roots := contexthealth.DefaultFiveHourRoots()
	return contexthealth.AggregateFiveHour(roots, nowMs, cap)
}
