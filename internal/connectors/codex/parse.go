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

	"github.com/klyne-ai/klyne/internal/connectors"
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
//
// Note: function_call_output.output is normally a string, but Codex now
// occasionally emits it as an array (e.g. when a tool returns image
// content). To tolerate both shapes without a JSON unmarshal error, this
// field is decoded as json.RawMessage and post-processed by handleFunctionCallOutput.
// Same for custom_tool_call.input which mirrors function_call.arguments,
// and custom_tool_call_output.output which mirrors the function variant.
type itemPayload struct {
	// Discriminator
	Type string `json:"type"`

	// message fields
	Role    string        `json:"role"`
	Content []contentItem `json:"content"`

	// function_call / custom_tool_call fields. Codex's `custom_tool_call`
	// is the function-call variant for arbitrary tools registered via
	// the new `tools` API; the shape mirrors function_call but uses
	// `input` instead of `arguments` for the JSON-encoded argument blob.
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Input     string `json:"input"`
	CallID    string `json:"call_id"`

	// function_call_output / custom_tool_call_output fields.
	// Output is decoded as RawMessage so we can accept either a JSON
	// string (the legacy/common shape) or a JSON array (newer Codex
	// flavours that pass back image attachments). The post-processor
	// downgrades arrays to a marshaled JSON string so the downstream
	// canonical Message.Content stays a plain string.
	Output json.RawMessage `json:"output"`

	// reasoning — no fields we use; payload.type == "reasoning" means skip
}

// contentItem is a single element of a message's content array.
type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// eventMsgPayload is the outer payload for an event_msg line.
// Only the Type field is needed to dispatch; subtype-specific structs
// decode the rest.
type eventMsgPayload struct {
	Type string `json:"type"`
}

