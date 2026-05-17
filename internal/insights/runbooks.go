package insights

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
	"github.com/klyne-ai/klyne/internal/store"
)

// Runbook proposal
// ================
// Deterministic detector that walks recent sessions for one project,
// extracts Bash command sequences executed between two consecutive
// user prompts (a "task window"), normalises each command into a
// stable signature, and ranks N-grams (N in 2..MaxN) that recur
// across multiple sessions.
//
// The output is a ranked list of *candidates* — recurring shell
// workflows that look like they could be runbooks. The user accepts
// or dismisses a candidate; klyne never writes to the memory store
// without an explicit accept.
//
// All inputs come from klyne's local SQLite store. Zero AI calls,
// zero network. Matches the rest of the insights package contract.

// RunbookConfig tunes the proposer. Defaults are conservative so the
// first wave of candidates is high-precision; loosen for recall.
type RunbookConfig struct {
	// MinN is the smallest sequence length considered. 2 catches
	// build-then-deploy pairs; 3+ catches deploy pipelines.
	MinN int
	// MaxN caps sequence length. Past 5 the signal is noisy —
	// developers rarely do the same 6-step workflow byte-for-byte.
	MaxN int
	// MinOccurrences is the minimum number of times a signature
	// must appear across task windows to qualify as a candidate.
	MinOccurrences int
	// MinDistinctSessions is the minimum number of distinct sessions
	// the signature must appear in. Prevents "one debugging spree
	// in one session" from looking like a recurring runbook.
	MinDistinctSessions int
	// MaxCandidates caps how many proposals the detector returns
	// per project (after ranking).
	MaxCandidates int
	// HorizonMs is the lookback window in epoch-ms. Sessions older
	// than now-HorizonMs are skipped. Zero disables the cutoff.
	HorizonMs int64
}

// DefaultRunbookConfig returns the v1 tuned defaults.
func DefaultRunbookConfig() RunbookConfig {
	return RunbookConfig{
		MinN:                2,
		MaxN:                5,
		MinOccurrences:      3,
		MinDistinctSessions: 2,
		MaxCandidates:       10,
		HorizonMs:           int64(90*24*time.Hour) / int64(time.Millisecond),
	}
}

// Candidate is one proposed runbook — a normalised sequence of
// commands that recurs across the user's sessions for a project.
type Candidate struct {
	// Signature is the normalised, deterministic key used for
	// dedup, dismissal lookup, and stable display ids.
	Signature string `json:"signature"`
	// ID is the short hash form of Signature (12 hex chars) for
	// human-friendly CLI/MCP references.
	ID string `json:"id"`
	// ProjectPath is the project the candidate was discovered in.
	ProjectPath string `json:"project_path"`
	// Commands are the original (un-normalised) example commands
	// from the most recent occurrence. Useful for the user to
	// reason about the proposal without seeing only $PATH-ified
	// placeholders.
	Commands []string `json:"commands"`
	// NormalisedCommands is each step after normalisation, so the
	// user can see exactly what the matcher sees.
	NormalisedCommands []string `json:"normalised_commands"`
	// Occurrences is the total number of times this signature was
	// observed across all sampled task windows.
	Occurrences int `json:"occurrences"`
	// DistinctSessions is the number of distinct session ids that
	// produced at least one occurrence.
	DistinctSessions int `json:"distinct_sessions"`
	// FirstSeenMs / LastSeenMs are the epoch-ms timestamps of the
	// earliest and latest observed occurrence.
	FirstSeenMs int64 `json:"first_seen_ms"`
	LastSeenMs  int64 `json:"last_seen_ms"`
	// SampleSessionIDs lists the (deduped) session ids that
	// contributed an occurrence, oldest first.
	SampleSessionIDs []string `json:"sample_session_ids,omitempty"`
	// Score is the composite ranking score: occurrences weighted
	// by log(distinct_sessions+1) and decayed by recency. Higher
	// is better. Exposed for debugging; the candidate list is
	// already sorted by Score DESC.
	Score float64 `json:"score"`
	// SuggestedName is an auto-generated short name based on the
	// dominant verbs in the sequence. Always a valid memory title
	// the user can tweak before accepting.
	SuggestedName string `json:"suggested_name"`
}

