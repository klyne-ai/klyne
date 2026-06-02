package worklog

import (
	"strings"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/productivity"
)

// fixtureReport builds a productivity.Report covering two repos on
// 2026-05-19: one with unpushed local work + a done-uncommitted risk,
// one merged. Used as the deterministic substrate for the worklog
// improvement tests so they never shell out to git.
func fixtureReport() productivity.Report {
	d := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	return productivity.Report{
		Day: "2026-05-19",
		Services: []productivity.Service{
			{
				Repo:        "consult-service",
				ProjectPath: "/repos/consult-service",
				Branches: []productivity.Branch{
					{
						Name:     "feat/CLI-1396-pipeline",
						TicketID: "CLI-1396",
						Ship:     productivity.ShipLocal,
						Ahead:    9,
						Commits: []productivity.Commit{
							{SHA: "aaaaaaaa", Subject: "wire pipeline entrypoint", CommittedAt: d.Add(9 * time.Hour), IsUser: true, Files: 3, Insertions: 120, Deletions: 4},
							{SHA: "bbbbbbbb", Subject: "extractReportIdFromLink", CommittedAt: d.Add(10 * time.Hour), IsUser: true, Files: 2, Insertions: 40, Deletions: 2},
						},
						AttributedMinutes: 130,
						Narrative:         "2 commits on feat/CLI-1396-pipeline",
					},
				},
				Risks: []productivity.RiskSignal{
					{Kind: "unpushed", Detail: "9 commit(s) ahead, not on origin", AgeMinutes: 240},
					{Kind: "done-uncommitted", Detail: "2 uncommitted file(s) since last session ended", AgeMinutes: 20},
				},
			},
			{
				Repo:        "orders-service",
				ProjectPath: "/repos/orders-service",
				Branches: []productivity.Branch{
					{
						Name:     "main",
						Ship:     productivity.ShipMerged,
						Commits: []productivity.Commit{
							{SHA: "12345678", Subject: "merge refund work", CommittedAt: d.Add(8 * time.Hour), IsUser: true, Files: 1, Insertions: 5},
						},
						AttributedMinutes: 30,
						Narrative:         "1 commit on main",
					},
				},
			},
		},
	}
}

// Improvement 1: Open Loops / Unpushed block.
func TestGitSubstrateSections_OpenLoopsBlock(t *testing.T) {
	rep := fixtureReport()
	secs := GitSubstrateSections(rep, "/repos/consult-service")
	md := strings.Join(secs, "\n")
	if !strings.Contains(md, "Open Loops") {
		t.Errorf("expected an Open Loops block, got:\n%s", md)
	}
	if !strings.Contains(md, "feat/CLI-1396-pipeline") {
		t.Errorf("open-loops block must name the unpushed branch, got:\n%s", md)
	}
	if !strings.Contains(md, "9 commit") {
		t.Errorf("open-loops block must report the ahead count, got:\n%s", md)
	}
	if !strings.Contains(md, "done-uncommitted") && !strings.Contains(md, "uncommitted file") {
		t.Errorf("open-loops block must surface the done-uncommitted risk, got:\n%s", md)
	}
}

// Improvement 1: a repo with no risks gets no Open Loops section
// (degrade gracefully — the section is only emitted when there is
// something to report).
func TestGitSubstrateSections_NoOpenLoopsWhenClean(t *testing.T) {
	rep := fixtureReport()
	secs := GitSubstrateSections(rep, "/repos/orders-service")
	md := strings.Join(secs, "\n")
	if strings.Contains(md, "Open Loops") {
		t.Errorf("orders-service has no risks — Open Loops block must be omitted, got:\n%s", md)
	}
}

// Improvement 5: terse per-repo "Shipped" ledger line from ship state.
func TestGitSubstrateSections_ShippedLedger(t *testing.T) {
	rep := fixtureReport()
	secs := GitSubstrateSections(rep, "/repos/orders-service")
	md := strings.Join(secs, "\n")
	if !strings.Contains(md, "Shipped") {
		t.Errorf("expected a Shipped ledger line, got:\n%s", md)
	}
	if !strings.Contains(md, "merged-to-default") {
		t.Errorf("shipped ledger must quote the ship state verbatim, got:\n%s", md)
	}
	if !strings.Contains(md, "12345678") {
		t.Errorf("shipped ledger must cite a short sha, got:\n%s", md)
	}
}

