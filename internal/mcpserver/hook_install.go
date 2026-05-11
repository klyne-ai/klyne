package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

