package worklog

func Score(e Entry) int {
	if e.Has(TagExplicitUserLog) {
		return 10
	}
	if e.Has(TagRoutineLintFix) {
		return 2
	}
	score := 5
	for _, t := range e.EventTags {
		switch t {
		case TagCommitLanded:
			score += 2
		case TagPROpened:
			score += 3
		case TagDecisionRecorded:
			score += 3
		case TagRunbookAccepted:
			score += 1
		case TagTestAddedOrChanged:
			score += 1
		case TagDependencyChange:
			score += 1
		case TagMigrationOrSchemaChange:
			score += 2
		case TagSecurityRelevantChange:
			score += 2
		case TagRuntimeConfigChange:
			score += 1
		case TagErrorResolved:
			score += 1
		case TagDebugLoopResolved:
			score += 1
		case TagRevertOrRollback:
			score += 2
		}
	}
	if score > 10 {
		score = 10
	}
	if score < 1 {
		score = 1
	}
	return score
}
