package main

// resume.go — `klyne resume` sub-command tree (v0).
//
// Sub-commands:
//
//	klyne resume list                     — rank sessions against current cwd
//	klyne resume show <id>                — print session details
//	klyne resume hydrate <id> [flags]     — preview or inject a resume payload
//
// I/O lives here; all scoring and trimming are in internal/resume (pure).

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/resume"
	"github.com/klyne-ai/klyne/internal/store"
)

func newResumeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "resume",
		Short: "Auto-Resume with Receipts — rank and hydrate past sessions",
		Long: `Resume any past session with a token-cost receipt before injection.

Sub-commands:

  klyne resume list
    Rank past sessions against the current working directory and print
    top candidates with estimated token sizes.

  klyne resume show <id>
    Print metadata for a single session (decisions, message count, files).

  klyne resume hydrate <id> [flags]
    Preview or inject a resume payload.  Always shows the token cost
    ("receipt") before injecting so you can confirm the size.

Flags (hydrate):
  --decisions-only   Include only recorded decisions (~800 tokens)
  --last-N=<n>       Include only the last N conversation turns
  --budget=<tokens>  Trim payload to this many tokens (default 8192)
  --dry-run          Print what would be injected without injecting`,
		SilenceUsage: true,
	}

	c.AddCommand(newResumeListCmd())
	c.AddCommand(newResumeShowCmd())
	c.AddCommand(newResumeHydrateCmd())
	return c
}

// ---- resume list -------------------------------------------------------

