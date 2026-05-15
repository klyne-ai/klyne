package main

// xray_aggregate.go — `klyne xray --week` and related aggregation flags.
//
// The per-session xray tells what's LOADED in one session. This file
// implements the cross-session view: what's LOADED and what's actually USED
// across many sessions, so users can identify MCPs that burn context tokens
// without ever being called ("loaded but never invoked").
//
// I/O lives here; all analysis lives in internal/contexthealth (pure).

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/contexthealth"
	"github.com/klyne-ai/klyne/internal/mcpserver"
)

// xaggFlags holds the parsed aggregate flags so runXrayAggregate's signature
// stays stable as we add more options.
type xaggFlags struct {
	week    bool
	project bool
	since   string // e.g. "30d", "7d"
}

// xaggSinceWindow parses an xaggFlags into an absolute start-time for
// session enumeration. Returns zero time when no window applies (all-time).
func xaggSinceWindow(f xaggFlags) time.Time {
	// --week takes precedence over --since.
	if f.week {
		return time.Now().Add(-7 * 24 * time.Hour)
	}
	if f.since != "" {
		if d, err := xaggParseDuration(f.since); err == nil {
			return time.Now().Add(-d)
		}
	}
	// --project with no time window → all-time (zero value signals no cutoff).
	return time.Time{}
}

// xaggParseDuration parses a simplified duration string like "30d", "7d",
// "24h". Supports "d" (days) in addition to standard Go duration suffixes.
func xaggParseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		var days int
		if _, err := fmt.Sscanf(strings.TrimSuffix(s, "d"), "%d", &days); err != nil {
			return 0, fmt.Errorf("parse %q: %w", s, err)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// xaggSessionEntry is a lightweight descriptor of a JSONL transcript that
// falls within the requested time window.
type xaggSessionEntry struct {
	// path is the absolute path to the .jsonl file.
	path string
	// modTime is the file's last-modified time (proxy for last-activity time).
	modTime time.Time
	// projectSlug is the directory name under ~/.claude/projects/ — used for
	// --project filtering without needing to decode the full cwd.
	projectSlug string
}

// xaggClaudeProjectsDir returns the absolute path to ~/.claude/projects,
// mirroring the unexported mcpserver helper so xray_aggregate.go stays
// self-contained without duplicating logic into the mcpserver package.
func xaggClaudeProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// xaggListAllSessions enumerates every Claude JSONL transcript under
// ~/.claude/projects/ and returns those modified after `since`. Passing
// zero time returns all sessions (up to xaggMaxSessions cap).
//
// This is the cross-project enumerator that the per-session resolver does not
// need: we walk ALL project directories rather than matching against a cwd.
func xaggListAllSessions(ctx context.Context, since time.Time, projectSlugFilter string) ([]xaggSessionEntry, error) {
	projectsDir, err := xaggClaudeProjectsDir()
	if err != nil {
		return nil, fmt.Errorf("xray-agg: resolve projects dir: %w", err)
	}
	dirs, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("xray-agg: read projects dir: %w", err)
	}

	var entries []xaggSessionEntry
	for _, d := range dirs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !d.IsDir() {
			continue
		}
		slug := d.Name()
		if projectSlugFilter != "" && slug != projectSlugFilter {
			continue
		}
		projPath := filepath.Join(projectsDir, slug)
		files, err := os.ReadDir(projPath)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || filepath.Ext(f.Name()) != ".jsonl" {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			if !since.IsZero() && info.ModTime().Before(since) {
				continue
			}
			entries = append(entries, xaggSessionEntry{
				path:        filepath.Join(projPath, f.Name()),
				modTime:     info.ModTime(),
				projectSlug: slug,
			})
		}
	}

	// Sort newest-first so the cap below keeps the most recent sessions.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].modTime.After(entries[j].modTime)
	})

	// Cap at xaggMaxSessions to keep analysis times sane on large installs.
	if len(entries) > xaggMaxSessions {
		entries = entries[:xaggMaxSessions]
	}
	return entries, nil
}

// xaggMaxSessions is the hard cap on how many sessions we'll analyse in one
// aggregate run. 100 is a comfortable ceiling: at ~50 ms per JSONL parse it
// takes ~5 s max, which is acceptable for a CLI command.
const xaggMaxSessions = 100

// xaggToolCount holds invocation counts per logical source name.
type xaggToolCount struct {
	// bySource maps a logical source name (e.g. "klyne", "serena",
	// "anthropic-builtins") to the number of tool calls across all sessions.
	bySource map[string]int
	// byTool maps a full tool name to its count. Useful for the detailed section.
	byTool map[string]int
}

