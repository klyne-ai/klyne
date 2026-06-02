package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// cursor_hook_install.go — installs klyne hook entries into Cursor
// CLI's ~/.cursor/hooks.json so Cursor sessions populate
// stop_summaries the same way Claude Code and Codex CLI sessions do.
//
// Cursor's hook config (cursor.com/docs/hooks) uses a flatter shape
// than Claude/codex:
//
//	{
//	  "version": 1,
//	  "hooks": {
//	    "sessionStart":       [{"command": "/abs/path/klyne-hook cursor"}],
//	    "afterAgentResponse": [{"command": "/abs/path/klyne-hook cursor"}],
//	    "stop":               [{"command": "/abs/path/klyne-hook cursor"}]
//	  }
//	}
//
// We register the SAME command across every event klyne cares about
// — `klyne-hook cursor` reads the payload's `hook_event_name` field
// from stdin and dispatches in-process (internal/hooks/cursor_hook.go).
// One command keeps the install table small and means adding a new
// event later doesn't require rewriting hooks.json.

// cursorHookEvents is the list of Cursor hook events klyne wires to
// `klyne-hook cursor`. Adding a new event here is the only change
// needed when klyne starts honouring it — the in-process dispatcher
// already routes by hook_event_name.
//
// Coverage rationale:
//   - sessionStart: injects the KLYNE_SUMMARY-emit instruction via
//     additional_context (sessionStart is Cursor's documented
//     injection channel; beforeSubmitPrompt does NOT support
//     additional_context).
//   - afterAgentResponse: captures KLYNE_SUMMARY from the agent's
//     final text and writes one stop_summaries row per turn.
//   - stop: noop today; reserved so future loop-aware behaviour
//     doesn't need a hooks.json rewrite.
//   - sessionEnd: noop today; reserved.
var cursorHookEvents = []string{
	"sessionStart",
	"afterAgentResponse",
	"stop",
	"sessionEnd",
}

// cursorHooksPath returns ~/.cursor/hooks.json — Cursor's
// per-user hook config file. Project-level hooks live at
// <project>/.cursor/hooks.json; klyne only manages the user-level
// file for v1 since the dashboard is a single-user surface.
func cursorHooksPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".cursor", "hooks.json"), nil
}

// CursorHookInstallReport is the outcome of one InstallCursorHooks
// run. Per-event action labels mirror the codex installer's report.
type CursorHookInstallReport struct {
	Path   string
	Events map[string]InstallAction
}

// InstallCursorHooks writes klyne entries into ~/.cursor/hooks.json
// for every event in cursorHookEvents. Idempotent — re-running only
// rewrites the file when the canonical entries would actually
// change. Preserves foreign hook entries (other tools' commands stay
// untouched).
//
// binaryPath is the absolute path to the klyne-hook stub (preferred)
// or the main klyne binary. Same binary handles every cursor event;
// dispatch is by the payload's hook_event_name field.
func InstallCursorHooks(binaryPath string) (*CursorHookInstallReport, error) {
	hooksPath, err := cursorHooksPath()
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

	// Cursor's hooks.json schema requires a `version` field at the
	// root. Default to 1 if missing; preserve whatever the user has.
	if _, ok := root["version"]; !ok {
		root["version"] = 1
	}

	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	command := binaryPath + " cursor"
	rep := &CursorHookInstallReport{Path: hooksPath, Events: map[string]InstallAction{}}
	anyChange := false
	for _, event := range cursorHookEvents {
		action := applyCursorHookEvent(hooks, event, command)
		rep.Events[event] = action
		if action != InstallActionAlreadyInstalled {
			anyChange = true
		}
	}

	root["hooks"] = hooks

	if !anyChange {
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
	return rep, nil
}

// applyCursorHookEvent merges one klyne command into one event's
// hook list. Cursor's schema is `{"hooks": {"<event>": [{"command":
// "..."}]}}` — much flatter than Claude's matcher/hooks nesting.
//
// Algorithm:
//   - Filter out any prior klyne command for this event (anything
//     mentioning "klyne" in the command string).
//   - Append the canonical `klyne-hook cursor` command.
//   - If the resulting list matches the pre-existing list exactly,
//     return AlreadyInstalled.
func applyCursorHookEvent(hooks map[string]any, event, command string) InstallAction {
	entries, _ := hooks[event].([]any)

	total, canonical := scanKlyneCursorHook(entries, command)
	if total == 1 && canonical == 1 {
		return InstallActionAlreadyInstalled
	}

	action := InstallActionAdded
	if total > 0 {
		action = InstallActionUpdated
	}

	updated := make([]any, 0, len(entries)+1)
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			updated = append(updated, raw)
			continue
		}
		cmd, _ := entry["command"].(string)
		if isKlyneCursorHookCommand(cmd) {
			continue
		}
		updated = append(updated, entry)
	}
	updated = append(updated, map[string]any{"command": command})
	hooks[event] = updated
	return action
}

// scanKlyneCursorHook counts klyne entries in event's hook list and
// reports how many match the canonical command exactly.
func scanKlyneCursorHook(entries []any, want string) (total, canonical int) {
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := entry["command"].(string)
		if !isKlyneCursorHookCommand(cmd) {
			continue
		}
		total++
		if cmd == want {
			canonical++
		}
	}
	return total, canonical
}

// isKlyneCursorHookCommand reports whether the given command string
// refers to a klyne hook — used to filter klyne entries out before
// re-appending the canonical command. Match is intentionally broad
// (anything mentioning "klyne") so legacy install paths are caught.
func isKlyneCursorHookCommand(cmd string) bool {
	if cmd == "" {
		return false
	}
	for i := 0; i+5 <= len(cmd); i++ {
		if cmd[i:i+5] == "klyne" {
			return true
		}
	}
	return false
}