func newResumeListCmd() *cobra.Command {
	var limit int
	var showAll bool

	c := &cobra.Command{
		Use:   "list",
		Short: "Rank past sessions against the current working directory",
		Long: `Rank past sessions against the current working directory.

By default, returns up to 3 sessions that score ≥ 0.55. Use --limit to
raise the cap, or --all to browse every ingested session (drops the
threshold and the cap so you can pick by memory rather than by score).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return resumeRunList(cmd, limit, showAll)
		},
		SilenceUsage: true,
	}

	c.Flags().IntVar(&limit, "limit", 0, "max candidates to return (0 = default top-3)")
	c.Flags().BoolVar(&showAll, "all", false, "list every ingested session (no threshold, no cap)")
	return c
}

func resumeRunList(cmd *cobra.Command, limit int, showAll bool) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resume list: get cwd: %w", err)
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return fmt.Errorf("resume list: open db: %w", err)
	}
	defer db.Close() //nolint:errcheck

	// When --all is set we pull a much wider window from the store so genuinely
	// old sessions become reachable (default ListSessions cap is 50).
	storeLimit := 50
	if showAll {
		storeLimit = 500
	}
	sessions, err := store.ListSessions(ctx, db, store.SessionFilter{Limit: storeLimit})
	if err != nil {
		return fmt.Errorf("resume list: list sessions: %w", err)
	}

	gitRecent := resumeGitRecentFiles()

	now := time.Now()
	inputs := make([]resume.SessionInput, 0, len(sessions))
	for _, s := range sessions {
		inputs = append(inputs, resume.SessionInput{
			ID:          s.ID,
			LastMsgAt:   s.LastMsgAt,
			ProjectPath: s.ProjectPath,
			// TouchedFiles: derived from tool_calls per session — expensive;
			// in v0 we skip per-session file extraction for list (use empty set).
			// TODO(resume): extract touched files from messages for a better score.
		})
	}

	// Fetch decision counts per session for the briefing output.
	decisionCounts := resumeDecisionCounts(ctx, db, inputs)

	opts := resume.RankOptions{Limit: limit}
	if showAll {
		opts.IgnoreThreshold = true
		opts.Limit = -1
	}
	ranked := resume.RankWithOptions(inputs, cwd, gitRecent, now, opts)
	if len(ranked) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no candidates — no recent sessions score ≥ 0.55 for this directory")
		fmt.Fprintln(cmd.OutOrStdout(), "  (try `klyne resume list --all` to see every ingested session)")
		return nil
	}

	w := cmd.OutOrStdout()
	for i, r := range ranked {
		s := r.Session
		age := resumeHumanAge(now, time.UnixMilli(s.LastMsgAt))
		dcount := decisionCounts[s.ID]

		// Derive a human label from the project path tail.
		projLabel := resumePathLabel(s.ProjectPath)

		fullEst := resume.EstimateTokensForVariant("full", 20, dcount)
		decEst := resume.EstimateTokensForVariant("decisions-only", 0, dcount)

		fmt.Fprintf(w, "%d. session on `%s` — %d decisions, %s ago. (score %.2f)\n",
			i+1, projLabel, dcount, age, r.Score)
		fmt.Fprintf(w, "   resume: `klyne resume hydrate %s` (~%s)\n",
			resumeShortID(s.ID), resumeFormatTokens(fullEst))
		fmt.Fprintf(w, "           `klyne resume hydrate %s --decisions-only` (~%s)\n\n",
			resumeShortID(s.ID), resumeFormatTokens(decEst))
	}
	return nil
}

// ---- resume show -------------------------------------------------------

func newResumeShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <session-id>",
		Short: "Print metadata for a session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return resumeRunShow(cmd, args[0])
		},
		SilenceUsage: true,
	}
}

func resumeRunShow(cmd *cobra.Command, sessionID string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return fmt.Errorf("resume show: open db: %w", err)
	}
	defer db.Close() //nolint:errcheck

	s, err := store.GetSession(ctx, db, sessionID)
	if err != nil {
		return fmt.Errorf("resume show: get session %q: %w", sessionID, err)
	}

	decisions, err := store.ListDecisions(ctx, db, store.DecisionFilter{SessionID: sessionID})
	if err != nil {
		return fmt.Errorf("resume show: list decisions: %w", err)
	}

	w := cmd.OutOrStdout()
	age := resumeHumanAge(time.Now(), time.UnixMilli(s.LastMsgAt))

	fmt.Fprintf(w, "Session: %s\n", s.ID)
	fmt.Fprintf(w, "Project: %s\n", s.ProjectPath)
	fmt.Fprintf(w, "Last active: %s ago\n", age)
	fmt.Fprintf(w, "Messages: %d\n", s.MsgCount)
	fmt.Fprintf(w, "Decisions: %d\n", len(decisions))

	if len(decisions) > 0 {
		fmt.Fprintln(w, "\nDecisions:")
		for _, d := range decisions {
			fmt.Fprintf(w, "  [%s] %s\n", resumeFormatTs(d.Ts), d.Text)
		}
	}

	fullEst := resume.EstimateTokensForVariant("full", int(s.MsgCount), len(decisions))
	decEst := resume.EstimateTokensForVariant("decisions-only", 0, len(decisions))
	fmt.Fprintf(w, "\nEstimated size:\n")
	fmt.Fprintf(w, "  full:           ~%s\n", resumeFormatTokens(fullEst))
	fmt.Fprintf(w, "  decisions-only: ~%s\n", resumeFormatTokens(decEst))

	return nil
}

// ---- resume hydrate ----------------------------------------------------

func newResumeHydrateCmd() *cobra.Command {
	var decisionsOnly bool
	var lastN int
	var budgetTokens int
	var dryRun bool

	c := &cobra.Command{
		Use:   "hydrate <session-id>",
		Short: "Preview or inject a resume payload for a past session",
		Args:  cobra.ExactArgs(1),
		Long: `Build a resume payload for the given session and either print
it (--dry-run) or emit it as context injection JSON on stdout.

The token-cost receipt is always printed before injection so you can
confirm the size.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return resumeRunHydrate(cmd, args[0], decisionsOnly, lastN, budgetTokens, dryRun)
		},
		SilenceUsage: true,
	}

	c.Flags().BoolVar(&decisionsOnly, "decisions-only", false, "include only recorded decisions (~800 tokens)")
	c.Flags().IntVar(&lastN, "last-N", 0, "include only the last N conversation turns (0 = all)")
	c.Flags().IntVar(&budgetTokens, "budget", 8192, "trim payload to this many tokens")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be injected without injecting")

	return c
}

