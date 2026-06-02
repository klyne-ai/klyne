package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/klyne-ai/klyne/internal/connectors"
)

// newFsnotify creates a real fsnotify watcher for tests; it skips the test if
// fsnotify is unavailable on the platform.
func newFsnotify(t *testing.T) (*fsnotify.Watcher, error) {
	t.Helper()
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		t.Skipf("fsnotify not available: %v", err)
	}
	return fw, err
}

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

// ── TestDiscover_SkipsStaleFiles ──────────────────────────────────────────────

func TestDiscover_SkipsStaleFiles(t *testing.T) {
	// A fresh file and a long-untouched file live side by side. discover must
	// return only the fresh one: stale files are inert and watching them wastes
	// file descriptors and kqueue wakeups.
	root := t.TempDir()
	dir := filepath.Join(root, "-Users-dev-project")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fresh := filepath.Join(dir, "fresh.jsonl")
	stale := filepath.Join(dir, "stale.jsonl")
	if err := os.WriteFile(fresh, []byte(sampleLine), 0644); err != nil {
		t.Fatalf("write fresh: %v", err)
	}
	if err := os.WriteFile(stale, []byte(sampleLine), 0644); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	old := time.Now().Add(-(maxFileAge + 24*time.Hour))
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	paths, err := discover(root)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(paths) != 1 || filepath.Base(paths[0]) != "fresh.jsonl" {
		t.Errorf("expected only fresh.jsonl within %s window, got %v", maxFileAge, paths)
	}
}

// ── TestDirHasRecentJSONL ─────────────────────────────────────────────────────

