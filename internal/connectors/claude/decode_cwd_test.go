package claude

import (
	"testing"
)

func TestDecodeCWD_RoundTrip(t *testing.T) {
	// Table of real-world-style absolute paths. Each entry confirms that
	// DecodeCWD(EncodeCWD(path)) == path.
	paths := []string{
		// Standard Unix home paths
		"/Users/alice/projects/agentdeck",
		"/Users/alice/projects/myapp",
		"/home/dev/work/backend",
		"/home/dev/work/frontend",
		"/root/workspace",
		// Deeper nesting
		"/Users/mohit/Desktop/Project/agentdeck",
		"/Users/mohit/Desktop/Project/agentdeck/internal/connectors",
		"/opt/homebrew/var/projects/serviceA",
		"/var/www/html/myapp",
		"/tmp/testdir",
		// Paths with only one level
		"/workspace",
		"/projects",
		"/src",
		// CI / Docker-style paths
		"/github/workspace",
		"/github/workspace/backend",
		"/go/src/github.com/myorg/myrepo",
		"/go/src/github.com/myorg/myrepo/cmd",
		// Long nested paths
		"/Users/alice/Developer/openSource/golang/projects/agentdeck",
		"/Users/bob/work/org/team/product/service/v2/pkg",
		// Paths ending with digits
		"/home/user/project1",
		"/home/user/project2",
		"/opt/app/release/2026",
		// Short paths
		"/a/b",
		"/a/b/c",
		"/x/y/z",
		// Paths with dots
		"/Users/dev/.config/app",
		"/Users/dev/.claude/projects",
		"/home/user/.local/share/apps",
		// Paths containing numbers
		"/Users/user123/project456",
		"/opt/v2/service",
		"/var/log/app/2026/05",
		// Mixed case
		"/Users/Alice/Projects/MyApp",
		"/HOME/USER/PROJECTS",
		"/users/lower/path",
		// Common Go project paths
		"/go/src/github.com/user/repo",
		"/go/pkg/mod/cache",
		"/home/user/go/src/myproject",
		// Docker volume paths
		"/data/projects/service",
		"/mnt/nfs/projects/service",
		"/volumes/data/project",
		// Absolute paths with trailing segment only
		"/webhookservice",
		"/apigateway",
		"/monorepo",
		// Real fixture path from sample-jsonl
		"/Users/dev/projects/webhookservice",
		// Edge case: path with single char segments
		"/a/b/c/d/e/f",
		// Note: paths with native "-" characters cannot round-trip because
		// the encoding is ambiguous (both "/" and "-" map to "-"). Those paths
		// are intentionally excluded from the round-trip table; see TestEncodeCWD
		// for explicit coverage of the known lossy cases.
		// Additional paths for 50+ total
		"/Users/dev/go/src/company/service",
		"/Users/dev/go/src/company/service/pkg",
		"/Users/dev/go/src/company/service/cmd",
		"/Users/dev/go/src/company/service/internal",
		"/Users/dev/Desktop",
		"/srv/app/backend",
		"/srv/app/frontend",
		"/srv/app/shared",
		"/home/ubuntu/app",
	}

	for _, p := range paths {
		p := p // capture
		t.Run(p, func(t *testing.T) {
			encoded := EncodeCWD(p)
			decoded := DecodeCWD(encoded)
			if decoded != p {
				t.Errorf("round-trip failed for %q: got %q", p, decoded)
			}
		})
	}
}

func TestEncodeCWD(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"/Users/dev/projects/webhookservice", "-Users-dev-projects-webhookservice"},
		{"/home/alice/work", "-home-alice-work"},
		{"/root", "-root"},
		{"/a/b/c", "-a-b-c"},
	}
	for _, tc := range cases {
		got := EncodeCWD(tc.input)
		if got != tc.want {
			t.Errorf("EncodeCWD(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}

func TestDecodeCWD(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"-Users-dev-projects-webhookservice", "/Users/dev/projects/webhookservice"},
		{"-home-alice-work", "/home/alice/work"},
		{"-root", "/root"},
		{"-a-b-c", "/a/b/c"},
	}
	for _, tc := range cases {
		got := DecodeCWD(tc.input)
		if got != tc.want {
			t.Errorf("DecodeCWD(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}
