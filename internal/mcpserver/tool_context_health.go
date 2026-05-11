package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/contexthealth"
)

// GetContextHealthInput is the JSON-Schema input for the
// get_context_health MCP tool. All fields are optional; sensible
// defaults make the tool callable with `{}`.
type GetContextHealthInput struct {
	// SessionID is the explicit Claude Code session id to analyse.
	// When omitted, the tool resolves the latest session under the
	// current working directory's project tree under
	// ~/.claude/projects.
	SessionID string `json:"session_id,omitempty" jsonschema:"explicit Claude Code session id; defaults to latest session in current working directory"`
	// CWD overrides os.Getwd() for session resolution. Useful when the
	// AI knows the project root differs from where it spawned the MCP
	// subprocess from. Ignored when SessionID is set.
	CWD string `json:"cwd,omitempty" jsonschema:"override the working directory used to resolve the latest session"`
}

// GetContextHealthOutput mirrors contexthealth.Result with two
// additions an MCP consumer needs: SessionID and Path so the AI can
// quote them back to the user.
type GetContextHealthOutput struct {
	// SessionID is the resolved session identifier. Empty when the
	// call was Ambiguous.
	SessionID string `json:"session_id,omitempty" jsonschema:"the session id whose health is reported"`
	// Path is the absolute path of the JSONL transcript that was
	// analysed. Useful for the AI to cite when explaining the result.
	// Empty when the call was Ambiguous.
	Path string `json:"path,omitempty" jsonschema:"absolute path of the analysed transcript"`
	// Model is the assistant model on the most recent qualifying turn.
	Model string `json:"model,omitempty" jsonschema:"model id of the most recent assistant turn"`
	// State is one of healthy / drifting / risky / rescue_now. Empty
	// when Ambiguous.
	State string `json:"state,omitempty" jsonschema:"context-health state classification"`
	// Action is the recommended next step. Empty when Ambiguous.
	Action string `json:"action,omitempty" jsonschema:"recommended next action"`
	// Reason is a one-sentence explanation of the verdict.
	Reason string `json:"reason" jsonschema:"one-sentence rationale for the verdict"`
	// ContextFillPct is the cache-aware fill percentage 0..100.
	// Zero when Ambiguous.
	ContextFillPct float64 `json:"context_fill_pct,omitempty" jsonschema:"percent of context window consumed by the next turn"`
	// MsgCount is the total parsed message count. Zero when Ambiguous.
	MsgCount int `json:"msg_count,omitempty" jsonschema:"total message count in the transcript"`
	// Bloat is the top-N attribution rows from contexthealth.
	Bloat []contexthealth.BloatRow `json:"bloat,omitempty" jsonschema:"top tool-output sources contributing to context bloat"`
	// Signals exposes the classifier's inputs.
	Signals contexthealth.Signals `json:"signals,omitempty" jsonschema:"raw signals the classifier used to decide"`
	// Ambiguous is true when the resolver could not unambiguously pick
	// one session out of multiple candidates in the cwd. The AI must
	// inspect Candidates and call again with an explicit session_id.
	Ambiguous bool `json:"ambiguous,omitempty" jsonschema:"true when multiple sessions in this cwd require explicit session_id disambiguation"`
	// Candidates is the list of sessions the resolver found in the
	// cwd. Populated only when Ambiguous is true.
	Candidates []CandidateRow `json:"candidates,omitempty" jsonschema:"sessions to choose from when ambiguous"`
}

// CandidateRow is the JSON shape exposed for one disambiguation
// candidate. Mirrors SessionCandidate but omits internal fields.
type CandidateRow struct {
	SessionID string `json:"session_id" jsonschema:"the session id to pass back as session_id"`
	Preview   string `json:"preview" jsonschema:"first user message preview, truncated"`
	IsActive  bool   `json:"is_active" jsonschema:"true when the session was modified within the last 30 seconds"`
	ModTime   string `json:"mod_time" jsonschema:"RFC3339 timestamp of the file's last modification"`
	MsgCount  int    `json:"msg_count" jsonschema:"total message count in this candidate's transcript"`
}

