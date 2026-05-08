// Package app — demo-mode fixture seeder.
//
// This file owns the offline ingestion pipeline used by `agentdeck start
// --demo`. The seeder walks a fixture directory of *.jsonl files, runs each
// line through the matching connector parser (Claude or Codex, inferred
// from the file path), and writes the resulting Messages into the supplied
// *store.DB exactly the way the live writer goroutine would.
//
// # Why a separate seeder
//
// The live ingestion pipeline (see app.runWriter / app.processRawEvent) is
// driven by fsnotify-tailed RawEvent values produced by the connectors'
// Watch goroutines. Demo mode is offline by definition — there are no
// fsnotify watchers and no live channels. Replicating the writer's
// invariants here in a one-shot, single-goroutine form keeps demo mode
// from depending on the watcher startup sequence.
//
// # Path → CLI inference
//
// We pick the connector by looking for "/claude/" or "/codex/" path
// segments in the JSONL file's absolute path. The fixtures under
// examples/sample-jsonl/ already follow that layout. Files that don't
// match either pattern are skipped with a warning rather than guessed at.
package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/connectors/claude"
	"github.com/klyne-ai/klyne/internal/connectors/codex"
	"github.com/klyne-ai/klyne/internal/store"
)

// demoMaxLineBytes mirrors the codex watcher's scanner buffer ceiling
// (16 MiB). Some real Codex turn_context payloads exceed the stdlib
// default 64 KiB, so we raise the limit here too — otherwise long lines
// in fixtures would silently be skipped by bufio.Scanner.
const demoMaxLineBytes = 16 * 1024 * 1024

// fixtureSeedResult summarises a single fixture file's ingestion outcome.
// Returned from seedFile so callers (and tests) can assert on counts
// without scraping log lines.
type fixtureSeedResult struct {
	Path     string // absolute path to the fixture file
	Inserted int    // messages successfully stored
	Skipped  int    // lines the parser rejected as malformed/unknown
	Sessions int    // distinct session IDs touched
}

// seedFromFixtures walks dir for *.jsonl files and ingests every one into
// db. It is non-fatal when dir is missing or empty — demo mode is meant to
// remain bootable on a fresh checkout that has not yet generated fixtures.
//
// Errors that prevent ingestion of a single file are logged and the walk
// continues; only an unrecoverable filesystem error (e.g. dir is a file)
// terminates the seed.
func seedFromFixtures(ctx context.Context, db *store.DB, dir string, logger *slog.Logger) ([]fixtureSeedResult, error) {
	if logger == nil {
		logger = slog.Default()
	}

	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			logger.Warn("demo: fixtures directory not found; starting with empty DB",
				slog.String("dir", dir))
			return nil, nil
		}
		return nil, fmt.Errorf("demo: stat fixtures dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("demo: fixtures path %s is not a directory", dir)
	}

	files, err := discoverFixtureFiles(dir)
	if err != nil {
		return nil, fmt.Errorf("demo: discover fixtures in %s: %w", dir, err)
	}
	if len(files) == 0 {
		logger.Warn("demo: no .jsonl fixtures found; starting with empty DB",
			slog.String("dir", dir))
		return nil, nil
	}

	// Build connectors used as stateless parsers (no Watch goroutines).
	// Codex.Parse is stateful per file (session_meta + turn_context lines
	// accumulate metadata) so we share one Codex instance across the seed.
	claudeC := claude.New("")
	codexC := codex.New("")

	results := make([]fixtureSeedResult, 0, len(files))
	for _, path := range files {
		// Honour cancellation between files so a Stop on the parent ctx
		// terminates the seed promptly.
		if ctx.Err() != nil {
			return results, ctx.Err()
		}

		conn := pickFixtureConnector(path, claudeC, codexC)
		if conn == nil {
			logger.Warn("demo: cannot infer CLI from path; skipping fixture",
				slog.String("path", path))
			continue
		}

		res, ferr := seedFile(ctx, db, conn, path, logger)
		if ferr != nil {
			// Per the brief: never abort the whole seed because of one
			// bad file. Log and move on.
			logger.Warn("demo: fixture ingest aborted",
				slog.String("path", path),
				slog.Any("error", ferr))
			results = append(results, res)
			continue
		}
		logger.Info("demo: ingested fixture",
			slog.String("path", path),
			slog.String("connector", conn.Name()),
			slog.Int("messages", res.Inserted),
			slog.Int("skipped_lines", res.Skipped),
			slog.Int("sessions", res.Sessions))
		results = append(results, res)
	}
	return results, nil
}