// Improvement 3: reflection dated/attributed by commit committed_at, not
// session start. The substrate sections must surface the commit date.
func TestGitSubstrateSections_CommitTimestampDating(t *testing.T) {
	rep := fixtureReport()
	secs := GitSubstrateSections(rep, "/repos/consult-service")
	md := strings.Join(secs, "\n")
	// 2026-05-19 is the day; the latest commit was at 10:00 UTC.
	if !strings.Contains(md, "2026-05-19") {
		t.Errorf("substrate must date work by commit committed_at, got:\n%s", md)
	}
}

// crossRepoFixture builds a report where the SAME ticket token
// (CLI-1396) appears on branches in two different repos on the same day
// — the cross-project initiative thread case (improvement 7).
func crossRepoFixture() productivity.Report {
	d := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	mk := func(repo, pp, branch, ticket string) productivity.Service {
		return productivity.Service{
			Repo: repo, ProjectPath: pp,
			Branches: []productivity.Branch{{
				Name: branch, TicketID: ticket, Ship: productivity.ShipLocal,
				Commits: []productivity.Commit{
					{SHA: "sha" + repo[:5], Subject: "work on " + ticket, CommittedAt: d.Add(9 * time.Hour), IsUser: true, Insertions: 50},
				},
			}},
		}
	}
	return productivity.Report{
		Day: "2026-05-19",
		Services: []productivity.Service{
			mk("consult-service", "/repos/consult-service", "feat/CLI-1396-pipeline", "CLI-1396"),
			mk("orders-service", "/repos/orders-service", "feat/CLI-1396-refund", "CLI-1396"),
			mk("klyne", "/repos/klyne", "feat/unrelated", ""),
		},
	}
}

// Improvement 7: when one ticket token spans multiple repos on the same
// day, CrossProjectThread emits one umbrella entry linking them.
func TestCrossProjectThread_UmbrellaWhenTicketSpansRepos(t *testing.T) {
	rep := crossRepoFixture()
	threads := CrossProjectThread(rep)
	if len(threads) != 1 {
		t.Fatalf("expected 1 umbrella thread for CLI-1396, got %d: %v", len(threads), threads)
	}
	th := threads[0]
	if !strings.Contains(th, "CLI-1396") {
		t.Errorf("umbrella entry must name the shared ticket token, got: %q", th)
	}
	if !strings.Contains(th, "consult-service") || !strings.Contains(th, "orders-service") {
		t.Errorf("umbrella entry must link both repos, got: %q", th)
	}
}

// Improvement 7: a ticket that touches only ONE repo is not an
// initiative thread — no umbrella entry.
func TestCrossProjectThread_NoUmbrellaForSingleRepoTicket(t *testing.T) {
	rep := fixtureReport() // CLI-1396 only in consult-service
	threads := CrossProjectThread(rep)
	for _, th := range threads {
		if strings.Contains(th, "CLI-1396") {
			t.Errorf("CLI-1396 touches one repo only — must not get an umbrella entry, got: %q", th)
		}
	}
}

// salienceFixture: one repo, two branches — a 3-commit high-impact
// feature branch and a 1-commit trivial docs branch, plus a bot commit
// that the productivity layer would have filtered out (it never reaches
// a Branch). Used for the salience-gate and grounding-prompt tests.
func salienceFixture() productivity.Report {
	d := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	return productivity.Report{
		Day: "2026-05-19",
		Services: []productivity.Service{{
			Repo:        "svc",
			ProjectPath: "/repos/svc",
			Branches: []productivity.Branch{
				{
					Name: "docs/cleanup", Ship: productivity.ShipLocal,
					Commits: []productivity.Commit{
						{SHA: "d0000000", Subject: "tidy README", CommittedAt: d.Add(8 * time.Hour), IsUser: true, Insertions: 3},
					},
				},
				{
					Name: "feat/CLI-9-core", TicketID: "CLI-9", Ship: productivity.ShipLocal,
					Commits: []productivity.Commit{
						{SHA: "f1000000", Subject: "core pipeline a", CommittedAt: d.Add(9 * time.Hour), IsUser: true, Insertions: 200},
						{SHA: "f2000000", Subject: "core pipeline b", CommittedAt: d.Add(10 * time.Hour), IsUser: true, Insertions: 150},
						{SHA: "f3000000", Subject: "core pipeline c", CommittedAt: d.Add(11 * time.Hour), IsUser: true, Insertions: 80},
					},
				},
			},
		}},
	}
}

