// Package productivity computes the deterministic-first AI productivity
// dashboard substrate (spec
// docs/superpowers/specs/2026-05-19-ai-productivity-dashboard-design.md):
// repo discovery (D5), live git scan (D6 live half), identity filter
// (§6.3), the ship-state machine (D2/§6.5), session-anchored time
// attribution (D1/§6.4), grouping (§6.6), the Layer-1 deterministic
// templated narrative (§7 L1), and current-state risk signals (§6.5).
//
// Hard determinism boundary (spec §7.1): NOTHING in this package calls an
// LLM. Every number, fact, ship state, and the Layer-1 narrative is
// computed purely from git + session telemetry. The narrative is built
// with `fmt` only and is structurally prevented from emitting invented
// PR/ticket IDs (the "PR #57" regression guard).
//
// Prototype boundary (spec §11): this package is the substrate. The
// session-end snapshot CAPTURE, Layer-2 reflection enrichment + the 7
// worklog improvements, and dashboard_cache memoization are documented
// follow-ups, NOT built here.
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
type Branch struct {
	Name              string       `json:"name"`
	TicketID          string       `json:"ticket_id"`
	Ship              ShipState    `json:"ship"`
	Ahead             int          `json:"ahead"`
	Behind            int          `json:"behind"`
	Commits           []Commit     `json:"commits"`
	AttributedMinutes int          `json:"attributed_minutes"`
	Narrative         string       `json:"narrative"`
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
type Service struct {
	Repo        string       `json:"repo"`
	ProjectPath string       `json:"project_path"`
	Branches    []Branch     `json:"branches"`
	Risks       []RiskSignal `json:"risks"`
	ManualOnly  bool         `json:"manual_only"`
}

// Report is the top-level fused record served to the API/UI.
// ReflectionStatus is "missing" | "stale" | "current" (§7 L3); Nudge is
// the human prompt shown when no/stale reflection exists.
type Report struct {
	Day              string    `json:"day"`
	Services         []Service `json:"services"`
	ReflectionStatus string    `json:"reflection_status"`
	Nudge            string    `json:"nudge"`
}
