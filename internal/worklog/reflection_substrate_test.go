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
				Repo:        "consultation-service",
				ProjectPath: "/repos/consultation-service",
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
				Repo:        "oms-service",
				ProjectPath: "/repos/oms-service",
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
	secs := GitSubstrateSections(rep, "/repos/consultation-service")
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
	secs := GitSubstrateSections(rep, "/repos/oms-service")
	md := strings.Join(secs, "\n")
	if strings.Contains(md, "Open Loops") {
		t.Errorf("oms-service has no risks — Open Loops block must be omitted, got:\n%s", md)
	}
}

// Improvement 5: terse per-repo "Shipped" ledger line from ship state.
func TestGitSubstrateSections_ShippedLedger(t *testing.T) {
	rep := fixtureReport()
	secs := GitSubstrateSections(rep, "/repos/oms-service")
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
	secs := GitSubstrateSections(rep, "/repos/consultation-service")
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
			mk("consultation-service", "/repos/consultation-service", "feat/CLI-1396-pipeline", "CLI-1396"),
			mk("oms-service", "/repos/oms-service", "feat/CLI-1396-refund", "CLI-1396"),
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
	if !strings.Contains(th, "consultation-service") || !strings.Contains(th, "oms-service") {
		t.Errorf("umbrella entry must link both repos, got: %q", th)
	}
}

// Improvement 7: a ticket that touches only ONE repo is not an
// initiative thread — no umbrella entry.
func TestCrossProjectThread_NoUmbrellaForSingleRepoTicket(t *testing.T) {
	rep := fixtureReport() // CLI-1396 only in consultation-service
	threads := CrossProjectThread(rep)
	for _, th := range threads {
		if strings.Contains(th, "CLI-1396") {
			t.Errorf("CLI-1396 touches one repo only — must not get an umbrella entry, got: %q", th)
		}
	}
}
