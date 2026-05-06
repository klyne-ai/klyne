// Package claude implements the Claude Code connector.
//
// Encoding convention: Claude Code encodes the working directory path as
// the directory name under ~/.claude/projects/ by replacing every "/" with "-".
// For example, /Users/dev/projects/foo → -Users-dev-projects-foo.
//
// This file provides EncodeCWD / DecodeCWD to convert between the two forms.
package claude

import "strings"

// EncodeCWD converts an absolute path to the directory name Claude Code uses
// under ~/.claude/projects/. Each "/" is replaced with "-".
// Example: /home/dev/project → -home-dev-project
func EncodeCWD(path string) string {
	return strings.ReplaceAll(path, "/", "-")
}

// DecodeCWD converts a Claude Code encoded directory name back to the original
// absolute path. Each "-" is replaced with "/".
//
// Note: This is a best-effort reversal. Paths that legitimately contain "-"
// characters cannot be distinguished from "/" separators by this encoding
// alone. In practice, Claude Code project paths are real filesystem paths
// where this ambiguity is tolerable.
//
// Example: -home-dev-project → /home/dev/project
func DecodeCWD(encoded string) string {
	return strings.ReplaceAll(encoded, "-", "/")
}
