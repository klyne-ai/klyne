package usage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// CodexContextSnapshot is the latest per-turn context snapshot written by
// Codex's token_count event. Unlike total_token_usage, LastInputTokens is the
// input packed for the most recent turn, which is what the next-turn context
// fill indicator needs.
type CodexContextSnapshot struct {
	LastInputTokens    int64
	ModelContextWindow int64
}

type codexContextEvent struct {
	Type    string `json:"type"`
	Payload struct {
		Type string `json:"type"`
		Info struct {
			LastTokenUsage struct {
				InputTokens int64 `json:"input_tokens"`
			} `json:"last_token_usage"`
			ModelContextWindow int64 `json:"model_context_window"`
		} `json:"info"`
	} `json:"payload"`
}

// LatestCodexContextSnapshot scans a Codex session JSONL and returns the last
// token_count context snapshot. It is intentionally file-scoped: session_usage
// already has the raw_path for the exact session being viewed, so this avoids
// mixing context data from another active Codex session.
func LatestCodexContextSnapshot(path string) (CodexContextSnapshot, bool, error) {
	if path == "" {
		return CodexContextSnapshot{}, false, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return CodexContextSnapshot{}, false, fmt.Errorf("codex context: open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck

	const maxLine = 16 * 1024 * 1024
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxLine)

	var latest CodexContextSnapshot
	found := false
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev codexContextEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Type != "event_msg" || ev.Payload.Type != "token_count" {
			continue
		}
		if ev.Payload.Info.LastTokenUsage.InputTokens <= 0 && ev.Payload.Info.ModelContextWindow <= 0 {
			continue
		}
		latest = CodexContextSnapshot{
			LastInputTokens:    ev.Payload.Info.LastTokenUsage.InputTokens,
			ModelContextWindow: ev.Payload.Info.ModelContextWindow,
		}
		found = true
	}
	if err := scanner.Err(); err != nil {
		if found {
			return latest, true, nil
		}
		return CodexContextSnapshot{}, false, fmt.Errorf("codex context: scan %s: %w", path, err)
	}
	return latest, found, nil
}
