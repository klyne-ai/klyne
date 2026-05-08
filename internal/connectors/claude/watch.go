package claude

import (
	"bufio"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mohitpatell/agentdeck/internal/connectors"
)

const (
	// backstopInterval is how often we scan for new files not caught by fsnotify.
	backstopInterval = 30 * time.Second
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

// backstopScan discovers any files not yet in w.files and starts watching them.
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
		if !known {
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

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxLineSize)
	for scanner.Scan() {
		if ctx.Err() != nil {
			break
		}
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		line := make([]byte, len(raw))
		copy(line, raw)
		select {
		case events <- connectors.RawEvent{Path: path, Line: line, Ts: time.Now().UnixMilli()}:
		case <-ctx.Done():
			return
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("claude watch: replay scan %s: %v (line larger than %d bytes will be skipped)", path, err, maxLineSize)
	}

	// Advance offset to actual end of file (Stat) — relying on Seek(0,1)
	// after a scanner error returns the buffered position, not the on-disk
	// size, which would cause us to re-read truncated bytes on next tail.
	if info, err := f.Stat(); err == nil {
		fs.offset = info.Size()
	} else if pos, err2 := f.Seek(0, 1); err2 == nil {
		fs.offset = pos
	}
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

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxLineSize)
	for scanner.Scan() {
		if ctx.Err() != nil {
			break
		}
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		line := make([]byte, len(raw))
		copy(line, raw)
		select {
		case events <- connectors.RawEvent{Path: path, Line: line, Ts: time.Now().UnixMilli()}:
		case <-ctx.Done():
			return
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("claude watch: tail scan %s: %v (line larger than %d bytes will be skipped)", path, err, maxLineSize)
	}

	// Use Stat() rather than Seek(0,1) so a scanner error doesn't leave the
	// offset short of the real on-disk size and cause permanent re-tailing.
	if info, err := f.Stat(); err == nil {
		fs.offset = info.Size()
	} else if pos, err2 := f.Seek(0, 1); err2 == nil {
		fs.offset = pos
	}
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
		if e.IsDir() {
			subdir := filepath.Join(root, e.Name())
			_ = fw.Add(subdir)
		}
	}
	return nil
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
			if !se.IsDir() && strings.HasSuffix(se.Name(), ".jsonl") {
				paths = append(paths, filepath.Join(subdir, se.Name()))
			}
		}
	}
	return paths, nil
}
