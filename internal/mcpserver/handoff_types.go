// Package mcpserver — handoff_types.go
//
// Public-shaped output types for the hybrid handoff. Split from
// handoff.go so tests can import the types without dragging in
// the renderer's heavier transitive dependencies.

package mcpserver

// AnchorFileRef is one row of the deterministic "anchor files"
// list. Fields cover what the next session needs to triage: the
// path, whether it has uncommitted edits, how long since it was
// last touched in this session.
type AnchorFileRef struct {
	Path         string  `json:"path" jsonschema:"absolute or repo-relative file path"`
	Dirty        bool    `json:"dirty,omitempty" jsonschema:"true when the file has uncommitted git changes at snapshot-load time"`
	DirtyUnknown bool    `json:"dirty_unknown,omitempty" jsonschema:"true when git status could not be captured (no repo, git missing, etc)"`
	LastTouchAgo string  `json:"last_touch_ago,omitempty" jsonschema:"human-readable relative time since the last Read/Edit, e.g. '4m', '1h', '2d'"`
	Score        float64 `json:"score,omitempty" jsonschema:"contexthealth relevance score in [0,1]"`
}

// TicketHint is one regex-mined ticket key (e.g. CLI-1362) plus
// where we found it. Mentions counts only user-turn occurrences;
// FromURL is true when the key also appears inside a pasted URL.
type TicketHint struct {
	Key      string `json:"key" jsonschema:"ticket / issue key matching [A-Z]{2,}-[0-9]+"`
	Mentions int    `json:"mentions" jsonschema:"count of user-turn appearances"`
	FromURL  bool   `json:"from_url,omitempty" jsonschema:"true when the key also appears inside a pasted URL"`
}

// PlanOfRecordRef points at the most-recently-read planning doc
// in the session (paths matching `**/plans/*.md`, `**/research/**/*.md`,
// or `**/00-plan.md`). Nil on Skeleton when no such file was read.
type PlanOfRecordRef struct {
	Path         string `json:"path" jsonschema:"path of the most-recently-read planning file"`
	ReadCount    int    `json:"read_count" jsonschema:"number of times the file was Read/Edit/Viewed in this session"`
	LastTouchAgo string `json:"last_touch_ago,omitempty" jsonschema:"human-readable relative time since the last touch"`
}

// TodoItem is one row out of the most recent TodoWrite tool call.
// Status is one of "in_progress", "pending", "completed".
type TodoItem struct {
	Content string `json:"content" jsonschema:"todo item text"`
	Status  string `json:"status" jsonschema:"in_progress | pending | completed"`
}

// Skeleton is the structured form of the deterministic handoff.
// Markdown (on HandoffOutput) is the rendered form of this same
// data; Skeleton lets the slashcommand and any programmatic
// consumer pull individual fields without re-parsing markdown.
type Skeleton struct {
	Branch          string           `json:"branch,omitempty"`
	PlanOfRecord    *PlanOfRecordRef `json:"plan_of_record,omitempty"`
	AnchorFiles     []AnchorFileRef  `json:"anchor_files,omitempty"`
	StaleFilesCount int              `json:"stale_files_count,omitempty" jsonschema:"count of touched files relegated to the collapsed tail"`
	LikelyTickets   []TicketHint     `json:"likely_tickets,omitempty"`
	LinkedURLs      []string         `json:"linked_urls,omitempty"`
	InProgressTodos []TodoItem       `json:"in_progress_todos,omitempty"`
	PendingTodos    []TodoItem       `json:"pending_todos,omitempty"`
	KnownBlockers   []string         `json:"known_blockers,omitempty"`
}
