package codex

import (
	"bufio"
	"context"
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
	// backstopInterval is the period between full re-scans of the session root.
	// This guards against missed fsnotify events (risk R9).
	backstopInterval = 30 * time.Second

	// rolloutGlob matches Codex session files inside a date directory.
	rolloutGlob = "rollout-*.jsonl"
)

// tailState tracks read position for a single JSONL file.
type tailState struct {
	mu     sync.Mutex
	offset int64 // byte offset of the next unread byte
}

// watcher manages file tailing and directory watching for a Codex session root.
type watcher struct {
	root   string
	events chan<- connectors.RawEvent

	mu      sync.Mutex
	tails   map[string]*tailState // path → tail state
	watched map[string]struct{}   // directories currently watched by fsnotify
}

// discoverFiles returns all rollout-*.jsonl files under root, walking
// date-partitioned subdirectories (YYYY/MM/DD).
func discoverFiles(root string) ([]string, error) {
	var files []string
	// Walk up to 4 levels deep: root/YYYY/MM/DD/rollout-*.jsonl
	yearEntries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, ye := range yearEntries {
		if !ye.IsDir() {
			continue
		}
		yearPath := filepath.Join(root, ye.Name())
		monthEntries, err := os.ReadDir(yearPath)
		if err != nil {
			continue
		}
		for _, me := range monthEntries {
			if !me.IsDir() {
				continue
			}
			monthPath := filepath.Join(yearPath, me.Name())
			dayEntries, err := os.ReadDir(monthPath)
			if err != nil {
				continue
			}
			for _, de := range dayEntries {
				if !de.IsDir() {
					continue
				}
				dayPath := filepath.Join(monthPath, de.Name())
				matches, err := filepath.Glob(filepath.Join(dayPath, rolloutGlob))
				if err != nil {
					continue
				}
				files = append(files, matches...)
			}
		}
	}
	return files, nil
}

// emitNew reads any bytes appended to path since the last read and sends
// RawEvent values for each complete line.
func (w *watcher) emitNew(path string) {
	w.mu.Lock()
	ts, ok := w.tails[path]
	if !ok {
		ts = &tailState{}
		w.tails[path] = ts
	}
	w.mu.Unlock()

	ts.mu.Lock()
	defer ts.mu.Unlock()

	f, err := os.Open(path) // #nosec G304 — path is derived from a trusted root
	if err != nil {
		log.Printf("codex: cannot open %s: %v", path, err)
		return
	}
	defer f.Close()

	if ts.offset > 0 {
		if _, err := f.Seek(ts.offset, io.SeekStart); err != nil {
			log.Printf("codex: seek error in %s: %v", path, err)
			return
		}
	}

	scanner := bufio.NewScanner(f)
	// Codex turn_context payloads include full instructions blocks that can
	// run into hundreds of KB. The default 64KB scanner buffer silently
	// returns ErrTooLong on the first oversized line — see W4/W5 bug fix.
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		lineBytes := scanner.Bytes()
		if len(lineBytes) == 0 {
			continue
		}
		// Advance offset: line bytes + newline
		ts.offset += int64(len(lineBytes)) + 1

		// Copy bytes — scanner reuses its buffer.
		lineCopy := make([]byte, len(lineBytes))
		copy(lineCopy, lineBytes)

		w.events <- connectors.RawEvent{
			Path: path,
			Line: lineCopy,
			Ts:   time.Now().UnixMilli(),
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("codex: scan error in %s: %v (line larger than 16MB will be skipped)", path, err)
		// Recover: trust the on-disk size so we don't permanently re-read
		// a partial buffered position.
		if info, statErr := f.Stat(); statErr == nil {
			ts.offset = info.Size()
		}
	}
}

// warmupOffsets back-fills any existing JSONL content on Watch start.
// Earlier this function only recorded the end-of-file offset (no emission)
// to avoid re-emitting lines already on disk; that broke first-run ingestion
// because nothing ever reads historical lines. Now we replay each existing
// file from byte 0 — store.InsertMessage deduplicates by Message ID PK so
// repeated runs are safe.
func (w *watcher) warmupOffsets(files []string) {
	for _, path := range files {
		w.emitNew(path)
	}
}

// advanceOffset reads through path to its current end, updating the tail
// state offset without emitting any events to the channel.
func (w *watcher) advanceOffset(path string) {
	w.mu.Lock()
	ts, ok := w.tails[path]
	if !ok {
		ts = &tailState{}
		w.tails[path] = ts
	}
	w.mu.Unlock()

	ts.mu.Lock()
	defer ts.mu.Unlock()

	f, err := os.Open(path) // #nosec G304
	if err != nil {
		return
	}
	defer f.Close()

	// Seek to end to record current file size as offset.
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return
	}
	ts.offset = size
}

