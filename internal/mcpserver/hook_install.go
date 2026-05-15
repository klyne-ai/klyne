package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/klyne-ai/klyne/internal/store"
)

// hook_install.go — installs the proactive-advisor UserPromptSubmit
// hook into ~/.claude/settings.json so klyne advises automatically
// every time the user types a prompt in Claude Code.
//
// The hook entry uses Claude Code's standard schema:
//
//	{
//	  "hooks": {
//	    "UserPromptSubmit": [
//	      {
//	        "matcher": "*",
//	        "hooks": [
//	          {
//	            "type": "command",
//	            "command": "/abs/path/to/klyne advise"
//	          }
//	        ]
//	      }
//	    ]
//	  }
//	}
//
// Idempotent: re-running the install only rewrites the file when
// the klyne entry would actually change. Other hook entries the
// user has configured for unrelated tools are preserved untouched.

// hookCommandSuffix is the argv klyne is invoked with from the
// UserPromptSubmit hook. Lives next to klyneArgs so all install
// surfaces share one source of truth.
const hookCommandSuffix = "advise"

// claudeSettingsPath returns ~/.claude/settings.json — the file
// where Claude Code's per-user hooks configuration lives.
func claudeSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// HookInstallReport carries the outcome of one InstallAdvisorHook call.
type HookInstallReport struct {
	Path   string
	Action InstallAction
}

// InstallAdvisorHook writes the klyne advise hook into Claude Code's
// settings.json, creating the file when missing and merging into
// any existing UserPromptSubmit entries the user already has.
//
// binaryPath is the absolute path to the klyne binary (typically
// the result of os.Executable()).
//
// Returns an InstallAction describing whether the file was added,
// updated, or already had the entry. Errors only on real I/O / parse
// failures.
func InstallAdvisorHook(binaryPath string) (*HookInstallReport, error) {
	path, err := claudeSettingsPath()
	if err != nil {
		return nil, err
	}

	body, err := os.ReadFile(path) //nolint:gosec
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	root := map[string]any{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &root); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}

	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	entries, _ := hooks["UserPromptSubmit"].([]any)
	updatedEntries, action := mergeAdvisorHookEntry(entries, binaryPath)
	if action == InstallActionAlreadyInstalled {
		return &HookInstallReport{Path: path, Action: action}, nil
	}
	hooks["UserPromptSubmit"] = updatedEntries
	root["hooks"] = hooks

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return &HookInstallReport{Path: path, Action: action}, nil
}

// mergeAdvisorHookEntry updates the slice of UserPromptSubmit
// matcher entries to include exactly one klyne advise hook with
// the canonical binary path. Returns the updated slice plus an
// InstallAction describing what (if anything) changed.
//
// Algorithm:
//
//  1. Walk every existing entry. Count any klyne-advise hooks we
//     find AND whether one of them already matches the canonical
//     command exactly.
//  2. If a klyne-advise hook with the canonical command is already
//     wired AND there are no extra ones, return InstallActionAlreadyInstalled
//     and leave the slice untouched.
//  3. Otherwise filter out every existing klyne-advise hook from
//     "*" / unmatched entries, then append the canonical command
//     either to the first such entry or as a brand-new "*" entry.
func mergeAdvisorHookEntry(entries []any, binaryPath string) ([]any, InstallAction) {
	want := map[string]any{
		"type":    "command",
		"command": binaryPath + " " + hookCommandSuffix,
	}

	totalKlyne, canonicalKlyne := scanKlyneAdvise(entries, want)
	if totalKlyne == 1 && canonicalKlyne == 1 {
		// Already in canonical shape; no rewrite needed.
		return entries, InstallActionAlreadyInstalled
	}

	// Action label: "added" when we have to introduce klyne-advise
	// from scratch, "updated" when we are replacing or de-duping
	// existing klyne-advise entries.
	action := InstallActionAdded
	if totalKlyne > 0 {
		action = InstallActionUpdated
	}

	starIdx := -1
	updated := make([]any, len(entries))
	copy(updated, entries)
	for i, raw := range updated {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		matcher, _ := entry["matcher"].(string)
		if matcher != "" && matcher != "*" {
			continue
		}
		if starIdx == -1 {
			starIdx = i
		}
		inner, _ := entry["hooks"].([]any)
		filtered := make([]any, 0, len(inner))
		for _, h := range inner {
			hookObj, ok := h.(map[string]any)
			if !ok {
				filtered = append(filtered, h)
				continue
			}
			if isKlyneAdviseCommand(hookObj) {
				continue
			}
			filtered = append(filtered, hookObj)
		}
		entry["hooks"] = filtered
		updated[i] = entry
	}

	if starIdx == -1 {
		updated = append(updated, map[string]any{
			"matcher": "*",
			"hooks":   []any{want},
		})
		return updated, action
	}

	target := updated[starIdx].(map[string]any)
	inner, _ := target["hooks"].([]any)
	inner = append(inner, want)
	target["hooks"] = inner
	updated[starIdx] = target
	return updated, action
}