// discoverFixtureFiles returns every *.jsonl file under dir, sorted by
// filepath.WalkDir's deterministic lexical order so the same fixtures
// produce the same DB state on every run (helpful for tests).
func discoverFixtureFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// pickFixtureConnector chooses the parser for path by looking for a CLI
// segment ("/claude/" or "/codex/") in the path. Returns nil if neither
// segment matches — the caller logs and skips.
//
// We deliberately avoid sniffing the file content: ambiguous content
// (e.g. a malformed line at the top) would otherwise misroute the entire
// file. The fixture layout is the contract.
func pickFixtureConnector(path string, claudeC, codexC connectors.Connector) connectors.Connector {
	// Normalise separators so the test passes on Windows too.
	p := filepath.ToSlash(path)
	switch {
	case strings.Contains(p, "/claude/"):
		return claudeC
	case strings.Contains(p, "/codex/"):
		return codexC
	}
	return nil
}

// seedFile reads path line-by-line, parses each line via conn, and writes
// the resulting Message into db. Malformed lines are counted but never
// abort the file — partial ingestion is preferable to silent demo mode
// failure on a slightly-broken fixture.
func seedFile(ctx context.Context, db *store.DB, conn connectors.Connector, path string, logger *slog.Logger) (fixtureSeedResult, error) {
	res := fixtureSeedResult{Path: path}

	f, err := os.Open(path) // #nosec G304 — path is from a trusted fixtures dir walked above.
	if err != nil {
		return res, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), demoMaxLineBytes)

	seenSessions := make(map[string]struct{})

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Copy bytes — scanner reuses its buffer between Scan calls and
		// the connector parsers retain references for the duration of
		// the parse step.
		buf := make([]byte, len(line))
		copy(buf, line)

		msg, perr := conn.Parse(buf, path)
		if perr != nil {
			res.Skipped++
			logger.Debug("demo: skipping malformed line",
				slog.String("path", path),
				slog.Any("error", perr))
			continue
		}
		if msg == nil {
			// Stateful lines (Codex session_meta / turn_context) and
			// intentionally-skipped types (reasoning, developer) yield
			// (nil, nil) — not an error, just nothing to insert.
			continue
		}

		if err := upsertSessionForMessage(ctx, db, msg, path); err != nil {
			res.Skipped++
			logger.Warn("demo: upsert session failed",
				slog.String("session_id", msg.SessionID),
				slog.Any("error", err))
			continue
		}
		if err := store.InsertMessage(ctx, db, msg); err != nil {
			// Duplicates (same UUID re-seeded) are expected when the
			// fixture contains repeats; demote to debug.
			res.Skipped++
			logger.Debug("demo: insert message failed",
				slog.String("message_id", msg.ID),
				slog.Any("error", err))
			continue
		}
		res.Inserted++
		seenSessions[msg.SessionID] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return res, fmt.Errorf("scan %s: %w", path, err)
	}
	res.Sessions = len(seenSessions)
	return res, nil
}

// upsertSessionForMessage ensures the parent session row exists before
// the message is inserted. Mirrors processRawEvent so the demo path and
// the live path produce comparable session metadata.
func upsertSessionForMessage(ctx context.Context, db *store.DB, msg *connectors.Message, rawPath string) error {
	if msg.SessionID == "" {
		return errors.New("message has no session_id")
	}
	sess := &connectors.Session{
		ID:          msg.SessionID,
		CLI:         msg.CLI,
		ProjectPath: msg.ProjectPath,
		StartedAt:   msg.Ts,
		LastMsgAt:   msg.Ts,
		Model:       msg.Model,
		Status:      connectors.SessionStatusIdle,
		RawPath:     rawPath,
	}
	return store.UpsertSession(ctx, db, sess)
}
