package resume

import (
	"math"
	"testing"
	"time"
)

// fixedNow is a stable reference time for deterministic tests.
var fixedNow = time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

func millis(t time.Time) int64 { return t.UnixMilli() }

// hoursAgo returns a Unix-millisecond timestamp for n hours before fixedNow.
func hoursAgo(n float64) int64 {
	return millis(fixedNow.Add(-time.Duration(n * float64(time.Hour))))
}

func TestScore_RecencyDecay(t *testing.T) {
	t.Run("recent_session_high_recency", func(t *testing.T) {
		s := SessionInput{
			ID:          "a",
			LastMsgAt:   hoursAgo(1),
			ProjectPath: "/other/project", // no cwd/git match → only recency
		}
		sc := Score(s, "/my/project", nil, fixedNow)
		// 0.40 * exp(-1/48) ≈ 0.40 * 0.979 ≈ 0.392 — below threshold, but close to 0.40
		wantApprox := 0.40 * math.Exp(-1.0/48.0)
		if math.Abs(sc-wantApprox) > 0.001 {
			t.Errorf("Score recency 1h: got %.4f, want ~%.4f", sc, wantApprox)
		}
	})

	t.Run("old_session_low_recency", func(t *testing.T) {
		s := SessionInput{
			ID:          "b",
			LastMsgAt:   hoursAgo(96), // 4 days ago
			ProjectPath: "/other/project",
		}
		sc := Score(s, "/my/project", nil, fixedNow)
		// 0.40 * exp(-96/48) = 0.40 * exp(-2) ≈ 0.054
		wantApprox := 0.40 * math.Exp(-2.0)
		if math.Abs(sc-wantApprox) > 0.001 {
			t.Errorf("Score recency 96h: got %.4f, want ~%.4f", sc, wantApprox)
		}
	})
}

func TestScore_CWDMatch(t *testing.T) {
	cases := []struct {
		name    string
		proj    string
		cwd     string
		wantCWD float64
	}{
		{"exact", "/foo/bar", "/foo/bar", 1.0},
		{"cwd_inside_proj", "/foo/bar", "/foo/bar/baz", 0.6},
		{"proj_inside_cwd", "/foo/bar/baz", "/foo/bar", 0.6},
		{"siblings", "/foo/bar", "/foo/qux", 0.3},
		{"unrelated", "/alpha/beta", "/gamma/delta", 0.0},
	}

	// Use very old session so recency ≈ 0; use no git so only cwdScore matters.
	// score ≈ 0.40 * tiny + 0.30 * cwdScore
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cwdMatch(tc.proj, tc.cwd)
			if got != tc.wantCWD {
				t.Errorf("cwdMatch(%q, %q) = %.1f, want %.1f", tc.proj, tc.cwd, got, tc.wantCWD)
			}
		})
	}
}

func TestScore_GitJaccard(t *testing.T) {
	cases := []struct {
		name      string
		touched   []string
		gitRecent []string
		want      float64
	}{
		{"empty_both", nil, nil, 0.0},
		{"empty_git", []string{"a.go", "b.go"}, nil, 0.0},
		{"empty_touched", nil, []string{"a.go"}, 0.0},
		{"full_overlap", []string{"a.go", "b.go"}, []string{"a.go", "b.go"}, 1.0},
		{"partial_overlap", []string{"a.go", "b.go"}, []string{"a.go", "c.go"}, 1.0 / 3.0}, // |i|=1, |u|=3
		{"no_overlap", []string{"a.go"}, []string{"b.go"}, 0.0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := gitJaccard(tc.touched, tc.gitRecent)
			if math.Abs(got-tc.want) > 0.001 {
				t.Errorf("gitJaccard got %.4f, want %.4f", got, tc.want)
			}
		})
	}
}

