package contexthealth

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
	claudeparse "github.com/klyne-ai/klyne/internal/connectors/claude"
	codexparse "github.com/klyne-ai/klyne/internal/connectors/codex"
)

// five_hour_window.go — cross-session aggregator for the 5-hour
// rate-limit advisory.
//
// Anthropic's 5-hour rate limit applies across every Claude Code
// session a user runs in the rolling window. To produce a "you've
// used X% of your 5-hour budget" signal, klyne walks every JSONL
// transcript under the user's configured roots, identifies messages
// timestamped within the last five hours, and sums their effective
// input tokens.
//
// "Effective" here means TokensIn - CachedReadTokens — the same
// formula the acceleration detector uses, for the same reason
// (cached reads are heavily discounted by the provider). Cache-
// creation tokens count as fresh because they are billed at the
// regular rate.
//
// Per the spec, this measurement is purely an *estimate* against
// a user-configured plan cap. The aggregator does not pretend to
// know the user's account-level remaining quota; it computes a
// best-effort consumption number that the advisor surfaces as
// "estimated against your configured plan."

const (
	// FiveHourWindow is the rolling window the aggregator measures.
	FiveHourWindow = 5 * time.Hour

	// fileMTimeFloor caps the search to JSONL files whose mtime is
	// within this window. A file last modified six hours ago cannot
	// have any messages within the 5-hour window. The extra hour of
	// slack covers clock-skew weirdness and the case where the file
	// was just touched but contains old messages.
	fileMTimeFloor = 6 * time.Hour
)

// FiveHourSession is one session's contribution to the rolling
// window. Returned by AggregateFiveHour for the advisor's
// "dominant consumer" copy.
type FiveHourSession struct {
	// SessionID is the connector-reported session id.
	SessionID string
	// Path is the JSONL file the session lives in.
	Path string
	// CLI is which connector wrote the transcript.
	CLI connectors.CLI
	// EffectiveInput is the sum of (TokensIn - CachedReadTokens)
	// across messages in the session whose Ts falls within the
	// 5-hour window.
	EffectiveInput int64
	// MessageCount is the count of messages contributing to the
	// EffectiveInput sum.
	MessageCount int
}

// FiveHourSummary is the aggregate result of AggregateFiveHour.
type FiveHourSummary struct {
	// TotalEffective is sum(EffectiveInput) across every Sessions
	// row.
	TotalEffective int64
	// Cap is the configured plan cap the advisor compares against.
	// Zero means "no cap configured" — advisor stays silent.
	Cap int64
	// PctUsed is TotalEffective / Cap × 100, capped at 100. Zero
	// when Cap is zero.
	PctUsed float64
	// Sessions is the per-session contribution rows, sorted by
	// EffectiveInput descending.
	Sessions []FiveHourSession
	// MeasuredAtMs is the epoch-ms timestamp when the aggregation
	// ran. Used to throttle re-aggregation by the hook.
	MeasuredAtMs int64
}

// FiveHourThreshold names the consumption tier the summary crosses.
type FiveHourThreshold int

const (
	// FiveHourThresholdNone means consumption is below the warn
	// level — no advisory.
	FiveHourThresholdNone FiveHourThreshold = iota
	// FiveHourThresholdWarn means consumption ≥ 50% but < 75%.
	FiveHourThresholdWarn
	// FiveHourThresholdUrgent means consumption ≥ 75%.
	FiveHourThresholdUrgent
)

// Threshold returns the threshold the summary's PctUsed crosses.
func (s FiveHourSummary) Threshold() FiveHourThreshold {
	switch {
	case s.Cap <= 0:
		return FiveHourThresholdNone
	case s.PctUsed >= 75:
		return FiveHourThresholdUrgent
	case s.PctUsed >= 50:
		return FiveHourThresholdWarn
	default:
		return FiveHourThresholdNone
	}
}

// DominantSession returns the highest-EffectiveInput session in the
// summary, or nil when Sessions is empty.
func (s FiveHourSummary) DominantSession() *FiveHourSession {
	if len(s.Sessions) == 0 {
		return nil
	}
	return &s.Sessions[0]
}

// FiveHourRoots is the per-CLI list of root directories the
// aggregator scans.
type FiveHourRoots struct {
	// Claude is the directory recursed for Claude Code transcripts.
	// Default: ~/.claude/projects.
	Claude string
	// Codex is the directory recursed for Codex rollouts.
	// Default: ~/.codex/sessions.
	Codex string
}

// DefaultFiveHourRoots resolves the user's HOME and returns the
// canonical root paths. Returns empty strings when HOME cannot be
// resolved (in which case the caller should treat the aggregator
// as unavailable).
func DefaultFiveHourRoots() FiveHourRoots {
	home, err := os.UserHomeDir()
	if err != nil {
		return FiveHourRoots{}
	}
	return FiveHourRoots{
		Claude: filepath.Join(home, ".claude", "projects"),
		Codex:  filepath.Join(home, ".codex", "sessions"),
	}
}

