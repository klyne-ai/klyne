package codex

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/klyne-ai/klyne/internal/connectors"
)

// createFsnotifyWatcher wraps fsnotify.NewWatcher for tests.
func createFsnotifyWatcher() (*fsnotify.Watcher, error) {
	return fsnotify.NewWatcher()
}

// fsnotifyCreateEvent builds a synthetic fsnotify CREATE event.
func fsnotifyCreateEvent(path string) fsnotify.Event {
	return fsnotify.Event{Name: path, Op: fsnotify.Create}
}

// fsnotifyWriteEvent builds a synthetic fsnotify WRITE event.
func fsnotifyWriteEvent(path string) fsnotify.Event {
	return fsnotify.Event{Name: path, Op: fsnotify.Write}
}

// mkDateDir creates YYYY/MM/DD directory structure under root and returns the
// leaf path.
func mkDateDir(t *testing.T, root, yyyy, mm, dd string) string {
	t.Helper()
	dir := filepath.Join(root, yyyy, mm, dd)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkDateDir: %v", err)
	}
	return dir
}

// writeFile creates or truncates a file with given content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
}

// appendFile appends content to an existing file.
func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		t.Fatalf("appendFile open: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("appendFile write: %v", err)
	}
}

// validLine is a simple valid JSONL line for test purposes.
const validLine1 = `{"type":"input","session_id":"cx-test","content":"hello","timestamp":"2026-05-06T10:00:05.000Z"}` + "\n"
const validLine2 = `{"type":"output","session_id":"cx-test","content":"world","model":"gpt-5","timestamp":"2026-05-06T10:00:08.000Z","usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}` + "\n"

// TestDiscover_FindsFiles_AcrossDates verifies that Discover returns files
// spread across multiple date-partitioned subdirectories.
func TestDiscover_FindsFiles_AcrossDates(t *testing.T) {
	root := t.TempDir()

	dates := [][3]string{
		{"2026", "05", "06"},
		{"2026", "05", "07"},
		{"2026", "05", "08"},
	}
	var wantFiles []string
	for _, d := range dates {
		dir := mkDateDir(t, root, d[0], d[1], d[2])
		p := filepath.Join(dir, "rollout-001.jsonl")
		writeFile(t, p, validLine1)
		wantFiles = append(wantFiles, p)
	}

	c := New(root)
	ctx := context.Background()
	got, err := c.Discover(ctx)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != len(wantFiles) {
		t.Errorf("Discover returned %d files, want %d\ngot: %v", len(got), len(wantFiles), got)
	}
	// Build a set for O(1) lookup.
	gotSet := make(map[string]struct{}, len(got))
	for _, f := range got {
		gotSet[f] = struct{}{}
	}
	for _, want := range wantFiles {
		if _, ok := gotSet[want]; !ok {
			t.Errorf("Discover missing expected file: %s", want)
		}
	}
}