func resumeRunHydrate(cmd *cobra.Command, sessionID string, decisionsOnly bool, lastN, budget int, dryRun bool) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	db, err := store.Open(config.DBPath())
	if err != nil {
		return fmt.Errorf("resume hydrate: open db: %w", err)
	}
	defer db.Close() //nolint:errcheck

	s, err := store.GetSession(ctx, db, sessionID)
	if err != nil {
		return fmt.Errorf("resume hydrate: get session %q: %w", sessionID, err)
	}

	decisions, err := store.ListDecisions(ctx, db, store.DecisionFilter{SessionID: sessionID})
	if err != nil {
		return fmt.Errorf("resume hydrate: list decisions: %w", err)
	}

	// Determine message limit.
	msgLimit := 20
	if lastN > 0 {
		msgLimit = lastN
	}
	if decisionsOnly {
		msgLimit = 0
	}

	// Fetch messages (most recent first, then reverse to chronological for payload).
	var turns []resume.Turn
	if msgLimit > 0 {
		msgs, merr := store.ListMessagesBySessionOrdered(ctx, db, sessionID, msgLimit, 0, "desc")
		if merr != nil {
			return fmt.Errorf("resume hydrate: list messages: %w", merr)
		}
		// Reverse to chronological order.
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}
		for _, m := range msgs {
			t := resume.Turn{
				Role:    string(m.Role),
				Content: m.Content,
				TsMs:    m.Ts,
			}
			// Surface tool result payloads for the trimmer.
			if len(m.ToolResults) > 0 {
				outputs := make([]string, 0, len(m.ToolResults))
				for _, tr := range m.ToolResults {
					outputs = append(outputs, tr.Output)
				}
				t.ToolResultOutput = strings.Join(outputs, "\n")
			}
			turns = append(turns, t)
		}
	}

	// Mark decision turns.
	decisionTexts := make([]string, 0, len(decisions))
	for _, d := range decisions {
		decisionTexts = append(decisionTexts, d.Text)
		// Inject decisions as decision turns too (so trimmer treats them as sacrosanct).
		turns = append(turns, resume.Turn{
			Role:       "assistant",
			Content:    "[decision] " + d.Text,
			IsDecision: true,
			TsMs:       d.Ts,
		})
	}

	payload := resume.Payload{
		Turns:     turns,
		Decisions: decisionTexts,
	}

	// Run the trimmer.
	result := resume.Trim(payload, budget)

	// Build the human-readable hydration text.
	var sb strings.Builder
	sb.WriteString("# klyne resume — session " + resumeShortID(sessionID) + "\n\n")
	sb.WriteString("**Project:** " + s.ProjectPath + "\n")
	sb.WriteString("**Last active:** " + resumeHumanAge(time.Now(), time.UnixMilli(s.LastMsgAt)) + " ago\n\n")

	if len(result.Payload.Decisions) > 0 {
		sb.WriteString("## Decisions\n\n")
		for _, d := range result.Payload.Decisions {
			sb.WriteString("- " + d + "\n")
		}
		sb.WriteString("\n")
	}

	if !decisionsOnly && len(result.Payload.Turns) > 0 {
		sb.WriteString("## Recent turns\n\n")
		for _, t := range result.Payload.Turns {
			if t.IsDecision {
				continue // already shown in decisions section
			}
			role := t.Role
			if role == "" {
				role = "unknown"
			}
			sb.WriteString("**" + role + ":** " + strings.TrimSpace(t.Content) + "\n\n")
		}
	}

	hydrateText := sb.String()
	estTokens := result.EstimatedTokens

	w := cmd.OutOrStdout()

	// Always print the receipt.
	fmt.Fprintf(w, "klyne resume receipt\n")
	fmt.Fprintf(w, "  session:  %s\n", sessionID)
	fmt.Fprintf(w, "  variant:  ")
	if decisionsOnly {
		fmt.Fprintln(w, "decisions-only")
	} else if lastN > 0 {
		fmt.Fprintf(w, "last-%d turns\n", lastN)
	} else {
		fmt.Fprintln(w, "full")
	}
	fmt.Fprintf(w, "  budget:   %s\n", resumeFormatTokens(budget))
	fmt.Fprintf(w, "  estimate: ~%s\n", resumeFormatTokens(estTokens))
	if result.CutSummary != "" {
		fmt.Fprintf(w, "  trimmed:  %s\n", result.CutSummary)
	}
	fmt.Fprintln(w)

	if dryRun {
		fmt.Fprintln(w, "--- dry run: payload that would be injected ---")
		fmt.Fprintln(w, hydrateText)
		fmt.Fprintln(w, "--- end dry run ---")
		return nil
	}

	// In v0, emit the payload text to stdout so the user can copy it
	// into a new session. SessionStart auto-injection is v1.
	fmt.Fprintln(w, hydrateText)
	return nil
}