func newXaggToolCount() *xaggToolCount {
	return &xaggToolCount{
		bySource: make(map[string]int),
		byTool:   make(map[string]int),
	}
}

// addFromJSONL tallies tool calls from a JSONL session file by reading each
// line and extracting tool_use blocks from assistant messages.
//
// The tool_calls_json column in the DB is the canonical store, but for the
// xray path we read the JSONL directly (same pattern as loadXraySystemMessages)
// so we don't need a DB connection.
func (tc *xaggToolCount) addFromJSONL(path string) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return
	}
	defer f.Close() //nolint:errcheck

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var line struct {
			Type    string          `json:"type"`
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(raw, &line); err != nil || line.Type != "assistant" {
			continue
		}
		if len(line.Message) == 0 {
			continue
		}
		var msg struct {
			Content []struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"content"`
		}
		if err := json.Unmarshal(line.Message, &msg); err != nil {
			continue
		}
		for _, block := range msg.Content {
			if block.Type != "tool_use" || block.Name == "" {
				continue
			}
			src := mcpFromToolName(block.Name)
			tc.bySource[src]++
			tc.byTool[block.Name]++
		}
	}
}

// runXrayAggregate is the entry point for all aggregate xray variants.
// It is called when at least one of --week, --project, or --since is set.
func runXrayAggregate(cmd *cobra.Command, f xaggFlags) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	since := xaggSinceWindow(f)
	projectSlugFilter := ""
	if f.project {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve cwd: %w", err)
		}
		projectSlugFilter = mcpserver.EncodeCWD(cwd)
	}

	entries, err := xaggListAllSessions(ctx, since, projectSlugFilter)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	if len(entries) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "klyne xray — no sessions found in the requested window.")
		return nil
	}

	// Count unique project slugs for the header.
	projectSet := map[string]struct{}{}
	for _, e := range entries {
		projectSet[e.projectSlug] = struct{}{}
	}
	nProjects := len(projectSet)

	// Collect per-session source rows and tool invocation counts.
	var allSourceRows [][]contexthealth.SourceRow
	toolCounts := newXaggToolCount()

	for _, e := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rows := contexthealth.AttributeSources(loadXraySystemMessages(e.path))
		if len(rows) > 0 {
			allSourceRows = append(allSourceRows, rows)
		}
		toolCounts.addFromJSONL(e.path)
	}

	aggregated := contexthealth.AggregateSources(allSourceRows)

	// Attach invocation counts from tool tally so Redundancy() works.
	for i, row := range aggregated {
		// Match by source name: the aggregated name may have suffixes like
		// " MCP" or " skill", so we try both the bare name and prefixed forms.
		count := xaggInvocationsForSource(row.Name, toolCounts.bySource)
		aggregated[i].InvocationCount = count
	}

	out := xaggRenderScorecard(f, entries, nProjects, aggregated, toolCounts)
	fmt.Fprint(cmd.OutOrStdout(), out)
	return nil
}

// xaggInvocationsForSource looks up the tool invocation count for a named
// context source. The aggregated source name may include a suffix like " MCP"
// or " skill", while the tool-count key is the raw mcpFromToolName output
// (e.g. "serena", "klyne"). We try several normalizations in order.
func xaggInvocationsForSource(sourceName string, bySource map[string]int) int {
	// Exact match first.
	if n, ok := bySource[sourceName]; ok {
		return n
	}
	// Strip common suffixes added by the classifier.
	bare := sourceName
	for _, suffix := range []string{" MCP", " skill", " hooks", " hook"} {
		bare = strings.TrimSuffix(bare, suffix)
	}
	bare = strings.ToLower(bare)
	bare = strings.ReplaceAll(bare, " ", "-")

	// Case-insensitive scan through bySource.
	for k, n := range bySource {
		if strings.ToLower(k) == bare {
			return n
		}
	}
	return 0
}

// xaggRenderScorecard produces the aggregate scorecard text.
func xaggRenderScorecard(
	f xaggFlags,
	entries []xaggSessionEntry,
	nProjects int,
	rows []contexthealth.AggregatedRow,
	toolCounts *xaggToolCount,
) string {
	var b strings.Builder

	// Header line.
	headerDesc := xaggHeaderDesc(f, entries, nProjects)
	fmt.Fprintf(&b, "klyne xray — %s\n\n", headerDesc)

	// Pre-prompt context section.
	if len(rows) == 0 {
		fmt.Fprintln(&b, "Pre-prompt context loaded across sessions: (no attributed sources)")
	} else {
		totalTok := 0
		for _, r := range rows {
			totalTok += r.TotalTokens
		}
		fmt.Fprintf(&b, "Pre-prompt context loaded across sessions: %s tokens total\n",
			formatKTokens(totalTok))

		// Column widths: name (20), tokens (7), sessions (20), calls+marker.
		const nameW = 20
		for _, r := range rows {
			marker := xaggRedundancyMarker(r)
			sessCol := fmt.Sprintf("%d session", r.SessionCount)
			if r.SessionCount != 1 {
				sessCol += "s"
			}

			invCol := ""
			switch r.InvocationCount {
			case 0:
				invCol = "  0 calls"
			case 1:
				invCol = "  1 call"
			default:
				invCol = fmt.Sprintf("%3d calls", r.InvocationCount)
			}

			fmt.Fprintf(&b, "  %s %s  %s%s%s\n",
				padRight(r.Name, nameW),
				padRight(formatKTokens(r.TotalTokens)+" tokens", 9),
				padRight(sessCol, 16),
				invCol,
				marker,
			)
		}
	}
	b.WriteByte('\n')

	// Tool-invocation counts section.
	if len(toolCounts.bySource) > 0 {
		fmt.Fprintln(&b, "Tool-invocation counts (by source):")
		// Sort sources by count descending.
		type kv struct {
			name  string
			count int
		}
		pairs := make([]kv, 0, len(toolCounts.bySource))
		for k, v := range toolCounts.bySource {
			pairs = append(pairs, kv{k, v})
		}
		sort.Slice(pairs, func(i, j int) bool {
			if pairs[i].count != pairs[j].count {
				return pairs[i].count > pairs[j].count
			}
			return pairs[i].name < pairs[j].name
		})
		for _, p := range pairs {
			kind := ""
			if p.name == "anthropic-builtins" {
				kind = "  (Anthropic builtins)"
			} else {
				kind = fmt.Sprintf("  → %s MCP", p.name)
			}
			fmt.Fprintf(&b, "  %-30s %5d%s\n", p.name, p.count, kind)
		}
		b.WriteByte('\n')
	}

	// Redundancy summary section.
	var hardRedundant, softRedundant []contexthealth.AggregatedRow
	for _, r := range rows {
		switch r.Redundancy() {
		case contexthealth.RedundancyHard:
			hardRedundant = append(hardRedundant, r)
		case contexthealth.RedundancySoft:
			softRedundant = append(softRedundant, r)
		}
	}
	if len(hardRedundant)+len(softRedundant) > 0 {
		fmt.Fprintln(&b, "Top redundant sources:")
		for _, r := range hardRedundant {
			fmt.Fprintf(&b, "  ✗ %s  %s loaded across %d sessions, 0 invocations\n",
				r.Name, formatKTokens(r.TotalTokens)+" tokens", r.SessionCount)
		}
		for _, r := range softRedundant {
			fmt.Fprintf(&b, "  ⚠ %s  %s loaded across %d sessions, %d invocations\n",
				r.Name, formatKTokens(r.TotalTokens)+" tokens", r.SessionCount, r.InvocationCount)
		}
	} else {
		fmt.Fprintln(&b, "No redundant sources detected.")
	}

	return b.String()
}

// xaggHeaderDesc builds the human-readable description for the scorecard header.
func xaggHeaderDesc(f xaggFlags, entries []xaggSessionEntry, nProjects int) string {
	n := len(entries)
	sessionWord := "session"
	if n != 1 {
		sessionWord = "sessions"
	}

	var window string
	if f.week {
		window = "last 7 days"
	} else if f.since != "" {
		window = "last " + f.since
	} else {
		window = "all time"
	}

	projectPart := ""
	if f.project {
		projectPart = ", this project"
	} else {
		projectWord := "project"
		if nProjects != 1 {
			projectWord = "projects"
		}
		projectPart = fmt.Sprintf(", %d %s", nProjects, projectWord)
	}

	return fmt.Sprintf("%s, %d %s%s", window, n, sessionWord, projectPart)
}

// xaggRedundancyMarker returns the annotation string for the scorecard row.
func xaggRedundancyMarker(r contexthealth.AggregatedRow) string {
	switch r.Redundancy() {
	case contexthealth.RedundancyHard:
		return "   ✗ never invoked"
	case contexthealth.RedundancySoft:
		return "   ⚠ redundant?"
	default:
		return ""
	}
}

