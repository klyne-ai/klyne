package mcpserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// codex_hook_install.go — installs the klyne hook surface into Codex
// CLI's ~/.codex/hooks.json so codex sessions populate stop_summaries
// the same way Claude Code sessions do.
//
// As of codex-cli 0.133.x the CLI honours a settings file at
// ~/.codex/hooks.json with the SAME event shape Claude Code uses
// (SessionStart / UserPromptSubmit / PreToolUse / Stop). It also
// requires the `hooks` feature flag in ~/.codex/config.toml; the
// legacy `codex_hooks` flag name is deprecated as of 0.133.
//
// We install the same hook commands the Claude installer uses
// (klyne-hook advise / pretool / session-end / session-start). The
// shared internal/hooks code path then handles both Claude and codex
// JSONL formats — Routing is done by mcpserver.CLIForPath at snapshot
// load time.

// codexHooksPath returns ~/.codex/hooks.json — the file where Codex
// CLI's per-user hook config lives.
func codexHooksPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".codex", "hooks.json"), nil
}

// codexConfigPath is defined in install.go.

// CodexHookInstallReport bundles the outcomes of one InstallCodexHooks
// run. One report per hook event, plus the feature-flag action.
type CodexHookInstallReport struct {
	HooksPath    string
	SessionStart InstallAction
	UserPrompt   InstallAction
	PreToolUse   InstallAction
	Stop         InstallAction
	// FeatureFlag describes what happened to ~/.codex/config.toml's
	// [features].hooks setting.
	FeatureFlag InstallAction
}

// InstallCodexHooks wires every klyne hook event into Codex CLI's
// hooks.json and ensures the `hooks` feature flag is enabled in
// config.toml. Idempotent — re-running only rewrites files whose
// content would actually change.
//
// binaryPath is the absolute path to the klyne-hook stub (preferred)
// or the main klyne binary. The same binary handles each event;
// dispatch is done via the per-event subcommand suffix.
func InstallCodexHooks(binaryPath string) (*CodexHookInstallReport, error) {
	hooksPath, err := codexHooksPath()
	if err != nil {
		return nil, err
	}

	body, err := os.ReadFile(hooksPath) //nolint:gosec
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", hooksPath, err)
	}

	root := map[string]any{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &root); err != nil {
			return nil, fmt.Errorf("parse %s: %w", hooksPath, err)
		}
	}

	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	rep := &CodexHookInstallReport{HooksPath: hooksPath}

	rep.SessionStart = applyCodexHookEvent(hooks, "SessionStart",
		binaryPath+" session-start", "startup|resume")
	rep.UserPrompt = applyCodexHookEvent(hooks, "UserPromptSubmit",
		binaryPath+" advise", "")
	rep.PreToolUse = applyCodexHookEvent(hooks, "PreToolUse",
		binaryPath+" pretool", "*")
	rep.Stop = applyCodexHookEvent(hooks, "Stop",
		binaryPath+" session-end", "")

	root["hooks"] = hooks

	// Skip the write when every event was already canonical — saves
	// touching mtime on no-op re-runs.
	if rep.SessionStart == InstallActionAlreadyInstalled &&
		rep.UserPrompt == InstallActionAlreadyInstalled &&
		rep.PreToolUse == InstallActionAlreadyInstalled &&
		rep.Stop == InstallActionAlreadyInstalled {
		// Still need to handle the feature flag.
		rep.FeatureFlag, err = ensureCodexHooksFeatureFlag()
		if err != nil {
			return rep, err
		}
		return rep, nil
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", hooksPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o750); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(hooksPath), err)
	}
	if err := writeFileAtomic(hooksPath, append(out, '\n'), 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", hooksPath, err)
	}

	rep.FeatureFlag, err = ensureCodexHooksFeatureFlag()
	if err != nil {
		return rep, err
	}
	return rep, nil
}

