package insights

import (
	"fmt"
	"sort"
)

// Pattern is one inefficiency signal discovered in a session.
type Pattern struct {
	// Kind identifies the rule that fired (machine-readable).
	Kind string `json:"kind"`
	// Severity is "info" | "warn" | "alert".
	Severity string `json:"severity"`
	// SessionID names the session this finding is scoped to. Empty for
	// global findings.
	SessionID string `json:"session_id,omitempty"`
	// ProjectPath is the session's project root, copied through for display.
	ProjectPath string `json:"project_path,omitempty"`
	// Message is a one-line human-readable explanation.
	Message string `json:"message"`
	// Metric is the measured value that triggered the rule.
	Metric float64 `json:"metric"`
	// Threshold is the rule's tripping point.
	Threshold float64 `json:"threshold"`
}

// PatternKind constants — keep the set small and stable so downstream
// consumers (`klyne patterns --kind=...`) can filter reliably.
const (
	KindTightLoop     = "tight_loop"
	KindBashOveruse   = "bash_overuse"
	KindLowCacheReuse = "low_cache_reuse"
)

// Thresholds for pattern detection. Defaults are tuned for v1 from
// inspection of the maintainer's own transcripts; they can be overridden
// by the CLI flag set or future config keys.
type Thresholds struct {
	// TightLoopMin: a same-tool run of >= this many calls is a loop.
	TightLoopMin int
	// BashRatio: bash_calls / total_tool_calls >= this is overuse.
	BashRatio float64
	// CacheReuseFloor: cached_read / tokens_in < this is "low".
	CacheReuseFloor float64
	// MinToolCalls: skip bash check on sessions with fewer tool calls.
	MinToolCalls int
	// MinTokens: skip cache check on sessions with fewer input tokens.
	MinTokens int64
}

// DefaultThresholds returns the v1 tuned defaults.
func DefaultThresholds() Thresholds {
	return Thresholds{
		TightLoopMin:    5,
		BashRatio:       0.40,
		CacheReuseFloor: 0.30,
		MinToolCalls:    10,
		MinTokens:       50_000,
	}
}

// DetectPatterns returns the inefficiency signals for the given session
// stats. Results are sorted by Severity (alert > warn > info) then by
// LastMsgAt DESC of the owning session.
func DetectPatterns(stats []SessionStats, t Thresholds) []Pattern {
	if t.TightLoopMin <= 0 {
		t = DefaultThresholds()
	}
	out := make([]Pattern, 0, 8)
	for _, s := range stats {
		out = append(out, detectForSession(s, t)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri := severityRank(out[i].Severity)
		rj := severityRank(out[j].Severity)
		if ri != rj {
			return ri > rj
		}
		// fall back to alphabetical session id for determinism
		return out[i].SessionID < out[j].SessionID
	})
	return out
}

func detectForSession(s SessionStats, t Thresholds) []Pattern {
	var out []Pattern

	// 1) Tight loop: long run of the same tool back-to-back.
	if s.LongestRun >= t.TightLoopMin {
		out = append(out, Pattern{
			Kind:        KindTightLoop,
			Severity:    classify(float64(s.LongestRun), float64(t.TightLoopMin), 1.5, 3.0),
			SessionID:   s.ID,
			ProjectPath: s.ProjectPath,
			Message: fmt.Sprintf("%d consecutive %s calls — possible loop",
				s.LongestRun, s.LongestRunTool),
			Metric:    float64(s.LongestRun),
			Threshold: float64(t.TightLoopMin),
		})
	}

	// 2) Bash overuse: too high a share of tool calls are Bash.
	if s.TotalToolCalls >= t.MinToolCalls {
		bash := s.ToolCounts["Bash"]
		ratio := float64(bash) / float64(s.TotalToolCalls)
		if ratio >= t.BashRatio {
			out = append(out, Pattern{
				Kind:        KindBashOveruse,
				Severity:    classify(ratio, t.BashRatio, 1.25, 1.75),
				SessionID:   s.ID,
				ProjectPath: s.ProjectPath,
				Message: fmt.Sprintf("Bash %d/%d (%.0f%%) — prefer Read/Edit/Grep/Glob",
					bash, s.TotalToolCalls, ratio*100),
				Metric:    ratio,
				Threshold: t.BashRatio,
			})
		}
	}

	// 3) Low cache reuse: a lot of input tokens that did not hit cache.
	// Skip tiny sessions where the ratio is meaningless.
	if s.TokensIn >= t.MinTokens && s.CacheReadRatio < t.CacheReuseFloor {
		// Distance below the floor drives severity.
		gap := t.CacheReuseFloor - s.CacheReadRatio
		out = append(out, Pattern{
			Kind:        KindLowCacheReuse,
			Severity:    classify(gap, 0, 0.10, 0.20),
			SessionID:   s.ID,
			ProjectPath: s.ProjectPath,
			Message: fmt.Sprintf("cache reuse %.0f%% of input tokens — likely re-feeding context",
				s.CacheReadRatio*100),
			Metric:    s.CacheReadRatio,
			Threshold: t.CacheReuseFloor,
		})
	}

	return out
}

// classify maps a metric to a severity band given two multipliers off the
// threshold (warnX, alertX). For "below-the-floor" metrics, callers pass
// threshold=0 and a "gap" magnitude.
func classify(metric, threshold, warnX, alertX float64) string {
	if threshold == 0 {
		// gap-style: metric is the magnitude already
		if metric >= alertX {
			return "alert"
		}
		if metric >= warnX {
			return "warn"
		}
		return "info"
	}
	if metric >= threshold*alertX {
		return "alert"
	}
	if metric >= threshold*warnX {
		return "warn"
	}
	return "info"
}

func severityRank(s string) int {
	switch s {
	case "alert":
		return 3
	case "warn":
		return 2
	case "info":
		return 1
	}
	return 0
}
