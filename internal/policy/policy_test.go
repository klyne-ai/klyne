package policy_test

import (
	"testing"

	"github.com/klyne-ai/klyne/internal/policy"
)

// shippedPolicy is the exact 10-pattern JSON from policy/risky_commands.json,
// inlined here so the test is hermetic and doesn't depend on the file path
// being resolved at test time.
const shippedPolicy = `{
  "version": 1,
  "patterns": [
    {"id": "git-reset-hard",     "regex": "^git\\s+reset\\s+--hard",             "severity": "high",     "snapshot": true},
    {"id": "git-clean-fd",       "regex": "^git\\s+clean\\s+-fd",                "severity": "high",     "snapshot": true},
    {"id": "git-checkout-dot",   "regex": "^git\\s+checkout\\s+--\\s*\\.",        "severity": "high",     "snapshot": true},
    {"id": "git-restore-dot",    "regex": "^git\\s+restore\\s+(\\.|--source)",    "severity": "high",     "snapshot": true},
    {"id": "git-worktree-rm",    "regex": "^git\\s+worktree\\s+remove",           "severity": "medium",   "snapshot": true},
    {"id": "git-branch-D",       "regex": "^git\\s+branch\\s+-D\\s",              "severity": "medium",   "snapshot": true},
    {"id": "rm-rf-broad",        "regex": "^rm\\s+-rf\\s+(/|~|\\$HOME|\\.\\./)", "severity": "critical", "snapshot": true, "block_unless_confirm": true},
    {"id": "git-push-force-main","regex": "git\\s+push.*--force(\\s|$).*\\b(main|master|develop)\\b|git\\s+push.*\\b(main|master|develop)\\b.*--force(\\s|$)", "severity": "critical", "block_unless_confirm": true},
    {"id": "schema-drop",        "regex": "(DROP\\s+(TABLE|DATABASE|SCHEMA))",     "severity": "high",     "snapshot": true},
    {"id": "migration-rollback", "regex": "(rollback|down)\\s+(--all|--all-the-way|all)", "severity": "high", "snapshot": true}
  ]
}`

func mustLoad(t *testing.T) *policy.Matcher {
	t.Helper()
	m, err := policy.LoadJSON([]byte(shippedPolicy))
	if err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	return m
}

