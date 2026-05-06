package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohitpatell/agentdeck/internal/connectors"
)

// sampleLine is a valid JSONL line for writing to test files.
const sampleLine = `{"type":"system","uuid":"sys-test-001","sessionId":"sess-001","timestamp":"2026-05-06T10:00:00.000Z","message":"init","cwd":"/tmp/testproject"}` + "\n"

// makeTempRoot creates a ~/.claude/projects-like temp structure:
//
//	<tmpdir>/
//	  <encodedCWD>/
//	    <sessionFile>.jsonl
func makeTempRoot(t *testing.T, encodedCWD, sessionFile, content string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, encodedCWD)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}
	if sessionFile != "" {
		path := filepath.Join(dir, sessionFile)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("writefile: %v", err)
		}
	}
	return root
}

// ── TestDiscover_FindsFiles ───────────────────────────────────────────────────

func TestDiscover_FindsFiles(t *testing.T) {
	root := t.TempDir()

	// Create two encoded-CWD subdirectories with JSONL files.
	for i := 1; i <= 2; i++ {
		dir := filepath.Join(root, fmt.Sprintf("-Users-dev-project%d", i))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		p := filepath.Join(dir, fmt.Sprintf("session-%d.jsonl", i))
		if err := os.WriteFile(p, []byte(sampleLine), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	// Add a non-.jsonl file that should be ignored.
	dir := filepath.Join(root, "-Users-dev-project1")
	_ = os.WriteFile(filepath.Join(dir, "meta.txt"), []byte("ignore"), 0644)
	// Add a plain file at the root (not in a subdir) — should be ignored.
	_ = os.WriteFile(filepath.Join(root, "stray.jsonl"), []byte(sampleLine), 0644)

	c := New(root)
	ctx := context.Background()
	paths, err := c.Discover(ctx)
	if err != nil {
		t.Fatalf("Discover() error: %v", err)
	}
	if len(paths) != 2 {
		t.Errorf("expected 2 paths, got %d: %v", len(paths), paths)
	}
	for _, p := range paths {
		if filepath.Ext(p) != ".jsonl" {
			t.Errorf("non-jsonl path returned: %s", p)
		}
	}
}

// ── TestWatch_NewLinesArrive ──────────────────────────────────────────────────

func TestWatch_NewLinesArrive(t *testing.T) {
	// Start with an empty JSONL file, attach watcher, then append a line.
	// The event must arrive on the channel within 200ms.
	root := makeTempRoot(t, "-Users-dev-project", "session.jsonl", "")
	jsonlPath := filepath.Join(root, "-Users-dev-project", "session.jsonl")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events := make(chan connectors.RawEvent, 16)
	c := New(root)

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Watch(ctx, events)
	}()

	// Give watcher time to initialise.
	time.Sleep(50 * time.Millisecond)

	// Append a new line to the file.
	f, err := os.OpenFile(jsonlPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	_, _ = f.WriteString(sampleLine)
	f.Close()

	select {
	case ev := <-events:
		if len(ev.Line) == 0 {
			t.Error("received empty line")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("no event received within 200ms")
	}
}

// ── TestWatch_NoDoubleEmit_OnWarmup ───────────────────────────────────────────

func TestWatch_NoDoubleEmit_OnWarmup(t *testing.T) {
	// File has exactly 10 lines before Watch starts. Warm-up should emit
	// exactly 10 events; not 20.
	var content string
	for i := 0; i < 10; i++ {
		content += sampleLine
	}
	root := makeTempRoot(t, "-Users-dev-project", "session.jsonl", content)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events := make(chan connectors.RawEvent, 32)
	c := New(root)

	go func() {
		_ = c.Watch(ctx, events)
	}()

	// Drain events for 500ms, then assert count.
	deadline := time.After(500 * time.Millisecond)
	var received int
loop:
	for {
		select {
		case <-events:
			received++
		case <-deadline:
			break loop
		}
	}

	if received != 10 {
		t.Errorf("expected exactly 10 events on warm-up, got %d", received)
	}
}

// ── TestWatch_BackstopDetectsNewFile ─────────────────────────────────────────

func TestWatch_BackstopDetectsNewFile(t *testing.T) {
	// Timing assumption: the backstop scan fires when backstopTrigger is
	// signalled, so we do NOT need to sleep 30s. We trigger it manually by
	// accessing the watcher's channel directly via an internal helper.
	root := t.TempDir()
	// Start with an empty root (no subdirs).

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events := make(chan connectors.RawEvent, 32)

	// We need access to the watcher to trigger the backstop manually.
	w := newWatcher(root)

	// Start watching in a goroutine.
	go func() {
		_ = w.Watch(ctx, events)
	}()

	// Give the watcher goroutines time to start.
	time.Sleep(60 * time.Millisecond)

	// Now create a new subdirectory + JSONL file that fsnotify may not have
	// caught (simulating a new project directory appearing on disk).
	dir := filepath.Join(root, "-Users-dev-newproject")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(p, []byte(sampleLine), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Trigger the backstop scan manually instead of sleeping 30s.
	select {
	case w.backstopTrigger <- struct{}{}:
	default:
	}

	// The new file's lines should arrive within 500ms.
	select {
	case ev := <-events:
		if len(ev.Line) == 0 {
			t.Error("received empty line from new file")
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("backstop: no event received within 500ms after trigger")
	}
}

// ── TestWatch_CancelStops ─────────────────────────────────────────────────────

func TestWatch_CancelStops(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())

	events := make(chan connectors.RawEvent, 8)
	c := New(root)

	done := make(chan struct{})
	go func() {
		_ = c.Watch(ctx, events)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// expected
	case <-time.After(2 * time.Second):
		t.Error("Watch did not return after ctx cancel")
	}
}

// ── TestDiscover_EmptyRoot ────────────────────────────────────────────────────

func TestDiscover_EmptyRoot(t *testing.T) {
	root := t.TempDir()
	c := New(root)
	paths, err := c.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover on empty root: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("expected 0 paths, got %d", len(paths))
	}
}

func TestDiscover_NonexistentRoot(t *testing.T) {
	c := New("/nonexistent/path/that/does/not/exist")
	paths, err := c.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover nonexistent root should return nil error, got: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("expected 0 paths, got %d", len(paths))
	}
}
