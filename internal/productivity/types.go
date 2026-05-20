// Package productivity computes the deterministic-first AI productivity
// dashboard substrate (spec
// docs/superpowers/specs/2026-05-19-ai-productivity-dashboard-design.md):
// repo discovery (D5), live git scan (D6 live half), the session-end git
// snapshot builder (D6 snapshot half — CaptureSnapshot /
// CaptureSessionSnapshots), identity filter (§6.3), the ship-state
// machine (D2/§6.5), session-anchored time attribution (D1/§6.4),
// grouping (§6.6), the Layer-1 deterministic templated narrative (§7 L1),
// and current-state risk signals (§6.5).
//
// Hard determinism boundary (spec §7.1): NOTHING in this package calls an
// LLM. Every number, fact, ship state, and the Layer-1 narrative is
// computed purely from git + session telemetry. The narrative is built
// with `fmt` only and is structurally prevented from emitting invented
// PR/ticket IDs (the "PR #57" regression guard).
//
// Scope (spec §11 / D8): this package is the deterministic substrate.
// The Layer-2 reflection enrichment + the 7 worklog improvements (§7.2)
// are BUILT in internal/worklog and consume this package read-only
// (worklog.BuildProjectSubstrate / RecordReflectionWithSubstrate). The
// D6 session-end snapshot CAPTURE half is BUILT in internal/hooks via
// productivity.CaptureSessionSnapshots. Remaining follow-up: the
// dashboard_cache render-time memoization.
package productivity

import "time"

// ShipState is the locked three-state ship machine (D2 / §6.5). Every
// branch is exactly one of these.
type ShipState string

const (
	// ShipLocal — local commits exist, ahead > 0, branch not on origin.
	ShipLocal ShipState = "committed-local-only"
	// ShipPushed — present on origin, not merged into the default branch.
	ShipPushed ShipState = "pushed-to-remote"
	// ShipMerged — merged into the repo's default branch.
	ShipMerged ShipState = "merged-to-default"
)

// Commit is one git commit, with deterministic per-commit facts only.
// IsUser is the §6.3 identity filter verdict: false for co-actors and
// bots (e.g. Jenkins / Ravi-Ranjan), which are excluded from the user's
// productivity figures.
type Commit struct {
	SHA         string    `json:"sha"`
	Subject     string    `json:"subject"`
	Author      string    `json:"author"`
	AuthorEmail string    `json:"author_email"`
	CommittedAt time.Time `json:"committed_at"`
	Files       int       `json:"files"`
	Insertions  int       `json:"insertions"`
	Deletions   int       `json:"deletions"`
	IsUser      bool      `json:"is_user"`
	// BranchName is the branch this commit belongs to. ScanRepo leaves
	// it empty (the scan is single-branch — see ScanResult.Branch);
	// report assembly uses it to group multi-branch fixtures and the
	// future per-commit branch attribution. Additive to the locked
	// Task-2 type — existing field names are unchanged.
	BranchName string `json:"branch_name,omitempty"`
}

// Branch groups the commits on one branch with its ship state, the raw
// branch-derived ticket ID (no external lookup — D3), the deterministic
// attributed minutes (D1), and the Layer-1 narrative (§7 L1).
//
// FirstCommitAt/LastCommitAt are the earliest and latest commit
// CommittedAt on the branch; ShipSpanMinutes is the minutes between them
// (0 when the branch has fewer than 2 commits) — the honest "work span /
// time to ship" proxy.
type Branch struct {
	Name              string    `json:"name"`
	TicketID          string    `json:"ticket_id"`
	Ship              ShipState `json:"ship"`
	Ahead             int       `json:"ahead"`
	Behind            int       `json:"behind"`
	Commits           []Commit  `json:"commits"`
	AttributedMinutes int       `json:"attributed_minutes"`
	Narrative         string    `json:"narrative"`
	FirstCommitAt     time.Time `json:"first_commit_at"`
	LastCommitAt      time.Time `json:"last_commit_at"`
	ShipSpanMinutes   int       `json:"ship_span_minutes"`
}

// RiskSignal is a current-state risk (§6.5). Kind is "unpushed" or
// "done-uncommitted". AgeMinutes is elapsed since the relevant anchor
// (oldest unpushed commit, or session end for done-uncommitted).
type RiskSignal struct {
	Kind       string `json:"kind"`
	Detail     string `json:"detail"`
	AgeMinutes int    `json:"age_minutes"`
}