// ProposeRunbooks is the top-level entry point. Walks recent
// sessions for projectPath, builds task windows from each, indexes
// every length-N sub-sequence of Bash commands, and returns the
// top MaxCandidates that pass MinOccurrences + MinDistinctSessions
// AND are not already dismissed.
//
// projectPath is required — global proposals across projects produce
// too much noise to be useful in v1.
func ProposeRunbooks(ctx context.Context, db *store.DB, projectPath string, cfg RunbookConfig) ([]Candidate, error) {
	if cfg.MinN <= 0 {
		cfg = DefaultRunbookConfig()
	}
	if strings.TrimSpace(projectPath) == "" {
		return nil, fmt.Errorf("insights: ProposeRunbooks requires project_path")
	}

	since := int64(0)
	if cfg.HorizonMs > 0 {
		since = time.Now().UnixMilli() - cfg.HorizonMs
	}

	sessions, err := GatherSessions(ctx, db, Filter{
		ProjectPath: projectPath,
		SinceMs:     since,
		MaxSessions: 500,
	})
	if err != nil {
		return nil, fmt.Errorf("gather sessions: %w", err)
	}

	dismissed, err := store.DismissedSignatureSet(ctx, db, projectPath)
	if err != nil {
		return nil, fmt.Errorf("load dismissals: %w", err)
	}

	index := newCandidateIndex()
	now := time.Now().UnixMilli()
	for _, sess := range sessions {
		msgs, err := store.ListMessagesBySession(ctx, db, sess.ID, 10_000, 0)
		if err != nil {
			return nil, fmt.Errorf("messages for %s: %w", sess.ID, err)
		}
		windows := extractTaskWindows(msgs, since)
		for _, w := range windows {
			if len(w.Commands) < cfg.MinN {
				continue
			}
			for n := cfg.MinN; n <= cfg.MaxN && n <= len(w.Commands); n++ {
				for i := 0; i+n <= len(w.Commands); i++ {
					slice := w.Commands[i : i+n]
					normSlice := w.Normalised[i : i+n]
					sig := joinSignature(normSlice)
					if _, drop := dismissed[sig]; drop {
						continue
					}
					index.add(sig, slice, normSlice, sess.ID, w.LastTs)
				}
			}
		}
	}

	out := index.materialize(cfg, projectPath, now)
	return out, nil
}

// taskWindow is one stretch between two consecutive user prompts
// in a session. We treat that as a "task" — the commands the agent
// ran to satisfy one user request.
type taskWindow struct {
	// Commands are the original Bash command strings observed in
	// chronological order.
	Commands []string
	// Normalised are the dedup-friendly signatures of each command.
	Normalised []string
	// LastTs is the epoch-ms timestamp of the last command in the
	// window. Used for recency decay.
	LastTs int64
}

// extractTaskWindows walks a session's messages in chronological
// order and produces one taskWindow per inter-prompt gap. We only
// record the *Bash* tool calls — other tools (Read/Edit/Write/Grep)
// are excluded in v1 because they introduce too much ambiguity in
// the normalised form. (See QUESTIONS doc: bash sequences are 80%
// of what runbooks actually are.)
func extractTaskWindows(msgs []*connectors.Message, sinceMs int64) []taskWindow {
	var out []taskWindow
	var cur taskWindow
	flush := func() {
		if len(cur.Commands) > 0 {
			out = append(out, cur)
		}
		cur = taskWindow{}
	}
	for _, m := range msgs {
		if m == nil {
			continue
		}
		if sinceMs > 0 && m.Ts < sinceMs {
			continue
		}
		switch m.Role {
		case connectors.RoleUser:
			flush()
		case connectors.RoleAssistant:
			for _, tc := range m.ToolCalls {
				if !isBashTool(tc.Name) {
					continue
				}
				cmd := extractBashCommand(tc.Input)
				if cmd == "" {
					continue
				}
				cur.Commands = append(cur.Commands, cmd)
				cur.Normalised = append(cur.Normalised, normaliseCommand(cmd))
				cur.LastTs = m.Ts
			}
		}
	}
	flush()
	return out
}