// scanKlyneAdvise walks "*" / unmatched entries and reports the
// total count of klyne-advise hooks plus how many of them match
// the canonical want hook (same type, same command).
func scanKlyneAdvise(entries []any, want map[string]any) (total, canonical int) {
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		matcher, _ := entry["matcher"].(string)
		if matcher != "" && matcher != "*" {
			continue
		}
		inner, _ := entry["hooks"].([]any)
		for _, h := range inner {
			hookObj, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if !isKlyneAdviseCommand(hookObj) {
				continue
			}
			total++
			if commandsEqual(hookObj, want) {
				canonical++
			}
		}
	}
	return total, canonical
}

// isKlyneAdviseCommand reports whether the given hook object is a
// "command" type whose command string ends with our advise suffix.
// Matches binary paths regardless of installation directory.
func isKlyneAdviseCommand(h map[string]any) bool {
	if t, _ := h["type"].(string); t != "command" {
		return false
	}
	cmd, _ := h["command"].(string)
	if cmd == "" {
		return false
	}
	// Either ends with "klyne advise" or has " advise" preceded by
	// a "klyne" path component. The suffix-only check is enough
	// because users would not name an unrelated binary "klyne".
	if hasSuffix(cmd, " "+hookCommandSuffix) {
		return containsKlyneToken(cmd)
	}
	return false
}

