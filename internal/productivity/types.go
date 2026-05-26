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

// RiskCommit is a minimal commit reference (short SHA + subject)
// attached to an "unpushed" RiskSignal so the dashboard can show WHICH
// commits are not on the remote, not merely how many.
type RiskCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
}

// RiskSignal is a current-state risk (§6.5). Kind is "unpushed" or
// "done-uncommitted". AgeMinutes is elapsed since the relevant anchor
// (oldest unpushed commit, or session end for done-uncommitted).
//
// Branch + WorktreePath identify exactly where the risk is — they
// disambiguate the otherwise-identical rows a repo with several
// worktrees produces. Commits is the concrete evidence for an
// "unpushed" risk (the commits ahead of origin, SHA + subject); Files
// is the concrete evidence for a "done-uncommitted" risk (the
// uncommitted/untracked paths). Both are always emitted as [] not null
// and are empty for the other kind.
type RiskSignal struct {
	Kind         string       `json:"kind"`
	Detail       string       `json:"detail"`
	AgeMinutes   int          `json:"age_minutes"`
	Branch       string       `json:"branch"`
	WorktreePath string       `json:"worktree_path"`
	Commits      []RiskCommit `json:"commits"`
	Files        []string     `json:"files"`
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
//
// ReflectionGroups is the iterative-reflection list
// (docs/features/iterative-reflection.md): one entry per
// worklog_reflections row for this (project, day), ordered ts ASC.
// The UI renders one group per entry with a "view details" disclosure;
// ReflectionMarkdown is the legacy concatenation kept for back-compat
// consumers that haven't moved to per-group rendering yet.
type Service struct {
	Repo               string            `json:"repo"`
	ProjectPath        string            `json:"project_path"`
	Branches           []Branch          `json:"branches"`
	Risks              []RiskSignal      `json:"risks"`
	ManualOnly         bool              `json:"manual_only"`
	MinutesByCLI       map[string]int    `json:"minutes_by_cli"`
	ReflectionMarkdown string            `json:"reflection_markdown"`
	ReflectionGroups   []ReflectionGroup `json:"reflection_groups,omitempty"`
	// MergedPRs is the GitHub pull requests the user merged within the
	// report window — a Layer-2 `gh`-sourced enrichment (see MergedPR),
	// NOT a deterministic local-git fact. MergedPRsAsOf is when that
	// GitHub data was fetched (the cache timestamp); zero when no PR
	// data is available. MergedPRs is always emitted as [] not null.
	MergedPRs     []MergedPR `json:"merged_prs"`
	MergedPRsAsOf time.Time  `json:"merged_prs_as_of"`
	// GitFetchedAt is when `git fetch` last refreshed the local
	// mirror of origin for this repo (FETCH_HEAD mtime). The
	// dashboard piggybacks on the merged-PR cache TTL to auto-refresh
	// stale remotes, so ahead/behind and ship-state stay honest;
	// surface "git as of N ago" so the user can see it. Zero when no
	// fetch has ever run in this clone.
	GitFetchedAt time.Time `json:"git_fetched_at"`
	// WhatWasDone is the typed two-tier card surfaced on the
	// productivity dashboard's "What was done" panel — Tier 1 headline
	// (counts + tldr + top commit shas) plus the expandable Tier 2
	// details list. Computed by ComposeWWD over the day's
	// worklog_reflections rows whose body_json is non-empty. Nil when
	// no typed reflection exists for this service-on-day (legacy
	// prose-only rows produce no card; the UI falls back to the
	// legacy reflection_markdown / reflection_groups path for those).
	WhatWasDone *WhatWasDoneCard `json:"what_was_done,omitempty"`
}

// MergedPR is one GitHub pull request the user authored and merged
// within the report window.
//
// Unlike everything else in this package, MergedPR is NOT derived from
// local git: it is a Layer-2 enrichment populated by the API handler
// (internal/api/handlers/productivity_github.go) via `gh pr list`,
// cached with a TTL so GitHub is not hit on every dashboard load. When
// gh / auth / network is unavailable Service.MergedPRs is simply empty
// and the deterministic branch ship-state still carries the merged
// signal — the enrichment never gates the deterministic report (D8).
//
// OpenedAt is when the PR was created; TimeToShipMinutes is the
// minutes from OpenedAt to MergedAt — the honest "open → merge" cycle
// time. (Per-commit dates are not fetched because requesting them
// through GitHub's GraphQL inflates the `authors` node traversal past
// the 500K limit on repos with many PRs.)
type MergedPR struct {
	Number            int       `json:"number"`
	Title             string    `json:"title"`
	HeadRef           string    `json:"head_ref"`
	MergedAt          time.Time `json:"merged_at"`
	OpenedAt          time.Time `json:"opened_at"`
	TimeToShipMinutes int       `json:"time_to_ship_minutes"`
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

// WWDDetail is one typed entry in a service's "What was done" Tier 2
// list. The shape mirrors §1.2 of docs/plan/2026-05-26-wwd-typed-cards.md
// (which itself mirrors the §1.1 stored shape persisted in
// worklog_reflections.body_json).
//
// Kind is the SHIPPED/MAJOR/FIXED/DECISION/INVESTIGATED/IN_PROGRESS
// taxonomy. When is local "HH:MM". Text is the verb-led ≤200-char
// detail. Evidence is at least one citation token drawn LITERALLY from
// the source stop_summary (commit sha, file path, test name, etc.).
// SessionID is the originating stop_summary's session_id — the UI
// renders each detail as a clickable row that opens that source.
type WWDDetail struct {
	Kind      string   `json:"kind"`
	When      string   `json:"when"`
	Text      string   `json:"text"`
	Evidence  []string `json:"evidence"`
	SessionID string   `json:"session_id,omitempty"`
}

// WWDTier1 is the deterministically-computed headline row of a service's
// "What was done" card — the always-visible summary above the
// expandable Tier 2 details list. Every field is derived by ComposeWWD
// from the merged Tier 2 details across every reflection row for the
// service-on-day; no LLM call participates in this computation.
//
// PillCounts uses lowercased kind names ("shipped", "fixed", "decisions",
// "investigated", "in_progress", "major") so the UI can render fixed
// chip slots without re-keying.
type WWDTier1 struct {
	TLDR        string         `json:"tldr"`
	PillCounts  map[string]int `json:"pill_counts"`
	TopEvidence []string       `json:"top_evidence"`
	TurnCount   int            `json:"turn_count"`
	CommitCount int            `json:"commit_count"`
}

// WhatWasDoneCard is the per-service typed "What was done" card served
// on GET /api/productivity at services[].what_was_done. Matches §1.2 of
// docs/plan/2026-05-26-wwd-typed-cards.md.
//
// Tier1 is always present. Tier2.Details is ordered SHIPPED → MAJOR →
// FIXED → DECISION → INVESTIGATED → IN_PROGRESS, and within each kind
// newest-first by `When` — the same render order the dashboard uses
// (no per-render sort). The composer drops legacy reflection rows
// (BodyJSON empty) so cards are only emitted for services whose
// reflections opted into the typed payload.
//
// LLMCompiled is true when the card's `tier1.tldr` and (optionally
// refined) detail prose were written by the second-pass Sonnet
// /klyne:productivity-sync compiler and persisted via
// productivity.PersistLLMCompiledCard. False when the card was lazily
// composed from typed reflection rows by the deterministic ComposeWWD
// fallback. The UI surfaces a tiny "sonnet · auto" badge on
// LLM-compiled cards so the user can tell which prose is machine-
// authored vs deterministic templated.
type WhatWasDoneCard struct {
	Service string   `json:"service"`
	Tier1   WWDTier1 `json:"tier1"`
	Tier2   struct {
		Details []WWDDetail `json:"details"`
	} `json:"tier2"`
	LLMCompiled bool `json:"llm_compiled"`
}

// ReflectionGroup is one worklog_reflections row in the
// iterative-reflection workflow (docs/features/iterative-reflection.md).
// The dashboard renders each group as a separate entry under WHAT WAS
// DONE so the T1/T2/T3 history of the day is visible chronologically
// — A1 (per-row groups, no cross-time AI re-synthesis).
type ReflectionGroup struct {
	ID                  string   `json:"id"`
	TS                  int64    `json:"ts"`
	BodyMD              string   `json:"body_md"`
	EvidenceEntryIDs    []string `json:"evidence_entry_ids"`
	StopSummaryCursorTS int64    `json:"stop_summary_cursor_ts,omitempty"`
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
	Day                string            `json:"day"`
	Services           []Service         `json:"services"`
	ReflectionStatus   string            `json:"reflection_status"`
	Nudge              string            `json:"nudge"`
	TotalActiveMinutes int               `json:"total_active_minutes"`
	MinutesByCLI       map[string]int    `json:"minutes_by_cli"`
	Sessions           []SessionStat     `json:"sessions"`
	ReflectionMarkdown string            `json:"reflection_markdown,omitempty"`
	ReflectionGroups   []ReflectionGroup `json:"reflection_groups,omitempty"`
	// PendingEntries is the count of stop_summaries written across all
	// visible services (recap_visible=1) with ts > the project's latest
	// reflection cursor — i.e. work that has happened but hasn't been
	// folded into a /klyne:reflect yet. Drives the dashboard's "Sync
	// productivity dashboard" button highlight + N-new badge.
	PendingEntries int `json:"pending_entries"`
}
