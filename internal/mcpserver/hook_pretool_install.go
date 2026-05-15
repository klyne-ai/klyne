package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// hook_pretool_install.go — installs the PreToolUse safety-net hook into
// ~/.claude/settings.json alongside the existing UserPromptSubmit advisor hook.
//
// The hook fires before every Claude Code tool call and passes the payload
// to `klyne pretool`, which matches the command against the risky-command
// policy and records a git stash (or cp-r fallback) snapshot when matched.
//
// Hook entry shape (appended under "PreToolUse" → matcher "*"):
//
//	{
//	  "type":    "command",
//	  "command": "/abs/path/to/klyne pretool"
//	}

// InstallPreToolHook writes the klyne pretool hook into Claude Code's
// settings.json. It follows the same merge/idempotency rules as
// InstallAdvisorHook.
func InstallPreToolHook(binaryPath string) (*HookInstallReport, error) {
	path, err := claudeSettingsPath()
	if err != nil {
		return nil, err
	}

	root, err := loadSettingsRoot(path)
	if err != nil {
		return nil, err
	}

	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	entries, _ := hooks["PreToolUse"].([]any)
	updatedEntries, action := mergePreToolHookEntry(entries, binaryPath)
	if action == InstallActionAlreadyInstalled {
		return &HookInstallReport{Path: path, Action: action}, nil
	}
	hooks["PreToolUse"] = updatedEntries
	root["hooks"] = hooks

	if err := saveSettingsRoot(path, root); err != nil {
		return nil, err
	}
	return &HookInstallReport{Path: path, Action: action}, nil
}

// mergePreToolHookEntry mirrors mergeAdvisorHookEntry but for the
// PreToolUse event and the "pretool" command suffix.
func mergePreToolHookEntry(entries []any, binaryPath string) ([]any, InstallAction) {
	want := map[string]any{
		"type":    "command",
		"command": binaryPath + " " + preToolHookSuffix,
	}

	totalKlyne, canonicalKlyne := scanKlynePreTool(entries, want)
	if totalKlyne == 1 && canonicalKlyne == 1 {
		return entries, InstallActionAlreadyInstalled
	}

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
			if isKlynePreToolCommand(hookObj) {
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

// scanKlynePreTool counts klyne pretool hooks in PreToolUse entries.
func scanKlynePreTool(entries []any, want map[string]any) (total, canonical int) {
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
			if !isKlynePreToolCommand(hookObj) {
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

// isKlynePreToolCommand reports whether the hook object is a "command"
// type whose command string ends with our pretool suffix.
func isKlynePreToolCommand(h map[string]any) bool {
	if t, _ := h["type"].(string); t != "command" {
		return false
	}
	cmd, _ := h["command"].(string)
	if cmd == "" {
		return false
	}
	return hasSuffix(cmd, " "+preToolHookSuffix) && containsKlyneToken(cmd)
}

// loadSettingsRoot reads ~/.claude/settings.json and returns it as a
// parsed map. Returns an empty map when the file does not yet exist.
func loadSettingsRoot(path string) (map[string]any, error) {
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
	return root, nil
}

// saveSettingsRoot marshals root and writes it to path, creating parent
// directories as needed. The file is written with 0o600 permissions.
func saveSettingsRoot(path string, root map[string]any) error {
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
