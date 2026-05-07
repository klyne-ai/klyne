// Package codex implements the Codex CLI connector.
//
// Codex CLI writes session transcripts to:
//
//	~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl
//
// Every line is a JSON object: {"timestamp":"ISO-8601","type":"...","payload":{...}}
//
// Top-level types:
//   - session_meta  — first line; payload.id = session UUID, payload.cwd = working dir
//   - turn_context  — per-turn metadata; payload.model = model in use
//   - response_item — actual messages and tool calls; payload.type disambiguates
//   - event_msg     — high-level events (duplicates of response_item); skipped
//
// This parser is stateful per file: session_meta and turn_context lines update
// a *fileMeta value, which is then applied to message-emitting lines.
// Unknown top-level types are silently skipped for forward-compatibility.
package codex

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/mohitpatell/agentdeck/internal/connectors"
)

// ---------------------------------------------------------------------------
// Wire types — minimal structs to decode real Codex JSONL.
// ---------------------------------------------------------------------------

// envelope is the top-level wrapper present on every line.
type envelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// metaPayload decodes payload of a session_meta line.
type metaPayload struct {
	ID  string `json:"id"`
	CWD string `json:"cwd"`
}

// contextPayload decodes payload of a turn_context line.
type contextPayload struct {
	Model string `json:"model"`
	CWD   string `json:"cwd"`
}

// itemPayload is the union of all response_item payload shapes.
type itemPayload struct {
	// Discriminator
	Type string `json:"type"`

	// message fields
	Role    string           `json:"role"`
	Content []contentItem    `json:"content"`

	// function_call fields
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	CallID    string `json:"call_id"`

	// function_call_output fields
	Output string `json:"output"`

	// reasoning — no fields we use; payload.type == "reasoning" means skip
}

// contentItem is a single element of a message's content array.
type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ---------------------------------------------------------------------------
// parseTimestamp converts an ISO 8601 string to epoch milliseconds.
// Returns 0 on failure; caller substitutes wall-clock time.
// ---------------------------------------------------------------------------

func parseTimestamp(ts string) int64 {
	if ts == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		// Try millisecond-only precision ("2006-01-02T15:04:05.000Z")
		t, err = time.Parse("2006-01-02T15:04:05.000Z", ts)
		if err != nil {
			return 0
		}
	}
	return t.UnixMilli()
}

// ---------------------------------------------------------------------------
// messageID derives a deterministic ID for a message.
// Format: <sessionID>-<sha256(line)[:12]>
// This is stable across restarts and avoids storing offsets externally.
// ---------------------------------------------------------------------------

func messageID(sessionID string, line []byte) string {
	h := sha256.Sum256(line)
	return fmt.Sprintf("%s-%x", sessionID, h[:6])
}

// ---------------------------------------------------------------------------
// joinText concatenates all content items whose type matches wantType.
// Whitespace-trims the result.
// ---------------------------------------------------------------------------

func joinText(items []contentItem, wantType string) string {
	var parts []string
	for _, item := range items {
		if item.Type == wantType && item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// ---------------------------------------------------------------------------
// parseLine is the core stateful parse function. It is called by
// Connector.Parse with the per-file *fileMeta and the Connector's mutex.
//
// The mutex is used only to guard writes to meta; the caller holds no lock
// when calling parseLine. This keeps the critical section small.
// ---------------------------------------------------------------------------

func parseLine(line []byte, path string, meta *fileMeta, mu *sync.Mutex) (*connectors.Message, error) {
	if len(line) == 0 {
		return nil, fmt.Errorf("codex: empty line in %s", path)
	}

	var env envelope
	if err := json.Unmarshal(line, &env); err != nil {
		log.Printf("codex: malformed json in %s: %v", path, err)
		return nil, fmt.Errorf("codex: malformed json: %w", err)
	}

	ts := parseTimestamp(env.Timestamp)
	if ts == 0 {
		ts = time.Now().UnixMilli()
	}

	switch env.Type {
	case "session_meta":
		return handleSessionMeta(env.Payload, meta, mu, path)

	case "turn_context":
		return handleTurnContext(env.Payload, meta, mu, path)

	case "response_item":
		return handleResponseItem(env.Payload, line, meta, mu, path, ts)

	case "event_msg":
		// All event_msg subtypes are skipped — canonical data lives in response_item.
		return nil, nil

	default:
		log.Printf("codex: unknown line type %q in %s — skipping (forward-compat)", env.Type, path)
		return nil, nil
	}
}

// handleSessionMeta updates meta.sessionID and meta.projectPath.
func handleSessionMeta(raw json.RawMessage, meta *fileMeta, mu *sync.Mutex, path string) (*connectors.Message, error) {
	var p metaPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		log.Printf("codex: failed to decode session_meta payload in %s: %v", path, err)
		return nil, nil
	}
	mu.Lock()
	meta.sessionID = p.ID
	meta.projectPath = p.CWD
	mu.Unlock()
	return nil, nil
}

// handleTurnContext updates meta.model. The first session_meta's cwd wins;
// we do not overwrite projectPath here.
func handleTurnContext(raw json.RawMessage, meta *fileMeta, mu *sync.Mutex, path string) (*connectors.Message, error) {
	var p contextPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		log.Printf("codex: failed to decode turn_context payload in %s: %v", path, err)
		return nil, nil
	}
	if p.Model == "" {
		return nil, nil
	}
	mu.Lock()
	meta.model = p.Model
	mu.Unlock()
	return nil, nil
}

