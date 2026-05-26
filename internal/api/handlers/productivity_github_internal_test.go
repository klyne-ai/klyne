package handlers

import "testing"

// TestParseRepoSlug — pure function, table-tested across every
// remote-URL shape `git remote get-url` can return. The function feeds
// `gh pr list --repo <slug>` so a slip here means the entire
// merged-PR enrichment silently degrades to "no PRs ever" for that
// origin shape.
//
// External contributors changing this should add a row before
// modifying logic; existing rows lock in current behaviour.
func TestParseRepoSlug(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Canonical forms.
		{"https no .git", "https://github.com/owner/name", "owner/name"},
		{"https with .git", "https://github.com/owner/name.git", "owner/name"},
		{"https with leading whitespace", "  https://github.com/owner/name.git ", "owner/name"},
		{"http (no s)", "http://github.com/owner/name.git", "owner/name"},

		// SCP-style — the format `git remote get-url` returns for SSH
		// remotes by default ("git@host:owner/name.git"). The parser
		// collapses host + owner/name into a 3-segment path and takes
		// the last two, so the slug strips the host. That's fine for
		// `gh --repo` which only wants owner/name.
		{"scp-like ssh", "git@github.com:owner/name.git", "owner/name"},
		{"scp-like with host alias", "git@github-personal:owner/name.git", "owner/name"},
		{"scp-like no .git", "git@github.com:owner/name", "owner/name"},

		// ssh:// canonical form.
		{"ssh:// protocol", "ssh://git@github.com/owner/name.git", "owner/name"},

		// Subgroup / nested-path origins (GitLab-style) — we take the
		// LAST two segments, which yields the immediate parent + repo
		// name; preserves gh-list compatibility.
		{"gitlab subgroup", "https://gitlab.com/group/subgroup/name.git", "subgroup/name"},

		// Trailing/leading slashes get trimmed.
		{"trailing slash", "https://github.com/owner/name/", "owner/name"},

		// Hardening: empty, whitespace-only, malformed empty segments.
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"missing owner", "https://github.com//name", ""},

		// QUIRK: the parser takes the last two path segments
		// unconditionally, so a URL with only one segment after the
		// host (e.g. `host/repo`) returns `host/repo` — host gets
		// promoted to owner. We lock this in rather than "fix" it
		// because changing the behaviour would silently break any
		// callers relying on it. If you need stricter validation, add
		// it at the call site.
		{"single segment (host becomes owner)", "https://github.com/lonely", "github.com/lonely"},
		{"missing name (host becomes owner)", "https://github.com/owner/", "github.com/owner"},
		{"single segment scp", "git@github.com:lonely", ""}, // 'git@github.com' is rejected by the @-guard

		// Reject ambiguous owner shapes that could land as a flag value
		// to `gh --repo`. The function explicitly bans `@` and spaces in
		// the owner segment.
		{"@ in owner", "https://github.com/own@er/name", ""},
		{"space in owner", "https://github.com/own er/name", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseRepoSlug(c.in)
			if got != c.want {
				t.Fatalf("parseRepoSlug(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
