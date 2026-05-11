// Package codereviewgraph is an optional enrichment surface that
// consumes the on-disk artifacts of the upstream tirth8205/code-review-graph
// project (https://github.com/tirth8205/code-review-graph).
//
// Schema assumption
// -----------------
// As of the project's README at the time of writing (May 2026), the
// upstream tool only documents that it persists a local SQLite file
// inside `.code-review-graph/`; the on-disk filenames and schema for
// the kind of summary data klyne wants to surface (high-risk files,
// recent blockers, frequent reviewers) are NOT publicly specified.
//
// To stay decoupled from any unstable internal schema, this package
// defines a small, OPTIONAL JSON contract that the upstream tool — or
// any third-party exporter — can drop alongside the SQLite database:
//
//	<projectRoot>/.code-review-graph/summary.json
//
// Shape (all fields optional, unknown fields ignored, snake_case):
//
//	{
//	  "high_risk_files":   ["path/to/file.go", "..."],
//	  "recent_blockers":   [
//	    { "title": "...", "url": "...", "severity": "high", "opened_at": "RFC3339" }
//	  ],
//	  "frequent_reviewers": ["alice", "bob"]
//	}
//
// When the directory exists but `summary.json` is absent or unreadable,
// `Load` still returns a non-nil ReviewGraph (with zero-valued slices)
// rather than an error — the contract is "consume what's there, no-op
// gracefully otherwise". A malformed `summary.json` (invalid JSON)
// IS surfaced as an error so users can spot configuration mistakes.
package codereviewgraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// DirName is the conventional directory name created by the upstream
// `code-review-graph` tool. Exported so callers can build their own
// paths consistently.
const DirName = ".code-review-graph"

// SummaryFile is the optional JSON file klyne reads for enrichment.
// See package docs for the schema and the rationale for this contract.
const SummaryFile = "summary.json"

// Blocker is a single recent reviewer-blocking issue surfaced by the
// upstream tool — typically an unresolved review comment, a failing
// gate, or an open follow-up PR. Fields beyond Title are best-effort:
// the upstream layout may omit them.
type Blocker struct {
	// Title is the short human-readable label. Required.
	Title string `json:"title"`
	// URL points at the source artifact (PR review, issue, etc.).
	// Empty when the upstream tool did not record one.
	URL string `json:"url,omitempty"`
	// Severity is a free-form label ("low" | "medium" | "high" |
	// "critical") — surfaced as-is so the UI/MCP consumer can format.
	Severity string `json:"severity,omitempty"`
	// OpenedAt is an RFC3339 timestamp. Empty when not recorded.
	OpenedAt string `json:"opened_at,omitempty"`
}

// ReviewGraph is the parsed view of `.code-review-graph/summary.json`.
// All slices are non-nil after a successful Load — callers may iterate
// without nil checks. Zero-length slices indicate "no data" while a
// nil *ReviewGraph indicates "directory absent / not detected".
type ReviewGraph struct {
	// HighRiskFiles is the set of files the upstream tool flagged as
	// historically risky (frequent regressions, hotspot churn, etc.).
	HighRiskFiles []string `json:"high_risk_files"`
	// RecentBlockers is the time-sorted list of currently-open review
	// blockers, newest first when the upstream layout supports it.
	RecentBlockers []Blocker `json:"recent_blockers"`
	// FrequentReviewers is the de-duped list of reviewer handles who
	// most often touch the project. Useful when picking a reviewer.
	FrequentReviewers []string `json:"frequent_reviewers"`
}

// Detect reports whether `<projectRoot>/.code-review-graph/` exists
// AND is non-empty. An empty directory is treated as "not detected"
// because callers want a definitive signal that there is enrichment
// data to consume.
//
// Returns false for any I/O error — the contract is "be silent when
// the upstream tool isn't installed". Genuine errors are not the
// caller's concern at detection time.
func Detect(projectRoot string) bool {
	if projectRoot == "" {
		return false
	}
	dir := filepath.Join(projectRoot, DirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

// Load parses the enrichment data from `<projectRoot>/.code-review-graph/`.
//
// Behavior matrix:
//
//	directory absent           → (nil, nil)   — graceful no-op
//	directory present, no JSON → (&empty, nil) — caller iterates zero slices
//	summary.json malformed     → (nil, error) — bubble up so users can fix
//	summary.json well-formed   → (parsed, nil)
//
// The directory-absent case intentionally returns a nil pointer rather
// than `&ReviewGraph{}` so callers can cheaply distinguish "tool not
// installed" from "tool installed but no findings".
func Load(projectRoot string) (*ReviewGraph, error) {
	if projectRoot == "" {
		return nil, errors.New("codereviewgraph: empty project root")
	}
	dir := filepath.Join(projectRoot, DirName)
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("codereviewgraph: stat dir: %w", err)
	}
	if !info.IsDir() {
		// A file with the conventional name is not the tool's output.
		// Treat it as "not detected" rather than an error.
		return nil, nil
	}

	rg := &ReviewGraph{
		HighRiskFiles:     []string{},
		RecentBlockers:    []Blocker{},
		FrequentReviewers: []string{},
	}

	summaryPath := filepath.Join(dir, SummaryFile)
	data, err := os.ReadFile(summaryPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Directory present but no schema-conformant summary file.
			// Return the empty enrichment so callers can still report
			// "directory detected, no findings".
			return rg, nil
		}
		return nil, fmt.Errorf("codereviewgraph: read summary: %w", err)
	}

	// Use a strict decoder so unknown fields don't silently drop;
	// however, the public contract says "unknown fields ignored", so
	// we use the lenient Unmarshal path. Malformed JSON still errors.
	if err := json.Unmarshal(data, rg); err != nil {
		return nil, fmt.Errorf("codereviewgraph: parse summary.json: %w", err)
	}

	// Normalize: keep slices non-nil so consumers can iterate without
	// nil checks (Go ranges over nil slices safely, but JSON encoders
	// emit `null` for nil — empty `[]` is a friendlier API surface).
	if rg.HighRiskFiles == nil {
		rg.HighRiskFiles = []string{}
	}
	if rg.RecentBlockers == nil {
		rg.RecentBlockers = []Blocker{}
	}
	if rg.FrequentReviewers == nil {
		rg.FrequentReviewers = []string{}
	}
	return rg, nil
}
