// Package fileheat computes a "file heatmap" over klyne's stored
// messages: which files were touched most often, by which tools, and
// with what hint of cost.
//
// Inspired by nateherkai/token-dashboard's heatmap feature. Read-only,
// deterministic; no AI calls.
package fileheat

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// Filter scopes a heatmap query.
type Filter struct {
	ProjectPath       string
	SinceMs           int64
	CLI               string
	MaxSessions       int
	MaxMsgsPerSession int
}

// Touch is one tool-input observation that referenced a file path.
type Touch struct {
	Path      string
	Tool      string
	SessionID string
	Ts        int64
}

// FileStat aggregates touches per file path.
type FileStat struct {
	Path         string         `json:"path"`
	Reads        int            `json:"reads"`
	Edits        int            `json:"edits"`
	Writes       int            `json:"writes"`
	Other        int            `json:"other"`
	Total        int            `json:"total"`
	SessionCount int            `json:"session_count"`
	LastTouched  int64          `json:"last_touched"`
	ByTool       map[string]int `json:"by_tool,omitempty"`
}

// Inspected returns whether s was Read/Edit/Write — useful when callers
// want to know if the touch describes mutation or just inspection.
func (s FileStat) Inspected() int { return s.Reads }

// Mutated returns Edits + Writes.
func (s FileStat) Mutated() int { return s.Edits + s.Writes }

// Compute walks sessions and messages, extracts file-path-bearing tool
// calls, and aggregates them by path. The result is sorted by Total DESC,
// then by Path ASC for determinism.
//
// "File-path-bearing" tools recognised in v1:
//
//	Read, Edit, Write, Glob, Grep, MultiEdit, NotebookEdit,
//	apply_patch (Codex), shell_command + cd args (best-effort)
//
// The function is intentionally generous about unknown tools — they get
// bucketed under `Other` so total counts stay honest.
func Compute(ctx context.Context, db *store.DB, f Filter) ([]FileStat, error) {
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
		return nil, fmt.Errorf("fileheat: list sessions: %w", err)
	}
	msgCap := f.MaxMsgsPerSession
	if msgCap <= 0 {
		msgCap = 5000
	}

	agg := map[string]*FileStat{}
	sessSeen := map[string]map[string]bool{}

	for _, s := range sessions {
		msgs, err := store.ListMessagesBySession(ctx, db, s.ID, msgCap, 0)
		if err != nil {
			continue
		}
		for _, m := range msgs {
			if f.SinceMs > 0 && m.Ts < f.SinceMs {
				continue
			}
			for _, tc := range m.ToolCalls {
				for _, p := range extractPaths(tc) {
					fs, ok := agg[p]
					if !ok {
						fs = &FileStat{Path: p, ByTool: map[string]int{}}
						agg[p] = fs
					}
					bumpByTool(fs, tc.Name)
					fs.Total++
					fs.ByTool[tc.Name]++
					if m.Ts > fs.LastTouched {
						fs.LastTouched = m.Ts
					}
					seen, ok := sessSeen[p]
					if !ok {
						seen = map[string]bool{}
						sessSeen[p] = seen
					}
					if !seen[s.ID] {
						seen[s.ID] = true
						fs.SessionCount++
					}
				}
			}
		}
	}

	out := make([]FileStat, 0, len(agg))
	for _, v := range agg {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// bumpByTool increments the right typed counter on FileStat. Keeps the
// public surface narrow so the rendering side has stable column meanings.
func bumpByTool(fs *FileStat, tool string) {
	switch tool {
	case "Read":
		fs.Reads++
	case "Edit", "MultiEdit", "NotebookEdit", "apply_patch":
		fs.Edits++
	case "Write":
		fs.Writes++
	default:
		fs.Other++
	}
}

// extractPaths pulls file-path-looking strings out of a tool call's
// JSON input. Tools differ in input schema; the function uses a small
// per-tool dispatch table and falls back to a generic "looks like a path"
// scan for unknown tools.
func extractPaths(tc connectors.ToolCall) []string {
	if tc.Input == "" {
		return nil
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal([]byte(tc.Input), &args); err != nil {
		return nil
	}
	keys := map[string][]string{
		"Read":         {"file_path", "path"},
		"Write":        {"file_path", "path"},
		"Edit":         {"file_path", "path"},
		"MultiEdit":    {"file_path", "path"},
		"NotebookEdit": {"notebook_path", "path"},
		"Glob":         {"path"},
		"Grep":         {"path"},
		"apply_patch":  {"patch"},
	}
	wanted, ok := keys[tc.Name]
	if !ok {
		// Best-effort fallback for unknown tools (Codex shell, MCP tools, …)
		// — look at every top-level string value.
		return looksLikePathsFromMap(args)
	}
	out := make([]string, 0, 2)
	for _, k := range wanted {
		raw, ok := args[k]
		if !ok {
			continue
		}
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		// apply_patch carries a full multi-line diff string; we don't try
		// to parse it line by line — that would be brittle. Counting it
		// once is the honest answer.
		if tc.Name == "apply_patch" {
			out = append(out, "<apply_patch diff>")
			break
		}
		out = append(out, v)
	}
	return out
}

func looksLikePathsFromMap(args map[string]json.RawMessage) []string {
	var out []string
	for _, raw := range args {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			continue
		}
		v = strings.TrimSpace(v)
		// Cheap heuristic: looks like a path if it contains `/` or `.` or `\`
		// AND has no spaces (CLI args with spaces are not paths in this context).
		if v == "" || strings.ContainsRune(v, ' ') {
			continue
		}
		if strings.ContainsAny(v, "/\\.") {
			out = append(out, v)
		}
	}
	return out
}
