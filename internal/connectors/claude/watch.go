package claude

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/klyne-ai/klyne/internal/connectors"
)

const (
	// backstopInterval is how often we scan for new files not caught by fsnotify.
	backstopInterval = 30 * time.Second
	// maxFileAge bounds how stale a session file may be and still be watched.
	// Claude never re-opens a session file once it has gone quiet for more than
	// a day or two, so files untouched for longer are inert: watching them only
	// burns file descriptors and kqueue wakeups (battery). Anything older than
	// this cutoff is skipped during discovery and the backstop scan.
	maxFileAge = 7 * 24 * time.Hour
	// maxLineSize is the largest single JSONL line we will tolerate. Claude
	// tool results (file reads, command outputs) routinely exceed 1MB; the
	// bufio.Scanner default of 64KB silently drops anything larger and the
	// loop exits without an error. 16MB is a comfortable ceiling — files
	// bigger than this are pathological and worth logging.
	maxLineSize = 16 * 1024 * 1024
)

// fileState tracks read progress for a watched JSONL file.
type fileState struct {
	mu     sync.Mutex
	offset int64
}

// Watcher implements the file-watching half of the claude Connector.
// It is an internal type; the public API is Connector.Watch.
type Watcher struct {
	root  string
	mu    sync.Mutex
	files map[string]*fileState // absolute path → state
	// backstopTrigger allows tests to fast-forward the backstop scan
	// without sleeping 30 seconds. Send a value to trigger an immediate scan.
	backstopTrigger chan struct{}
}

// newWatcher creates a new Watcher rooted at root.
func newWatcher(root string) *Watcher {
	return &Watcher{
		root:            root,
		files:           make(map[string]*fileState),
		backstopTrigger: make(chan struct{}, 1),
	}
}

// Watch is the main entry point. It:
//  1. Discovers all existing JSONL files, emitting their content as warm-up.
//  2. Attaches an fsnotify watcher for subsequent writes.
//  3. Runs a 30-second backstop to catch files the OS-level watcher misses.
//
// It blocks until ctx is cancelled, then returns nil.
func (w *Watcher) Watch(ctx context.Context, events chan<- connectors.RawEvent) error {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fw.Close()

	// Discover and warm-up existing files.
	paths, err := discover(w.root)
	if err != nil {
		log.Printf("claude watch: discover error: %v", err)
	}
	for _, p := range paths {
		w.ensureWatched(fw, p)
		w.replay(ctx, p, events)
	}

	// Watch root directories for newly created files.
	_ = watchDirs(fw, w.root)

	go w.fsnotifyLoop(ctx, fw, events)
	w.backstopLoop(ctx, fw, events)
	return nil
}

// fsnotifyLoop processes fsnotify events and tails newly-written lines.
func (w *Watcher) fsnotifyLoop(ctx context.Context, fw *fsnotify.Watcher, events chan<- connectors.RawEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-fw.Events:
			if !ok {
				return
			}
			if !strings.HasSuffix(ev.Name, ".jsonl") {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create) != 0 {
				w.ensureWatched(fw, ev.Name)
				w.tail(ctx, ev.Name, events)
			}
		case err, ok := <-fw.Errors:
			if !ok {
				return
			}
			log.Printf("claude watch: fsnotify error: %v", err)
		}
	}
}

// backstopLoop periodically scans for new files that fsnotify may have missed.
func (w *Watcher) backstopLoop(ctx context.Context, fw *fsnotify.Watcher, events chan<- connectors.RawEvent) {
	ticker := time.NewTicker(backstopInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.backstopScan(ctx, fw, events)
		case <-w.backstopTrigger:
			w.backstopScan(ctx, fw, events)
		}
	}
}

// backstopScan is the 30-second safety net for fsnotify gaps. Brand-new files
// are started from byte 0 via replay; ALREADY-KNOWN files are re-tailed so that
// a dropped or coalesced fsnotify Write on an active session is still recovered
// (tail is idempotent — it only emits bytes past fs.offset). This mirrors the
// codex backstop, which re-scans every file and self-heals.
func (w *Watcher) backstopScan(ctx context.Context, fw *fsnotify.Watcher, events chan<- connectors.RawEvent) {
	paths, err := discover(w.root)
	if err != nil {
		log.Printf("claude watch: backstop discover error: %v", err)
		return
	}
	for _, p := range paths {
		w.mu.Lock()
		_, known := w.files[p]
		w.mu.Unlock()
		if known {
			// Recover any appended bytes a missed fsnotify Write left behind.
			w.tail(ctx, p, events)
		} else {
			w.ensureWatched(fw, p)
			w.replay(ctx, p, events)
		}
	}
}

// ensureWatched registers a file with fsnotify and initialises its fileState if
// not already tracked.
func (w *Watcher) ensureWatched(fw *fsnotify.Watcher, path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.files[path]; !ok {
		w.files[path] = &fileState{}
		dir := filepath.Dir(path)
		_ = fw.Add(dir)
		_ = fw.Add(path)
	}
}

// replay reads all lines in a file from byte 0 and emits them as RawEvents.
// It is used on warm-up so the upstream store can ingest historical lines.
// After replay completes, the fileState.offset is set to the end of the file,
// so subsequent tail calls do NOT re-emit the same lines.
func (w *Watcher) replay(ctx context.Context, path string, events chan<- connectors.RawEvent) {
	w.mu.Lock()
	fs, ok := w.files[path]
	w.mu.Unlock()
	if !ok {
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		log.Printf("claude watch: replay open %s: %v", path, err)
		return
	}
	defer f.Close()

	// Seek to the recorded offset (0 on warm-up). Using the offset rather than
	// hard-coding byte 0 keeps replay idempotent if it is ever re-invoked.
	if fs.offset > 0 {
		if _, err := f.Seek(fs.offset, 0); err != nil {
			log.Printf("claude watch: replay seek %s: %v", path, err)
			return
		}
	}

	w.emitCompleteLines(ctx, f, fs, path, events)
}

