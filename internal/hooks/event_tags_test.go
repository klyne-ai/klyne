package hooks

import (
	"testing"

	"github.com/klyne-ai/klyne/internal/worklog"
)

// TestDeriveEventTags asserts the deterministic event-tag classifier
// fires the right tags for the canonical git-commit-with-edits case
// over a security-relevant file.
//
// Lives next to internal/hooks/sessionend.go (the canonical
// implementation). Previously lived under cmd/klyne with a duplicate
// classifier; that duplication is the same drift pattern that broke
// ai_drafted_summary system-wide, so the implementation AND its tests
// are co-located here as a single source of truth.
func TestDeriveEventTags(t *testing.T) {
	s := SessionFixture{
		LastBash:       "git commit -m x",
		EditWriteCount: 4,
		Files:          []string{"src/auth.go"},
	}
	tags := DeriveEventTags(s)
	want := map[worklog.EventTag]bool{
		worklog.TagCommitLanded:            true,
		worklog.TagFileSignificantlyEdited: true,
		worklog.TagSecurityRelevantChange:  true,
	}
	for w := range want {
		found := false
		for _, got := range tags {
			if got == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing tag %s", w)
		}
	}
}

// TestDeriveEventTags_PRMigrationDependency covers the remaining
// classifier branches: gh pr create → TagPROpened, migrations/*.sql →
// TagMigrationOrSchemaChange, go.mod / package.json → TagDependencyChange.
func TestDeriveEventTags_PRMigrationDependency(t *testing.T) {
	cases := []struct {
		name string
		in   SessionFixture
		want worklog.EventTag
	}{
		{
			name: "gh pr create triggers TagPROpened",
			in:   SessionFixture{LastBash: "gh pr create --title x", Files: nil},
			want: worklog.TagPROpened,
		},
		{
			name: "migrations/*.sql triggers TagMigrationOrSchemaChange",
			in:   SessionFixture{Files: []string{"db/migrations/001_init.sql"}},
			want: worklog.TagMigrationOrSchemaChange,
		},
		{
			name: ".proto triggers TagMigrationOrSchemaChange",
			in:   SessionFixture{Files: []string{"api/service.proto"}},
			want: worklog.TagMigrationOrSchemaChange,
		},
		{
			name: "go.mod triggers TagDependencyChange",
			in:   SessionFixture{Files: []string{"go.mod"}},
			want: worklog.TagDependencyChange,
		},
		{
			name: "package.json triggers TagDependencyChange",
			in:   SessionFixture{Files: []string{"web/package.json"}},
			want: worklog.TagDependencyChange,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tags := DeriveEventTags(tc.in)
			found := false
			for _, got := range tags {
				if got == tc.want {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("want %s in tags, got %v", tc.want, tags)
			}
		})
	}
}