// Improvement 2: the salience gate. The git-facts block fed to the LLM
// must LEAD with the highest-code-impact branch, not chronological or
// trivial work.
func TestSalienceRankedFacts_LeadsWithHighestImpact(t *testing.T) {
	rep := salienceFixture()
	facts := SalienceRankedFacts(rep, "/repos/svc")
	if facts == "" {
		t.Fatal("expected a non-empty salience-ranked facts block")
	}
	idxFeat := strings.Index(facts, "feat/CLI-9-core")
	idxDocs := strings.Index(facts, "docs/cleanup")
	if idxFeat < 0 || idxDocs < 0 {
		t.Fatalf("facts block must mention both branches, got:\n%s", facts)
	}
	if idxFeat > idxDocs {
		t.Errorf("salience gate: high-impact feat/CLI-9-core (3 commits) must lead docs/cleanup (1 commit), got:\n%s", facts)
	}
}

// Improvement 6: the author/identity filter. The git facts are sourced
// from productivity.Service.Branches, which carry only IsUser commits —
// so a bot/co-actor SHA can never appear in the facts block. This test
// proves the substrate path is identity-filtered by construction.
func TestSalienceRankedFacts_ExcludesNonUserCommits(t *testing.T) {
	rep := salienceFixture()
	// Inject a bot commit as if it had leaked through — it must not be
	// surfaced because the substrate Branch only ever holds IsUser
	// commits; SalienceRankedFacts must additionally drop any IsUser=false
	// commit defensively.
	rep.Services[0].Branches[1].Commits = append(rep.Services[0].Branches[1].Commits,
		productivity.Commit{SHA: "b0700000", Subject: "Jenkins: bump build", IsUser: false})
	facts := SalienceRankedFacts(rep, "/repos/svc")
	if strings.Contains(facts, "b0700000") || strings.Contains(facts, "Jenkins") {
		t.Errorf("identity filter: bot commit must be excluded from facts, got:\n%s", facts)
	}
	// The real user commits are still there.
	if !strings.Contains(facts, "f1000000") {
		t.Errorf("user commits must remain in facts, got:\n%s", facts)
	}
}

// Improvement 2 (prompt-contract half): the grounding contract text
// states the §7.1 rules the LLM must follow, including the salience rule
// and the no-invented-PR rule.
func TestGroundingContractText_StatesKeyRules(t *testing.T) {
	c := GroundingContractText()
	for _, want := range []string{"salience", "PR", "sha", "verbatim"} {
		if !strings.Contains(strings.ToLower(c), strings.ToLower(want)) {
			t.Errorf("grounding contract must mention %q, got:\n%s", want, c)
		}
	}
}

// Improvement 4: the unverified-artifact-ID guard. A "PR #<n>" not
// backed by any branch name or commit message is the documented
// "PR #57" hallucination — it must be stripped/flagged.
func TestSanitizeArtifactIDs(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		evidence []string
		// wantClean asserts the result no longer contains the unverified
		// reference verbatim; wantKept asserts a backed reference survives.
		wantStripped []string
		wantKept     []string
		wantFlagged  bool
	}{
		{
			name:         "unverified PR number is stripped",
			body:         "Shipped the refund pipeline via PR #57.",
			evidence:     []string{"feat/CLI-1396-pipeline", "wire pipeline entrypoint"},
			wantStripped: []string{"PR #57"},
			wantFlagged:  true,
		},
		{
			name:         "PR number present in a commit message is kept",
			body:         "Merged PR #49 into main.",
			evidence:     []string{"main", "Merge pull request PR #49 from feat/x"},
			wantKept:     []string{"PR #49"},
			wantFlagged:  false,
		},
		{
			name:         "PR number present in a branch name is kept",
			body:         "Continued work, see PR #88.",
			evidence:     []string{"feature/pr-88-cleanup", "tidy imports"},
			wantKept:     []string{"PR #88"},
			wantFlagged:  false,
		},
		{
			name:         "clean body with no PR refs is untouched",
			body:         "Refactored the auth layer.",
			evidence:     []string{"feat/auth", "refactor auth"},
			wantStripped: nil,
			wantFlagged:  false,
		},
		{
			name:         "multiple unverified refs all stripped",
			body:         "Did PR #1 and PR #2 today.",
			evidence:     []string{"main", "some commit"},
			wantStripped: []string{"PR #1", "PR #2"},
			wantFlagged:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, flagged := SanitizeArtifactIDs(tt.body, tt.evidence)
			for _, s := range tt.wantStripped {
				if strings.Contains(got, s) {
					t.Errorf("expected %q stripped, still present in: %q", s, got)
				}
			}
			for _, k := range tt.wantKept {
				if !strings.Contains(got, k) {
					t.Errorf("expected backed ref %q kept, missing from: %q", k, got)
				}
			}
			if flagged != tt.wantFlagged {
				t.Errorf("flagged = %v; want %v (body=%q)", flagged, tt.wantFlagged, got)
			}
		})
	}
}