// tail reads lines appended to a file since the last known offset and emits
// them as RawEvents.
func (w *Watcher) tail(ctx context.Context, path string, events chan<- connectors.RawEvent) {
	w.mu.Lock()
	fs, ok := w.files[path]
	w.mu.Unlock()
	if !ok {
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		log.Printf("claude watch: tail open %s: %v", path, err)
		return
	}
	defer f.Close()

	if _, err := f.Seek(fs.offset, 0); err != nil {
		log.Printf("claude watch: tail seek %s: %v", path, err)
		return
	}

	w.emitCompleteLines(ctx, f, fs, path, events)
}

// emitCompleteLines reads from f (already positioned at fs.offset) and emits a
// RawEvent for every COMPLETE, newline-terminated line, advancing fs.offset by
// the exact bytes consumed. A trailing partial line (one flushed without its
// '\n' yet) is left unconsumed: fs.offset stays pointing at its start so it is
// re-read in full once the newline arrives. This avoids splitting a mid-flush
// JSONL line into two corrupt fragments (the partial-flush bug). f.mu (fs.mu)
// must be held by the caller.
func (w *Watcher) emitCompleteLines(ctx context.Context, f *os.File, fs *fileState, path string, events chan<- connectors.RawEvent) {
	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		lineBytes, err := readLineLimited(reader, maxLineSize)
		if len(lineBytes) > 0 && lineBytes[len(lineBytes)-1] == '\n' {
			// Complete line: advance by exact bytes consumed (incl. newline and
			// any CRLF '\r'), then emit the trimmed content.
			fs.offset += int64(len(lineBytes))

			trimmed := trimLineEnding(lineBytes)
			if len(trimmed) > 0 {
				line := make([]byte, len(trimmed))
				copy(line, trimmed)
				select {
				case events <- connectors.RawEvent{Path: path, Line: line, Ts: time.Now().UnixMilli()}:
				case <-ctx.Done():
					return
				}
			}
			if ctx.Err() != nil {
				return
			}
			continue
		}

		// No trailing newline: partial line, EOF, or over-long line. Stop
		// without emitting or advancing so the bytes are re-read next time.
		if err == errLineTooLong {
			log.Printf("claude watch: line larger than %d bytes in %s — skipping", maxLineSize, path)
			if info, statErr := f.Stat(); statErr == nil {
				fs.offset = info.Size()
			}
			return
		}
		if err != nil && err != io.EOF {
			log.Printf("claude watch: read error in %s: %v", path, err)
		}
		return
	}
}

// errLineTooLong signals that a single line exceeded maxLineSize before a
// newline was seen.
var errLineTooLong = errors.New("claude: line exceeds max size")

// readLineLimited reads a single line (up to and including '\n') from r,
// bounded by limit bytes. If a newline is found the returned slice ends in
// '\n'. If EOF is hit first, the partial bytes are returned with io.EOF. If
// limit is exceeded before a newline, it returns errLineTooLong.
func readLineLimited(r *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			return buf, err
		}
		buf = append(buf, b)
		if b == '\n' {
			return buf, nil
		}
		if len(buf) > limit {
			return buf, errLineTooLong
		}
	}
}

// trimLineEnding strips a trailing '\n' and an optional preceding '\r' (CRLF).
func trimLineEnding(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	if len(b) > 0 && b[len(b)-1] == '\r' {
		b = b[:len(b)-1]
	}
	return b
}

// ── directory watching helpers ────────────────────────────────────────────────

// watchDirs registers all subdirectories under root with fw.
func watchDirs(fw *fsnotify.Watcher, root string) error {
	_ = fw.Add(root)
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		subdir := filepath.Join(root, e.Name())
		// On macOS fsnotify uses kqueue, which opens a file descriptor for
		// EVERY file inside a watched directory. Watching project directories
		// that hold only stale sessions therefore burns thousands of fds and
		// kqueue wakeups for no benefit (those files never change again). Skip
		// any directory without a recent file; the always-watched root plus the
		// backstop scan still pick up brand-new project directories.
		if dirHasRecentJSONL(subdir) {
			_ = fw.Add(subdir)
		}
	}
	return nil
}

// dirHasRecentJSONL reports whether dir contains at least one *.jsonl file
// modified within maxFileAge. Used to decide whether a directory is worth
// watching at all (see watchDirs).
func dirHasRecentJSONL(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) <= maxFileAge {
			return true
		}
	}
	return false
}

// discover returns all *.jsonl files under root.
func discover(root string) ([]string, error) {
	var paths []string
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		subdir := filepath.Join(root, entry.Name())
		subEntries, err := os.ReadDir(subdir)
		if err != nil {
			log.Printf("claude discover: read dir %s: %v", subdir, err)
			continue
		}
		for _, se := range subEntries {
			if se.IsDir() || !strings.HasSuffix(se.Name(), ".jsonl") {
				continue
			}
			// Skip files untouched for longer than maxFileAge: they are inert
			// (Claude never re-opens a quiet session) and watching them only
			// wastes file descriptors and kqueue wakeups.
			info, err := se.Info()
			if err != nil {
				log.Printf("claude discover: stat %s: %v", se.Name(), err)
				continue
			}
			if time.Since(info.ModTime()) > maxFileAge {
				continue
			}
			paths = append(paths, filepath.Join(subdir, se.Name()))
		}
	}
	return paths, nil
}