// applyCodexHookEvent installs (or re-installs) one event's hook
// command into the codex hooks map. Mirrors the Claude installer's
// merge semantics: filter out any prior klyne entries for this event,
// then append the canonical command under the requested matcher.
//
// matcher: "" means no matcher field (events like UserPromptSubmit /
// Stop don't take one); "*" or specific matcher strings ride along
// in the JSON.
func applyCodexHookEvent(hooks map[string]any, event, command, matcher string) InstallAction {
	want := map[string]any{
		"type":    "command",
		"command": command,
	}

	entries, _ := hooks[event].([]any)
	total, canonical := scanKlyneCodexHook(entries, command)
	if total == 1 && canonical == 1 {
		return InstallActionAlreadyInstalled
	}

	action := InstallActionAdded
	if total > 0 {
		action = InstallActionUpdated
	}

	// Filter out any existing klyne entries for this event.
	updated := make([]any, 0, len(entries)+1)
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			updated = append(updated, raw)
			continue
		}
		inner, _ := entry["hooks"].([]any)
		filtered := make([]any, 0, len(inner))
		for _, h := range inner {
			hookObj, ok := h.(map[string]any)
			if !ok {
				filtered = append(filtered, h)
				continue
			}
			if isKlyneCodexHook(hookObj) {
				continue
			}
			filtered = append(filtered, hookObj)
		}
		entry["hooks"] = filtered
		// Drop entirely-empty entries we may have just hollowed out.
		if len(filtered) == 0 {
			continue
		}
		updated = append(updated, entry)
	}

	newEntry := map[string]any{
		"hooks": []any{want},
	}
	if matcher != "" {
		newEntry["matcher"] = matcher
	}
	updated = append(updated, newEntry)

	hooks[event] = updated
	return action
}

// scanKlyneCodexHook walks an event's entries and reports the count
// of klyne hook commands plus how many match the canonical command
// exactly.
func scanKlyneCodexHook(entries []any, want string) (total, canonical int) {
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		inner, _ := entry["hooks"].([]any)
		for _, h := range inner {
			hookObj, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if !isKlyneCodexHook(hookObj) {
				continue
			}
			total++
			cmd, _ := hookObj["command"].(string)
			if cmd == want {
				canonical++
			}
		}
	}
	return total, canonical
}

// isKlyneCodexHook reports whether the given hook object is a
// "command" type whose command string mentions klyne or klyne-hook.
// Matches binary paths regardless of installation directory.
func isKlyneCodexHook(h map[string]any) bool {
	if t, _ := h["type"].(string); t != "command" {
		return false
	}
	cmd, _ := h["command"].(string)
	if cmd == "" {
		return false
	}
	// Match any klyne / klyne-hook command. Codex install uses the
	// same per-event suffixes Claude does, so the same token check
	// works.
	return strings.Contains(cmd, "klyne")
}

// ensureCodexHooksFeatureFlag idempotently sets `[features].hooks =
// true` in ~/.codex/config.toml. The legacy `codex_hooks` name is
// deprecated as of codex-cli 0.133 — we remove it when present.
//
// Light-touch TOML edit: we don't pull in a TOML library here. The
// file is line-oriented in practice; we scan for the [features]
// section header and patch the right line. If the file or section
// is missing we append them.
//
// Returns InstallAction describing what happened:
//   - AlreadyInstalled: hooks=true was already set.
//   - Updated: replaced a legacy `codex_hooks` flag or flipped from
//     false → true.
//   - Added: file or section didn't carry the flag at all.
// tomlValueIsTrue reports whether the value side of a `key = value`
// TOML line is the boolean literal `true`. It strips a trailing inline
// comment and trims whitespace, then compares the bare token to
// "true". A quoted `"true"`/`'true'` is a STRING, not the boolean, so
// it deliberately does NOT match — that's the bug this guards against
// (`hooks = "untrue"` previously satisfied a naive Contains check).
func tomlValueIsTrue(raw string) bool {
	v := raw
	// Drop an inline comment. A '#' inside a quoted string is not a
	// comment, but the only values we care about are bare booleans
	// (true/false) and quoted strings; a bare boolean never contains
	// '#', so cutting at the first '#' is safe for our purposes.
	if hash := strings.IndexByte(v, '#'); hash >= 0 {
		v = v[:hash]
	}
	return strings.TrimSpace(v) == "true"
}