func TestRank_TopThreeCap(t *testing.T) {
	// All sessions are exact cwd match + very recent → score ≈ 0.70+; should all pass threshold.
	// We give 5 sessions and expect only 3 back.
	sessions := make([]SessionInput, 5)
	for i := range sessions {
		sessions[i] = SessionInput{
			ID:          string(rune('a' + i)),
			LastMsgAt:   hoursAgo(float64(i + 1)), // 1h,2h,3h,4h,5h ago
			ProjectPath: "/my/project",
		}
	}
	ranked := Rank(sessions, "/my/project", nil, fixedNow)
	if len(ranked) != MaxCandidates {
		t.Errorf("Rank returned %d candidates, want %d", len(ranked), MaxCandidates)
	}
	// First ranked should be most recent.
	if ranked[0].Session.ID != "a" {
		t.Errorf("top ranked should be 'a' (1h ago), got %q", ranked[0].Session.ID)
	}
	for i := 1; i < len(ranked); i++ {
		if ranked[i].Score > ranked[i-1].Score {
			t.Errorf("ranked[%d].Score=%.4f > ranked[%d].Score=%.4f — should be descending",
				i, ranked[i].Score, i-1, ranked[i-1].Score)
		}
	}
}

func TestRank_BelowThreshold(t *testing.T) {
	// Old session, unrelated project → score well below 0.55.
	sessions := []SessionInput{
		{
			ID:          "old",
			LastMsgAt:   hoursAgo(200), // ~8 days ago
			ProjectPath: "/completely/different",
		},
	}
	ranked := Rank(sessions, "/my/project", nil, fixedNow)
	if len(ranked) != 0 {
		t.Errorf("expected 0 candidates below threshold, got %d (score=%.4f)",
			len(ranked), ranked[0].Score)
	}
}

func TestRank_HighScoreWithGit(t *testing.T) {
	// Session 2 hours ago, same project, overlapping git files → should score high.
	s := SessionInput{
		ID:           "s1",
		LastMsgAt:    hoursAgo(2),
		ProjectPath:  "/my/project",
		TouchedFiles: []string{"internal/auth/handler.go", "internal/auth/middleware.go"},
	}
	gitRecent := []string{"internal/auth/handler.go", "internal/auth/middleware.go", "cmd/main.go"}

	sc := Score(s, "/my/project", gitRecent, fixedNow)
	// Expected: 0.40*exp(-2/48) + 0.30*1.0 + 0.30*(2/3)
	//         ≈ 0.40*0.9592 + 0.30 + 0.20 ≈ 0.8837
	if sc < 0.80 {
		t.Errorf("expected high score for recent + exact cwd + git overlap, got %.4f", sc)
	}
}

func TestRank_EmptyGitRecent(t *testing.T) {
	// Empty gitRecent should not panic and should score the cwd+recency terms only.
	s := SessionInput{
		ID:           "s2",
		LastMsgAt:    hoursAgo(10),
		ProjectPath:  "/my/project",
		TouchedFiles: []string{"go.mod", "main.go"},
	}
	got := Score(s, "/my/project", []string{}, fixedNow)
	// 0.40*exp(-10/48) + 0.30*1.0 + 0.30*0
	want := 0.40*math.Exp(-10.0/48.0) + 0.30
	if math.Abs(got-want) > 0.001 {
		t.Errorf("Score empty gitRecent: got %.4f, want %.4f", got, want)
	}
}

func TestRank_FutureTimestamp(t *testing.T) {
	// Future LastMsgAt should not produce negative hours; clamp to 0.
	s := SessionInput{
		ID:          "future",
		LastMsgAt:   millis(fixedNow.Add(time.Hour)), // 1h in the future
		ProjectPath: "/other",
	}
	sc := Score(s, "/other", nil, fixedNow)
	// hours < 0 → clamped to 0 → recency = exp(0) = 1.0
	wantRecency := 0.40 * 1.0 // + 0.30*1.0 (exact cwd match) = 0.70
	wantTotal := wantRecency + 0.30*1.0
	if math.Abs(sc-wantTotal) > 0.001 {
		t.Errorf("Score future ts: got %.4f, want %.4f", sc, wantTotal)
	}
}

func TestCwdMatch_RootDirectory(t *testing.T) {
	// Root "/" should not produce a sibling match with arbitrary paths.
	got := cwdMatch("/", "/foo")
	// /foo starts with "/"+"/" = "//", which doesn't match → should fall through to sibling
	// filepath.Dir("/foo") = "/", filepath.Dir("/") = "/" → same parent but it's "/"
	// The code checks projParent != "/" to avoid false sibling matches at root.
	if got != 0.0 {
		t.Errorf("cwdMatch('/', '/foo') = %.1f, want 0.0", got)
	}
}