// Service is one repo (canonical root) with its branches and risks.
// ManualOnly is true when the repo had in-window commits but no AI
// session active-time (D4: manual time is never folded into AI time).
//
// MinutesByCLI breaks the repo's AI session time down per CLI
// ("claude" / "codex" → merged active minutes for that CLI within the
// repo). Time is repo-scoped, not branch-scoped, so this lives on the
// Service. Per-CLI values are themselves union totals (§6.4), so when
// the user ran both CLIs at once they may sum to slightly more than the
// all-CLI attributed total — that is correct and expected.
//
// ReflectionMarkdown is the worklog reflection body (body_md) for this
// repo on the report's day (Change 3) — the worklog's own account of
// what was done. Empty when no reflection exists for the project+day.
type Service struct {
	Repo               string         `json:"repo"`
	ProjectPath        string         `json:"project_path"`
	Branches           []Branch       `json:"branches"`
	Risks              []RiskSignal   `json:"risks"`
	ManualOnly         bool           `json:"manual_only"`
	MinutesByCLI       map[string]int `json:"minutes_by_cli"`
	ReflectionMarkdown string         `json:"reflection_markdown"`
}

// SessionStat is the per-session proof-of-work breakdown (Change 2): the
// deterministic evidence the user can SEE behind the headline AI-time
// number. One per contributing session in the window.
//
// ActiveMinutes is the session's OWN gap-capped active total (its active
// sub-intervals' lengths summed). Repo is the repo/Service the session
// is attributed to (derived from its project_path). StartedAt/EndedAt
// are the first and last in-window message timestamps.
//
// ActiveIntervals is the session's gap-capped active sub-intervals — the
// same intervals whose global union produces Report.TotalActiveMinutes.
// The dashboard timeline draws these as solid segments (with
// StartedAt→EndedAt as a faint presence track behind them), and runs the
// concurrency sweep-line over them so the breakdown sums to the headline
// elapsed wall-clock. The interval lengths sum to ActiveMinutes. Always
// emitted as [] not null.
type SessionStat struct {
	SessionID       string           `json:"session_id"`
	CLI             string           `json:"cli"`
	Repo            string           `json:"repo"`
	StartedAt       time.Time        `json:"started_at"`
	EndedAt         time.Time        `json:"ended_at"`
	ActiveMinutes   int              `json:"active_minutes"`
	ActiveIntervals []ActiveInterval `json:"active_intervals"`
	MessageCount    int              `json:"message_count"`
}

// Report is the top-level fused record served to the API/UI.
// ReflectionStatus is "missing" | "stale" | "current" (§7 L3); Nudge is
// the human prompt shown when no/stale reflection exists.
//
// TotalActiveMinutes is the headline AI time (Change 1): the GLOBAL
// union of every session's active wall-clock intervals across ALL
// repos in the window — true elapsed wall-clock, structurally ≤ 24h/day.
// It is NOT the sum of per-Service unions (that double-counts parallel
// cross-repo agents).
//
// MinutesByCLI is the per-CLI GLOBAL union (cli → that CLI's own
// all-repo wall-clock union), NOT the sum of per-Service MinutesByCLI.
// When the user ran both CLIs at once claude+codex may slightly exceed
// TotalActiveMinutes — that is correct.
//
// Sessions is the deterministic per-session evidence list (Change 2):
// every contributing session in the window, sorted by StartedAt.
//
// ReflectionMarkdown is an optional overall worklog reflection body
// (Change 3); empty when there is no single sensible project-agnostic
// reflection — per-Service ReflectionMarkdown carries the per-repo body.
type Report struct {
	Day                string         `json:"day"`
	Services           []Service      `json:"services"`
	ReflectionStatus   string         `json:"reflection_status"`
	Nudge              string         `json:"nudge"`
	TotalActiveMinutes int            `json:"total_active_minutes"`
	MinutesByCLI       map[string]int `json:"minutes_by_cli"`
	Sessions           []SessionStat  `json:"sessions"`
	ReflectionMarkdown string         `json:"reflection_markdown,omitempty"`
}
