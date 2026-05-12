// Package otelexport emits one OTel-compatible JSON line per assistant
// message turn ingested into klyne's local DB. Inspired by
// ColeMurray/claude-code-otel but kept dependency-free: no OTel SDK,
// no network. The export writes ndjson that an OTLP collector or a
// downstream pipeline can pick up.
//
// Design rationale:
//   - klyne is local-first and read-only. An always-on OTLP exporter
//     would cross those boundaries. The opt-in `klyne otel emit`
//     command writes to a file or stdout — the user (or a one-shot
//     cron task) decides when data leaves the machine.
//   - We mirror OTel span semantic conventions ("gen_ai.*" attributes
//     per the OpenTelemetry GenAI WG draft) so the output is usable in
//     Tempo / Jaeger / Datadog after light field-mapping.
package otelexport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/cost"
	"github.com/klyne-ai/klyne/internal/store"
)

// Span is one OTel-shaped span, encoded as a single JSON line.
// Field names follow the OTel JSON Lines export convention so consumers
// that understand OTLP/JSON can ingest the file directly.
type Span struct {
	TraceID    string            `json:"trace_id"`
	SpanID     string            `json:"span_id"`
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	StartTime  string            `json:"start_time"` // RFC3339Nano
	EndTime    string            `json:"end_time"`
	Attributes map[string]any    `json:"attributes"`
	Resource   map[string]any    `json:"resource"`
	Status     map[string]string `json:"status,omitempty"`
}

// Filter scopes an export.
type Filter struct {
	ProjectPath       string
	SinceMs           int64
	MaxSessions       int
	MaxMsgsPerSession int
}

// Emit writes one JSON-lines span per assistant message to w. Sessions
// without any assistant messages are skipped. Returns the count written.
func Emit(ctx context.Context, db *store.DB, engine *cost.Engine, w io.Writer, f Filter) (int, error) {
	limit := f.MaxSessions
	if limit <= 0 {
		limit = 500
	}
	sessions, err := store.ListSessions(ctx, db, store.SessionFilter{
		ProjectPath: f.ProjectPath,
		Limit:       limit,
	})
	if err != nil {
		return 0, fmt.Errorf("otel: list sessions: %w", err)
	}
	msgCap := f.MaxMsgsPerSession
	if msgCap <= 0 {
		msgCap = 5000
	}

	enc := json.NewEncoder(w)
	count := 0
	for _, s := range sessions {
		msgs, err := store.ListMessagesBySession(ctx, db, s.ID, msgCap, 0)
		if err != nil {
			continue
		}
		var prev *connectors.Message
		for _, m := range msgs {
			if m.Role != connectors.RoleAssistant {
				prev = m
				continue
			}
			if f.SinceMs > 0 && m.Ts < f.SinceMs {
				prev = m
				continue
			}
			span := buildSpan(s, prev, m, engine)
			if err := enc.Encode(span); err != nil {
				return count, fmt.Errorf("otel: encode span: %w", err)
			}
			count++
			prev = m
		}
	}
	return count, nil
}

// buildSpan derives one OTel span from an assistant message + its
// preceding message (typically a user message — used to anchor
// start_time so the span has non-zero duration when timestamps are
// available).
func buildSpan(s *connectors.Session, prev *connectors.Message, m *connectors.Message, engine *cost.Engine) Span {
	start := m.Ts
	if prev != nil && prev.Ts > 0 && prev.Ts < start {
		start = prev.Ts
	}
	end := m.Ts
	var costUSD float64
	if engine != nil {
		costUSD = engine.Cost(m.TokensIn, m.TokensOut, m.CachedReadTokens, m.CachedWriteTokens, m.Model)
	}
	return Span{
		TraceID:   deriveTraceID(s.ID),
		SpanID:    deriveSpanID(m.ID),
		Name:      "gen_ai.completion",
		Kind:      "SPAN_KIND_INTERNAL",
		StartTime: time.UnixMilli(start).UTC().Format(time.RFC3339Nano),
		EndTime:   time.UnixMilli(end).UTC().Format(time.RFC3339Nano),
		Attributes: map[string]any{
			"gen_ai.system":                          "anthropic",
			"gen_ai.request.model":                   m.Model,
			"gen_ai.usage.input_tokens":              m.TokensIn,
			"gen_ai.usage.output_tokens":             m.TokensOut,
			"gen_ai.usage.cache_read_input_tokens":   m.CachedReadTokens,
			"gen_ai.usage.cache_write_input_tokens":  m.CachedWriteTokens,
			"gen_ai.cost.usd":                        costUSD,
			"klyne.session_id":                       s.ID,
			"klyne.cli":                              string(s.CLI),
			"klyne.project_path":                     s.ProjectPath,
		},
		Resource: map[string]any{
			"service.name":      "klyne",
			"service.namespace": "ai-coding-cli",
		},
		Status: map[string]string{"code": "STATUS_CODE_UNSET"},
	}
}

// deriveTraceID returns a 32-hex-char trace id derived from the session
// id (FNV-64a, doubled). Deterministic, so re-exporting the same data
// produces identical trace ids.
func deriveTraceID(sid string) string {
	h := fnv64a(sid)
	return fmt.Sprintf("%016x%016x", h, h^0xdeadbeefcafef00d)
}

// deriveSpanID returns a 16-hex-char span id derived from the message id.
func deriveSpanID(mid string) string {
	return fmt.Sprintf("%016x", fnv64a(mid))
}

// fnv64a is the FNV-1a 64-bit hash. Inlined to keep the package
// dependency-free.
func fnv64a(s string) uint64 {
	const (
		offset64 uint64 = 14695981039346656037
		prime64  uint64 = 1099511628211
	)
	h := offset64
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}
