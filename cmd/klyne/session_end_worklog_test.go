package main

import (
	"testing"

	"github.com/klyne-ai/klyne/internal/worklog"
)

// TestDeriveEventTags asserts the deterministic event-tag classifier
// fires the right tags for the canonical git-commit-with-edits case
// over a security-relevant file.
func TestDeriveEventTags(t *testing.T) {
	s := sessionFixture{
		LastBash:       "git commit -m x",
		EditWriteCount: 4,
		Files:          []string{"src/auth.go"},
	}
	tags := deriveEventTags(s)
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
		in   sessionFixture
		want worklog.EventTag
	}{
		{
			name: "gh pr create triggers TagPROpened",
			in:   sessionFixture{LastBash: "gh pr create --title x", Files: nil},
			want: worklog.TagPROpened,
		},
		{
			name: "migrations/*.sql triggers TagMigrationOrSchemaChange",
			in:   sessionFixture{Files: []string{"db/migrations/001_init.sql"}},
			want: worklog.TagMigrationOrSchemaChange,
		},
		{
			name: ".proto triggers TagMigrationOrSchemaChange",
			in:   sessionFixture{Files: []string{"api/service.proto"}},
			want: worklog.TagMigrationOrSchemaChange,
		},
		{
			name: "go.mod triggers TagDependencyChange",
			in:   sessionFixture{Files: []string{"go.mod"}},
			want: worklog.TagDependencyChange,
		},
		{
			name: "package.json triggers TagDependencyChange",
			in:   sessionFixture{Files: []string{"web/package.json"}},
			want: worklog.TagDependencyChange,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tags := deriveEventTags(tc.in)
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