// isBashTool reports whether a tool-call name should be treated as
// a shell command. Both Claude Code and Codex emit "Bash" today;
// we also tolerate "shell" because that's what some Codex builds
// historically used.
func isBashTool(name string) bool {
	switch strings.ToLower(name) {
	case "bash", "shell":
		return true
	}
	return false
}

// extractBashCommand pulls the "command" field out of a Bash
// tool-call's JSON input payload. Returns "" when the payload is
// not parseable or the command is empty/whitespace.
func extractBashCommand(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(input), &obj); err != nil {
		// Some connectors emit raw strings; treat them as the
		// command itself when JSON parsing fails.
		return strings.TrimSpace(input)
	}
	cmd, _ := obj["command"].(string)
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		// Fall back to "cmd" (some shells) or "input" (Codex
		// exec-tool variants).
		if v, ok := obj["cmd"].(string); ok {
			cmd = strings.TrimSpace(v)
		}
	}
	return cmd
}

// joinSignature flattens a normalised step slice into a single
// stable string. Uses " ;; " as a delimiter that cannot appear in
// a normalised single command (commands have ;; collapsed to a
// single ; during normalisation).
func joinSignature(norms []string) string {
	return strings.Join(norms, " ;; ")
}

// candidateIndex accumulates observations of each signature so
// materialize can rank them.
type candidateIndex struct {
	rows map[string]*candidateRow
}

type candidateRow struct {
	Signature        string
	NormalisedSteps  []string
	ExampleCommands  []string
	Occurrences      int
	SessionIDs       map[string]struct{}
	FirstSeenMs      int64
	LastSeenMs       int64
	SessionOrder     []string
}

func newCandidateIndex() *candidateIndex {
	return &candidateIndex{rows: map[string]*candidateRow{}}
}

// add records one observation of sig in session sessID at ts.
// example is the original (pre-normalisation) command slice — we
// keep the most recent example since that's what the user is most
// likely to recognize.
func (idx *candidateIndex) add(sig string, example, normalised []string, sessID string, ts int64) {
	row, ok := idx.rows[sig]
	if !ok {
		row = &candidateRow{
			Signature:       sig,
			NormalisedSteps: append([]string(nil), normalised...),
			SessionIDs:      map[string]struct{}{},
		}
		idx.rows[sig] = row
	}
	row.Occurrences++
	if _, seen := row.SessionIDs[sessID]; !seen {
		row.SessionIDs[sessID] = struct{}{}
		row.SessionOrder = append(row.SessionOrder, sessID)
	}
	if row.FirstSeenMs == 0 || ts < row.FirstSeenMs {
		row.FirstSeenMs = ts
	}
	if ts > row.LastSeenMs {
		row.LastSeenMs = ts
		row.ExampleCommands = append([]string(nil), example...)
	}
}

