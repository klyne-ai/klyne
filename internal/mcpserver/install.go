package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/pelletier/go-toml/v2"
)

// Install command
// ===============
// `klyne mcp install` writes (or updates) the klyne MCP server
// entry in each detected host's configuration file. Idempotent — safe
// to re-run on every binary upgrade.
//
// Hosts supported:
//   - Claude Code: ~/.claude.json     {.mcpServers.klyne = {...}}
//   - Codex CLI:   ~/.codex/config.toml [mcp_servers.klyne]
//
// The Codex config key was confirmed against OpenAI's published Codex
// MCP docs (2026-05): mcp_servers.<name> with command/args/env keys.
// Claude's mcpServers shape is the long-standing format used by every
// MCP host the SDK ships with.

// Platform identifies one MCP host whose config we know how to edit.
type Platform string

const (
	PlatformClaude Platform = "claude"
	PlatformCodex  Platform = "codex"
	// PlatformCursor identifies the Cursor CLI. Cursor's MCP server
	// config lives at ~/.cursor/mcp.json (a separate format from
	// Claude's mcpServers shape), which klyne doesn't yet manage —
	// for v1 we only wire hooks. The platform value is plumbed
	// through so install reports + auto-detection treat Cursor as a
	// first-class target.
	PlatformCursor Platform = "cursor"
)

// InstallAction describes what InstallForPlatform actually did.
type InstallAction string

const (
	// InstallActionAdded means the klyne entry was missing and
	// was newly written into the file.
	InstallActionAdded InstallAction = "added"
	// InstallActionUpdated means the entry existed but had a stale
	// path or args; the file was rewritten with the current values.
	InstallActionUpdated InstallAction = "updated"
	// InstallActionAlreadyInstalled means the entry was already
	// present and identical to what we would write — file was not
	// touched.
	InstallActionAlreadyInstalled InstallAction = "already-installed"
)

// InstallReport carries the outcome of one InstallForPlatform call.
type InstallReport struct {
	Platform Platform
	Path     string
	Action   InstallAction
}

// klyneEntryName is the MCP server name written into host configs.
const klyneEntryName = "klyne"

// klyneArgs is the argv passed to the klyne binary by the MCP host.
// Stays in one place so Claude + Codex agree.
var klyneArgs = []string{"mcp"}

// claudeConfigPath returns ~/.claude.json. Returns ("", error) when
// HOME is unresolvable.
func claudeConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".claude.json"), nil
}

// codexConfigPath returns ~/.codex/config.toml.
func codexConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}

// DetectAvailablePlatforms returns the platforms whose config file
// exists on disk right now. Used by the install command's auto-detect
// path so users don't have to specify --platform.
//
// We deliberately do NOT check writability — that check is deferred
// to the actual write attempt so we surface a precise OS error
// rather than a vague "skipped" message.
func DetectAvailablePlatforms() []Platform {
	var out []Platform
	if p, err := claudeConfigPath(); err == nil {
		if _, err := os.Stat(p); err == nil {
			out = append(out, PlatformClaude)
		}
	}
	if p, err := codexConfigPath(); err == nil {
		if _, err := os.Stat(p); err == nil {
			out = append(out, PlatformCodex)
		}
	}
	// Cursor is detected by the presence of its config dir, since
	// hooks.json may be absent on a fresh Cursor install (the
	// installer creates it on first --platform cursor / auto-detect).
	if home, err := os.UserHomeDir(); err == nil {
		cursorDir := filepath.Join(home, ".cursor")
		if info, err := os.Stat(cursorDir); err == nil && info.IsDir() {
			out = append(out, PlatformCursor)
		}
	}
	return out
}