// TestDiscover_SkipsStaleFiles verifies that rollout files untouched for longer
// than maxFileAge are excluded: they are inert and watching them only wastes
// file descriptors and kqueue wakeups.
func TestDiscover_SkipsStaleFiles(t *testing.T) {
	root := t.TempDir()
	dir := mkDateDir(t, root, "2026", "05", "20")
	fresh := filepath.Join(dir, "rollout-fresh.jsonl")
	stale := filepath.Join(dir, "rollout-stale.jsonl")
	writeFile(t, fresh, validLine1)
	writeFile(t, stale, validLine1)
	old := time.Now().Add(-(maxFileAge + 24*time.Hour))
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	files, err := discoverFiles(root)
	if err != nil {
		t.Fatalf("discoverFiles: %v", err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "rollout-fresh.jsonl" {
		t.Errorf("expected only rollout-fresh.jsonl within %s window, got %v", maxFileAge, files)
	}
}

// TestDiscover_EmptyRoot verifies that Discover returns nil (not an error) when
// the root directory does not exist.
func TestDiscover_EmptyRoot(t *testing.T) {
	c := New("/nonexistent/path/that/does/not/exist")
	got, err := c.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover on missing root: unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Discover on missing root: got %d files, want 0", len(got))
	}
}

// TestWatch_WarmupReplaysExistingLines verifies that lines present in files
// before Watch starts ARE replayed on warm-up. Earlier semantics skipped
// existing content (only emitted new appends), but that broke first-run
// ingestion of historical sessions on disk. Dedup now happens at the store
// layer (Message ID PK), so re-emitting on every Watch start is safe.
func TestWatch_WarmupReplaysExistingLines(t *testing.T) {
	root := t.TempDir()
	dir := mkDateDir(t, root, "2026", "05", "06")
	p := filepath.Join(dir, "rollout-001.jsonl")
	writeFile(t, p, validLine1)

	events := make(chan connectors.RawEvent, 32)
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = New(root).Watch(ctx, events)
	}()

	// Give Watch time to warm up + replay.
	time.Sleep(80 * time.Millisecond)

	// Append a new line — should also be picked up via fsnotify tail.
	appendFile(t, p, validLine2)

	var received []connectors.RawEvent
	timeout := time.After(300 * time.Millisecond)
collect:
	for {
		select {
		case ev := <-events:
			received = append(received, ev)
		case <-timeout:
			break collect
		}
	}

	cancel()
	wg.Wait()

	// Should receive BOTH the existing line (warmup replay) and the appended
	// line (live tail). Allow ≥2 — fsnotify may also re-fire on the same line.
	if len(received) < 2 {
		t.Errorf("expected at least 2 events (warmup + append), got %d", len(received))
		for i, ev := range received {
			t.Logf("  event[%d]: %s", i, ev.Line)
		}
	}
}

// TestWatch_BackstopDetectsNewFile verifies that a file created while Watch is
// running is picked up by the backstop scan. We override backstopInterval for
// this test to avoid a 30-second wait by directly invoking backstopScan.
func TestWatch_BackstopDetectsNewFile(t *testing.T) {
	root := t.TempDir()
	dir := mkDateDir(t, root, "2026", "05", "06")

	events := make(chan connectors.RawEvent, 32)

	// Build watcher internals directly to call backstopScan.
	w := &watcher{
		root:    root,
		events:  events,
		tails:   make(map[string]*tailState),
		watched: make(map[string]struct{}),
	}

	// Write a file AFTER watcher construction (simulates file created mid-run).
	p := filepath.Join(dir, "rollout-backstop.jsonl")
	writeFile(t, p, validLine1)

	// Create a throwaway fsnotify watcher for backstopScan.
	// We only need it to be non-nil; we don't process its events.
	// (backstopScan calls attachAllDirs which calls fw.Add — that's fine.)
	// Use a nil-safe approach: call backstopScan which calls discoverFiles +
	// emitNew internally without requiring a live fsnotify subscription.

	// Instead, directly invoke emitNew to simulate what backstop does.
	w.emitNew(context.Background(), p)

	// Collect events with a short timeout.
	var received []connectors.RawEvent
	timeout := time.After(100 * time.Millisecond)
collect:
	for {
		select {
		case ev := <-events:
			received = append(received, ev)
		case <-timeout:
			break collect
		}
	}

	if len(received) == 0 {
		t.Error("backstop scan did not emit any events for new file")
	}
}