// materialize filters by MinOccurrences / MinDistinctSessions,
// scores each surviving row, sorts by Score DESC, and returns the
// top MaxCandidates.
func (idx *candidateIndex) materialize(cfg RunbookConfig, projectPath string, nowMs int64) []Candidate {
	out := make([]Candidate, 0, len(idx.rows))
	for sig, row := range idx.rows {
		if row.Occurrences < cfg.MinOccurrences {
			continue
		}
		distinct := len(row.SessionIDs)
		if distinct < cfg.MinDistinctSessions {
			continue
		}
		out = append(out, Candidate{
			Signature:          sig,
			ID:                 signatureShortID(sig),
			ProjectPath:        projectPath,
			Commands:           row.ExampleCommands,
			NormalisedCommands: row.NormalisedSteps,
			Occurrences:        row.Occurrences,
			DistinctSessions:   distinct,
			FirstSeenMs:        row.FirstSeenMs,
			LastSeenMs:         row.LastSeenMs,
			SampleSessionIDs:   row.SessionOrder,
			Score:              scoreCandidate(row.Occurrences, distinct, row.LastSeenMs, nowMs),
			SuggestedName:      suggestRunbookName(row.NormalisedSteps),
		})
	}
	// Sort by score DESC, tiebreak by occurrences DESC, then by
	// signature ASC for determinism.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Occurrences != out[j].Occurrences {
			return out[i].Occurrences > out[j].Occurrences
		}
		return out[i].Signature < out[j].Signature
	})
	if cfg.MaxCandidates > 0 && len(out) > cfg.MaxCandidates {
		out = out[:cfg.MaxCandidates]
	}
	return out
}

// scoreCandidate is the ranking function. Occurrences weighted by
// distinct sessions (log so 10x more sessions doesn't dominate)
// and decayed linearly toward zero over the recency horizon.
func scoreCandidate(occurrences, distinct int, lastSeenMs, nowMs int64) float64 {
	if occurrences <= 0 {
		return 0
	}
	sessionWeight := 1.0
	if distinct > 1 {
		sessionWeight = 1.0 + float64(distinct-1)*0.5
	}
	recency := 1.0
	if lastSeenMs > 0 && nowMs > lastSeenMs {
		ageDays := float64(nowMs-lastSeenMs) / float64(24*60*60*1000)
		// Linear decay over 90 days; floor at 0.25 so old-but-real
		// runbooks still surface, just below recent ones.
		recency = 1.0 - ageDays/90.0
		if recency < 0.25 {
			recency = 0.25
		}
	}
	return float64(occurrences) * sessionWeight * recency
}

// signatureShortID returns the first 12 hex chars of a sha1 of sig.
// Used as the human-friendly id in CLI/MCP surfaces. 12 hex chars
// gives 48 bits of collision space — comfortably more than the
// expected MaxCandidates per project per run.
func signatureShortID(sig string) string {
	h := sha1.Sum([]byte(sig))
	return hex.EncodeToString(h[:6])
}

// SignatureShortID is the exported form of signatureShortID so
// the MCP layer can render the same short id without re-implementing
// the hash. Pure function; cheap; safe to call repeatedly.
func SignatureShortID(sig string) string {
	return signatureShortID(sig)
}

// Normalisation — see package-level header for the design rationale.
// All patterns are deliberately tight; v2 can add aggressive
// "guess what varies" tokenisation behind a flag.

var (
	reUUID      = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)
	reSHA       = regexp.MustCompile(`\b[0-9a-fA-F]{40}\b`)
	reShortSHA  = regexp.MustCompile(`\b[0-9a-f]{7,12}\b`)
	reIPv4      = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	rePort      = regexp.MustCompile(`:[0-9]{2,5}\b`)
	reISOTime   = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2})?(?:Z|[+\-]\d{2}:?\d{2})?)?\b`)
	reAbsPath   = regexp.MustCompile(`(/[A-Za-z0-9_.\-]+){2,}/?`)
	reHomePath  = regexp.MustCompile(`~/[A-Za-z0-9_./\-]+`)
	reQuoted    = regexp.MustCompile(`"[^"]{12,}"|'[^']{12,}'`)
	reSemicol   = regexp.MustCompile(`;{2,}`)
	reMultiWS   = regexp.MustCompile(`\s+`)
)

