// Package insights computes deterministic, read-only analytics over klyne's
// stored sessions and messages. It powers the `klyne top`, `klyne patterns`,
// and `klyne roast` CLI surfaces.
//
// All inputs come from the SQLite store; no AI calls, no network traffic.
// Inspired by claudestat (https://github.com/DeibyGS/claudestat) but kept
// faithful to klyne's local-first / deterministic / no-telemetry contract.
package insights

import (
	"context"
	"fmt"
	"sort"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// Filter scopes any insights query.
type Filter struct {
	// SessionID, when non-empty, restricts results to a single session.
	SessionID string
	// ProjectPath, when non-empty, restricts to a single project root.
	ProjectPath string
	// CLI, when non-empty ("claude" or "codex"), restricts to one CLI.
	CLI string
	// SinceMs, when > 0, excludes messages older than this epoch-ms.
	SinceMs int64
	// MaxSessions caps the number of sessions walked. Zero defaults to 500.
	MaxSessions int
	// MaxMsgsPerSession caps per-session message scans. Zero defaults to 5000.
	MaxMsgsPerSession int
}

// SessionStats summarises one session's analytics-relevant facts.
//
// All numeric fields are derived deterministically from the messages table
// plus the session row's cumulative counters. None of them require any
// external service.
type SessionStats struct {
	ID                string  `json:"id"`
	CLI               string  `json:"cli"`
	ProjectPath       string  `json:"project_path"`
	Model             string  `json:"model"`
	LastMsgAt         int64   `json:"last_msg_at"`
	StartedAt         int64   `json:"started_at"`
	MsgCount          int64   `json:"msg_count"`
	TokensIn          int64   `json:"tokens_in"`
	TokensOut         int64   `json:"tokens_out"`
	CachedReadTokens  int64   `json:"cached_read_tokens"`
	CachedWriteTokens int64   `json:"cached_write_tokens"`
	CostUSD           float64 `json:"cost_usd"`

	// ToolCounts: tool name → number of invocations in this session.
	ToolCounts map[string]int `json:"tool_counts"`
	// ToolErrors: tool name → number of error responses for that tool.
	ToolErrors map[string]int `json:"tool_errors"`
	// TotalToolCalls is the sum of ToolCounts values.
	TotalToolCalls int `json:"total_tool_calls"`

	// LongestRun is the maximum number of consecutive tool calls of the
	// same name observed across the session, in message order (asc ts).
	LongestRun int `json:"longest_run"`
	// LongestRunTool is the tool name owning LongestRun. Empty when zero.
	LongestRunTool string `json:"longest_run_tool"`

	// CacheReadRatio = CachedReadTokens / TokensIn, clamped to [0, 1].
	// Zero when TokensIn == 0.
	CacheReadRatio float64 `json:"cache_read_ratio"`
}

// ToolStat aggregates one tool's usage across multiple sessions.
type ToolStat struct {
	Name         string `json:"name"`
	Count        int    `json:"count"`
	ErrorCount   int    `json:"error_count"`
	SessionCount int    `json:"session_count"`
}

// GatherSessions returns sessions matching the filter, ordered by last_msg_at DESC.
func GatherSessions(ctx context.Context, db *store.DB, f Filter) ([]*connectors.Session, error) {
	limit := f.MaxSessions
	if limit <= 0 {
		limit = 500
	}
	sessions, err := store.ListSessions(ctx, db, store.SessionFilter{
		CLI:         f.CLI,
		ProjectPath: f.ProjectPath,
		Limit:       limit,
	})
	if err != nil {
		return nil, fmt.Errorf("insights: list sessions: %w", err)
	}
	if f.SessionID != "" {
		// Filter to the explicit id; if not in the list, fetch directly.
		for _, s := range sessions {
			if s.ID == f.SessionID {
				return []*connectors.Session{s}, nil
			}
		}
		one, err := store.GetSession(ctx, db, f.SessionID)
		if err != nil {
			return nil, fmt.Errorf("insights: session %q: %w", f.SessionID, err)
		}
		return []*connectors.Session{one}, nil
	}
	return sessions, nil
}

// ComputeSessionStats walks the messages of one session and returns its
// SessionStats. Messages older than sinceMs (when > 0) are skipped.
func ComputeSessionStats(ctx context.Context, db *store.DB, s *connectors.Session, f Filter) (SessionStats, error) {
	msgCap := f.MaxMsgsPerSession
	if msgCap <= 0 {
		msgCap = 5000
	}
	msgs, err := store.ListMessagesBySession(ctx, db, s.ID, msgCap, 0)
	if err != nil {
		return SessionStats{}, fmt.Errorf("insights: list messages for %q: %w", s.ID, err)
	}

	stats := SessionStats{
		ID:                s.ID,
		CLI:               string(s.CLI),
		ProjectPath:       s.ProjectPath,
		Model:             s.Model,
		LastMsgAt:         s.LastMsgAt,
		StartedAt:         s.StartedAt,
		MsgCount:          s.MsgCount,
		TokensIn:          s.TokensIn,
		TokensOut:         s.TokensOut,
		CachedReadTokens:  s.CachedReadTokens,
		CachedWriteTokens: s.CachedWriteTokens,
		CostUSD:           s.CostUSD,
		ToolCounts:        map[string]int{},
		ToolErrors:        map[string]int{},
	}
	if s.TokensIn > 0 {
		r := float64(s.CachedReadTokens) / float64(s.TokensIn)
		if r < 0 {
			r = 0
		}
		if r > 1 {
			r = 1
		}
		stats.CacheReadRatio = r
	}

	var prevTool string
	var run int
	for _, m := range msgs {
		if f.SinceMs > 0 && m.Ts < f.SinceMs {
			continue
		}
		// Look up error flags for tool results emitted by the SAME message.
		// Real error pairing across messages requires a multi-pass walk; this
		// covers the common case where the connector co-locates them.
		errByID := map[string]bool{}
		for _, r := range m.ToolResults {
			errByID[r.ID] = r.IsError
		}
		for _, tc := range m.ToolCalls {
			name := tc.Name
			if name == "" {
				name = "(unknown)"
			}
			stats.ToolCounts[name]++
			if errByID[tc.ID] {
				stats.ToolErrors[name]++
			}
			stats.TotalToolCalls++
			if name == prevTool {
				run++
			} else {
				run = 1
				prevTool = name
			}
			if run > stats.LongestRun {
				stats.LongestRun = run
				stats.LongestRunTool = name
			}
		}
	}
	return stats, nil
}

// CollectStats is a convenience wrapper that runs GatherSessions and
// ComputeSessionStats for every match. The result is ordered by LastMsgAt DESC.
func CollectStats(ctx context.Context, db *store.DB, f Filter) ([]SessionStats, error) {
	sessions, err := GatherSessions(ctx, db, f)
	if err != nil {
		return nil, err
	}
	out := make([]SessionStats, 0, len(sessions))
	for _, s := range sessions {
		st, err := ComputeSessionStats(ctx, db, s, f)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// AggregateTools merges per-session tool counts into a global ranking.
// The result is sorted by Count DESC, then Name ASC for determinism.
func AggregateTools(stats []SessionStats) []ToolStat {
	agg := map[string]*ToolStat{}
	for _, s := range stats {
		seen := map[string]bool{}
		for name, c := range s.ToolCounts {
			ts, ok := agg[name]
			if !ok {
				ts = &ToolStat{Name: name}
				agg[name] = ts
			}
			ts.Count += c
			ts.ErrorCount += s.ToolErrors[name]
			if !seen[name] {
				ts.SessionCount++
				seen[name] = true
			}
		}
	}
	out := make([]ToolStat, 0, len(agg))
	for _, ts := range agg {
		out = append(out, *ts)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// TotalCost returns the sum of CostUSD across the given stats.
func TotalCost(stats []SessionStats) float64 {
	var total float64
	for _, s := range stats {
		total += s.CostUSD
	}
	return total
}

// TotalTokens returns the aggregate token counts across the given stats.
func TotalTokens(stats []SessionStats) (in, out, cachedRead, cachedWrite int64) {
	for _, s := range stats {
		in += s.TokensIn
		out += s.TokensOut
		cachedRead += s.CachedReadTokens
		cachedWrite += s.CachedWriteTokens
	}
	return
}
