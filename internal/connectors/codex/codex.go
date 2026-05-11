// Package codex implements the Codex CLI connector for klyne.
//
// Codex sessions are stored under:
//
//	~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl
//
// Usage:
//
//	c := codex.New(expandedRoot)   // root = expanded "~/.codex/sessions"
//	files, err := c.Discover(ctx)
//	events := make(chan connectors.RawEvent, 64)
//	go c.Watch(ctx, events)
//	for ev := range events {
//	    msg, err := c.Parse(ev.Line, ev.Path)
//	    ...
//	}
package codex

import (
	"context"
	"sync"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// fileMeta holds the per-file state accumulated while parsing a JSONL file.
// Fields are populated from session_meta and turn_context lines.
type fileMeta struct {
	sessionID   string
	projectPath string
	model       string

	// prevTokensIn and prevTokensOut track the last cumulative token totals
	// seen in a token_count event_msg. They are used to compute per-event
	// deltas so that InsertMessage's incrementing session counters converge
	// on the correct session-wide totals.
	prevTokensIn  int64
	prevTokensOut int64
	// prevCachedRead tracks the last cumulative cached_input_tokens value
	// (the cache-hit subset of input_tokens) so we can emit per-event
	// deltas alongside the input/output deltas.
	prevCachedRead int64

	// lastTurnIn / lastTurnOut / lastTurnCachedRead capture the most recent
	// per-turn token usage from a token_count event's `last_token_usage`
	// sub-object. They are NOT used during streaming parsing (that path
	// emits the system-role delta message); LoadSnapshot's post-pass
	// projection reads them indirectly via the system-role messages it
	// finds, but holding them on the meta struct lets future code that
	// drives a non-streaming snapshot path attach them to the matching
	// assistant message without re-reading the file.
	lastTurnIn         int64
	lastTurnOut        int64
	lastTurnCachedRead int64
}

// Connector implements connectors.Connector for the Codex CLI.
type Connector struct {
	root string

	// mu guards state (per-file metadata) and the watcher maps.
	mu      sync.Mutex
	state   map[string]*fileMeta // path → accumulated metadata
	_tails  map[string]*tailState
	_watched map[string]struct{}
}

// Ensure Connector satisfies the interface at compile time.
var _ connectors.Connector = (*Connector)(nil)

// New constructs a Codex connector rooted at root.
// root should be the expanded path to ~/.codex/sessions (callers must expand ~
// before calling New — this package does not import os/user to keep it lean).
func New(root string) *Connector {
	return &Connector{
		root:     root,
		state:    make(map[string]*fileMeta),
		_tails:   make(map[string]*tailState),
		_watched: make(map[string]struct{}),
	}
}

// Name returns the stable connector identifier.
func (c *Connector) Name() string { return "codex" }

// Discover returns absolute paths to all existing Codex JSONL files under
// root. It does not open or validate any files.
func (c *Connector) Discover(ctx context.Context) ([]string, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return discoverFiles(c.root)
}

// Watch streams RawEvent values for newly-appended lines until ctx is
// cancelled. It uses fsnotify to detect writes and a 30-second backstop scan
// to catch any events that fsnotify might miss (risk R9).
//
// Watch attaches to every existing date directory at startup and dynamically
// attaches to new YYYY/MM/DD directories as they are created during a day
// rollover, satisfying the date-partitioned layout requirement.
func (c *Connector) Watch(ctx context.Context, events chan<- connectors.RawEvent) error {
	w := &watcher{
		root:    c.root,
		events:  events,
		tails:   c._tails,
		watched: c._watched,
	}
	return w.watch(ctx)
}

// Parse converts a single raw JSONL line into a canonical connectors.Message.
// It uses per-file state (keyed by path) to accumulate session metadata from
// session_meta and turn_context lines, then applies that metadata to message-
// emitting lines.
//
// Unknown fields are silently ignored; malformed JSON returns a non-nil error
// and the caller should skip the line.
func (c *Connector) Parse(line []byte, path string) (*connectors.Message, error) {
	c.mu.Lock()
	meta, ok := c.state[path]
	if !ok {
		meta = &fileMeta{}
		c.state[path] = meta
	}
	c.mu.Unlock()

	return parseLine(line, path, meta, &c.mu)
}

// DropPath removes per-file state for path. Call this when the watcher
// detects that a file has been removed or when replaying a file from byte 0
// (warm-up re-read) to force a fresh re-parse of session_meta.
func (c *Connector) DropPath(path string) {
	c.mu.Lock()
	delete(c.state, path)
	c.mu.Unlock()
}

// Pricing returns an empty pricing table. Codex does not embed pricing hints
// in its JSONL format; the cost engine (W9) owns the authoritative table.
func (c *Connector) Pricing() connectors.PricingTable {
	return connectors.PricingTable{}
}
