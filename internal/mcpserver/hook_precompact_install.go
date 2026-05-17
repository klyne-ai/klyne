package mcpserver

// hook_precompact_install.go — installs the PreCompact compact-shield hook
// into ~/.claude/settings.json alongside the existing hooks.
//
// The hook fires before every Claude Code auto-compact and passes the
// payload to `klyne precompact`, which decides to block or allow based on
// fill percentage and armed snapshot state.
//
// Hook entry shape (appended under "PreCompact" → matcher "*"):
//
//	{
//	  "matcher": "*",
//	  "hooks": [
//	    { "type": "command", "command": "/abs/path/to/klyne precompact" }
//	  ]
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
//
// PreCompact uses the same {matcher, hooks} envelope as every other
// Claude Code hook event — Claude Code's schema rejects flat hook
// objects placed directly under hooks.PreCompact.
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
			if isKlynePreCompactCommand(hookObj) {
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

// scanKlynePreCompact counts klyne precompact hooks in the PreCompact entries.
// It walks the outer {matcher, hooks} envelopes and inspects the inner
// hooks []any list, mirroring scanKlynePreTool.
func scanKlynePreCompact(entries []any, want map[string]any) (total, canonical int) {
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
			if !isKlynePreCompactCommand(hookObj) {
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
