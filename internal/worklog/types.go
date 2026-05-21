// Package worklog implements the cross-AI worklog feature: memory capture,
// surfaces, and reflection synthesis. See docs/research/worklog/.
package worklog

import "time"

type EventTag string

const (
	TagCommitLanded            EventTag = "commit_landed"
	TagPROpened                EventTag = "pr_opened"
	TagDecisionRecorded        EventTag = "decision_recorded"
	TagRunbookAccepted         EventTag = "runbook_accepted"
	TagFileSignificantlyEdited EventTag = "file_significantly_edited"
	TagTestAddedOrChanged      EventTag = "test_added_or_changed"
	TagDependencyChange        EventTag = "dependency_change"
	TagMigrationOrSchemaChange EventTag = "migration_or_schema_change"
	TagSecurityRelevantChange  EventTag = "security_relevant_change"
	TagRuntimeConfigChange     EventTag = "runtime_config_change"
	TagErrorResolved           EventTag = "error_resolved"
	TagDebugLoopResolved       EventTag = "debug_loop_resolved"
	TagRoutineLintFix          EventTag = "routine_lint_fix"
	TagRevertOrRollback        EventTag = "revert_or_rollback"
	TagExplicitUserLog         EventTag = "explicit_user_log"
)

type Entry struct {
	SessionID        string
	TS               time.Time
	ProjectPath      string
	CLI              string // "claude" or "codex"
	LastUser         string
	LastBash         string
	Files            []string
	CommitSHA        string
	WallTime         time.Duration
	ToolCallCount    int
	EditWriteCount   int
	EventTags        []EventTag
	ExplicitUserText string
	// AIDraftedSummary is the per-turn prose summary the assistant
	// emitted via the `KLYNE_SUMMARY: <text>` instruction injected by
	// the UserPromptSubmit hook. Empty when the assistant skipped or
	// when the line couldn't be parsed. The Stop hook populates this
	// from the assistant's last message; the daemon NEVER drafts it.
	AIDraftedSummary string
}

func (e Entry) Has(t EventTag) bool {
	for _, x := range e.EventTags {
		if x == t {
			return true
		}
	}
	return false
}
