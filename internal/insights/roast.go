package insights

import (
	"fmt"
	"sort"
)

// Roast is a single deterministic, templated zinger. No AI calls.
//
// Lines are intentionally a touch sardonic — claudestat-inspired — but
// templated from real numbers so they stay honest. Each Roast carries
// its provenance for the curious reader.
type Roast struct {
	Kind    string  `json:"kind"`
	Line    string  `json:"line"`
	Metric  float64 `json:"metric"`
	Context string  `json:"context,omitempty"`
}

// RoastInput is the aggregate view consumed by GenerateRoasts.
type RoastInput struct {
	SessionCount     int
	TotalCostUSD     float64
	TotalTokensIn    int64
	CachedReadTokens int64
	Tools            []ToolStat
	WorstLoop        SessionStats // session with the largest LongestRun
	BashHeavy        SessionStats // session with the highest bash ratio (subject to MinToolCalls)
	BashRatio        float64
	TopProject       string
	TopProjectShare  float64 // fraction of sessions in that one project
}

// GenerateRoasts returns up to `max` roasts derived from r. Always
// returns at least one entry so the CLI never prints an empty page.
func GenerateRoasts(r RoastInput, max int) []Roast {
	if max <= 0 {
		max = 5
	}
	var out []Roast

	// 1) Sample size — set the tone if data is sparse.
	if r.SessionCount == 0 {
		return []Roast{{
			Kind:   "empty",
			Line:   "No sessions ingested yet. The daemon hasn't even seen you work — bold of you to ask for a roast.",
			Metric: 0,
		}}
	}
	if r.SessionCount < 5 {
		out = append(out, Roast{
			Kind:    "small_sample",
			Line:    fmt.Sprintf("%d sessions on record. Cute. Come back when you've actually used the tool.", r.SessionCount),
			Metric:  float64(r.SessionCount),
			Context: "small_sample",
		})
	}

	// 2) Spend.
	switch {
	case r.TotalCostUSD >= 100:
		out = append(out, Roast{
			Kind:    "spend",
			Line:    fmt.Sprintf("$%.2f burned across your sessions. Hope at least one of those prompts shipped to production.", r.TotalCostUSD),
			Metric:  r.TotalCostUSD,
			Context: "spend_high",
		})
	case r.TotalCostUSD >= 25:
		out = append(out, Roast{
			Kind:    "spend",
			Line:    fmt.Sprintf("$%.2f in API spend. Not catastrophic, but Anthropic appreciates your patronage.", r.TotalCostUSD),
			Metric:  r.TotalCostUSD,
			Context: "spend_med",
		})
	case r.TotalCostUSD > 0:
		out = append(out, Roast{
			Kind:    "spend",
			Line:    fmt.Sprintf("$%.2f total. Pretty restrained, almost suspicious.", r.TotalCostUSD),
			Metric:  r.TotalCostUSD,
			Context: "spend_low",
		})
	}

	// 3) Cache reuse, system-wide.
	if r.TotalTokensIn > 0 {
		ratio := float64(r.CachedReadTokens) / float64(r.TotalTokensIn)
		switch {
		case ratio < 0.10:
			out = append(out, Roast{
				Kind:    "cache",
				Line:    fmt.Sprintf("Cache hit rate: %.0f%%. You're paying full price for tokens like it's 2023.", ratio*100),
				Metric:  ratio,
				Context: "cache_low",
			})
		case ratio < 0.25:
			out = append(out, Roast{
				Kind:    "cache",
				Line:    fmt.Sprintf("Cache reuse %.0f%%. Mediocre. The cache is right there.", ratio*100),
				Metric:  ratio,
				Context: "cache_med",
			})
		case ratio > 0.75:
			out = append(out, Roast{
				Kind:    "cache",
				Line:    fmt.Sprintf("Cache reuse %.0f%%. Frugal! Either disciplined or just very repetitive.", ratio*100),
				Metric:  ratio,
				Context: "cache_high",
			})
		}
	}

	// 4) Bash overuse.
	if r.BashHeavy.ID != "" && r.BashRatio >= 0.50 {
		out = append(out, Roast{
			Kind:    "bash",
			Line:    fmt.Sprintf("One session was %.0f%% Bash calls. Have you considered using the file tools? They exist for a reason.", r.BashRatio*100),
			Metric:  r.BashRatio,
			Context: r.BashHeavy.ID,
		})
	}

	// 5) Tight loop.
	if r.WorstLoop.LongestRun >= 8 {
		out = append(out, Roast{
			Kind: "loop",
			Line: fmt.Sprintf("%d consecutive %s calls in one session. That's not iteration — that's commitment.",
				r.WorstLoop.LongestRun, r.WorstLoop.LongestRunTool),
			Metric:  float64(r.WorstLoop.LongestRun),
			Context: r.WorstLoop.ID,
		})
	}

	// 6) Top tool monoculture.
	if len(r.Tools) > 0 {
		top := r.Tools[0]
		totalCalls := 0
		for _, t := range r.Tools {
			totalCalls += t.Count
		}
		if totalCalls > 0 {
			share := float64(top.Count) / float64(totalCalls)
			if share >= 0.55 {
				out = append(out, Roast{
					Kind:    "tool_share",
					Line:    fmt.Sprintf("%.0f%% of your tool calls are %s. There are other tools in the box.", share*100, top.Name),
					Metric:  share,
					Context: top.Name,
				})
			}
		}
	}

	// 7) Single-project monoculture.
	if r.TopProjectShare >= 0.70 && r.SessionCount >= 5 && r.TopProject != "" {
		out = append(out, Roast{
			Kind:    "project_share",
			Line:    fmt.Sprintf("%.0f%% of your sessions are in %s. Commitment, focus, or stuck — you decide.", r.TopProjectShare*100, shortProject(r.TopProject)),
			Metric:  r.TopProjectShare,
			Context: r.TopProject,
		})
	}

	// Stable order: keep the original insertion order, then trim.
	// (No sort — the order above is already the priority order.)
	if len(out) == 0 {
		out = []Roast{{
			Kind: "clean",
			Line: "Nothing to roast. You're either disciplined or boring.",
		}}
	}
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// BuildRoastInput composes a RoastInput from a slice of SessionStats and
// the matching ToolStat aggregation.
func BuildRoastInput(stats []SessionStats, tools []ToolStat) RoastInput {
	in := RoastInput{
		SessionCount: len(stats),
		Tools:        tools,
	}
	projShare := map[string]int{}
	for _, s := range stats {
		in.TotalCostUSD += s.CostUSD
		in.TotalTokensIn += s.TokensIn
		in.CachedReadTokens += s.CachedReadTokens
		if s.LongestRun > in.WorstLoop.LongestRun {
			in.WorstLoop = s
		}
		if s.TotalToolCalls >= 10 {
			bash := float64(s.ToolCounts["Bash"]) / float64(s.TotalToolCalls)
			if bash > in.BashRatio {
				in.BashRatio = bash
				in.BashHeavy = s
			}
		}
		if s.ProjectPath != "" {
			projShare[s.ProjectPath]++
		}
	}
	if len(projShare) > 0 {
		type kv struct {
			k string
			v int
		}
		ranked := make([]kv, 0, len(projShare))
		for k, v := range projShare {
			ranked = append(ranked, kv{k, v})
		}
		sort.Slice(ranked, func(i, j int) bool {
			if ranked[i].v != ranked[j].v {
				return ranked[i].v > ranked[j].v
			}
			return ranked[i].k < ranked[j].k
		})
		in.TopProject = ranked[0].k
		in.TopProjectShare = float64(ranked[0].v) / float64(in.SessionCount)
	}
	return in
}

// shortProject returns just the last path segment (or the input if there
// is no separator) so the roast line stays terminal-friendly.
func shortProject(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}
