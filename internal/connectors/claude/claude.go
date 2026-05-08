// Package claude is the klyne connector for the Claude Code CLI.
//
// It implements the connectors.Connector interface by:
//   - Discovering *.jsonl files under ~/.claude/projects/<encoded-cwd>/<uuid>.jsonl
//   - Parsing each line into a canonical *connectors.Message (see parse.go)
//   - Watching for newly-appended lines via fsnotify (see watch.go)
//
// Construct via claude.New(root) where root is the expanded path of
// ~/.claude/projects (or an override for testing).
package claude

import (
	"context"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// Connector implements connectors.Connector for the Claude Code CLI.
type Connector struct {
	root string
}

// New returns a new Connector rooted at root.
// root is the expanded path of the Claude projects directory,
// e.g. "/Users/alice/.claude/projects".
func New(root string) *Connector {
	return &Connector{root: root}
}

// Name returns "claude", the stable connector identifier.
func (c *Connector) Name() string { return "claude" }

// Discover returns absolute paths to every *.jsonl file under c.root.
// It recursively visits one level of subdirectories (each subdirectory
// represents one encoded CWD path, e.g. "-Users-alice-projects-foo").
func (c *Connector) Discover(ctx context.Context) ([]string, error) {
	return discover(c.root)
}

// Watch streams RawEvent values for newly-appended JSONL lines until ctx is
// cancelled. On start-up it replays existing file content (warm-up) before
// attaching the OS-level watcher, ensuring no double-emission.
func (c *Connector) Watch(ctx context.Context, events chan<- connectors.RawEvent) error {
	w := newWatcher(c.root)
	return w.Watch(ctx, events)
}

// Parse converts a single raw JSONL line into a canonical *connectors.Message.
// The path argument is used to derive CLI and project context when the line
// does not carry them.
func (c *Connector) Parse(line []byte, path string) (*connectors.Message, error) {
	return Parse(line, path)
}

// Pricing returns an empty PricingTable. The Claude connector does not
// carry native pricing hints; the cost engine (W9) is the authoritative source.
func (c *Connector) Pricing() connectors.PricingTable {
	return connectors.PricingTable{}
}

// Ensure Connector satisfies the interface at compile time.
var _ connectors.Connector = (*Connector)(nil)