// AggregateFiveHour walks both roots, parses every JSONL whose
// mtime falls inside the candidate window, sums effective input
// tokens for messages whose Ts falls inside the 5-hour window, and
// returns the per-session contributions plus the aggregate summary.
//
// nowMs is taken as the right edge of the window. The function is
// pure with respect to this argument plus the on-disk state — same
// inputs produce the same outputs.
//
// cap is the user's configured FiveHourCap; pass 0 when the user
// has not configured a plan tier (the summary's PctUsed will be 0).
func AggregateFiveHour(roots FiveHourRoots, nowMs, cap int64) FiveHourSummary {
	cutoffMs := nowMs - FiveHourWindow.Milliseconds()
	mtimeCutoff := time.UnixMilli(nowMs).Add(-fileMTimeFloor)

	rows := map[string]*FiveHourSession{}
	walkRoot(roots.Claude, ".jsonl", connectors.CLIClaude, mtimeCutoff, cutoffMs, rows, parseClaudeFile)
	walkRoot(roots.Codex, ".jsonl", connectors.CLICodex, mtimeCutoff, cutoffMs, rows, parseCodexFile)

	out := make([]FiveHourSession, 0, len(rows))
	var total int64
	for _, r := range rows {
		out = append(out, *r)
		total += r.EffectiveInput
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EffectiveInput != out[j].EffectiveInput {
			return out[i].EffectiveInput > out[j].EffectiveInput
		}
		return out[i].SessionID < out[j].SessionID
	})

	summary := FiveHourSummary{
		TotalEffective: total,
		Cap:            cap,
		Sessions:       out,
		MeasuredAtMs:   nowMs,
	}
	if cap > 0 {
		pct := float64(total) / float64(cap) * 100
		if pct > 100 {
			pct = 100
		}
		summary.PctUsed = pct
	}
	return summary
}

// fileParser is the per-CLI parsing function signature shared
// between Claude and Codex. Each parser returns the canonical
// Message values for one file (skipping malformed lines, same
// tolerance the audit and snapshot loaders use).
type fileParser func(path string) []*connectors.Message

// walkRoot recurses root looking for JSONL files whose mtime is
// within mtimeCutoff. For each, parse, then accumulate any message
// whose Ts ≥ tsCutoffMs into the rows map keyed by file path.
//
// Errors during recursion are silently skipped. The aggregator
// must never block the hook on a permission error or a single
// malformed file.
func walkRoot(
	root, ext string,
	cli connectors.CLI,
	mtimeCutoff time.Time,
	tsCutoffMs int64,
	rows map[string]*FiveHourSession,
	parse fileParser,
) {
	if root == "" {
		return
	}
	if _, err := os.Stat(root); err != nil {
		return
	}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ext) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().Before(mtimeCutoff) {
			return nil
		}

		msgs := parse(path)
		if len(msgs) == 0 {
			return nil
		}
		row := rows[path]
		if row == nil {
			row = &FiveHourSession{Path: path, CLI: cli}
			rows[path] = row
		}
		for _, m := range msgs {
			if m.Role != connectors.RoleAssistant {
				continue
			}
			if m.Ts < tsCutoffMs {
				continue
			}
			if m.TokensIn <= 0 {
				continue
			}
			eff := m.TokensIn - m.CachedReadTokens
			if eff < 0 {
				eff = 0
			}
			row.EffectiveInput += eff
			row.MessageCount++
			if row.SessionID == "" && m.SessionID != "" {
				row.SessionID = m.SessionID
			}
		}
		// Drop empty rows so the dominant-session report stays clean.
		if row.EffectiveInput == 0 {
			delete(rows, path)
		}
		return nil
	})
}

// parseClaudeFile parses one Claude Code JSONL file end-to-end and
// returns every canonical message it could extract. Malformed lines
// are skipped silently.
func parseClaudeFile(path string) []*connectors.Message {
	return parseJSONLFile(path, func(line []byte) (*connectors.Message, error) {
		return claudeparse.Parse(line, path)
	})
}

// parseCodexFile is the same for Codex rollouts.
func parseCodexFile(path string) []*connectors.Message {
	parser := codexparse.New("")
	return parseJSONLFile(path, func(line []byte) (*connectors.Message, error) {
		return parser.Parse(line, path)
	})
}

// parseJSONLFile is the shared scan loop. parseLine is the per-CLI
// adapter that turns a raw line into a canonical message.
func parseJSONLFile(path string, parseLine func([]byte) (*connectors.Message, error)) []*connectors.Message {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil
	}
	defer f.Close()

	scanBuf := make([]byte, 0, 64*1024)
	maxScanToken := 16 * 1024 * 1024
	sc := bufio.NewScanner(f)
	sc.Buffer(scanBuf, maxScanToken)

	out := make([]*connectors.Message, 0, 64)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		msg, err := parseLine(line)
		if err != nil || msg == nil {
			continue
		}
		out = append(out, msg)
	}
	return out
}