func ensureCodexHooksFeatureFlag() (InstallAction, error) {
	path, err := codexConfigPath()
	if err != nil {
		return InstallActionAdded, err
	}

	body, err := os.ReadFile(path) //nolint:gosec
	if err != nil && !os.IsNotExist(err) {
		return InstallActionAdded, fmt.Errorf("read %s: %w", path, err)
	}

	lines := []string{}
	if len(body) > 0 {
		s := bufio.NewScanner(strings.NewReader(string(body)))
		s.Buffer(make([]byte, 64*1024), 1024*1024)
		for s.Scan() {
			lines = append(lines, s.Text())
		}
		if err := s.Err(); err != nil {
			return InstallActionAdded, fmt.Errorf("scan %s: %w", path, err)
		}
	}

	inFeatures := false
	sawHooksTrue := false
	sawDeprecated := false
	sectionEnd := -1
	hooksLineIdx := -1
	deprecatedLineIdx := -1
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			// Section boundary.
			if inFeatures {
				sectionEnd = i
				inFeatures = false
			}
			if trimmed == "[features]" {
				inFeatures = true
			}
			continue
		}
		if !inFeatures {
			continue
		}
		// Inside [features] — look for the flag. Parse the key
		// EXACTLY (split on the first '=', trim the left side, require
		// equality) so `hooks_experimental = true` or `hooks = "untrue"`
		// are not misread as the canonical `hooks = true`.
		eq := strings.IndexByte(trimmed, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:eq])
		switch key {
		case "hooks":
			hooksLineIdx = i
			if tomlValueIsTrue(trimmed[eq+1:]) {
				sawHooksTrue = true
			}
		case "codex_hooks":
			sawDeprecated = true
			deprecatedLineIdx = i
		}
	}
	// If [features] runs to EOF without another section, mark its end.
	if inFeatures && sectionEnd == -1 {
		sectionEnd = len(lines)
	}

	switch {
	case sawHooksTrue && !sawDeprecated:
		return InstallActionAlreadyInstalled, nil

	case sawHooksTrue && sawDeprecated:
		// Both flags present; canonical is `hooks`. Strip the legacy
		// alias so codex stops emitting deprecation warnings.
		lines = removeLine(lines, deprecatedLineIdx)
		if err := writeLines(path, lines); err != nil {
			return InstallActionUpdated, err
		}
		return InstallActionUpdated, nil

	case hooksLineIdx >= 0:
		// `hooks` flag present but not true — flip it.
		lines[hooksLineIdx] = "hooks = true"
		if sawDeprecated {
			lines = removeLine(lines, deprecatedLineIdx)
		}
		if err := writeLines(path, lines); err != nil {
			return InstallActionUpdated, err
		}
		return InstallActionUpdated, nil

	case sawDeprecated:
		// Only the deprecated name is set — replace with canonical.
		lines[deprecatedLineIdx] = "hooks = true"
		if err := writeLines(path, lines); err != nil {
			return InstallActionUpdated, err
		}
		return InstallActionUpdated, nil

	default:
		// Neither flag present. Insert into the existing [features]
		// section, or append a new one.
		if sectionEnd > 0 {
			lines = insertLine(lines, sectionEnd, "hooks = true")
		} else {
			if len(lines) > 0 && lines[len(lines)-1] != "" {
				lines = append(lines, "")
			}
			lines = append(lines, "[features]", "hooks = true")
		}
		if err := writeLines(path, lines); err != nil {
			return InstallActionAdded, err
		}
		return InstallActionAdded, nil
	}
}

// writeLines persists lines back to path with a trailing newline.
// Creates the parent directory and file with conservative perms.
func writeLines(path string, lines []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	if err := writeFileAtomic(path, []byte(out), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// removeLine returns a copy of lines with index i removed.
func removeLine(lines []string, i int) []string {
	if i < 0 || i >= len(lines) {
		return lines
	}
	out := make([]string, 0, len(lines)-1)
	out = append(out, lines[:i]...)
	out = append(out, lines[i+1:]...)
	return out
}

// insertLine returns a copy of lines with `val` inserted at index i.
func insertLine(lines []string, i int, val string) []string {
	if i < 0 {
		i = 0
	}
	if i > len(lines) {
		i = len(lines)
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:i]...)
	out = append(out, val)
	out = append(out, lines[i:]...)
	return out
}