// attachDir adds a directory to the fsnotify watcher (idempotent).
func (w *watcher) attachDir(fw *fsnotify.Watcher, dir string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.watched[dir]; ok {
		return
	}
	if err := fw.Add(dir); err != nil {
		log.Printf("codex: cannot watch dir %s: %v", dir, err)
		return
	}
	w.watched[dir] = struct{}{}
}

// attachAllDirs discovers all existing date directories and attaches them.
func (w *watcher) attachAllDirs(fw *fsnotify.Watcher) {
	// Watch the root itself so we catch new YYYY dirs.
	w.attachDir(fw, w.root)

	yearEntries, err := os.ReadDir(w.root)
	if err != nil {
		return
	}
	for _, ye := range yearEntries {
		if !ye.IsDir() {
			continue
		}
		yearPath := filepath.Join(w.root, ye.Name())
		w.attachDir(fw, yearPath)

		monthEntries, err := os.ReadDir(yearPath)
		if err != nil {
			continue
		}
		for _, me := range monthEntries {
			if !me.IsDir() {
				continue
			}
			monthPath := filepath.Join(yearPath, me.Name())
			w.attachDir(fw, monthPath)

			dayEntries, err := os.ReadDir(monthPath)
			if err != nil {
				continue
			}
			for _, de := range dayEntries {
				if !de.IsDir() {
					continue
				}
				w.attachDir(fw, filepath.Join(monthPath, de.Name()))
			}
		}
	}
}

// backstopScan re-scans the root for files that may have been missed by
// fsnotify (risk R9 mitigation). New files are emitted; existing file
// offsets are advanced if new lines appeared.
func (w *watcher) backstopScan(fw *fsnotify.Watcher) {
	files, err := discoverFiles(w.root)
	if err != nil {
		log.Printf("codex: backstop scan error: %v", err)
		return
	}
	// Attach any new directories that appeared since last scan.
	w.attachAllDirs(fw)
	// Emit new lines from all known files.
	for _, path := range files {
		w.emitNew(path)
	}
}

// watch is the internal implementation of Connector.Watch.
func (w *watcher) watch(ctx context.Context) error {
	// Ensure root exists (or create it lazily on first mkdir).
	if err := os.MkdirAll(w.root, 0o750); err != nil {
		log.Printf("codex: cannot create root %s: %v", w.root, err)
	}

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer fw.Close()

	// Warm-up: advance offsets for all existing files without emitting events.
	// This prevents re-emitting lines already on disk before Watch started
	// (deduplication requirement, risk R5).
	existingFiles, err := discoverFiles(w.root)
	if err != nil {
		log.Printf("codex: discover error during warmup: %v", err)
	}
	w.warmupOffsets(existingFiles)

	// Attach all directories currently under root.
	w.attachAllDirs(fw)

	backstop := time.NewTicker(backstopInterval)
	defer backstop.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case event, ok := <-fw.Events:
			if !ok {
				return nil
			}
			w.handleFSEvent(fw, event)

		case err, ok := <-fw.Errors:
			if !ok {
				return nil
			}
			log.Printf("codex: fsnotify error: %v", err)

		case <-backstop.C:
			w.backstopScan(fw)
		}
	}
}

// handleFSEvent processes a single fsnotify event.
func (w *watcher) handleFSEvent(fw *fsnotify.Watcher, event fsnotify.Event) {
	path := event.Name

	switch {
	case event.Has(fsnotify.Create):
		// A new file or directory was created.
		fi, err := os.Stat(path)
		if err != nil {
			return
		}
		if fi.IsDir() {
			// New date directory — attach it so we catch files created inside.
			w.attachDir(fw, path)
			// Scan it immediately for any files already placed there.
			matches, _ := filepath.Glob(filepath.Join(path, rolloutGlob))
			for _, f := range matches {
				w.emitNew(f)
			}
		} else if isRolloutFile(path) {
			w.emitNew(path)
		}

	case event.Has(fsnotify.Write):
		if isRolloutFile(path) {
			w.emitNew(path)
		}
	}
}

// isRolloutFile returns true if path matches the rollout-*.jsonl pattern.
func isRolloutFile(path string) bool {
	base := filepath.Base(path)
	matched, _ := filepath.Match(rolloutGlob, base)
	return matched && strings.HasSuffix(path, ".jsonl")
}
