package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/klyne-ai/klyne/internal/policy"
	"github.com/klyne-ai/klyne/internal/safety"
	"github.com/klyne-ai/klyne/internal/store"
)

// preToolHookSuffix is the argv klyne is invoked with from the
// PreToolUse hook, mirroring hookCommandSuffix in hook_install.go.
const preToolHookSuffix = "pretool"

// PreToolHookInput is the JSON payload Claude Code delivers on stdin
// for every PreToolUse hook invocation.
type PreToolHookInput struct {
	// SessionID is the Claude Code session id.
	SessionID string `json:"session_id,omitempty"`
	// CWD is the working directory of the Claude Code process.
	CWD string `json:"cwd,omitempty"`
	// ToolName is the Claude tool being called (e.g. "Bash").
	ToolName string `json:"tool_name,omitempty"`
	// ToolInput holds the tool's arguments.
	ToolInput PreToolInputPayload `json:"tool_input,omitempty"`
}

// PreToolInputPayload covers the fields klyne cares about.
// Only the "command" field is used for now; other tool inputs are ignored.
type PreToolInputPayload struct {
	Command string `json:"command,omitempty"`
}

// preToolHookOutput is the JSON Claude Code expects from a PreToolUse hook.
// An empty Decision field means "allow the tool call". A systemMessage is
// injected as additional context shown to the model (but does not block).
type preToolHookOutput struct {
	HookSpecificOutput preToolHookSpecificOutput `json:"hookSpecificOutput"`
}

type preToolHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"`
	SystemMessage string `json:"systemMessage,omitempty"`
}

// PreToolHookResult is the structured outcome returned by HandlePreToolUse
// for use in tests.
type PreToolHookResult struct {
	// SnapshotID is the saved safety_snapshot row id (0 when nothing was snapshotted).
	SnapshotID int64
	// SystemMessage is the message injected into the model's context.
	SystemMessage string
	// Output is the JSON hook output ready to print to stdout.
	Output string
}

// HandlePreToolUse reads a PreToolUse hook payload from r, runs the policy
// matcher, optionally takes a snapshot, writes the snapshot to db, and
// returns the hook output JSON to emit on stdout.
//
// In v0 (snapshot-only mode) the handler NEVER blocks the tool call — it
// always returns allow regardless of severity or block_unless_confirm.
//
// db may be nil during testing; snapshot recording is skipped when nil.
func HandlePreToolUse(ctx context.Context, r io.Reader, db *store.DB, matcher *policy.Matcher) (*PreToolHookResult, error) {
	body, err := io.ReadAll(io.LimitReader(r, 256*1024))
	if err != nil {
		return emptyResult(), nil // be transparent on read errors
	}

	var input PreToolHookInput
	if len(body) > 0 {
		if err := json.Unmarshal(body, &input); err != nil {
			return emptyResult(), nil // malformed payload → silent pass-through
		}
	}

	// Only inspect Bash tool calls (other tools don't have a "command").
	command := input.ToolInput.Command
	if command == "" {
		return emptyResult(), nil
	}

	result := matcher.Match(command)
	if !result.Matched {
		return emptyResult(), nil
	}

	cwd := input.CWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	// Take snapshot when the pattern requests it.
	var snapResult *safety.SnapshotResult
	if result.Snapshot {
		snapResult, err = safety.TakeSnapshot(ctx, cwd, "")
		if err != nil {
			// Log the error but don't block the tool call.
			fmt.Fprintf(os.Stderr, "klyne pretool: snapshot failed: %v\n", err)
		}
	}

	var snapshotID int64
	if db != nil && snapResult != nil {
		row := &store.SafetySnapshot{
			Ts:        time.Now().UnixMilli(),
			SessionID: input.SessionID,
			CWD:       cwd,
			Command:   command,
			PatternID: result.PatternID,
			Severity:  string(result.Severity),
			FileCount: snapResult.FileCount,
		}
		if snapResult.StashSHA != "" {
			row.StashSHA = snapResult.StashSHA
		}
		if snapResult.FallbackDir != "" {
			row.FallbackDir = snapResult.FallbackDir
		}
		if insertErr := store.InsertSafetySnapshot(ctx, db, row); insertErr != nil {
			fmt.Fprintf(os.Stderr, "klyne pretool: record snapshot: %v\n", insertErr)
		} else {
			snapshotID = row.ID
		}
	}

	sysMsg := buildSystemMessage(command, snapshotID, snapResult)

	out, err := json.Marshal(preToolHookOutput{
		HookSpecificOutput: preToolHookSpecificOutput{
			HookEventName: "PreToolUse",
			SystemMessage: sysMsg,
		},
	})
	if err != nil {
		return emptyResult(), nil
	}

	return &PreToolHookResult{
		SnapshotID:    snapshotID,
		SystemMessage: sysMsg,
		Output:        string(out),
	}, nil
}

// buildSystemMessage constructs the advisory shown to the model after a
// risky command is intercepted.
func buildSystemMessage(command string, snapshotID int64, snap *safety.SnapshotResult) string {
	if snap == nil {
		return fmt.Sprintf("klyne: matched risky command: %q — no snapshot taken.", command)
	}
	what := "state"
	if snap.FileCount > 0 {
		what = fmt.Sprintf("%d file(s)", snap.FileCount)
	}
	if snap.StashSHA != "" {
		if snapshotID > 0 {
			return fmt.Sprintf(
				"klyne: snapshotted %s before `%s` (stash %s). Run `klyne restore %d` to undo.",
				what, command, snap.StashSHA[:min(8, len(snap.StashSHA))], snapshotID,
			)
		}
		return fmt.Sprintf("klyne: snapshotted %s before `%s` (stash %s).", what, command, snap.StashSHA[:min(8, len(snap.StashSHA))])
	}
	if snap.FallbackDir != "" {
		if snapshotID > 0 {
			return fmt.Sprintf(
				"klyne: snapshotted %s before `%s` (backup at %s). Run `klyne restore %d` to undo.",
				what, command, snap.FallbackDir, snapshotID,
			)
		}
		return fmt.Sprintf("klyne: snapshotted %s before `%s` (backup at %s).", what, command, snap.FallbackDir)
	}
	// Snapshot was taken but nothing to restore (clean git tree).
	return fmt.Sprintf("klyne: matched risky command `%s` — working tree was clean, no snapshot needed.", command)
}

// emptyResult returns an allow decision with no system message.
func emptyResult() *PreToolHookResult {
	return &PreToolHookResult{}
}

// min returns the smaller of a and b. Replaces math.Min for int.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