// handleResponseItem dispatches on payload.type within a response_item line.
func handleResponseItem(raw json.RawMessage, line []byte, meta *fileMeta, mu *sync.Mutex, path string, ts int64) (*connectors.Message, error) {
	var p itemPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		log.Printf("codex: failed to decode response_item payload in %s: %v", path, err)
		return nil, nil
	}

	switch p.Type {
	case "message":
		return handleMessage(p, line, meta, mu, path, ts)

	case "function_call":
		return handleFunctionCall(p, line, meta, mu, path, ts)

	case "function_call_output":
		return handleFunctionCallOutput(p, line, meta, mu, path, ts)

	case "reasoning":
		// Chain-of-thought — not user-visible; skip.
		return nil, nil

	default:
		log.Printf("codex: unknown response_item type %q in %s — skipping", p.Type, path)
		return nil, nil
	}
}

// stateSnapshot safely reads a copy of meta's fields.
func stateSnapshot(meta *fileMeta, mu *sync.Mutex) (sessionID, projectPath, model string) {
	mu.Lock()
	sessionID = meta.sessionID
	projectPath = meta.projectPath
	model = meta.model
	mu.Unlock()
	return
}

// handleMessage handles response_item with payload.type == "message".
func handleMessage(p itemPayload, line []byte, meta *fileMeta, mu *sync.Mutex, path string, ts int64) (*connectors.Message, error) {
	sessionID, projectPath, model := stateSnapshot(meta, mu)

	if sessionID == "" {
		log.Printf("codex: user/assistant message before session_meta in %s — skipping", path)
		return nil, nil
	}

	switch p.Role {
	case "user":
		content := joinText(p.Content, "input_text")
		if content == "" {
			// Skip empty/environment-context-only user messages.
			return nil, nil
		}
		return &connectors.Message{
			ID:          messageID(sessionID, line),
			SessionID:   sessionID,
			CLI:         connectors.CLICodex,
			ProjectPath: projectPath,
			Role:        connectors.RoleUser,
			Content:     content,
			Model:       model,
			Ts:          ts,
		}, nil

	case "assistant":
		content := joinText(p.Content, "output_text")
		return &connectors.Message{
			ID:          messageID(sessionID, line),
			SessionID:   sessionID,
			CLI:         connectors.CLICodex,
			ProjectPath: projectPath,
			Role:        connectors.RoleAssistant,
			Content:     content,
			Model:       model,
			Ts:          ts,
		}, nil

	case "developer":
		// System-injected developer context — skip.
		return nil, nil

	default:
		log.Printf("codex: unknown message role %q in %s — skipping", p.Role, path)
		return nil, nil
	}
}

// handleFunctionCall handles response_item with payload.type == "function_call".
func handleFunctionCall(p itemPayload, line []byte, meta *fileMeta, mu *sync.Mutex, path string, ts int64) (*connectors.Message, error) {
	sessionID, projectPath, model := stateSnapshot(meta, mu)

	if sessionID == "" {
		log.Printf("codex: function_call before session_meta in %s — skipping", path)
		return nil, nil
	}

	return &connectors.Message{
		ID:          messageID(sessionID, line),
		SessionID:   sessionID,
		CLI:         connectors.CLICodex,
		ProjectPath: projectPath,
		Role:        connectors.RoleAssistant,
		Content:     "",
		ToolCalls: []connectors.ToolCall{
			{
				ID:    p.CallID,
				Name:  p.Name,
				Input: p.Arguments,
			},
		},
		Model: model,
		Ts:    ts,
	}, nil
}

// handleFunctionCallOutput handles response_item with payload.type == "function_call_output".
func handleFunctionCallOutput(p itemPayload, line []byte, meta *fileMeta, mu *sync.Mutex, path string, ts int64) (*connectors.Message, error) {
	sessionID, projectPath, model := stateSnapshot(meta, mu)

	if sessionID == "" {
		log.Printf("codex: function_call_output before session_meta in %s — skipping", path)
		return nil, nil
	}

	return &connectors.Message{
		ID:          messageID(sessionID, line),
		SessionID:   sessionID,
		CLI:         connectors.CLICodex,
		ProjectPath: projectPath,
		Role:        connectors.RoleTool,
		Content:     p.Output,
		ToolResults: []connectors.ToolResult{
			{
				ID:     p.CallID,
				Output: p.Output,
			},
		},
		Model: model,
		Ts:    ts,
	}, nil
}
