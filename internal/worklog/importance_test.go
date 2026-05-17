package worklog

import "testing"

func TestScore(t *testing.T) {
	cases := []struct {
		name string
		tags []EventTag
		want int
	}{
		{"baseline", nil, 5},
		{"lint fix capped low", []EventTag{TagCommitLanded, TagRoutineLintFix}, 2},
		{"plain commit", []EventTag{TagCommitLanded}, 7},
		{"security+commit", []EventTag{TagCommitLanded, TagSecurityRelevantChange}, 9},
		{"decision", []EventTag{TagDecisionRecorded}, 8},
		{"explicit always max", []EventTag{TagExplicitUserLog}, 10},
		{"PR+security+migration caps at 10", []EventTag{TagPROpened, TagSecurityRelevantChange, TagMigrationOrSchemaChange}, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Score(Entry{EventTags: c.tags}); got != c.want {
				t.Errorf("Score(%v) = %d, want %d", c.tags, got, c.want)
			}
		})
	}
}

func BenchmarkScore(b *testing.B) {
	e := Entry{EventTags: []EventTag{TagCommitLanded, TagSecurityRelevantChange, TagTestAddedOrChanged}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Score(e)
	}
}
