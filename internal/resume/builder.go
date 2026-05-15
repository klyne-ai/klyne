// Package resume provides functions to construct the "Open in CLI" resume
// commands for Claude Code and Codex CLI.
//
// OS-aware quoting:
//   - Unix (darwin/linux): single-quote projectPath; internal single-quotes
//     are escaped as '\'' (end-quote, literal-quote, re-open-quote).
//   - Windows: double-quote paths containing spaces; backslashes are left as-is.
//
// The sessionID argument is used as-is (it is a UUID-like string with no
// shell-special characters). The projectPath quoting is purely defensive
// for edge cases where the path contains spaces or special characters.
package resume

import (
	"runtime"
	"strings"
)

// osName is the runtime OS identifier. Exposed as a package-level variable
// so tests can inject "windows" or "linux" without requiring a real OS switch.
// Default value is runtime.GOOS.
var osName = runtime.GOOS

// ClaudeCmd returns the resume command for Claude Code.
//
//	linux/macOS: claude --resume <id> -p <quoted-path>
//	windows:     claude --resume <id> -p "<path>"
//
// projectPath is optional. When empty the -p flag is omitted.
func ClaudeCmd(sessionID, projectPath string) string {
	cmd := "claude --resume " + sessionID
	if projectPath != "" {
		cmd += " -p " + quotePath(projectPath, osName)
	}
	return cmd
}

// CodexCmd returns the resume command for Codex CLI.
//
// Codex resumes the last session by default; we emit:
//
//	codex resume --last
//
// The sessionID and projectPath arguments are accepted for API consistency
// but are not used in the command (Codex does not accept a session-id arg
// as of May 2026; it always resumes the most recent session in the CWD).
func CodexCmd(sessionID, projectPath string) string {
	_ = sessionID
	_ = projectPath
	return "codex resume --last"
}

// quotePath returns a shell-safe quoted version of path suitable for the
// given OS. The goos parameter should be runtime.GOOS or a test-injected value.
func quotePath(path, goos string) string {
	switch goos {
	case "windows":
		return quoteWindows(path)
	default:
		return quoteUnix(path)
	}
}

// quoteUnix wraps path in single quotes and escapes any single-quote characters
// in the path using the shell idiom: ' → '\''
//
// Examples:
//
//	/home/user/my project  → '/home/user/my project'
//	/home/user/it's/here   → '/home/user/it'\''s/here'
func quoteUnix(path string) string {
	escaped := strings.ReplaceAll(path, "'", `'\''`)
	return "'" + escaped + "'"
}

// quoteWindows wraps path in double-quotes if it contains spaces; otherwise
// returns it unquoted. Internal double-quotes are escaped with backslash.
//
// Examples:
//
//	C:\Users\user\myproject  → C:\Users\user\myproject
//	C:\Users\user\my project → "C:\Users\user\my project"
func quoteWindows(path string) string {
	escaped := strings.ReplaceAll(path, `"`, `\"`)
	if strings.ContainsAny(escaped, " \t") {
		return `"` + escaped + `"`
	}
	return escaped
}

// EstimateTokensForVariant returns the projected token size of a hydrate
// payload variant. The variant string controls which sections are included:
//
//   - "full"           — all turns + all decisions (~8K typical)
//   - "decisions-only" — pinned decisions only (~800 tokens typical)
//   - "last-N"         — same as full but caller pre-filters to N turns;
//     this function estimates based on the provided turnCount
//
// The estimates are rough heuristics (4 chars per token) calibrated
// against real klyne session sizes. Callers display these before injection
// so the user can confirm the cost before committing.
//
// Parameters:
//   - variant:      one of "full", "decisions-only"
//   - turnCount:    number of conversation turns (used for "full" / "last-N")
//   - decisionCount: number of decision records
func EstimateTokensForVariant(variant string, turnCount, decisionCount int) int {
	// Empirically: each turn averages ~300 tokens (mix of short user asks
	// and longer assistant responses + tool payloads).
	const tokensPerTurn = 300
	// Each decision averages ~40 tokens of text.
	const tokensPerDecision = 40

	switch variant {
	case "decisions-only":
		base := decisionCount * tokensPerDecision
		if base == 0 {
			return 800 // spec default for empty decision set
		}
		return base
	default: // "full" or "last-N" — caller controls turnCount
		return turnCount*tokensPerTurn + decisionCount*tokensPerDecision
	}
}