// tokenUsage holds the per-million-token counters inside a token_count event.
//
// Per OpenAI's API, InputTokens already INCLUDES the CachedInputTokens
// subset — they are not additive. We expose CachedInputTokens separately so
// the cost engine can bill the cache-hit portion at the cache_read rate
// while billing the remainder at the full prompt rate.
type tokenUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	CachedInputTokens  int64 `json:"cached_input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
}

// tokenCountInfo is the "info" sub-object of a token_count event_msg payload.
//
// LastTokenUsage is the per-call usage for the just-completed assistant
// turn (NOT cumulative); TotalTokenUsage is the session-cumulative roll-up.
// Per real-fixture observation:
//
//	tc[0]: total.input=21990  last.input=21990   (turn 1)
//	tc[1]: total.input=44510  last.input=22520   (turn 2; sum of last == total)
//
// The cumulative-delta computed elsewhere happens to equal LastTokenUsage,
// but reading LastTokenUsage directly avoids depending on that invariant
// and surfaces the per-turn value the timeline aggregator wants.
type tokenCountInfo struct {
	LastTokenUsage  *tokenUsage `json:"last_token_usage"`
	TotalTokenUsage tokenUsage  `json:"total_token_usage"`
}

// tokenCountPayload fully decodes the payload of an event_msg/token_count line.
type tokenCountPayload struct {
	Type string         `json:"type"`
	Info tokenCountInfo `json:"info"`
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
		return handleEventMsg(env.Payload, line, meta, mu, path, ts)

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

// handleEventMsg dispatches on the payload.type field of an event_msg line.
// Most event_msg subtypes are high-level duplicates of response_item data and
// are skipped. The token_count subtype is the exception: it is the ONLY place
// Codex JSONL records token usage, so we emit a synthetic system message
// carrying delta token counts so that InsertMessage's accumulating session
// counters converge on the correct totals.
func handleEventMsg(raw json.RawMessage, line []byte, meta *fileMeta, mu *sync.Mutex, path string, ts int64) (*connectors.Message, error) {
	// Peek at the subtype first — cheap decode of just the "type" field.
	var ep eventMsgPayload
	if err := json.Unmarshal(raw, &ep); err != nil {
		log.Printf("codex: failed to decode event_msg payload in %s: %v", path, err)
		return nil, nil
	}

	if ep.Type == "token_count" {
		return handleTokenCount(raw, line, meta, mu, path, ts)
	}

	// All other subtypes (agent_message, agent_reasoning, user_message,
	// task_started, task_complete, turn_aborted, exec_command_end, error, …)
	// are high-level status events; their canonical data lives in response_item.
	return nil, nil
}

// handleTokenCount processes an event_msg with payload.type == "token_count".
// Codex emits these lines repeatedly; total_token_usage is the session-cumulative
// total at the moment the event fires. We convert to per-event deltas so that
// InsertMessage's incrementing session counters stay accurate.
func handleTokenCount(raw json.RawMessage, line []byte, meta *fileMeta, mu *sync.Mutex, path string, ts int64) (*connectors.Message, error) {
	mu.Lock()
	sessionID := meta.sessionID
	mu.Unlock()

	if sessionID == "" {
		// No session context yet — skip; we have nothing to attach this to.
		log.Printf("codex: token_count before session_meta in %s — skipping", path)
		return nil, nil
	}

	var p tokenCountPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		log.Printf("codex: failed to decode token_count payload in %s: %v", path, err)
		return nil, nil
	}

	cumIn := p.Info.TotalTokenUsage.InputTokens
	cumOut := p.Info.TotalTokenUsage.OutputTokens
	cumCachedRead := p.Info.TotalTokenUsage.CachedInputTokens

	// All zero means the "info" field was null or absent — nothing to emit.
	if cumIn == 0 && cumOut == 0 && cumCachedRead == 0 {
		return nil, nil
	}

	// Capture the per-turn snapshot when present (the LoadSnapshot post-pass
	// uses this to project per-call usage onto the matching assistant message).
	var lastIn, lastOut, lastCachedRead int64
	if p.Info.LastTokenUsage != nil {
		lastIn = p.Info.LastTokenUsage.InputTokens
		lastOut = p.Info.LastTokenUsage.OutputTokens
		lastCachedRead = p.Info.LastTokenUsage.CachedInputTokens
	}

	mu.Lock()
	prevIn := meta.prevTokensIn
	prevOut := meta.prevTokensOut
	prevCachedRead := meta.prevCachedRead
	meta.prevTokensIn = cumIn
	meta.prevTokensOut = cumOut
	meta.prevCachedRead = cumCachedRead
	meta.lastTurnIn = lastIn
	meta.lastTurnOut = lastOut
	meta.lastTurnCachedRead = lastCachedRead
	sessionID = meta.sessionID
	projectPath := meta.projectPath
	model := meta.model
	mu.Unlock()

	deltaIn := cumIn - prevIn
	deltaOut := cumOut - prevOut
	deltaCachedRead := cumCachedRead - prevCachedRead

	// Guard against negative deltas (rare: file truncated, race, etc.).
	if deltaIn < 0 {
		deltaIn = 0
	}
	if deltaOut < 0 {
		deltaOut = 0
	}
	if deltaCachedRead < 0 {
		deltaCachedRead = 0
	}

	// If all deltas are zero after clamping there is nothing useful to emit.
	if deltaIn == 0 && deltaOut == 0 && deltaCachedRead == 0 {
		return nil, nil
	}

	// Note: per OpenAI's API, deltaIn already INCLUDES deltaCachedRead — we
	// keep them separate (TokensIn = deltaIn, CachedReadTokens =
	// deltaCachedRead) so the cost engine can subtract and apply the
	// cache_read rate. CachedWriteTokens is always 0: OpenAI does not
	// expose a comparable cache-write counter.
	return &connectors.Message{
		ID:                messageID(sessionID, line),
		SessionID:         sessionID,
		CLI:               connectors.CLICodex,
		ProjectPath:       projectPath,
		Cwd:               projectPath,
		Role:              connectors.RoleSystem,
		Content:           "",
		TokensIn:          deltaIn,
		TokensOut:         deltaOut,
		CachedReadTokens:  deltaCachedRead,
		CachedWriteTokens: 0,
		Model:             model,
		Ts:                ts,
	}, nil
}

// handleResponseItem dispatches on payload.type within a response_item line.
//
// Forward-compat policy: unknown response_item types and decode errors are
// silently skipped. The Codex JSONL schema evolves frequently (the v0 set
// of types — message / function_call / function_call_output / reasoning —
// has since grown to include custom_tool_call, custom_tool_call_output,
// and richer payload shapes). Logging once-per-occurrence floods stderr
// for any command that walks Codex transcripts (klyne tokens, klyne
// audit-sessions, klyne advise via the 5h aggregator). Once-per-file
// logging is gated by warnOnce.
func handleResponseItem(raw json.RawMessage, line []byte, meta *fileMeta, mu *sync.Mutex, path string, ts int64) (*connectors.Message, error) {
	var p itemPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		warnOncePerFile(path, "decode-error", func() {
			log.Printf("codex: failed to decode response_item payload in %s: %v (further occurrences silenced)", path, err)
		})
		return nil, nil
	}

	switch p.Type {
	case "message":
		return handleMessage(p, line, meta, mu, path, ts)

	case "function_call", "custom_tool_call":
		// Codex's `custom_tool_call` is the function-call variant for
		// arbitrary registered tools (e.g. `apply_patch`). It uses
		// `input` instead of `arguments` to carry the JSON-encoded
		// argument blob; otherwise the canonical shape is identical.
		return handleFunctionCall(p, line, meta, mu, path, ts)

	case "function_call_output", "custom_tool_call_output":
		return handleFunctionCallOutput(p, line, meta, mu, path, ts)

	case "reasoning":
		// Chain-of-thought — not user-visible; skip.
		return nil, nil

	default:
		warnOncePerFile(path, "type:"+p.Type, func() {
			log.Printf("codex: unknown response_item type %q in %s — skipping (further occurrences silenced)", p.Type, path)
		})
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
			Cwd:         projectPath, // Codex doesn't emit per-message cwd; equals project root
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
			Cwd:         projectPath, // Codex doesn't emit per-message cwd; equals project root
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

// handleFunctionCall handles response_item with payload.type == "function_call"
// or the equivalent "custom_tool_call" shape. The two shapes differ only in
// the field carrying the JSON-encoded argument blob: function_call uses
// `arguments`, custom_tool_call uses `input`. Whichever is non-empty wins;
// `arguments` is preferred when both happen to be present (legacy precedence).
func handleFunctionCall(p itemPayload, line []byte, meta *fileMeta, mu *sync.Mutex, path string, ts int64) (*connectors.Message, error) {
	sessionID, projectPath, model := stateSnapshot(meta, mu)

	if sessionID == "" {
		log.Printf("codex: function_call before session_meta in %s — skipping", path)
		return nil, nil
	}

	args := p.Arguments
	if args == "" {
		args = p.Input
	}

	return &connectors.Message{
		ID:          messageID(sessionID, line),
		SessionID:   sessionID,
		CLI:         connectors.CLICodex,
		ProjectPath: projectPath,
		Cwd:         projectPath, // Codex doesn't emit per-message cwd; equals project root
		Role:        connectors.RoleAssistant,
		Content:     "",
		ToolCalls: []connectors.ToolCall{
			{
				ID:    p.CallID,
				Name:  p.Name,
				Input: args,
			},
		},
		Model: model,
		Ts:    ts,
	}, nil
}

// handleFunctionCallOutput handles response_item with payload.type
// == "function_call_output" or "custom_tool_call_output".
//
// p.Output is decoded as json.RawMessage so we can accept either of the two
// shapes Codex emits today:
//
//   - JSON string ("output":"stdout text\nfoo bar"). The legacy shape
//     produced by simple shell-style tool calls.
//   - JSON array ("output":[{"type":"input_image", ...}]). Newer Codex
//     flavours use this when a tool returns image attachments.
//
// We unwrap the string variant directly; the array variant is normalized
// to a re-marshaled JSON string so the canonical Message.Content stays a
// plain string and the cockpit / DB don't need to know about the dual
// shape. Either way, downstream code keeps reading `Content` as a string.
func handleFunctionCallOutput(p itemPayload, line []byte, meta *fileMeta, mu *sync.Mutex, path string, ts int64) (*connectors.Message, error) {
	sessionID, projectPath, model := stateSnapshot(meta, mu)

	if sessionID == "" {
		log.Printf("codex: function_call_output before session_meta in %s — skipping", path)
		return nil, nil
	}

	output := normalizeOutput(p.Output)

	return &connectors.Message{
		ID:          messageID(sessionID, line),
		SessionID:   sessionID,
		CLI:         connectors.CLICodex,
		ProjectPath: projectPath,
		Cwd:         projectPath, // Codex doesn't emit per-message cwd; equals project root
		Role:        connectors.RoleTool,
		Content:     output,
		ToolResults: []connectors.ToolResult{
			{
				ID:     p.CallID,
				Output: output,
			},
		},
		Model: model,
		Ts:    ts,
	}, nil
}

// normalizeOutput collapses the dual-shape `output` field into a plain
// string. Empty / null raw becomes the empty string. JSON-string raw is
// unmarshaled and returned as the inner text. Anything else (object,
// array, number, bool) is re-marshaled and returned verbatim — losing
// nothing, but presenting a single shape to the rest of the pipeline.
func normalizeOutput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Cheap shape check: a JSON string starts with a double-quote.
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	// Non-string variant — keep the raw JSON so downstream consumers
	// can pretty-print it if they want.
	return string(raw)
}

// warnOnceState tracks (path, key) pairs already warned about so the
// log output stays a single line per occurrence per file. Indexed by
// "<path>::<key>" — sync.Map keeps the lookup lock-free under typical
// fan-out loads.
var warnOnceState sync.Map

// warnOncePerFile invokes fn the first time it sees the given (path, key)
// pair. Subsequent calls with the same pair are no-ops. Used to gate
// once-per-file warnings for forward-compat skips so commands that walk
// many Codex JSONLs don't flood stderr.
func warnOncePerFile(path, key string, fn func()) {
	k := path + "\x00" + key
	if _, loaded := warnOnceState.LoadOrStore(k, struct{}{}); !loaded {
		fn()
	}
}