// HandleGetContextHealth resolves the session, loads the JSONL
// snapshot, runs contexthealth.Classify, and returns the verdict.
// Pure function over the input + filesystem state — no side effects.
//
// Disambiguation: when no session_id is given and multiple sessions
// exist in the cwd's project directory, the handler returns an
// Ambiguous result with the candidate list rather than guessing. The
// AI is expected to call again with an explicit session_id.
func HandleGetContextHealth(_ context.Context, _ *mcp.CallToolRequest, in GetContextHealthInput) (*mcp.CallToolResult, GetContextHealthOutput, error) {
	path, ambiguous, cands, err := resolveSession(in)
	if err != nil {
		return nil, GetContextHealthOutput{}, err
	}
	if ambiguous {
		return ambiguousResult(cands)
	}
	if path == "" {
		// Surfacing an empty result is more useful to the AI than an
		// error — it can tell the user "no session found in this
		// directory" without retry.
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "No Claude Code session found for this working directory."},
			},
		}, GetContextHealthOutput{
			Reason: "No Claude Code session found for this working directory.",
		}, nil
	}

	snap, err := LoadSnapshot(path)
	if err != nil {
		return nil, GetContextHealthOutput{}, fmt.Errorf("load snapshot: %w", err)
	}

	res := contexthealth.Classify(contexthealth.Input{
		SessionID:      snap.SessionID,
		CLI:            connectors.CLIClaude,
		Model:          snap.Model,
		ContextFillPct: snap.ContextFillPct,
		MsgCount:       snap.MsgCount,
		Messages:       snap.Messages,
	})

	out := GetContextHealthOutput{
		SessionID:      snap.SessionID,
		Path:           snap.Path,
		Model:          snap.Model,
		State:          string(res.State),
		Action:         string(res.Action),
		Reason:         res.Reason,
		ContextFillPct: snap.ContextFillPct,
		MsgCount:       snap.MsgCount,
		Bloat:          res.Bloat,
		Signals:        res.Signals,
	}
	// The Content text is what the AI reads first when deciding how to
	// summarise the call to the user. Keep it terse — one sentence.
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: res.Reason}},
	}, out, nil
}

// resolveSession turns the tool's optional inputs into either a
// concrete JSONL path (path != "", ambiguous == false) OR an
// ambiguity signal with the candidate list (path == "", ambiguous ==
// true, cands populated). Returns ("", false, nil, nil) when nothing
// matches the cwd at all.
//
// Resolution order:
//  1. Explicit session_id → exact match search across all project
//     directories.
//  2. Otherwise list candidates in the cwd's project tree. If
//     PickActiveSession can choose unambiguously, return that; else
//     surface the ambiguity.
func resolveSession(in GetContextHealthInput) (path string, ambiguous bool, cands []SessionCandidate, err error) {
	if in.SessionID != "" {
		path, err = FindSessionByID(in.SessionID)
		return path, false, nil, err
	}
	cwd := in.CWD
	if cwd == "" {
		w, gerr := os.Getwd()
		if gerr != nil {
			return "", false, nil, fmt.Errorf("resolve cwd: %w", gerr)
		}
		cwd = w
	}
	cands, err = ListSessionsForCWD(cwd)
	if err != nil {
		return "", false, nil, err
	}
	if pick, ok := PickActiveSession(cands); ok {
		return pick.Path, false, nil, nil
	}
	if len(cands) == 0 {
		return "", false, nil, nil
	}
	return "", true, cands, nil
}