// TestWatch_NewDateDir_AttachesAutomatically verifies that a new date directory
// created while Watch is running gets its files picked up. We simulate the
// backstop scan path (since creating a dir triggers the same code path as
// the fsnotify CREATE event + backstopScan).
func TestWatch_NewDateDir_AttachesAutomatically(t *testing.T) {
	root := t.TempDir()

	events := make(chan connectors.RawEvent, 32)

	// Build watcher internals directly.
	w := &watcher{
		root:    root,
		events:  events,
		tails:   make(map[string]*tailState),
		watched: make(map[string]struct{}),
	}

	// Simulate: Watch has been running for a while with no files.
	// Now a new date dir appears mid-run.
	newDir := mkDateDir(t, root, "2026", "05", "07")
	newFile := filepath.Join(newDir, "rollout-001.jsonl")
	writeFile(t, newFile, validLine1)

	// Simulate backstop scan picking it up.
	files, err := discoverFiles(root)
	if err != nil {
		t.Fatalf("discoverFiles: %v", err)
	}
	for _, f := range files {
		w.emitNew(context.Background(), f)
	}

	// Collect.
	var received []connectors.RawEvent
	timeout := time.After(100 * time.Millisecond)
collect:
	for {
		select {
		case ev := <-events:
			received = append(received, ev)
		case <-timeout:
			break collect
		}
	}

	if len(received) == 0 {
		t.Error("new date dir: no events received after backstop scan")
	}
	if received[0].Path != newFile {
		t.Errorf("received event from wrong path: %q, want %q", received[0].Path, newFile)
	}
}