func TestDirHasRecentJSONL(t *testing.T) {
	mkdir := func(t *testing.T) string {
		t.Helper()
		d := t.TempDir()
		return d
	}

	t.Run("fresh file present", func(t *testing.T) {
		d := mkdir(t)
		if err := os.WriteFile(filepath.Join(d, "a.jsonl"), []byte(sampleLine), 0644); err != nil {
			t.Fatal(err)
		}
		if !dirHasRecentJSONL(d) {
			t.Error("expected true for dir with a fresh .jsonl")
		}
	})

	t.Run("only stale files", func(t *testing.T) {
		d := mkdir(t)
		p := filepath.Join(d, "old.jsonl")
		if err := os.WriteFile(p, []byte(sampleLine), 0644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-(maxFileAge + 24*time.Hour))
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
		if dirHasRecentJSONL(d) {
			t.Error("expected false for dir with only stale .jsonl")
		}
	})

	t.Run("empty dir", func(t *testing.T) {
		if dirHasRecentJSONL(mkdir(t)) {
			t.Error("expected false for empty dir")
		}
	})
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

// drainClaudeEvents collects all events available within d.
func drainClaudeEvents(events <-chan connectors.RawEvent, d time.Duration) []connectors.RawEvent {
	var received []connectors.RawEvent
	timeout := time.After(d)
	for {
		select {
		case ev := <-events:
			received = append(received, ev)
		case <-timeout:
			return received
		}
	}
}

// ── TestTail_PartialFlush ─────────────────────────────────────────────────────

// TestTail_PartialFlush reproduces the partial-flush bug: when only the first N
// bytes of a JSONL line are on disk (no trailing newline, e.g. a large tool
// result caught mid-flush), tail must NOT emit a fragment and must NOT advance
// the offset past the incomplete line. Once the remainder + newline lands, the
// FULL intact line must be emitted exactly once.
func TestTail_PartialFlush(t *testing.T) {
	root := makeTempRoot(t, "-Users-dev-project", "session.jsonl", "")
	path := filepath.Join(root, "-Users-dev-project", "session.jsonl")

	full := `{"type":"system","uuid":"sys-1","sessionId":"s","timestamp":"2026-05-06T10:00:00.000Z","message":"a big tool result that gets flushed in two writes"}`
	split := len(full) / 2

	events := make(chan connectors.RawEvent, 8)
	w := newWatcher(root)
	fw, err := newFsnotify(t)
	defer fw.Close()
	_ = err
	w.ensureWatched(fw, path)

	// Write first half, no newline, then tail.
	if e := os.WriteFile(path, []byte(full[:split]), 0644); e != nil {
		t.Fatalf("write: %v", e)
	}
	w.tail(context.Background(), path, events)
	if got := drainClaudeEvents(events, 60*time.Millisecond); len(got) != 0 {
		t.Fatalf("partial line emitted %d events, want 0: %q", len(got), got)
	}
	w.mu.Lock()
	fs := w.files[path]
	w.mu.Unlock()
	fs.mu.Lock()
	off := fs.offset
	fs.mu.Unlock()
	if off != 0 {
		t.Fatalf("offset advanced to %d on partial line, want 0", off)
	}

	// Append remainder + newline, tail again.
	f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if e != nil {
		t.Fatalf("open append: %v", e)
	}
	_, _ = f.WriteString(full[split:] + "\n")
	f.Close()

	w.tail(context.Background(), path, events)
	got := drainClaudeEvents(events, 100*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("after completion got %d events, want exactly 1: %q", len(got), got)
	}
	if string(got[0].Line) != full {
		t.Fatalf("emitted line corrupt:\n got: %q\nwant: %q", got[0].Line, full)
	}
}

// TestReplay_PartialFlush verifies replay also refuses to emit a trailing
// partial line.
func TestReplay_PartialFlush(t *testing.T) {
	full := `{"type":"system","uuid":"u1","sessionId":"s","timestamp":"2026-05-06T10:00:00.000Z","message":"complete"}`
	partial := `{"type":"system","uuid":"u2","sessionId":"s","timestamp":"2026-05-06T10:00:01.000Z","message":"incompl`
	content := full + "\n" + partial // second line has no newline
	root := makeTempRoot(t, "-Users-dev-project", "session.jsonl", content)
	path := filepath.Join(root, "-Users-dev-project", "session.jsonl")

	events := make(chan connectors.RawEvent, 8)
	w := newWatcher(root)
	fw, _ := newFsnotify(t)
	defer fw.Close()
	w.ensureWatched(fw, path)

	w.replay(context.Background(), path, events)
	got := drainClaudeEvents(events, 100*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("replay got %d events, want exactly 1 (partial line withheld): %q", len(got), got)
	}
	if string(got[0].Line) != full {
		t.Fatalf("replay line corrupt: got %q want %q", got[0].Line, full)
	}
	// Offset must point at the start of the still-incomplete second line.
	w.mu.Lock()
	fs := w.files[path]
	w.mu.Unlock()
	fs.mu.Lock()
	off := fs.offset
	fs.mu.Unlock()
	if off != int64(len(full)+1) {
		t.Fatalf("offset = %d, want %d (start of incomplete line)", off, len(full)+1)
	}
}

// ── TestBackstop_RecoversAppendedKnownFile ────────────────────────────────────

// TestBackstop_RecoversAppendedKnownFile verifies the backstop re-tails an
// ALREADY-KNOWN file. Scenario: a file is tracked and tailed to EOF, then more
// lines are appended but the fsnotify Write is dropped/coalesced (we simply
// never call tail for that event). The backstop scan must recover the appended
// bytes.
func TestBackstop_RecoversAppendedKnownFile(t *testing.T) {
	root := makeTempRoot(t, "-Users-dev-project", "session.jsonl", sampleLine)
	path := filepath.Join(root, "-Users-dev-project", "session.jsonl")

	events := make(chan connectors.RawEvent, 16)
	w := newWatcher(root)
	fw, _ := newFsnotify(t)
	defer fw.Close()

	// Prime: file is known and replayed to EOF (mirrors Watch warm-up).
	w.ensureWatched(fw, path)
	w.replay(context.Background(), path, events)
	_ = drainClaudeEvents(events, 80*time.Millisecond) // discard warm-up line

	// Append new lines, but DO NOT deliver the fsnotify event (dropped).
	f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if e != nil {
		t.Fatalf("open append: %v", e)
	}
	_, _ = f.WriteString(sampleLine)
	_, _ = f.WriteString(sampleLine)
	f.Close()

	// Backstop must recover the appended lines from the known file.
	w.backstopScan(context.Background(), fw, events)
	got := drainClaudeEvents(events, 150*time.Millisecond)
	if len(got) != 2 {
		t.Fatalf("backstop recovered %d appended lines, want 2: %q", len(got), got)
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
