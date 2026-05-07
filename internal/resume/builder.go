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