// ---- helpers -----------------------------------------------------------

// resumeGitRecentFiles returns the union of files from `git diff --name-only HEAD~5`
// and uncommitted changes. Returns empty slice on any error (best-effort).
func resumeGitRecentFiles() []string {
	var files []string

	// HEAD~5 committed changes.
	if out, err := exec.Command("git", "diff", "--name-only", "HEAD~5").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				files = append(files, line)
			}
		}
	}

	// Uncommitted changes (staged + unstaged).
	if out, err := exec.Command("git", "diff", "--name-only").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				files = append(files, line)
			}
		}
	}
	if out, err := exec.Command("git", "diff", "--name-only", "--cached").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				files = append(files, line)
			}
		}
	}

	return files
}

// resumeDecisionCounts returns a map of sessionID → decision count for the
// given session inputs. Errors degrade gracefully to 0.
func resumeDecisionCounts(ctx context.Context, db *store.DB, inputs []resume.SessionInput) map[string]int {
	counts := make(map[string]int, len(inputs))
	for _, s := range inputs {
		decisions, err := store.ListDecisions(ctx, db, store.DecisionFilter{SessionID: s.ID, Limit: 1000})
		if err == nil {
			counts[s.ID] = len(decisions)
		}
	}
	return counts
}

// resumeHumanAge returns a human-readable age string like "14h" or "2d 3h".
func resumeHumanAge(now, then time.Time) string {
	dur := now.Sub(then)
	if dur < 0 {
		dur = 0
	}
	hours := int(dur.Hours())
	days := hours / 24
	remH := hours % 24

	if days > 0 {
		if remH > 0 {
			return fmt.Sprintf("%dd %dh", days, remH)
		}
		return fmt.Sprintf("%dd", days)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh", hours)
	}
	mins := int(dur.Minutes())
	return fmt.Sprintf("%dm", mins)
}

// resumePathLabel returns the last two components of a path (parent/leaf)
// as a readable label, truncated if needed.
func resumePathLabel(p string) string {
	p = filepath.Clean(p)
	base := filepath.Base(p)
	parent := filepath.Base(filepath.Dir(p))
	if parent == "." || parent == "/" {
		return base
	}
	label := parent + "/" + base
	const maxLen = 40
	if utf8.RuneCountInString(label) > maxLen {
		return "..." + label[len(label)-maxLen+3:]
	}
	return label
}

// resumeShortID returns the first 12 characters of a session ID for display.
func resumeShortID(id string) string {
	if utf8.RuneCountInString(id) <= 12 {
		return id
	}
	return id[:12]
}

// resumeFormatTokens formats a token count for human display (e.g. "6K", "800").
func resumeFormatTokens(n int) string {
	if n >= 1000 {
		k := float64(n) / 1000.0
		if k == float64(int(k)) {
			return fmt.Sprintf("%dK", int(k))
		}
		return fmt.Sprintf("%.1fK", k)
	}
	return fmt.Sprintf("%d", n)
}

// resumeFormatTs formats an epoch-millisecond timestamp as "2006-01-02".
func resumeFormatTs(ms int64) string {
	return time.UnixMilli(ms).Format("2006-01-02")
}
