package mcpserver

// hook_precompact_install.go — installs the PreCompact compact-shield hook
// into ~/.claude/settings.json alongside the existing hooks.
//
// The hook fires before every Claude Code auto-compact and passes the
// payload to `klyne precompact`, which decides to block or allow based on
// fill percentage and armed snapshot state.
//
// Hook entry shape (appended under "PreCompact"):
//
//	{
//	  "type":    "command",
//	  "command": "/abs/path/to/klyne precompact"
//	}

// InstallPreCompactHook writes the klyne precompact hook into Claude Code's
// settings.json. It follows the same merge/idempotency rules as
// InstallAdvisorHook.
func InstallPreCompactHook(binaryPath string) (*HookInstallReport, error) {
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

	entries, _ := hooks["PreCompact"].([]any)
	updatedEntries, action := mergePreCompactHookEntry(entries, binaryPath)
	if action == InstallActionAlreadyInstalled {
		return &HookInstallReport{Path: path, Action: action}, nil
	}
	hooks["PreCompact"] = updatedEntries
	root["hooks"] = hooks

	if err := saveSettingsRoot(path, root); err != nil {
		return nil, err
	}
	return &HookInstallReport{Path: path, Action: action}, nil
}

// mergePreCompactHookEntry mirrors mergePreToolHookEntry but for the
// PreCompact event and the "precompact" command suffix.
func mergePreCompactHookEntry(entries []any, binaryPath string) ([]any, InstallAction) {
	want := map[string]any{
		"type":    "command",
		"command": binaryPath + " " + preCompactHookSuffix,
	}

	totalKlyne, canonicalKlyne := scanKlynePreCompact(entries, want)
	if totalKlyne == 1 && canonicalKlyne == 1 {
		return entries, InstallActionAlreadyInstalled
	}

	action := InstallActionAdded
	if totalKlyne > 0 {
		action = InstallActionUpdated
	}

	// PreCompact entries don't use a matcher/hooks nesting — they're a flat
	// list of hook objects (same as how Claude Code documents the event).
	// Filter out stale klyne precompact hooks then append the canonical one.
	updated := make([]any, 0, len(entries))
	for _, raw := range entries {
		hookObj, ok := raw.(map[string]any)
		if !ok {
			updated = append(updated, raw)
			continue
		}
		if isKlynePreCompactCommand(hookObj) {
			continue // strip stale entry
		}
		updated = append(updated, hookObj)
	}
	updated = append(updated, want)
	return updated, action
}

// scanKlynePreCompact counts klyne precompact hooks in the PreCompact entries.
func scanKlynePreCompact(entries []any, want map[string]any) (total, canonical int) {
	for _, raw := range entries {
		hookObj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if !isKlynePreCompactCommand(hookObj) {
			continue
		}
		total++
		if commandsEqual(hookObj, want) {
			canonical++
		}
	}
	return total, canonical
}

// isKlynePreCompactCommand reports whether the hook object is a "command"
// type whose command string ends with our precompact suffix.
func isKlynePreCompactCommand(h map[string]any) bool {
	if t, _ := h["type"].(string); t != "command" {
		return false
	}
	cmd, _ := h["command"].(string)
	if cmd == "" {
		return false
	}
	return hasSuffix(cmd, " "+preCompactHookSuffix) && containsKlyneToken(cmd)
}
