package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/contexthealth"
	"github.com/klyne-ai/klyne/internal/mcpserver"
)

// advise.go — `klyne advise` subcommand.
//
// This is the entry point for the Claude Code UserPromptSubmit hook.
// On every prompt submission, the hook spawns this subprocess. The
// command:
//
//  1. Resolves the active session via cwd (or the session_id from
//     the hook payload, if Claude Code provides one).
//  2. Loads the snapshot directly from JSONL.
//  3. Aggregates the 5-hour-window cross-session consumption,
//     using a 60-second cached value when fresh enough to keep
//     repeat invocations cheap.
//  4. Runs contexthealth.RenderAdvisor.
//  5. If an advisory fires, prints structured hook JSON to stdout
//     so Claude Code injects it as additional context.
//  6. Persists the updated transition state to ~/.klyne/advisor-state.json.
//
// Hard rules:
//
//   - Stdout is the hook channel. Anything printed there ends up in
//     the AI's prompt. We print exactly the structured JSON or
//     nothing at all.
//   - stderr is for human-readable status during testing; never
//     touched in production.
//   - The hook must NEVER block the user prompt. Any error returns
//     exit 0 with empty stdout.

// fiveHourCacheMs is how long the previously-measured 5-hour-window
// summary is reused before recomputing. 60 seconds is short enough
// that the meter feels live and long enough that quick prompt
// follow-ups don't repeatedly walk every JSONL file in the user's
// home dir.
const fiveHourCacheMs int64 = 60_000

// adviseTimeout caps the wall-clock budget for the entire hook
// invocation. The spec target is <300 ms p99 on a 50 MB JSONL; we
// pin a generous 2-second hard deadline so an unexpected slow
// disk read can never stall the user's prompt.
const adviseTimeout = 2 * time.Second

// hookOutput is the JSON shape Claude Code expects from a
// UserPromptSubmit hook when the hook wants to inject context
// without changing the prompt.
type hookOutput struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

type hookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// hookInput is the JSON the UserPromptSubmit hook receives on
// stdin. We only consume the cwd field so we can route to the
// right session even when the hook's spawn cwd is somewhere odd.
type hookInput struct {
	CWD       string `json:"cwd,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// newAdviseCmd registers `klyne advise`.
func newAdviseCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "advise",
		Short: "Compute the proactive session advisory (used by the UserPromptSubmit hook)",
		Long: `Compute the proactive session advisory for the active Claude Code session.

This subcommand is the entry point for the UserPromptSubmit hook
installed by 'klyne mcp install'. It reads the session JSONL,
aggregates the 5-hour-window consumption, and prints a single
structured JSON line on stdout when an advisory should fire.

Stdin (optional): a JSON object with cwd and/or session_id
fields, as Claude Code passes to UserPromptSubmit hooks.

Stdout: empty when no advisory fires; otherwise a single line
of JSON conforming to the UserPromptSubmit hook schema. Stderr:
human-readable diagnostics.

Exit code: always 0 unless invoked with bad flags. The hook must
never block the user prompt on a klyne error.`,
		RunE: runAdvise,
		// Suppress cobra's usage-on-error: a hook should fail
		// silently rather than print boilerplate to the AI's prompt.
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	return c
}

// runAdvise is the cobra command body. Catches every error path
// and returns nil (writes nothing to stdout) so the user prompt
// proceeds.
func runAdvise(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), adviseTimeout)
	defer cancel()

	out, err := computeAdvisory(ctx, cmd.InOrStdin())
	if err != nil {
		// Log to stderr but do not surface as an error — the hook
		// must remain transparent.
		fmt.Fprintf(cmd.ErrOrStderr(), "klyne advise: %v\n", err)
		return nil
	}
	if out == "" {
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), out)
	return nil
}

// computeAdvisory is runAdvise's I/O-aware body, separated so a
// test can drive it without a cobra command. Returns the JSON
// string to print, or "" when the advisor stays silent.
func computeAdvisory(ctx context.Context, stdin io.Reader) (string, error) {
	in := readHookInput(stdin)

	// Resolve session via the same logic the MCP tools use.
	cwd := in.CWD
	if cwd == "" {
		w, _ := os.Getwd()
		cwd = w
	}

	path, err := resolvePath(in.SessionID, cwd)
	if err != nil {
		return "", err
	}
	if path == "" {
		// No session in this directory — silent.
		return "", nil
	}

	snap, err := mcpserver.LoadSnapshot(path)
	if err != nil {
		return "", fmt.Errorf("load snapshot: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		// Treat config errors as "skip the 5-hour trigger" rather
		// than blocking the whole hook.
		cfg = config.Defaults()
	}
	// User-level kill switch. When set, the hook produces no
	// output for the rest of its lifetime — useful for sessions
	// where the advisor's signal is noise (e.g. developing klyne
	// itself).
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
	summary := computeFiveHourSummary(ctx, cfg, state, now)
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

	// Persist updated state. Failure here is logged but not fatal.
	if err := contexthealth.SaveState(statePath, advisory.State); err != nil {
		fmt.Fprintf(os.Stderr, "klyne advise: state save failed: %v\n", err)
	}

	if advisory.Line == "" {
		return "", nil
	}
	body, err := json.Marshal(hookOutput{
		HookSpecificOutput: hookSpecificOutput{
			HookEventName:     "UserPromptSubmit",
			AdditionalContext: advisory.Line,
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshal hook output: %w", err)
	}
	return string(body), nil
}

// readHookInput tries to parse the hook payload from stdin. Returns
// an empty hookInput when stdin is empty or malformed (the hook
// must work even when invoked manually for debugging).
func readHookInput(r io.Reader) hookInput {
	if r == nil {
		return hookInput{}
	}
	body, err := io.ReadAll(io.LimitReader(r, 64*1024))
	if err != nil || len(body) == 0 {
		return hookInput{}
	}
	var in hookInput
	if err := json.Unmarshal(body, &in); err != nil {
		return hookInput{}
	}
	return in
}

// resolvePath turns the optional sessionID + cwd into a JSONL
// transcript path. Returns ("", nil) when no session matches.
func resolvePath(sessionID, cwd string) (string, error) {
	if sessionID != "" {
		path, err := mcpserver.FindSessionByID(sessionID)
		if err != nil {
			return "", err
		}
		return path, nil
	}
	cands, err := mcpserver.ListSessionsForCWD(cwd)
	if err != nil {
		return "", err
	}
	if pick, ok := mcpserver.PickActiveSession(cands); ok {
		return pick.Path, nil
	}
	if len(cands) == 1 {
		// Single candidate — use it even when not within the
		// active window, so freshly-spawned sessions still get
		// advised.
		return cands[0].Path, nil
	}
	return "", nil
}

// computeFiveHourSummary returns the cached summary when fresh,
// otherwise re-aggregates. Honest semantics: ctx is observed only
// through the timeout mechanism — the aggregator itself is
// synchronous Go code and can't be cancelled mid-walk.
func computeFiveHourSummary(_ context.Context, cfg *config.Config, state contexthealth.AdvisorState, nowMs int64) contexthealth.FiveHourSummary {
	cap := cfg.Plan.FiveHourCap()
	if cap <= 0 {
		// Skip aggregation entirely when the user has not
		// configured a plan; that trigger silently no-ops.
		return contexthealth.FiveHourSummary{Cap: 0, MeasuredAtMs: nowMs}
	}
	// Cache hit: reuse a recent measurement to keep the hook fast.
	if state.FiveHour.LastMeasuredMs > 0 &&
		nowMs-state.FiveHour.LastMeasuredMs < fiveHourCacheMs {
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

// errResolveCWD is the sentinel returned when neither cwd nor
// session_id is available. Kept distinct so tests can branch on
// it directly.
var errResolveCWD = errors.New("klyne advise: no cwd and no session_id provided")
