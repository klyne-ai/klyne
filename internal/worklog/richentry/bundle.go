package richentry

import (
	"fmt"
	"strings"
	"time"
)

// MergedPRRef is one merged PR the writer hands the LLM so it can
// pair PR numbers with their merge commit short-sha (the gold
// narrative format: "#400" + "e9a1b2a" cited together). Sourced
// from the migration-018 gh PR cache at writer-time.
type MergedPRRef struct {
	Number   int
	Title    string
	MergedAt time.Time
	MergeSHA string // short SHA of the merge commit on origin/main
}

// BundleInputs is the raw side-channel the writer assembles for one
// stop_summaries row (one turn). Every field comes from a real source
// the daemon has on hand at session-end:
//   - StopSummaryBody / UserMessages — from stop_summaries
//   - Commits — from `git log` filtered to user-identity + window
//   - MergedPRs — from migration-018 gh PR cache for this repo+window
//   - ActiveIntervals — from the productivity package's gap-capped intervals
//   - SessionFiles — unpacked from stop_summaries.files_json
//   - PriorSessionIDs — rare; only when this turn explicitly continues
//     from another klyne session (writer leaves nil otherwise)
//
// MergedPRs == nil is the explicit "no gh cache lookup happened"
// signal — propagates to Allowlist.PRCacheLive=false so the validator
// skips PR-number checks (avoids rejecting an entry on a fresh repo
// where we can't verify PR refs at all).
type BundleInputs struct {
	SessionID    string
	Ts           time.Time
	ProjectPath  string
	RepoName     string
	BranchName   string
	BranchTicket string

	StopSummaryBody string
	UserMessages    []string
	Commits         []CommitRef
	MergedPRs       []MergedPRRef // nil = no cache lookup; []{} = lookup returned zero
	ActiveIntervals []Interval
	SessionFiles    []string
	PriorSessionIDs []string
}

// Bundle is what the writer's LLM call needs: the rendered prompt
// AND the allowlist the validator will check the LLM's reply against.
// Both are derived from the same BundleInputs so they cannot drift.
type Bundle struct {
	Prompt    string
	Allowlist Allowlist
}

// BuildBundle assembles the writer's per-turn prompt + matching
// validator allowlist from raw inputs. Pure function — no I/O.
func BuildBundle(in BundleInputs) Bundle {
	prNums, prCacheLive := prNumbersAndLiveFlag(in.MergedPRs)
	branches := []string{in.BranchName}
	al := buildAllowlistFlexible(
		in.Commits, prNums, prCacheLive, branches,
		in.SessionFiles, in.ActiveIntervals, in.PriorSessionIDs,
	)
	// Seed every PR's merge commit short-sha into the SHA allowlist —
	// the gold pairs "#400" with "e9a1b2a" but the merge commit lives
	// on origin/main, not in the turn's Commits[]. Without this, the
	// writer can't cite the merge sha and validation fails.
	for _, p := range in.MergedPRs {
		if s := strings.TrimSpace(p.MergeSHA); s != "" {
			al.ShortSHAs[s] = true
		}
	}
	return Bundle{
		Prompt:    renderPrompt(in),
		Allowlist: al,
	}
}

// prNumbersAndLiveFlag mirrors BuildAllowlist's contract: nil
// MergedPRs → live=false (skip PR check); empty slice → live=true
// with zero allowed PRs.
func prNumbersAndLiveFlag(prs []MergedPRRef) (nums []int, live bool) {
	if prs == nil {
		return nil, false
	}
	out := make([]int, 0, len(prs))
	for _, p := range prs {
		out = append(out, p.Number)
	}
	return out, true
}

