package codex

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
	// backstopInterval is the period between full re-scans of the session root.
	// This guards against missed fsnotify events (risk R9).
	backstopInterval = 30 * time.Second

	// rolloutGlob matches Codex session files inside a date directory.
	rolloutGlob = "rollout-*.jsonl"

	// maxFileAge bounds how stale a rollout file may be and still be watched.
	// Codex never re-opens a session file once it has gone quiet, so files
	// untouched for longer are inert: watching them only burns file descriptors
	// and kqueue wakeups (battery). Older files are skipped during discovery and
	// the backstop scan.
	maxFileAge = 7 * 24 * time.Hour
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
				for _, m := range matches {
					// Skip files untouched for longer than maxFileAge: they are
					// inert (Codex never re-opens a quiet session) and watching
					// them only wastes file descriptors and kqueue wakeups.
					info, err := os.Stat(m)
					if err != nil {
						log.Printf("codex discover: stat %s: %v", m, err)
						continue
					}
					if time.Since(info.ModTime()) > maxFileAge {
						continue
					}
					files = append(files, m)
				}
			}
		}
	}
	return files, nil
}

// emitNew reads any bytes appended to path since the last read and sends
// RawEvent values for each complete line. The ctx is observed on every
// channel send so that a shutdown does not deadlock the goroutine when
// the events channel buffer fills up or the consumer has gone away.
func (w *watcher) emitNew(ctx context.Context, path string) {
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

	// Codex turn_context payloads include full instructions blocks that can
	// run into hundreds of KB. We use a bufio.Reader (not bufio.Scanner) so we
	// can distinguish a complete, newline-terminated line from a partial line
	// still being flushed: ReadBytes('\n') returns the bytes read so far plus
	// io.EOF when no newline has arrived yet. We must NOT emit or advance the
	// offset for such a partial fragment — otherwise a mid-flush fsnotify Write
	// splits one line into two corrupt halves and drifts the offset by a phantom
	// +1 (W4/W5/CRLF bug). Leaving the partial bytes unconsumed means the line
	// is re-read in full on the next event, once its trailing '\n' lands.
	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		lineBytes, err := readLineLimited(reader, maxLineSize)
		if len(lineBytes) > 0 && lineBytes[len(lineBytes)-1] == '\n' {
			// Complete line. Advance offset by the exact bytes consumed
			// (including the trailing newline, and any preceding '\r' for CRLF),
			// then emit the trimmed content.
			ts.offset += int64(len(lineBytes))

			trimmed := trimLineEnding(lineBytes)
			if len(trimmed) > 0 {
				lineCopy := make([]byte, len(trimmed))
				copy(lineCopy, trimmed)
				select {
				case w.events <- connectors.RawEvent{
					Path: path,
					Line: lineCopy,
					Ts:   time.Now().UnixMilli(),
				}:
				case <-ctx.Done():
					return
				}
			}
			if ctx.Err() != nil {
				return
			}
			continue
		}

		// No trailing newline: either a partial line still being written, EOF,
		// or an over-long line. In all cases we stop without emitting and
		// without advancing the offset, so the bytes are re-read next time.
		if err == errLineTooLong {
			// Pathological line exceeding maxLineSize. Skip it by advancing to
			// end-of-file so we don't spin forever re-reading it; a complete
			// line this large is not something we can buffer.
			log.Printf("codex: line larger than %d bytes in %s — skipping", maxLineSize, path)
			if info, statErr := f.Stat(); statErr == nil {
				ts.offset = info.Size()
			}
			return
		}
		if err != nil && err != io.EOF {
			log.Printf("codex: read error in %s: %v", path, err)
		}
		return
	}
}

// maxLineSize is the largest single JSONL line we will buffer. Codex
// turn_context payloads can run into hundreds of KB; the historical bufio
// scanner ceiling was 16MB, preserved here.
const maxLineSize = 16 * 1024 * 1024

// errLineTooLong signals that a single line exceeded maxLineSize before a
// newline was seen.
var errLineTooLong = errors.New("codex: line exceeds max size")

// readLineLimited reads a single line (up to and including '\n') from r,
// bounded by limit bytes. It returns the bytes read. If a newline is found the
// returned slice ends in '\n'. If EOF is hit first, the partial bytes are
// returned with io.EOF. If limit is exceeded before a newline, it returns
// errLineTooLong.
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

// warmupOffsets back-fills any existing JSONL content on Watch start.
// Earlier this function only recorded the end-of-file offset (no emission)
// to avoid re-emitting lines already on disk; that broke first-run ingestion
// because nothing ever reads historical lines. Now we replay each existing
// file from byte 0 — store.InsertMessage deduplicates by Message ID PK so
// repeated runs are safe.
func (w *watcher) warmupOffsets(ctx context.Context, files []string) {
	for _, path := range files {
		if ctx.Err() != nil {
			return
		}
		w.emitNew(ctx, path)
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
func (w *watcher) backstopScan(ctx context.Context, fw *fsnotify.Watcher) {
	files, err := discoverFiles(w.root)
	if err != nil {
		log.Printf("codex: backstop scan error: %v", err)
		return
	}
	// Attach any new directories that appeared since last scan.
	w.attachAllDirs(fw)
	// Emit new lines from all known files.
	for _, path := range files {
		if ctx.Err() != nil {
			return
		}
		w.emitNew(ctx, path)
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
	w.warmupOffsets(ctx, existingFiles)

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
			w.handleFSEvent(ctx, fw, event)

		case err, ok := <-fw.Errors:
			if !ok {
				return nil
			}
			log.Printf("codex: fsnotify error: %v", err)

		case <-backstop.C:
			w.backstopScan(ctx, fw)
		}
	}
}

// handleFSEvent processes a single fsnotify event.
func (w *watcher) handleFSEvent(ctx context.Context, fw *fsnotify.Watcher, event fsnotify.Event) {
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
				if ctx.Err() != nil {
					return
				}
				w.emitNew(ctx, f)
			}
		} else if isRolloutFile(path) {
			w.emitNew(ctx, path)
		}

	case event.Has(fsnotify.Write):
		if isRolloutFile(path) {
			w.emitNew(ctx, path)
		}
	}
}

// isRolloutFile returns true if path matches the rollout-*.jsonl pattern.
func isRolloutFile(path string) bool {
	base := filepath.Base(path)
	matched, _ := filepath.Match(rolloutGlob, base)
	return matched && strings.HasSuffix(path, ".jsonl")
}