// containsKlyneToken reports whether cmd contains "klyne" as a path
// component or basename. Quick and simple; no shell parsing.
func containsKlyneToken(cmd string) bool {
	const needle = "klyne"
	for i := 0; i+len(needle) <= len(cmd); i++ {
		if cmd[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// hasSuffix is strings.HasSuffix without the import to keep this
// file dependency-free.
func hasSuffix(s, suf string) bool {
	return len(s) >= len(suf) && s[len(s)-len(suf):] == suf
}

// commandsEqual compares two hook entries for "same intent" — same
// type and same command. Used to short-circuit the write when the
// install is a no-op.
func commandsEqual(a, b map[string]any) bool {
	at, _ := a["type"].(string)
	bt, _ := b["type"].(string)
	if at != bt {
		return false
	}
	ac, _ := a["command"].(string)
	bc, _ := b["command"].(string)
	return ac == bc
}

// ShieldInjectResult is the outcome of ScopedShieldContext.
type ShieldInjectResult struct {
	// Context is the ≤4K-token context fragment to inject, or "" when
	// no relevant shield snapshot was found.
	Context string
	// SnapshotID is the shield_snapshot row that was used (0 when none).
	SnapshotID int64
}

// ScopedShieldContext checks whether the immediately preceding compact
// event was klyne-blocked and, when it was, returns a keyword-scored
// context fragment from that snapshot's decisions and turns, capped at
// roughly 4K tokens (~16 000 bytes) and filtered to sentences that
// overlap with the keywords extracted from userPrompt.
//
// This implements the "Scoped re-inject on next UserPromptSubmit" step
// from the Compact Shield spec. The caller (klyne advise) should append
// the returned Context to the hook's additionalContext when non-empty.
//
// db may be nil; returns an empty result gracefully when nil.
func ScopedShieldContext(ctx context.Context, db *store.DB, sessionID, userPrompt string) (*ShieldInjectResult, error) {
	if db == nil || sessionID == "" {
		return &ShieldInjectResult{}, nil
	}

	// Find the most recent blocked snapshot for this session.
	snaps, err := store.ListShieldSnapshots(ctx, db, store.ShieldSnapshotFilter{
		SessionID:   sessionID,
		OnlyBlocked: true,
		Limit:       1,
	})
	if err != nil || len(snaps) == 0 {
		return &ShieldInjectResult{}, nil //nolint:nilerr
	}
	snap := snaps[0]

	// Build a keyword set from the user prompt for similarity scoring.
	keywords := extractKeywords(userPrompt)
	if len(keywords) == 0 {
		return &ShieldInjectResult{}, nil
	}

	// Score candidate sentences from decisions and turns against keywords.
	// Each JSON array value is treated as one candidate chunk.
	candidates := extractCandidates(snap.DecisionsJSON, snap.TurnsJSON)
	ranked := scoreByKeywords(candidates, keywords)

	// Budget: roughly 4K tokens (~16 KB at ~4 bytes/token on average).
	const maxBytes = 16_000
	var sb strings.Builder
	sb.WriteString("klyne compact-shield context (snapshot ")
	sb.WriteString(fmt.Sprintf("%d", snap.ID))
	sb.WriteString("):\n")
	used := sb.Len()
	for _, c := range ranked {
		if used+len(c)+1 > maxBytes {
			break
		}
		sb.WriteString("- ")
		sb.WriteString(c)
		sb.WriteByte('\n')
		used += len(c) + 3
	}
	if used == sb.Len()-len(fmt.Sprintf("klyne compact-shield context (snapshot %d):\n", snap.ID)) {
		// No candidates fit.
		return &ShieldInjectResult{}, nil
	}

	return &ShieldInjectResult{
		Context:    sb.String(),
		SnapshotID: snap.ID,
	}, nil
}

// extractKeywords returns a deduplicated lowercase word set from text,
// excluding very short words and common stop-words.
func extractKeywords(text string) map[string]struct{} {
	stop := map[string]struct{}{
		"the": {}, "a": {}, "an": {}, "and": {}, "or": {}, "but": {},
		"in": {}, "on": {}, "at": {}, "to": {}, "for": {}, "of": {},
		"is": {}, "it": {}, "be": {}, "as": {}, "by": {}, "we": {},
		"do": {}, "so": {}, "no": {}, "my": {}, "if": {}, "up": {},
		"can": {}, "are": {}, "was": {}, "not": {}, "has": {}, "had": {},
		"you": {}, "use": {}, "run": {}, "get": {}, "set": {}, "new": {},
	}
	out := make(map[string]struct{})
	for _, word := range strings.Fields(strings.ToLower(text)) {
		// Strip punctuation prefix/suffix.
		word = strings.Trim(word, ".,;:!?\"'`()[]{}#")
		if len(word) < 3 {
			continue
		}
		if _, ok := stop[word]; ok {
			continue
		}
		out[word] = struct{}{}
	}
	return out
}

// extractCandidates decodes JSON arrays from decisionsJSON and turnsJSON
// and returns the string values as individual candidates.
func extractCandidates(decisionsJSON, turnsJSON string) []string {
	var out []string
	for _, raw := range []string{decisionsJSON, turnsJSON} {
		var items []string
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			// Try []any for heterogeneous arrays.
			var anys []any
			if err2 := json.Unmarshal([]byte(raw), &anys); err2 != nil {
				continue
			}
			for _, a := range anys {
				if s, ok := a.(string); ok && s != "" {
					items = append(items, s)
				}
			}
		}
		out = append(out, items...)
	}
	return out
}

// scoreByKeywords sorts candidates by how many keywords they overlap with
// and returns them in descending score order (highest relevance first).
func scoreByKeywords(candidates []string, keywords map[string]struct{}) []string {
	type scored struct {
		text  string
		score int
	}
	items := make([]scored, 0, len(candidates))
	for _, c := range candidates {
		cWords := extractKeywords(c)
		score := 0
		for kw := range keywords {
			if _, ok := cWords[kw]; ok {
				score++
			}
		}
		if score > 0 {
			items = append(items, scored{text: c, score: score})
		}
	}
	// Simple insertion sort — candidate lists are small (≤30 items).
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].score > items[j-1].score; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.text
	}
	return out
}