// buildAllowlistFlexible exists because BuildAllowlist signals
// PRCacheLive solely by `prs != nil`, but the writer wants to set
// the flag explicitly (e.g. for tests where prs == empty slice).
// Builds the same struct BuildAllowlist would, then overrides
// PRCacheLive.
func buildAllowlistFlexible(
	cmts []CommitRef, prs []int, prCacheLive bool, branches []string,
	files []string, intervals []Interval, priorSessionIDs []string,
) Allowlist {
	// Pass nil to BuildAllowlist to keep its PRCacheLive=false default;
	// we set PRNumbers + PRCacheLive ourselves below for the explicit case.
	al := BuildAllowlist(cmts, nil, branches, files, intervals, priorSessionIDs)
	al.PRCacheLive = prCacheLive
	if prCacheLive {
		al.PRNumbers = map[string]bool{}
		for _, n := range prs {
			al.PRNumbers[fmt.Sprintf("#%d", n)] = true
		}
	}
	return al
}

// renderPrompt formats the writer prompt with the bundle's inputs
// interpolated. The template body is the prompt locked in Phase 0.3
// (`docs/superpowers/specs/2026-05-20-writer-prompt-v1.md`); kept
// inline here as a Go const so the writer is self-contained and the
// prompt evolves under git history alongside its tests.
//
// Format is plain string substitution rather than text/template
// because (a) the prompt has many `{` / `}` chars that would confuse
// template parsing, and (b) substitution is trivial and matches the
// Phase 0.3 doc 1:1.
func renderPrompt(in BundleInputs) string {
	tsLocal := in.Ts.Local().Format("2006-01-02 15:04 -0700")

	var sb strings.Builder
	sb.Grow(len(writerPromptHeader) + 2048)
	sb.WriteString(writerPromptHeader)
	sb.WriteString("\n\nINPUT BUNDLE for this turn:\n\n")

	sb.WriteString("[turn]\n")
	fmt.Fprintf(&sb, "session_id    = %s\n", in.SessionID)
	fmt.Fprintf(&sb, "ts            = %s\n", tsLocal)
	fmt.Fprintf(&sb, "project_path  = %s\n", in.ProjectPath)
	fmt.Fprintf(&sb, "repo          = %s\n", in.RepoName)
	fmt.Fprintf(&sb, "branch        = %s\n", in.BranchName)
	fmt.Fprintf(&sb, "branch_ticket = %s\n", in.BranchTicket)
	sb.WriteString("\n[stop_summary_body]\n")
	sb.WriteString(in.StopSummaryBody)
	if !strings.HasSuffix(in.StopSummaryBody, "\n") {
		sb.WriteString("\n")
	}

	sb.WriteString("\n[user_messages_in_this_turn]\n")
	if len(in.UserMessages) == 0 {
		sb.WriteString("(none)\n")
	}
	for i, m := range in.UserMessages {
		fmt.Fprintf(&sb, "(%d) %s\n", i+1, m)
	}

	sb.WriteString("\n[commits_in_this_turn]\n")
	if len(in.Commits) == 0 {
		sb.WriteString("(none)\n")
	}
	for _, c := range in.Commits {
		// short_sha · committed_at · files (capped at 6 for prompt brevity)
		when := ""
		if !c.CommittedAt.IsZero() {
			when = c.CommittedAt.Local().Format("15:04")
		}
		fmt.Fprintf(&sb, "- %s · %s · files: %s\n", c.SHA, when, truncFiles(c.Files, 6))
	}

	sb.WriteString("\n[merged_prs_in_this_repo_today]\n")
	if in.MergedPRs == nil {
		sb.WriteString("(no gh cache lookup; PR-number validation will be SKIPPED for this turn)\n")
	} else if len(in.MergedPRs) == 0 {
		sb.WriteString("(none)\n")
	}
	for _, p := range in.MergedPRs {
		fmt.Fprintf(&sb, "- #%d · merge=%s · merged_at=%s · title=%s\n",
			p.Number, p.MergeSHA, p.MergedAt.Local().Format("15:04"), p.Title)
	}

	sb.WriteString("\n[active_intervals]\n")
	if len(in.ActiveIntervals) == 0 {
		sb.WriteString("(none)\n")
	}
	for _, iv := range in.ActiveIntervals {
		fmt.Fprintf(&sb, "- %s -> %s\n",
			iv.Start.Local().Format("15:04"), iv.End.Local().Format("15:04"))
	}

	sb.WriteString("\n[touched_files_session_level]\n")
	if len(in.SessionFiles) == 0 {
		sb.WriteString("(none)\n")
	}
	for _, f := range in.SessionFiles {
		fmt.Fprintf(&sb, "- %s\n", f)
	}

	sb.WriteString("\n[prior_session_ids]\n")
	if len(in.PriorSessionIDs) == 0 {
		sb.WriteString("(none)\n")
	}
	for _, id := range in.PriorSessionIDs {
		fmt.Fprintf(&sb, "- %s\n", id)
	}

	sb.WriteString("\nEmit the JSON now. Nothing else.\n")
	return sb.String()
}