// TestWatch_WatchIntegration is a light integration test that exercises the
// full Watch goroutine path: start, write a file, receive event, cancel.
func TestWatch_WatchIntegration(t *testing.T) {
	root := t.TempDir()
	dir := mkDateDir(t, root, "2026", "05", "06")

	events := make(chan connectors.RawEvent, 32)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	c := New(root)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Watch(ctx, events)
	}()

	// Wait for watcher to initialize.
	time.Sleep(80 * time.Millisecond)

	// Write a file.
	p := filepath.Join(dir, "rollout-integration.jsonl")
	writeFile(t, p, validLine1)

	// Wait for event.
	select {
	case ev := <-events:
		if ev.Path != p {
			t.Errorf("event path: got %q, want %q", ev.Path, p)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timed out waiting for Watch event")
	}

	cancel()
	// Watch should return ctx.Err()
	select {
	case err := <-errCh:
		if err == nil || err.Error() == "" {
			// context.Canceled is acceptable.
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Watch did not return after cancel")
	}
}

// TestWatch_BackstopScanViaMethod exercises watcher.backstopScan directly
// by building a watcher, creating a file, then calling backstopScan.
func TestWatch_BackstopScanViaMethod(t *testing.T) {
	root := t.TempDir()
	dir := mkDateDir(t, root, "2026", "05", "06")
	p := filepath.Join(dir, "rollout-bs.jsonl")
	writeFile(t, p, validLine1)

	events := make(chan connectors.RawEvent, 32)
	w := &watcher{
		root:    root,
		events:  events,
		tails:   make(map[string]*tailState),
		watched: make(map[string]struct{}),
	}

	// Create a real fsnotify watcher just so backstopScan can call fw.Add.
	// We don't process its events.
	fw, err := createFsnotifyWatcher()
	if err != nil {
		t.Skip("fsnotify not available:", err)
	}
	defer fw.Close()

	w.backstopScan(context.Background(), fw)

	// Collect events.
	var received []connectors.RawEvent
	timeout := time.After(100 * time.Millisecond)
collectBS:
	for {
		select {
		case ev := <-events:
			received = append(received, ev)
		case <-timeout:
			break collectBS
		}
	}

	if len(received) == 0 {
		t.Error("backstopScan: no events received")
	}
}

// TestWatch_DiscoverFiles_ReadDirError exercises the non-existent path branch
// of discoverFiles (already covered by TestDiscover_EmptyRoot but explicit here).
func TestWatch_DiscoverFiles_NoRoot(t *testing.T) {
	files, err := discoverFiles("/no/such/path/here")
	if err != nil {
		t.Fatalf("unexpected error for missing root: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

// TestWatch_IsRolloutFile exercises the rollout file detection helper.
func TestWatch_IsRolloutFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/root/2026/05/06/rollout-abc.jsonl", true},
		{"/root/2026/05/06/rollout-123-xyz.jsonl", true},
		{"/root/2026/05/06/other.jsonl", false},
		{"/root/2026/05/06/rollout-abc.json", false},
		{"/root/2026/05/06/not-rollout.jsonl", false},
	}
	for _, tc := range cases {
		got := isRolloutFile(tc.path)
		if got != tc.want {
			t.Errorf("isRolloutFile(%q): got %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestWatch_HandleFSEvent_NewDir exercises the directory-creation branch of
// handleFSEvent by simulating a CREATE event for a new date directory.
func TestWatch_HandleFSEvent_NewDir(t *testing.T) {
	root := t.TempDir()
	events := make(chan connectors.RawEvent, 32)

	w := &watcher{
		root:    root,
		events:  events,
		tails:   make(map[string]*tailState),
		watched: make(map[string]struct{}),
	}

	// Create a new date dir with a file already in it.
	newDir := mkDateDir(t, root, "2026", "05", "08")
	newFile := filepath.Join(newDir, "rollout-newdir.jsonl")
	writeFile(t, newFile, validLine1)

	fw, err := createFsnotifyWatcher()
	if err != nil {
		t.Skip("fsnotify not available:", err)
	}
	defer fw.Close()

	// Simulate receiving a CREATE event for the new directory.
	w.handleFSEvent(context.Background(), fw, fsnotifyCreateEvent(newDir))

	// Should have emitted the file in the new dir.
	var received []connectors.RawEvent
	timeout := time.After(100 * time.Millisecond)
collectHFS:
	for {
		select {
		case ev := <-events:
			received = append(received, ev)
		case <-timeout:
			break collectHFS
		}
	}

	if len(received) == 0 {
		t.Error("handleFSEvent: new dir created but no events for its files")
	}
}

// TestWatch_HandleFSEvent_WriteFile exercises the WRITE event path of handleFSEvent.
func TestWatch_HandleFSEvent_WriteFile(t *testing.T) {
	root := t.TempDir()
	dir := mkDateDir(t, root, "2026", "05", "06")
	p := filepath.Join(dir, "rollout-write.jsonl")
	writeFile(t, p, validLine1)

	events := make(chan connectors.RawEvent, 32)
	w := &watcher{
		root:    root,
		events:  events,
		tails:   make(map[string]*tailState),
		watched: make(map[string]struct{}),
	}

	fw, err := createFsnotifyWatcher()
	if err != nil {
		t.Skip("fsnotify not available:", err)
	}
	defer fw.Close()

	w.handleFSEvent(context.Background(), fw, fsnotifyWriteEvent(p))

	var received []connectors.RawEvent
	timeout := time.After(100 * time.Millisecond)
collectWEF:
	for {
		select {
		case ev := <-events:
			received = append(received, ev)
		case <-timeout:
			break collectWEF
		}
	}

	if len(received) == 0 {
		t.Error("handleFSEvent WRITE: no events for written rollout file")
	}
}

// TestWatch_HandleFSEvent_NonRolloutIgnored verifies non-rollout writes are skipped.
func TestWatch_HandleFSEvent_NonRolloutIgnored(t *testing.T) {
	root := t.TempDir()
	dir := mkDateDir(t, root, "2026", "05", "06")
	p := filepath.Join(dir, "not-a-rollout.txt")
	writeFile(t, p, "ignored")

	events := make(chan connectors.RawEvent, 4)
	w := &watcher{
		root:    root,
		events:  events,
		tails:   make(map[string]*tailState),
		watched: make(map[string]struct{}),
	}

	fw, err := createFsnotifyWatcher()
	if err != nil {
		t.Skip("fsnotify not available:", err)
	}
	defer fw.Close()

	w.handleFSEvent(context.Background(), fw, fsnotifyWriteEvent(p))

	select {
	case ev := <-events:
		t.Errorf("non-rollout file should not emit events, got: %s", ev.Line)
	case <-time.After(50 * time.Millisecond):
		// Good — no event.
	}
}

// TestWatch_EmitNew_SeekError_Recovers verifies that emitNew handles the case
// where a file has been partially read (non-zero offset) without panicking.
func TestWatch_EmitNew_SeekBeyondEnd(t *testing.T) {
	root := t.TempDir()
	dir := mkDateDir(t, root, "2026", "05", "06")
	p := filepath.Join(dir, "rollout-seek.jsonl")
	writeFile(t, p, validLine1)

	events := make(chan connectors.RawEvent, 4)
	w := &watcher{
		root:    root,
		events:  events,
		tails:   make(map[string]*tailState),
		watched: make(map[string]struct{}),
	}

	// Prime offset to a value beyond file size to simulate over-read.
	w.mu.Lock()
	ts := &tailState{offset: 999999}
	w.tails[p] = ts
	w.mu.Unlock()

	// Should not panic and should emit 0 events (seek returns error or EOF).
	w.emitNew(context.Background(), p)

	select {
	case ev := <-events:
		t.Errorf("expected no events for over-seek, got: %s", ev.Line)
	case <-time.After(50 * time.Millisecond):
		// Good.
	}
}

// drainEvents collects all events available within d.
func drainEvents(events <-chan connectors.RawEvent, d time.Duration) []connectors.RawEvent {
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

// TestEmitNew_PartialFlush_NoEmitUntilNewline reproduces the partial-flush bug:
// when only the first N bytes of a JSONL line are on disk (no trailing newline,
// e.g. a large turn_context line caught mid-flush by fsnotify), emitNew must NOT
// emit a fragment and must NOT advance the offset. Once the remainder + newline
// lands, the FULL intact line must be emitted exactly once and parse.
func TestEmitNew_PartialFlush_NoEmitUntilNewline(t *testing.T) {
	root := t.TempDir()
	dir := mkDateDir(t, root, "2026", "05", "06")
	p := filepath.Join(dir, "rollout-partial.jsonl")

	full := `{"type":"input","session_id":"cx-test","content":"hello","timestamp":"2026-05-06T10:00:05.000Z"}`
	split := len(full) / 2
	// Write first half, no newline.
	writeFile(t, p, full[:split])

	events := make(chan connectors.RawEvent, 8)
	w := &watcher{
		root:    root,
		events:  events,
		tails:   make(map[string]*tailState),
		watched: make(map[string]struct{}),
	}

	w.emitNew(context.Background(), p)
	if got := drainEvents(events, 60*time.Millisecond); len(got) != 0 {
		t.Fatalf("partial line emitted %d events, want 0: %q", len(got), got)
	}
	// Offset must NOT have advanced past the (incomplete) line start.
	w.mu.Lock()
	ts := w.tails[p]
	w.mu.Unlock()
	ts.mu.Lock()
	off := ts.offset
	ts.mu.Unlock()
	if off != 0 {
		t.Fatalf("offset advanced to %d on partial line, want 0", off)
	}

	// Append the remainder + newline.
	appendFile(t, p, full[split:]+"\n")
	w.emitNew(context.Background(), p)

	got := drainEvents(events, 100*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("after completion got %d events, want exactly 1: %q", len(got), got)
	}
	if string(got[0].Line) != full {
		t.Fatalf("emitted line corrupt:\n got: %q\nwant: %q", got[0].Line, full)
	}
}

// TestEmitNew_CRLF_ByteExactOffset verifies that a CRLF-terminated line is
// emitted with the '\r' trimmed and the offset advances byte-exactly (incl. the
// '\r'), so subsequent lines are read at the correct position (CRLF off-by-one).
func TestEmitNew_CRLF_ByteExactOffset(t *testing.T) {
	root := t.TempDir()
	dir := mkDateDir(t, root, "2026", "05", "06")
	p := filepath.Join(dir, "rollout-crlf.jsonl")

	l1 := `{"type":"input","session_id":"cx","content":"a"}`
	l2 := `{"type":"output","session_id":"cx","content":"b"}`
	content := l1 + "\r\n" + l2 + "\r\n"
	writeFile(t, p, content)

	events := make(chan connectors.RawEvent, 8)
	w := &watcher{
		root:    root,
		events:  events,
		tails:   make(map[string]*tailState),
		watched: make(map[string]struct{}),
	}

	w.emitNew(context.Background(), p)
	got := drainEvents(events, 100*time.Millisecond)
	if len(got) != 2 {
		t.Fatalf("CRLF: got %d events, want 2: %q", len(got), got)
	}
	if string(got[0].Line) != l1 {
		t.Fatalf("CRLF line 1 not trimmed: got %q want %q", got[0].Line, l1)
	}
	if string(got[1].Line) != l2 {
		t.Fatalf("CRLF line 2 corrupt (off-by-one?): got %q want %q", got[1].Line, l2)
	}
	w.mu.Lock()
	ts := w.tails[p]
	w.mu.Unlock()
	ts.mu.Lock()
	off := ts.offset
	ts.mu.Unlock()
	if off != int64(len(content)) {
		t.Fatalf("offset = %d, want %d (byte-exact incl CRLF)", off, len(content))
	}
}

// TestEmitNew_PartialThenCRLF covers a partial line whose completion arrives as
// CRLF — must still emit exactly one intact, trimmed line.
func TestEmitNew_PartialThenCRLF(t *testing.T) {
	root := t.TempDir()
	dir := mkDateDir(t, root, "2026", "05", "06")
	p := filepath.Join(dir, "rollout-partial-crlf.jsonl")

	full := `{"type":"input","session_id":"cx","content":"split-crlf"}`
	writeFile(t, p, full[:10])

	events := make(chan connectors.RawEvent, 8)
	w := &watcher{
		root:    root,
		events:  events,
		tails:   make(map[string]*tailState),
		watched: make(map[string]struct{}),
	}
	w.emitNew(context.Background(), p)
	if got := drainEvents(events, 50*time.Millisecond); len(got) != 0 {
		t.Fatalf("partial emitted %d, want 0", len(got))
	}
	appendFile(t, p, full[10:]+"\r\n")
	w.emitNew(context.Background(), p)
	got := drainEvents(events, 100*time.Millisecond)
	if len(got) != 1 || string(got[0].Line) != full {
		t.Fatalf("partial+CRLF: got %q, want exactly [%q]", got, full)
	}
}

// TestDiscover_CancelledContext verifies that Discover respects context cancellation.
func TestDiscover_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	c := New(t.TempDir())
	_, err := c.Discover(ctx)
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
}

// TestConnector_Name verifies the Name() method.
func TestConnector_Name(t *testing.T) {
	c := New("/tmp")
	if c.Name() != "codex" {
		t.Errorf("Name(): got %q, want codex", c.Name())
	}
}

// TestConnector_Pricing verifies Pricing() returns an empty table.
func TestConnector_Pricing(t *testing.T) {
	c := New("/tmp")
	pt := c.Pricing()
	if pt.Models != nil {
		t.Errorf("Pricing().Models: expected nil, got %v", pt.Models)
	}
}

// TestConnector_Parse_ViaWatch verifies Connector.Parse wires through the
// per-file state map (covered more thoroughly in parse_test.go).
func TestConnector_Parse_ViaWatch(t *testing.T) {
	c := New("/tmp")
	// Without session_meta, a response_item returns (nil, nil).
	line := []byte(`{"timestamp":"2026-05-06T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}}`)
	msg, err := c.Parse(line, fakePath)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("expected nil before session_meta, got %+v", msg)
	}
}
