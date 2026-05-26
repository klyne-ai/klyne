package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/hooks"
)

// session_end.go — `klyne session-end` Stop-hook entry point.
//
// This subcommand is registered by `klyne mcp install` as a Stop
// hook in ~/.claude/settings.json. When Claude Code ends a session,
// it spawns this subprocess (via the klyne-hook fallback path —
// see cmd/klyne-hook/main.go) and pipes the Stop-event JSON to stdin.
//
// # Why this is a thin shim
//
// The full session-end body lives in internal/hooks.
// ComputeAndPersistSessionEnd. There used to be a second, divergent
// implementation here in cmd/klyne that built its own worklog.Entry
// WITHOUT the AIDraftedSummary field set — which silently dropped
// every per-turn KLYNE_SUMMARY line emitted by the assistant. That
// divergence persisted for over a week before it was caught. The fix
// is the structural guarantee: the cobra path and the daemon's
// hookserver both funnel through the SAME function. The only thing
// that lives here is the cobra registration glue.
//
// Hard rules:
//   - The hook must NEVER block session-end. Any error returns
//     exit 0 with empty stdout.
//   - Stdout is the hook channel; nothing is written there in v1
//     because we don't want to inject context at session-end.
//   - stderr carries debugging lines only.

// sessionEndTimeout caps wall-clock time for the entire hook. Larger
// than the advise hook's 2s because we may walk a longer transcript;
// still bounded so a corrupt JSONL never stalls Claude Code's exit.
const sessionEndTimeout = 5 * time.Second

// newSessionEndCmd registers `klyne session-end`. Hidden from the
// help menu — it's an entry point for the hook, not for humans.
func newSessionEndCmd() *cobra.Command {
	c := &cobra.Command{
		Use:    "session-end",
		Short:  "Stop-hook entry point: write a deterministic session summary",
		Hidden: true,
		Long: `Read a Claude Code Stop-event JSON payload from stdin and
write a deterministic summary of the just-ended session to klyne's
local store. Designed as a Stop hook entry point — installed
automatically by 'klyne mcp install'.

Stdin: Stop-event JSON (session_id, transcript_path, cwd, ...).
Stdout: empty (no context injected back into Claude Code).
Stderr: debugging lines only.
Exit code: always 0 unless invoked with bad flags. Hooks must
never block session-end on a klyne error.`,
		RunE:          runSessionEnd,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	return c
}

func runSessionEnd(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), sessionEndTimeout)
	defer cancel()

	if err := computeAndPersistSessionEnd(ctx, cmd.InOrStdin()); err != nil {
		// Log to stderr but never block Claude Code's exit.
		fmt.Fprintf(cmd.ErrOrStderr(), "klyne session-end: %v\n", err)
	}
	return nil
}

// computeAndPersistSessionEnd is the 2-arg shim that delegates to the
// canonical hooks.ComputeAndPersistSessionEnd. Preserved as a private
// function so the existing cmd/klyne tests (session_end_test.go,
// session_end_snapshot_test.go) can drive the cobra path with their
// established 2-arg signature.
//
// db is passed as nil — the canonical function opens its own
// connection via internal/hooks.resolveDB (the same defaulting the
// cobra path used to do inline). stderr is os.Stderr because we are
// in a standalone subprocess; the daemon-side caller injects a
// captured buffer instead.
func computeAndPersistSessionEnd(ctx context.Context, stdin io.Reader) error {
	return hooks.ComputeAndPersistSessionEnd(ctx, stdin, nil, os.Stderr)
}