// ambiguousResult constructs the response that asks the AI to retry
// with an explicit session_id. The Content text instructs the AI in
// plain English; the Candidates structured field gives it the data
// to choose from.
func ambiguousResult(cands []SessionCandidate) (*mcp.CallToolResult, GetContextHealthOutput, error) {
	rows := make([]CandidateRow, 0, len(cands))
	for _, c := range cands {
		rows = append(rows, CandidateRow{
			SessionID: c.SessionID,
			Preview:   c.Preview,
			IsActive:  c.IsActive,
			ModTime:   c.ModTime.UTC().Format(timeRFC3339),
			MsgCount:  c.MsgCount,
		})
	}
	const reason = "Multiple Claude Code sessions in this project. Pick one and call again with session_id."
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: reason}},
	}, GetContextHealthOutput{
		Reason:     reason,
		Ambiguous:  true,
		Candidates: rows,
	}, nil
}

// timeRFC3339 is the format the candidate rows expose. Stays in one
// place so every tool that returns timestamps formats them the same.
const timeRFC3339 = "2006-01-02T15:04:05Z07:00"

// FindSessionByID searches Claude AND Codex storage roots for a
// transcript matching sessionID. Returns ("", nil) when nothing
// matches. Restricts every match to the file basename so a malicious
// session_id cannot cause a directory traversal.
//
// Search order: Claude first (cheaper, encoded-cwd dirs), Codex
// second (requires a date-partitioned walk + filename match against
// `rollout-<id>.jsonl`).
func FindSessionByID(sessionID string) (string, error) {
	if sessionID == "" {
		return "", errors.New("empty session id")
	}
	if path, err := findClaudeSessionByID(sessionID); err != nil {
		return "", err
	} else if path != "" {
		return path, nil
	}
	return findCodexSessionByID(sessionID)
}

// findClaudeSessionByID is the original Claude-only logic.
func findClaudeSessionByID(sessionID string) (string, error) {
	projectsDir, err := claudeProjectsDir()
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read projects dir: %w", err)
	}
	for _, projEntry := range entries {
		if !projEntry.IsDir() {
			continue
		}
		projPath := projectsDir + "/" + projEntry.Name()
		files, err := os.ReadDir(projPath)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			name := f.Name()
			// Match `<sessionID>.jsonl` exactly OR `<sessionID>-…jsonl`.
			expectExact := sessionID + ".jsonl"
			expectPrefix := sessionID + "-"
			if name == expectExact ||
				(len(name) > len(expectPrefix) && name[:len(expectPrefix)] == expectPrefix) {
				return projPath + "/" + name, nil
			}
		}
	}
	return "", nil
}

// findCodexSessionByID walks Codex's date-partitioned rollout files
// and returns the path of the rollout-<id>.jsonl whose embedded
// session_meta.id matches sessionID. Falls back to filename-prefix
// match (`rollout-<id>...`) which mirrors how Codex names its files
// in practice; the embedded-id check exists so non-conforming names
// also resolve.
func findCodexSessionByID(sessionID string) (string, error) {
	root, err := codexSessionsDir()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("stat codex sessions dir: %w", err)
	}
	files, err := walkCodexRolloutFiles(root)
	if err != nil {
		return "", fmt.Errorf("walk codex rollouts: %w", err)
	}
	// Phase 1: cheap filename match.
	expect := "rollout-" + sessionID + ".jsonl"
	expectPrefix := "rollout-" + sessionID + "-"
	for _, p := range files {
		base := filepathBase(p)
		if base == expect ||
			(len(base) > len(expectPrefix) && base[:len(expectPrefix)] == expectPrefix) {
			return p, nil
		}
	}
	// Phase 2: fall back to embedded session_meta.id (rare path, but
	// preserves correctness when filenames don't follow the rollout-<id>
	// convention).
	for _, p := range files {
		meta := readCodexCandidateMeta(p)
		if meta.sessionID == sessionID {
			return p, nil
		}
	}
	return "", nil
}

// filepathBase returns the file basename without importing the
// path/filepath package at the call site (keeps the call sites tiny).
func filepathBase(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
