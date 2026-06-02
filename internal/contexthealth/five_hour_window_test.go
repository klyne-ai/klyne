package contexthealth

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// writeClaudeLine appends one valid Claude Code assistant JSONL line
// to f at the given timestamp + token counts. Used by the 5-hour
// aggregator tests to seed synthetic transcripts without rolling our
// own connector.
func writeClaudeLine(t *testing.T, f *os.File, sessionID string, ts time.Time, inputTokens, cacheRead int64) {
	t.Helper()
	line := fmt.Sprintf(
		`{"type":"assistant","uuid":"u-%d","sessionId":%q,"timestamp":%q,"cwd":"/tmp/proj","message":{"role":"assistant","content":[{"type":"text","text":"reply"}],"id":"msg-%d","model":"claude-sonnet-4.5","usage":{"input_tokens":%d,"output_tokens":1,"cache_read_input_tokens":%d,"cache_creation_input_tokens":0}}}`+"\n",
		ts.UnixMilli(), sessionID, ts.UTC().Format(time.RFC3339Nano), ts.UnixMilli(), inputTokens, cacheRead,
	)
	if _, err := f.WriteString(line); err != nil {
		t.Fatalf("writeClaudeLine: %v", err)
	}
}

// claudeProjectFile creates a project subdir under root (Claude
// Code's expected layout) and returns an open writeable handle plus
// the file's absolute path.
func claudeProjectFile(t *testing.T, root, project, sessionID string) (*os.File, string) {
	t.Helper()
	dir := filepath.Join(root, project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return f, path
}

// codexSessionFile creates a sessions subdir under root and returns an
// open writeable handle plus the file's absolute path. Codex rollouts
// live under ~/.codex/sessions; the layout under root doesn't matter to
// the walker (it recurses), so a flat file is fine.
func codexSessionFile(t *testing.T, root, sessionID string) (*os.File, string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(root, sessionID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return f, path
}

// writeCodexMeta writes the session_meta line that establishes sessionID
// and cwd for the rollout. Required before any token_count line.
func writeCodexMeta(t *testing.T, f *os.File, sessionID string, ts time.Time) {
	t.Helper()
	line := fmt.Sprintf(
		`{"timestamp":%q,"type":"session_meta","payload":{"id":%q,"cwd":"/tmp/proj"}}`+"\n",
		ts.UTC().Format(time.RFC3339Nano), sessionID,
	)
	if _, err := f.WriteString(line); err != nil {
		t.Fatalf("writeCodexMeta: %v", err)
	}
}

// writeCodexTokenCount appends one Codex event_msg/token_count line. Codex
// carries token usage on this RoleSystem message (NOT on assistant turns).
// cumIn/cumCachedRead are session-cumulative totals; the parser emits the
// per-line delta against the previous token_count, so a single line emits
// the full cumIn/cumCachedRead.
func writeCodexTokenCount(t *testing.T, f *os.File, ts time.Time, cumIn, cumCachedRead, cumOut int64) {
	t.Helper()
	line := fmt.Sprintf(
		`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d},"last_token_usage":{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d}}}}`+"\n",
		ts.UTC().Format(time.RFC3339Nano), cumIn, cumCachedRead, cumOut, cumIn, cumCachedRead, cumOut,
	)
	if _, err := f.WriteString(line); err != nil {
		t.Fatalf("writeCodexTokenCount: %v", err)
	}
}

// TestAggregateFiveHour_CodexContributes is the regression test for the
// HIGH finding that Codex usage was silently excluded from the 5-hour
// budget: the walker gated on Role==RoleAssistant, but Codex emits its
// tokens on a RoleSystem token_count message, so Codex sessions
// contributed 0. With the role gate removed (TokensIn>0 only), a Codex
// transcript must yield non-zero EffectiveInput and roll into the total.
func TestAggregateFiveHour_CodexContributes(t *testing.T) {
	dir := t.TempDir()
	claudeRoot := filepath.Join(dir, "claude", "projects")
	codexRoot := filepath.Join(dir, "codex", "sessions")

	now := time.Now()
	withinWindow := now.Add(-1 * time.Hour)

	// A Codex session with two token_count snapshots (cumulative).
	// EffectiveInput per emitted delta = TokensIn - CachedReadTokens.
	//   tc1: cumIn=10K cumCachedRead=0   → delta in=10K, cached=0  → eff 10K
	//   tc2: cumIn=25K cumCachedRead=4K  → delta in=15K, cached=4K → eff 11K
	// Total effective = 10K + 11K = 21K.
	fc, _ := codexSessionFile(t, codexRoot, "codexA")
	writeCodexMeta(t, fc, "codexA", withinWindow)
	writeCodexTokenCount(t, fc, withinWindow, 10_000, 0, 2_000)
	writeCodexTokenCount(t, fc, withinWindow, 25_000, 4_000, 5_000)
	fc.Close()

	roots := FiveHourRoots{Claude: claudeRoot, Codex: codexRoot}
	summary := AggregateFiveHour(roots, now.UnixMilli(), 100_000)

	const wantCodexEffective int64 = 21_000
	if summary.TotalEffective != wantCodexEffective {
		t.Fatalf("TotalEffective=%d, want %d (codex must contribute; sessions: %+v)",
			summary.TotalEffective, wantCodexEffective, summary.Sessions)
	}
	d := summary.DominantSession()
	if d == nil {
		t.Fatalf("DominantSession=nil, want the codex session")
	}
	if d.SessionID != "codexA" {
		t.Fatalf("DominantSession.SessionID=%q, want codexA", d.SessionID)
	}
	if d.CLI != connectors.CLICodex {
		t.Fatalf("DominantSession.CLI=%q, want codex", d.CLI)
	}
	if d.EffectiveInput != wantCodexEffective {
		t.Fatalf("DominantSession.EffectiveInput=%d, want %d", d.EffectiveInput, wantCodexEffective)
	}
}

func TestAggregateFiveHour_SumsRecentSessions(t *testing.T) {
	dir := t.TempDir()
	claudeRoot := filepath.Join(dir, "claude", "projects")

	now := time.Now()
	withinWindow := now.Add(-1 * time.Hour)
	tooOld := now.Add(-7 * time.Hour)

	// Session A — three recent assistant turns with rising input.
	fa, _ := claudeProjectFile(t, claudeRoot, "-tmp-projA", "sessA")
	writeClaudeLine(t, fa, "sessA", withinWindow, 5_000, 1_000)
	writeClaudeLine(t, fa, "sessA", withinWindow, 9_000, 1_000)
	writeClaudeLine(t, fa, "sessA", withinWindow, 15_000, 1_000)
	fa.Close()

	// Session B — only old turns, should be ignored.
	fb, _ := claudeProjectFile(t, claudeRoot, "-tmp-projB", "sessB")
	writeClaudeLine(t, fb, "sessB", tooOld, 50_000, 0)
	fb.Close()

	roots := FiveHourRoots{Claude: claudeRoot}
	summary := AggregateFiveHour(roots, now.UnixMilli(), 100_000)

	// Effective input per turn = TokensIn - CachedReadTokens.
	// TokensIn = input_tokens + cache_read + cache_creation, so the
	// formula reduces to (input_tokens + cache_creation) per turn.
	// With cache_creation=0 in the fixture this is simply the
	// input_tokens column: 5K + 9K + 15K = 29K.
	const wantTotal int64 = 29_000
	if summary.TotalEffective != wantTotal {
		t.Fatalf("TotalEffective=%d, want %d (sessions: %+v)", summary.TotalEffective, wantTotal, summary.Sessions)
	}
	if absf(summary.PctUsed-29.0) > 1e-6 {
		t.Fatalf("PctUsed=%v, want 29.0", summary.PctUsed)
	}
	if summary.Threshold() != FiveHourThresholdNone {
		t.Fatalf("Threshold=%d, want None at 29%%", summary.Threshold())
	}
	if d := summary.DominantSession(); d == nil || d.SessionID != "sessA" {
		t.Fatalf("DominantSession=%v, want sessA", d)
	}
}

func TestAggregateFiveHour_ThresholdsCrossed(t *testing.T) {
	dir := t.TempDir()
	claudeRoot := filepath.Join(dir, "claude", "projects")

	now := time.Now()
	recent := now.Add(-30 * time.Minute)

	f, _ := claudeProjectFile(t, claudeRoot, "-x-y", "sX")
	// One turn with 60K effective input.
	writeClaudeLine(t, f, "sX", recent, 60_000, 0)
	f.Close()

	roots := FiveHourRoots{Claude: claudeRoot}

	// Cap = 100K → 60% used → warn threshold.
	summary := AggregateFiveHour(roots, now.UnixMilli(), 100_000)
	if got := summary.Threshold(); got != FiveHourThresholdWarn {
		t.Fatalf("Threshold=%d at 60%%, want Warn", got)
	}

	// Cap = 75K → 80% used → urgent threshold.
	summary = AggregateFiveHour(roots, now.UnixMilli(), 75_000)
	if got := summary.Threshold(); got != FiveHourThresholdUrgent {
		t.Fatalf("Threshold=%d at 80%%, want Urgent", got)
	}
}

func TestAggregateFiveHour_NoCapNoThreshold(t *testing.T) {
	dir := t.TempDir()
	claudeRoot := filepath.Join(dir, "claude", "projects")

	now := time.Now()
	recent := now.Add(-30 * time.Minute)

	f, _ := claudeProjectFile(t, claudeRoot, "-x-y", "sX")
	writeClaudeLine(t, f, "sX", recent, 60_000, 0)
	f.Close()

	roots := FiveHourRoots{Claude: claudeRoot}
	summary := AggregateFiveHour(roots, now.UnixMilli(), 0)

	if summary.PctUsed != 0 {
		t.Fatalf("PctUsed=%v, want 0 with cap=0", summary.PctUsed)
	}
	if summary.Threshold() != FiveHourThresholdNone {
		t.Fatalf("Threshold=%d, want None with cap=0", summary.Threshold())
	}
	// TotalEffective should still be measured even without a cap so the
	// daemon UI can show it.
	if summary.TotalEffective != 60_000 {
		t.Fatalf("TotalEffective=%d, want 60000", summary.TotalEffective)
	}
}

func TestAggregateFiveHour_EmptyRoots(t *testing.T) {
	summary := AggregateFiveHour(FiveHourRoots{}, time.Now().UnixMilli(), 100_000)
	if summary.TotalEffective != 0 {
		t.Fatalf("TotalEffective=%d, want 0 with empty roots", summary.TotalEffective)
	}
	if summary.DominantSession() != nil {
		t.Fatalf("DominantSession should be nil with no data")
	}
}