// truncFiles caps the displayed files list at n entries to keep the
// prompt bounded; longer lists get a "+N more" tail.
func truncFiles(files []string, n int) string {
	if len(files) <= n {
		return strings.Join(files, ", ")
	}
	head := strings.Join(files[:n], ", ")
	return fmt.Sprintf("%s, +%d more", head, len(files)-n)
}

// writerPromptHeader is the static portion of the writer prompt —
// instructions, output contract, category meanings. The bundle
// inputs are appended below it by renderPrompt. Kept verbatim from
// `docs/superpowers/specs/2026-05-20-writer-prompt-v1.md` so the
// spec and the code agree on every byte; future revisions update
// both files in the same commit.
const writerPromptHeader = `You are summarising one development-session turn into a structured
worklog entry that will join other turns' entries into a daily
"What was done" timeline. You must be CONCRETE, GROUNDED, and BRIEF.

Anti-hallucination rule: every value in any ` + "`refs`" + ` array MUST appear
in one of the input bundles below. If you cannot find a backing
reference for a claim, OMIT the claim. Never invent SHAs, PRs,
tickets, paths, durations, clock times, or UUIDs. UUIDs are rejected
unless they appear in INPUT[prior_session_ids] AND you cite each
one in at most ONE bullet.

Output contract: emit exactly ONE JSON object with this shape and
no prose around it. Every category MUST be present (use [] if
empty). All 15 categories listed below — do not invent new ones.

{
  "schema_version": 1,
  "categories": {
    "features_worked_on":  [], "features_picked":  [], "shipped":   [],
    "bugs_found":          [], "bugs_fixed":       [],
    "investigations":      [], "decisions":        [], "config_changes": [],
    "blockers":            [], "blocked_on":       [], "pending":     [],
    "followups_for_others":[], "must_remember":    [], "mistakes_or_dead_ends": [],
    "reviews_given":       []
  }
}

Item shape (uniform across all 15 categories):
  { "summary":"...", "repo":"...?", "refs":["...","..."], "ticket":"...?" }

Category meanings — these are close cousins; pick the most specific
one that fits, never two:

- features_worked_on  : code in-progress on an EXISTING feature thread
- features_picked     : a NEW thread started today
- shipped             : a PR merged or a branch pushed that reaches users
- bugs_found          : a defect identified today (whether fixed or not)
- bugs_fixed          : a defect RESOLVED today (cite the fix commit)
- investigations      : non-shipping research / spikes / reading code
- decisions           : a process or architecture CHOICE made today
- config_changes      : env / settings / infra / dependency tweaks
- blockers            : I am stuck waiting on a problem I have to solve
- blocked_on          : waiting on an external party (vendor, team)
- pending             : parked by my own choice for tomorrow
- followups_for_others: a teammate needs to act because of my work
- must_remember       : a non-obvious fact future-me needs (NOT a choice)
- mistakes_or_dead_ends: tried something that didn't work + takeaway
- reviews_given       : PRs I reviewed for others (NOT my own PRs)

A bugs_found and a bugs_fixed MAY both fire for the same defect
when it was found and fixed the same day — emit both, with the same
ticket if any, the find item in bugs_found, the fix-commit in
bugs_fixed.

Style:
- summaries are ONE LINE, concrete, no fluff
- prefer naming the WHAT (the lab-orders payload chh_no field) over
  the WHERE (modified labOrderHelpers.ts)
- pair every PR # with its merge commit short-sha in the same refs[]
- preserve ticket IDs from branch names when present (CLI-NNNN)
- empty categories stay [] — never omit a category`
