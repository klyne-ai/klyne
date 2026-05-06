// Package codex implements the Codex CLI connector for agentdeck.
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

	"github.com/mohitpatell/agentdeck/internal/connectors"
)

// Connector implements connectors.Connector for the Codex CLI.
type Connector struct {
	root string

	// mu guards state used by Watch (tails, watched dirs).
	mu      sync.Mutex
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
// Unknown fields are silently ignored; malformed JSON returns a non-nil error
// and the caller should skip the line.
func (c *Connector) Parse(line []byte, path string) (*connectors.Message, error) {
	return ParseLine(line, path)
}

// Pricing returns an empty pricing table. Codex does not embed pricing hints
// in its JSONL format; the cost engine (W9) owns the authoritative table.
func (c *Connector) Pricing() connectors.PricingTable {
	return connectors.PricingTable{}
}