// normaliseCommand maps a raw shell command to a stable signature
// that collapses variants of the "same" command.
//
// Replacements applied in order (each uses ReplaceAllLiteralString
// so the literal `$NAME` placeholder is emitted verbatim — without
// it, Go's regexp engine would interpret `$NAME` as a backreference
// to a named capture group that doesn't exist and substitute "").
//
//   - UUIDs              → $UUID
//   - 40-char hex SHAs   → $SHA
//   - 7..12 char hex SHAs (last pass) → $SHORTSHA
//   - IPv4 octets        → $IP
//   - :port              → :$PORT
//   - ISO timestamps     → $TS
//   - absolute paths     → $PATH
//   - ~/relative paths   → $HOME_PATH
//   - long quoted strings → $STRING
//   - "&&", "||", ";" preserved (delimiters)
//   - whitespace collapsed
func normaliseCommand(cmd string) string {
	out := strings.TrimSpace(cmd)
	out = reUUID.ReplaceAllLiteralString(out, "$UUID")
	out = reSHA.ReplaceAllLiteralString(out, "$SHA")
	out = reISOTime.ReplaceAllLiteralString(out, "$TS")
	out = reIPv4.ReplaceAllLiteralString(out, "$IP")
	out = rePort.ReplaceAllLiteralString(out, ":$PORT")
	// Home paths first — they share a suffix with absolute paths
	// (.../foo/bar) and the absolute pattern would eat the tail.
	out = reHomePath.ReplaceAllLiteralString(out, "$HOME_PATH")
	out = reAbsPath.ReplaceAllLiteralString(out, "$PATH")
	out = reQuoted.ReplaceAllLiteralString(out, "$STRING")
	out = reSemicol.ReplaceAllLiteralString(out, ";")
	out = reMultiWS.ReplaceAllLiteralString(out, " ")
	// reShortSHA last so we don't disturb other replacements.
	out = reShortSHA.ReplaceAllLiteralString(out, "$SHORTSHA")
	return strings.TrimSpace(out)
}

// suggestRunbookName builds a short, deterministic name from the
// dominant verbs in the normalised sequence. Format:
//
//	"<verb1>-then-<verb2>" for 2 steps
//	"<verb1>-and-N-more" for 3+ steps
//
// We pull the first non-flag word of each step as the verb. When
// the first word is a shell builtin like "cd" we skip it.
func suggestRunbookName(steps []string) string {
	if len(steps) == 0 {
		return "untitled-runbook"
	}
	verbs := make([]string, 0, len(steps))
	for _, s := range steps {
		v := primaryVerb(s)
		if v == "" {
			continue
		}
		verbs = append(verbs, v)
	}
	if len(verbs) == 0 {
		return "untitled-runbook"
	}
	switch len(verbs) {
	case 1:
		return verbs[0]
	case 2:
		return verbs[0] + "-then-" + verbs[1]
	default:
		return verbs[0] + "-and-" + fmt.Sprintf("%d", len(verbs)-1) + "-more"
	}
}

// shellBuiltinSkip is the set of opening verbs we ignore when
// looking for a runbook's primary action (they're usually plumbing).
var shellBuiltinSkip = map[string]bool{
	"cd": true, "pushd": true, "popd": true, "exec": true,
	"sudo": true, "env": true, "ENV": true, "source": true, ".": true,
}

// primaryVerb returns the first non-builtin alphabetic token of cmd
// (after stripping leading flags). Empty string when nothing useful.
func primaryVerb(cmd string) string {
	parts := strings.Fields(cmd)
	for _, p := range parts {
		// Strip $-prefixed placeholders we generated above.
		if strings.HasPrefix(p, "$") {
			continue
		}
		if strings.HasPrefix(p, "-") {
			continue
		}
		// Take the binary name even when invoked via a path.
		if i := strings.LastIndexByte(p, '/'); i >= 0 {
			p = p[i+1:]
		}
		p = strings.TrimSuffix(p, ".sh")
		p = strings.TrimSuffix(p, ".py")
		if p == "" {
			continue
		}
		if shellBuiltinSkip[p] {
			continue
		}
		// Drop trailing punctuation.
		p = strings.TrimRight(p, ",.;:")
		if isReasonableVerb(p) {
			return p
		}
	}
	return ""
}

func isReasonableVerb(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