func TestRankWithOptions_CustomLimit(t *testing.T) {
	sessions := make([]SessionInput, 5)
	for i := range sessions {
		sessions[i] = SessionInput{
			ID:          string(rune('a' + i)),
			LastMsgAt:   hoursAgo(float64(i + 1)),
			ProjectPath: "/my/project",
		}
	}
	ranked := RankWithOptions(sessions, "/my/project", nil, fixedNow, RankOptions{Limit: 5})
	if len(ranked) != 5 {
		t.Errorf("Limit=5: got %d candidates, want 5", len(ranked))
	}
}

func TestRankWithOptions_NoCap(t *testing.T) {
	// Limit < 0 means no cap at all; with 7 qualifying sessions we expect 7 back.
	sessions := make([]SessionInput, 7)
	for i := range sessions {
		sessions[i] = SessionInput{
			ID:          string(rune('a' + i)),
			LastMsgAt:   hoursAgo(float64(i + 1)),
			ProjectPath: "/my/project",
		}
	}
	ranked := RankWithOptions(sessions, "/my/project", nil, fixedNow, RankOptions{Limit: -1})
	if len(ranked) != 7 {
		t.Errorf("Limit=-1: got %d candidates, want 7 (no cap)", len(ranked))
	}
}

func TestRankWithOptions_IgnoreThreshold(t *testing.T) {
	// Mix of below-threshold and qualifying sessions. IgnoreThreshold should
	// surface every one of them; ordering is still by descending score.
	sessions := []SessionInput{
		{ID: "stale", LastMsgAt: hoursAgo(200), ProjectPath: "/completely/different"},
		{ID: "fresh", LastMsgAt: hoursAgo(1), ProjectPath: "/my/project"},
	}
	ranked := RankWithOptions(sessions, "/my/project", nil, fixedNow, RankOptions{
		Limit:           -1,
		IgnoreThreshold: true,
	})
	if len(ranked) != 2 {
		t.Fatalf("IgnoreThreshold: got %d, want 2", len(ranked))
	}
	if ranked[0].Session.ID != "fresh" || ranked[1].Session.ID != "stale" {
		t.Errorf("expected ordering [fresh, stale], got [%s, %s]",
			ranked[0].Session.ID, ranked[1].Session.ID)
	}
}

func TestRankWithOptions_DefaultMatchesRank(t *testing.T) {
	// Zero-value RankOptions must reproduce legacy Rank behaviour (top-3, threshold).
	sessions := make([]SessionInput, 5)
	for i := range sessions {
		sessions[i] = SessionInput{
			ID:          string(rune('a' + i)),
			LastMsgAt:   hoursAgo(float64(i + 1)),
			ProjectPath: "/my/project",
		}
	}
	got := RankWithOptions(sessions, "/my/project", nil, fixedNow, RankOptions{})
	want := Rank(sessions, "/my/project", nil, fixedNow)
	if len(got) != len(want) {
		t.Fatalf("zero RankOptions vs Rank: len mismatch %d vs %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Session.ID != want[i].Session.ID {
			t.Errorf("idx %d: got %q, want %q", i, got[i].Session.ID, want[i].Session.ID)
		}
	}
}

func TestRank_MultipleSessionsOrdering(t *testing.T) {
	// Construct sessions with known scores and verify ordering.
	sessions := []SessionInput{
		{
			ID:          "low",
			LastMsgAt:   hoursAgo(48), // recency decay significant
			ProjectPath: "/unrelated",
		},
		{
			ID:          "high",
			LastMsgAt:   hoursAgo(2),
			ProjectPath: "/my/project",
		},
	}
	ranked := Rank(sessions, "/my/project", nil, fixedNow)
	if len(ranked) == 0 {
		t.Fatal("expected at least one ranked candidate")
	}
	if ranked[0].Session.ID != "high" {
		t.Errorf("expected 'high' to rank first, got %q", ranked[0].Session.ID)
	}
}
