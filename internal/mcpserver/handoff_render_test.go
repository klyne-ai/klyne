package mcpserver

import (
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
)

func TestRenderHandoffSkeleton_StructuralShape(t *testing.T) {
	snap := &SessionSnapshot{
		SessionID: "abcdef1234567890",
		Path:      "/tmp/session.jsonl",
		Messages: []*connectors.Message{
			{Role: connectors.RoleUser, Content: "let's pick up TICKET-1362", Cwd: "/repo", GitBranch: "feat/x"},
			{Role: connectors.RoleUser, Content: "still on TICKET-1362 and see https://linear.app/example/issue/TICKET-1362"},
		},
	}
	md := RenderHandoff(snap)
	mustContain(t, md, "# Handoff from session `abcdef12`")
	mustContain(t, md, "Working in `/repo` on branch `feat/x`.")
	mustContain(t, md, "## Likely ticket")
	mustContain(t, md, "TICKET-1362")
	mustNotContain(t, md, "## Commands run")
	mustNotContain(t, md, "## Last few exchanges")
}

func TestRenderHandoffSkeleton_PostCompactBannerIndependent(t *testing.T) {
	// The renderer does NOT emit the post-compact banner — that's the
	// slashcommand's responsibility. The renderer's output is the
	// same skeleton whether or not the snapshot is post-compact.
	snap := &SessionSnapshot{
		SessionID: "ss",
		Messages: []*connectors.Message{
			{Role: connectors.RoleUser, Content: "hi", Cwd: "/repo"},
			compactBoundaryMsg(),
		},
	}
	md := RenderHandoff(snap)
	mustNotContain(t, md, "post-compact")
}

func mustContain(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("output missing %q\n--- output ---\n%s", sub, s)
	}
}
func mustNotContain(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Errorf("output unexpectedly contains %q\n--- output ---\n%s", sub, s)
	}
}
