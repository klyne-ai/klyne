package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/connectors/claude"
	"github.com/klyne-ai/klyne/internal/connectors/codex"
	"github.com/klyne-ai/klyne/internal/store"
)

// claudeFixtureLine is a minimal valid Claude Code JSONL line. Mirrors the
// shape parsed by claude.Parse — type=user with an inline string content
// is the simplest path that produces a non-nil Message.
const claudeFixtureLine = `{"type":"user","uuid":"u-demo-1","sessionId":"s-demo-1","timestamp":"2026-05-06T10:00:00.000Z","cwd":"/tmp/demo","parentUuid":"null","message":{"role":"user","content":"hello demo"}}`

// codexFixtureLines is the minimum sequence Codex.Parse needs to emit a
// message: session_meta (sets sessionID + cwd), then a response_item
// message line.
const codexFixtureLines = `{"timestamp":"2026-05-06T10:00:00.000Z","type":"session_meta","payload":{"id":"s-demo-codex-1","cwd":"/tmp/demo-codex"}}
{"timestamp":"2026-05-06T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi codex"}]}}`

// newSeedTestDB creates a fresh on-disk store for a test, isolated under
// t.TempDir(). Returned DB is closed by t.Cleanup.
func newSeedTestDB(t *testing.T) *store.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "seed-test.db")
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// writeFixture writes content to <dir>/<sub>/<name> and returns the
// resulting absolute path. The intermediate "claude" or "codex" segment
// is what the seeder uses to pick a connector.
func writeFixture(t *testing.T, dir, sub, name, content string) string {
	t.Helper()
	subDir := filepath.Join(dir, sub)
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", subDir, err)
	}
	full := filepath.Join(subDir, name)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	return full
}

func TestSeedFromFixtures_Cases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                string
		setup               func(t *testing.T, dir string)
		wantSessions        int
		wantMessagesAtLeast int
	}{
		{
			name: "claude_only_inline_fixture",
			setup: func(t *testing.T, dir string) {
				writeFixture(t, dir, "claude", "session-001.jsonl", claudeFixtureLine+"\n")
			},
			wantSessions:        1,
			wantMessagesAtLeast: 1,
		},
		{
			name: "codex_only_inline_fixture",
			setup: func(t *testing.T, dir string) {
				writeFixture(t, dir, "codex", "rollout-test.jsonl", codexFixtureLines+"\n")
			},
			wantSessions:        1,
			wantMessagesAtLeast: 1,
		},
		{
			name: "claude_with_malformed_lines_is_partial",
			setup: func(t *testing.T, dir string) {
				// First line is JSON garbage; second is the valid line.
				// The seeder must skip the bad line, log it, and still
				// ingest the good one.
				writeFixture(t, dir, "claude", "mixed.jsonl",
					"{not valid json}\n"+claudeFixtureLine+"\n")
			},
			wantSessions:        1,
			wantMessagesAtLeast: 1,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := newSeedTestDB(t)
			fixtures := t.TempDir()
			tc.setup(t, fixtures)

			results, err := seedFromFixtures(context.Background(), db, fixtures, nil)
			if err != nil {
				t.Fatalf("seedFromFixtures: %v", err)
			}
			if len(results) == 0 {
				t.Fatalf("expected at least one fixture result, got 0")
			}

			sessions, err := store.ListSessions(context.Background(), db, store.SessionFilter{})
			if err != nil {
				t.Fatalf("ListSessions: %v", err)
			}
			if len(sessions) != tc.wantSessions {
				t.Fatalf("sessions in DB = %d, want %d", len(sessions), tc.wantSessions)
			}

			var totalInserted int
			for _, r := range results {
				totalInserted += r.Inserted
			}
			if totalInserted < tc.wantMessagesAtLeast {
				t.Fatalf("inserted messages = %d, want at least %d",
					totalInserted, tc.wantMessagesAtLeast)
			}
		})
	}
}

// TestSeedFromFixtures_EmptyDirIsNonFatal confirms that demo mode boots
// cleanly when the fixtures directory is empty — first-run users may try
// `--demo` before any fixtures exist.
func TestSeedFromFixtures_EmptyDirIsNonFatal(t *testing.T) {
	t.Parallel()

	db := newSeedTestDB(t)
	emptyDir := t.TempDir() // no files inside

	results, err := seedFromFixtures(context.Background(), db, emptyDir, nil)
	if err != nil {
		t.Fatalf("seedFromFixtures(empty): expected nil error, got %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("seedFromFixtures(empty): expected 0 results, got %d", len(results))
	}

	sessions, err := store.ListSessions(context.Background(), db, store.SessionFilter{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions in empty-fixture DB, got %d", len(sessions))
	}
}

// TestSeedFromFixtures_MissingDirIsNonFatal mirrors the empty-dir case but
// for a directory that does not exist at all. The seeder must return
// without error so app.Start can proceed.
func TestSeedFromFixtures_MissingDirIsNonFatal(t *testing.T) {
	t.Parallel()

	db := newSeedTestDB(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	results, err := seedFromFixtures(context.Background(), db, missing, nil)
	if err != nil {
		t.Fatalf("seedFromFixtures(missing): expected nil error, got %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("seedFromFixtures(missing): expected 0 results, got %d", len(results))
	}
}

// TestDemoDBPath_NotProductionPath asserts that the demo DB path the start
// command uses (os.TempDir()/klyne-demo.db) is distinct from the
// production DB path (config.DBPath()). This is a string-level guard
// against a future refactor that accidentally points demo mode at the
// real DB.
func TestDemoDBPath_NotProductionPath(t *testing.T) {
	t.Parallel()

	// The exact production path depends on $HOME — temporarily stub it
	// so the test is hermetic on every developer's machine.
	tmpHome := t.TempDir()
	orig := config.HomeDir
	config.HomeDir = func() (string, error) { return tmpHome, nil }
	t.Cleanup(func() { config.HomeDir = orig })

	prodPath := config.DBPath()
	demoPath := filepath.Join(os.TempDir(), "klyne-demo.db")

	if prodPath == demoPath {
		t.Fatalf("demo DB path %q must NOT equal production DB path %q",
			demoPath, prodPath)
	}
	if !strings.Contains(demoPath, "demo") {
		t.Fatalf("demo DB path %q should mention 'demo' for human clarity", demoPath)
	}
	// Double check the production path is the canonical location, not
	// some accidentally-shared tempdir.
	if !strings.Contains(prodPath, ".klyne") {
		t.Fatalf("production DB path %q lost its '.klyne' segment — refactor risk",
			prodPath)
	}
}

// TestPickFixtureConnector covers the path-segment routing so a future
// rearrangement of fixtures (e.g. nested under a date dir) doesn't
// silently fall through and skip the entire fixture set.
func TestPickFixtureConnector(t *testing.T) {
	t.Parallel()

	claudeC := claude.New("")
	codexC := codex.New("")

	cases := []struct {
		path string
		want string // "" means nil
	}{
		{"/abs/examples/sample-jsonl/claude/session-001.jsonl", "claude"},
		{"/abs/examples/sample-jsonl/codex/rollout.jsonl", "codex"},
		{"/abs/somewhere/else/random.jsonl", ""},
	}
	for _, tc := range cases {
		got := pickFixtureConnector(tc.path, claudeC, codexC)
		if tc.want == "" {
			if got != nil {
				t.Errorf("pickFixtureConnector(%q) = %v, want nil", tc.path, got)
			}
			continue
		}
		if got == nil || got.Name() != tc.want {
			t.Errorf("pickFixtureConnector(%q) = %v, want %s", tc.path, got, tc.want)
		}
	}
}
