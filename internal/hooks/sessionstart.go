package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// sessionStartHookOutput is the JSON shape Claude Code accepts for
// SessionStart hooks that want to inject context. Identical schema to
// the UserPromptSubmit advise output but with hookEventName set to
// "SessionStart".
type sessionStartHookOutput struct {
	HookSpecificOutput sessionStartHookSpecificOutput `json:"hookSpecificOutput"`
}

type sessionStartHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// SessionStart is the SessionStart hook handler. It injects the
// KLYNE_SUMMARY instruction as additionalContext exactly once per
// session — necessary because `claude --print` (one-shot mode) does
// not fire UserPromptSubmit, so without this the model never receives
// the instruction in scripted / CI / test flows.
//
// Interactive sessions also fire SessionStart, so the instruction is
// present from turn 1; the per-turn UserPromptSubmit advise re-injects
// it for robustness across long conversations where the model might
// otherwise drift from session-start instructions.
//
// Pure deterministic text — NO LM call. Failure paths return an empty
// payload (hook output is best-effort; never blocks the session).
func SessionStart(_ context.Context, stdin io.Reader) Result {
	// Drain stdin in case Claude Code is waiting for us to consume it;
	// the payload itself isn't needed (the instruction is session-
	// agnostic).
	if stdin != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(stdin, 64*1024))
	}

	body, err := json.Marshal(sessionStartHookOutput{
		HookSpecificOutput: sessionStartHookSpecificOutput{
			HookEventName:     "SessionStart",
			AdditionalContext: klyneSummaryInstruction,
		},
	})
	if err != nil {
		// Marshal can't realistically fail on this constant payload,
		// but if it ever does, swallow it — hooks must never block.
		return Result{Stderr: []byte(fmt.Sprintf("klyne session-start: marshal: %v\n", err))}
	}
	return Result{Stdout: append(body, '\n')}
}