// InstallForPlatform writes (or updates) the klyne entry in the
// target platform's config file. binaryPath is the absolute path to
// the klyne binary the host should invoke (typically the result
// of os.Executable()).
//
// Returns an InstallReport describing whether the file was added,
// updated, or left alone. Errors only on real I/O / parse failures —
// "no such file" for Codex's config dir is treated as "create it."
func InstallForPlatform(p Platform, binaryPath string) (*InstallReport, error) {
	switch p {
	case PlatformClaude:
		return installClaude(binaryPath)
	case PlatformCodex:
		return installCodex(binaryPath)
	case PlatformCursor:
		// Cursor has its own MCP server config shape at
		// ~/.cursor/mcp.json which klyne doesn't yet write to. The
		// per-platform install loop expects a report, so we return
		// AlreadyInstalled with the hook config path — the real
		// install work happens in the hook block (cmd/klyne/mcp.go's
		// shouldInstallCursorHooks branch). Treating this as a no-op
		// here keeps the loop's contract intact without lying about
		// what was written.
		path, _ := cursorHooksPath()
		return &InstallReport{Platform: PlatformCursor, Path: path, Action: InstallActionAlreadyInstalled}, nil
	default:
		return nil, fmt.Errorf("unsupported platform %q", p)
	}
}

// installClaude reads ~/.claude.json (creating an empty object when
// missing), merges the klyne entry under .mcpServers.klyne,
// and writes back. JSON is preserved as a parsed map so any other
// fields the user has there (theme settings, other MCP servers, etc.)
// survive untouched.
func installClaude(binaryPath string) (*InstallReport, error) {
	path, err := claudeConfigPath()
	if err != nil {
		return nil, err
	}
	parsed := map[string]any{}
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	servers, _ := parsed["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	want := map[string]any{
		"command": binaryPath,
		"args":    asAnySlice(klyneArgs),
	}
	existing, present := servers[klyneEntryName].(map[string]any)
	action := InstallActionAdded
	switch {
	case !present:
		action = InstallActionAdded
	case mcpEntryEqual(existing, want):
		return &InstallReport{Platform: PlatformClaude, Path: path, Action: InstallActionAlreadyInstalled}, nil
	default:
		action = InstallActionUpdated
	}
	servers[klyneEntryName] = want
	parsed["mcpServers"] = servers

	out, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := writeFileAtomic(path, append(out, '\n'), 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return &InstallReport{Platform: PlatformClaude, Path: path, Action: action}, nil
}

// installCodex reads ~/.codex/config.toml (creating dir + file when
// missing), merges the klyne entry under [mcp_servers.klyne],
// and writes back. Other top-level keys, [features], [projects.*],
// [plugins.*], and [marketplaces.*] are preserved through a parse +
// re-marshal cycle.
//
// Reformatting trade-off: pelletier/go-toml/v2's Marshal does NOT
// preserve original whitespace or comments. We accept that cost in
// exchange for guaranteed correctness — Codex auto-generates the file
// in the first place, so users rarely hand-edit it.
func installCodex(binaryPath string) (*InstallReport, error) {
	path, err := codexConfigPath()
	if err != nil {
		return nil, err
	}
	parsed := map[string]any{}
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(body) > 0 {
		if err := toml.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	servers, _ := parsed["mcp_servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	want := map[string]any{
		"command": binaryPath,
		"args":    asAnySlice(klyneArgs),
	}
	existing, present := servers[klyneEntryName].(map[string]any)
	action := InstallActionAdded
	switch {
	case !present:
		action = InstallActionAdded
	case mcpEntryEqual(existing, want):
		return &InstallReport{Platform: PlatformCodex, Path: path, Action: InstallActionAlreadyInstalled}, nil
	default:
		action = InstallActionUpdated
	}
	servers[klyneEntryName] = want
	parsed["mcp_servers"] = servers

	out, err := toml.Marshal(parsed)
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := writeFileAtomic(path, out, 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return &InstallReport{Platform: PlatformCodex, Path: path, Action: action}, nil
}

// asAnySlice converts a typed string slice to []any so JSON/TOML
// marshalling stays consistent with how json.Unmarshal hands us
// existing entries (always []any after a round-trip).
func asAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

// mcpEntryEqual reports whether two parsed mcp-server entries are
// equivalent — same command, same args order. Used to decide whether
// an install run should rewrite the file or skip the I/O. Comparison
// is deep-equal on the parsed shape so it tolerates either []any or
// []string variants the underlying library may produce.
func mcpEntryEqual(a, b map[string]any) bool {
	if a["command"] != b["command"] {
		return false
	}
	return reflect.DeepEqual(toAnySlice(a["args"]), toAnySlice(b["args"]))
}

// toAnySlice normalises a value that may be []any or []string into
// []any so DeepEqual produces a clean answer.
func toAnySlice(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case []string:
		return asAnySlice(t)
	}
	return nil
}
