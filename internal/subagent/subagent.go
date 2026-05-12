// Package subagent enumerates Claude Code subagent JSONL transcripts
// and attributes their token spend / activity back to the parent session.
//
// Background: when a Claude Code session uses the Task tool to spawn a
// subagent, the subagent's own conversation is written to a sibling
// JSONL under ~/.claude/projects/<project>/<parent-session>/subagents/
// agent-<id>.jsonl. Without subagent attribution, the parent session's
// rolled-up token counts under-report cost — the parent only sees the
// final Task tool result, not the subagent's full transcript.
//
// Inspired by token-dashboard's subagent attribution feature.
package subagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/config"
)

// Stat is one subagent transcript's aggregate.
type Stat struct {
	ParentSessionID string `json:"parent_session_id"`
	AgentID         string `json:"agent_id"`
	ProjectPath     string `json:"project_path"`
	Path            string `json:"path"`
	StartedAtMs     int64  `json:"started_at_ms,omitempty"`
	EndedAtMs       int64  `json:"ended_at_ms,omitempty"`
	MsgCount        int    `json:"msg_count"`
	TokensIn        int64  `json:"tokens_in"`
	TokensOut       int64  `json:"tokens_out"`
	CachedRead      int64  `json:"cached_read"`
	Model           string `json:"model"`
}

// ParentRollup aggregates subagent stats per parent session.
type ParentRollup struct {
	ParentSessionID string  `json:"parent_session_id"`
	ProjectPath     string  `json:"project_path"`
	SubagentCount   int     `json:"subagent_count"`
	TokensIn        int64   `json:"tokens_in"`
	TokensOut       int64   `json:"tokens_out"`
	CachedRead      int64   `json:"cached_read"`
	MsgCount        int     `json:"msg_count"`
	LastActivityMs  int64   `json:"last_activity_ms,omitempty"`
	Agents          []Stat  `json:"agents,omitempty"`
}

// Discover returns paths to every subagent JSONL under the user's
// Claude projects root.
func Discover() ([]string, error) {
	home, err := config.HomeDir()
	if err != nil {
		return nil, fmt.Errorf("subagent: home dir: %w", err)
	}
	root := filepath.Join(home, ".claude", "projects")
	matches, err := filepath.Glob(filepath.Join(root, "*", "*", "subagents", "*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("subagent: glob: %w", err)
	}
	return matches, nil
}

// ParseFile reads one subagent JSONL and returns its aggregated Stat.
// The parent session id is derived from the path, not the payload,
// because Claude's subagent lines often share the parent's sessionId
// field — relying on path is more robust.
func ParseFile(path string) (Stat, error) {
	stat := Stat{Path: path}
	// Path layout: <home>/.claude/projects/<enc-project>/<parent-session-id>/subagents/agent-XXX.jsonl
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) >= 4 {
		// Walk backwards: [..., <enc-project>, <parent-session-id>, "subagents", "agent-XXX.jsonl"]
		stat.ParentSessionID = parts[len(parts)-3]
		stat.AgentID = strings.TrimSuffix(strings.TrimPrefix(parts[len(parts)-1], "agent-"), ".jsonl")
	}
	f, err := os.Open(path)
	if err != nil {
		return stat, fmt.Errorf("subagent: open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var row jsonlRow
		if err := json.Unmarshal(line, &row); err != nil {
			continue // skip malformed lines (tail of an in-flight write etc.)
		}
		stat.MsgCount++
		if row.CWD != "" && stat.ProjectPath == "" {
			stat.ProjectPath = row.CWD
		}
		if ts := parseTs(row.Timestamp); ts > 0 {
			if stat.StartedAtMs == 0 || ts < stat.StartedAtMs {
				stat.StartedAtMs = ts
			}
			if ts > stat.EndedAtMs {
				stat.EndedAtMs = ts
			}
		}
		if row.Message != nil {
			if row.Message.Model != "" && stat.Model == "" {
				stat.Model = row.Message.Model
			}
			u := row.Message.Usage
			if u != nil {
				stat.TokensIn += u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
				stat.TokensOut += u.OutputTokens
				stat.CachedRead += u.CacheReadInputTokens
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return stat, fmt.Errorf("subagent: scan %s: %w", path, err)
	}
	return stat, nil
}

// Aggregate groups stats by parent session.
func Aggregate(stats []Stat) []ParentRollup {
	byParent := map[string]*ParentRollup{}
	for _, s := range stats {
		p, ok := byParent[s.ParentSessionID]
		if !ok {
			p = &ParentRollup{ParentSessionID: s.ParentSessionID}
			byParent[s.ParentSessionID] = p
		}
		if s.ProjectPath != "" && p.ProjectPath == "" {
			p.ProjectPath = s.ProjectPath
		}
		p.SubagentCount++
		p.TokensIn += s.TokensIn
		p.TokensOut += s.TokensOut
		p.CachedRead += s.CachedRead
		p.MsgCount += s.MsgCount
		if s.EndedAtMs > p.LastActivityMs {
			p.LastActivityMs = s.EndedAtMs
		}
		p.Agents = append(p.Agents, s)
	}
	out := make([]ParentRollup, 0, len(byParent))
	for _, v := range byParent {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TokensIn != out[j].TokensIn {
			return out[i].TokensIn > out[j].TokensIn
		}
		return out[i].ParentSessionID < out[j].ParentSessionID
	})
	return out
}

// CollectAll discovers, parses, and aggregates in one call. Files older
// than sinceMs (epoch ms; 0 means all time) are excluded by mtime.
func CollectAll(sinceMs int64) ([]ParentRollup, error) {
	paths, err := Discover()
	if err != nil {
		return nil, err
	}
	stats := make([]Stat, 0, len(paths))
	for _, p := range paths {
		if sinceMs > 0 {
			info, err := os.Stat(p)
			if err == nil && info.ModTime().UnixMilli() < sinceMs {
				continue
			}
		}
		s, err := ParseFile(p)
		if err != nil {
			continue
		}
		stats = append(stats, s)
	}
	return Aggregate(stats), nil
}

// --- internal types matching Claude's JSONL shape ----------------------

type jsonlRow struct {
	Timestamp string       `json:"timestamp"`
	CWD       string       `json:"cwd"`
	Message   *jsonlMsg    `json:"message,omitempty"`
}

type jsonlMsg struct {
	Model string      `json:"model,omitempty"`
	Usage *jsonlUsage `json:"usage,omitempty"`
}

type jsonlUsage struct {
	InputTokens               int64 `json:"input_tokens,omitempty"`
	OutputTokens              int64 `json:"output_tokens,omitempty"`
	CacheReadInputTokens      int64 `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens  int64 `json:"cache_creation_input_tokens,omitempty"`
}

// parseTs accepts RFC3339 / RFC3339Nano timestamps and returns epoch ms.
func parseTs(s string) int64 {
	if s == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return 0
		}
	}
	return t.UnixMilli()
}