// truth table: one positive + two negative guards per pattern.
var matchCases = []struct {
	name      string
	command   string
	wantMatch bool
	wantID    string
}{
	// git-reset-hard
	{"git-reset-hard positive", "git reset --hard HEAD~3", true, "git-reset-hard"},
	{"git-reset-hard no-match soft", "git reset --soft HEAD", false, ""},
	{"git-reset-hard no-match unrelated", "git status", false, ""},

	// git-clean-fd
	{"git-clean-fd positive", "git clean -fd .", true, "git-clean-fd"},
	{"git-clean-fd no-match -f only", "git clean -f", false, ""},
	{"git-clean-fd no-match unrelated", "git fetch --dry-run", false, ""},

	// git-checkout-dot
	{"git-checkout-dot positive", "git checkout -- .", true, "git-checkout-dot"},
	{"git-checkout-dot no-match branch", "git checkout main", false, ""},
	{"git-checkout-dot no-match new-branch", "git checkout -b feature/foo", false, ""},

	// git-restore-dot
	{"git-restore-dot positive dot", "git restore .", true, "git-restore-dot"},
	{"git-restore-dot positive source", "git restore --source HEAD~1 file.go", true, "git-restore-dot"},
	{"git-restore-dot no-match staged", "git restore --staged file.go", false, ""},

	// git-worktree-rm
	{"git-worktree-rm positive", "git worktree remove ../tmp-tree", true, "git-worktree-rm"},
	{"git-worktree-rm no-match list", "git worktree list", false, ""},
	{"git-worktree-rm no-match add", "git worktree add ../new-tree", false, ""},

	// git-branch-D
	{"git-branch-D positive", "git branch -D old-feature", true, "git-branch-D"},
	{"git-branch-D no-match lowercase-d", "git branch -d old-feature", false, ""},
	{"git-branch-D no-match list", "git branch -a", false, ""},

	// rm-rf-broad
	{"rm-rf-broad positive root", "rm -rf /tmp/foo", true, "rm-rf-broad"},
	{"rm-rf-broad positive home tilde", "rm -rf ~/Downloads", true, "rm-rf-broad"},
	{"rm-rf-broad positive HOME var", "rm -rf $HOME/foo", true, "rm-rf-broad"},
	{"rm-rf-broad positive dotdot", "rm -rf ../sibling", true, "rm-rf-broad"},
	{"rm-rf-broad no-match relative safe", "rm -rf build/", false, ""},
	{"rm-rf-broad no-match no-r", "rm -f /tmp/foo", false, ""},

	// git-push-force-main
	{"git-push-force-main positive", "git push origin main --force", true, "git-push-force-main"},
	{"git-push-force-main positive master", "git push --force origin master", true, "git-push-force-main"},
	{"git-push-force-main no-match force-with-lease", "git push --force-with-lease origin main", false, ""},
	{"git-push-force-main no-match feature branch", "git push --force origin feature/foo", false, ""},

	// schema-drop
	{"schema-drop positive TABLE", "DROP TABLE users;", true, "schema-drop"},
	{"schema-drop positive DATABASE", "DROP DATABASE mydb;", true, "schema-drop"},
	{"schema-drop positive SCHEMA", "DROP SCHEMA public;", true, "schema-drop"},
	{"schema-drop no-match select", "SELECT * FROM users", false, ""},
	{"schema-drop no-match truncate", "TRUNCATE TABLE users", false, ""},

	// migration-rollback
	{"migration-rollback positive rollback all", "rollback --all", true, "migration-rollback"},
	{"migration-rollback positive down all", "down all", true, "migration-rollback"},
	{"migration-rollback positive all-the-way", "rollback --all-the-way", true, "migration-rollback"},
	{"migration-rollback no-match partial", "rollback --steps 2", false, ""},
	{"migration-rollback no-match migrate up", "migrate up", false, ""},
}

func TestMatch_TruthTable(t *testing.T) {
	m := mustLoad(t)
	for _, tc := range matchCases {
		t.Run(tc.name, func(t *testing.T) {
			got := m.Match(tc.command)
			if got.Matched != tc.wantMatch {
				t.Fatalf("Match(%q).Matched = %v, want %v", tc.command, got.Matched, tc.wantMatch)
			}
			if tc.wantMatch && got.PatternID != tc.wantID {
				t.Fatalf("Match(%q).PatternID = %q, want %q", tc.command, got.PatternID, tc.wantID)
			}
		})
	}
}

func TestLoadJSON_InvalidJSON(t *testing.T) {
	_, err := policy.LoadJSON([]byte(`{not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadJSON_BadRegex(t *testing.T) {
	bad := `{"version":1,"patterns":[{"id":"bad","regex":"[invalid","severity":"high"}]}`
	_, err := policy.LoadJSON([]byte(bad))
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestLoadFile_NotFound(t *testing.T) {
	_, err := policy.LoadFile("/no/such/file/risky_commands.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestMatch_Severity(t *testing.T) {
	m := mustLoad(t)
	r := m.Match("git reset --hard HEAD~1")
	if r.Severity != policy.SeverityHigh {
		t.Fatalf("severity = %q, want high", r.Severity)
	}
	r2 := m.Match("rm -rf /")
	if r2.Severity != policy.SeverityCritical {
		t.Fatalf("severity = %q, want critical", r2.Severity)
	}
	if !r2.BlockUnlessConfirm {
		t.Fatal("rm-rf-broad should have block_unless_confirm=true")
	}
}

func TestMatch_SnapshotFlag(t *testing.T) {
	m := mustLoad(t)
	// Snapshot=true patterns
	r := m.Match("git reset --hard HEAD")
	if !r.Snapshot {
		t.Fatal("git-reset-hard should have snapshot=true")
	}
	// git-push-force-main has no snapshot flag (only block)
	r2 := m.Match("git push origin main --force")
	if r2.Snapshot {
		t.Fatal("git-push-force-main should have snapshot=false")
	}
}
