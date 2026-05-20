package providers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/klyne-ai/klyne/internal/ai"
	"github.com/klyne-ai/klyne/internal/ai/providers"
)

func TestCodexCLI_Chat_HappyPath(t *testing.T) {
	stub := writeStubCLI(t, "#!/bin/sh\necho 'codex says hello'\n")
	p := providers.NewCodexCLI(providers.CodexCLIOpts{Binary: stub})

	resp, err := p.Chat(context.Background(), ai.ChatRequest{
		Model:    "gpt-5-mini",
		Messages: []ai.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text != "codex says hello" {
		t.Errorf("Text = %q", resp.Text)
	}
}

func TestCodexCLI_Chat_PassesArgs(t *testing.T) {
	stub := writeStubCLI(t, `#!/bin/sh
for a in "$@"; do echo "ARG:$a"; done
`)
	p := providers.NewCodexCLI(providers.CodexCLIOpts{Binary: stub})

	resp, err := p.Chat(context.Background(), ai.ChatRequest{
		Model:        "gpt-5-mini",
		SystemPrompt: "system rules",
		Messages:     []ai.Message{{Role: "user", Content: "user prompt"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	out := resp.Text
	for _, want := range []string{
		"ARG:exec",
		"ARG:--skip-git-repo-check",
		"ARG:--model",
		"ARG:gpt-5-mini",
	} {
		if !contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// system prompt is prepended to the user prompt and passed as the
	// last positional argument.
	if !contains(out, "ARG:system rules") {
		t.Errorf("missing system prompt in:\n%s", out)
	}
}

func TestCodexCLI_Chat_NonZeroExitIsUnavailable(t *testing.T) {
	stub := writeStubCLI(t, "#!/bin/sh\nexit 1\n")
	p := providers.NewCodexCLI(providers.CodexCLIOpts{Binary: stub})
	_, err := p.Chat(context.Background(), ai.ChatRequest{
		Messages: []ai.Message{{Role: "user", Content: "x"}},
	})
	if !errors.Is(err, ai.ErrProviderUnavailable) {
		t.Errorf("want ErrProviderUnavailable, got %v", err)
	}
}

func TestCodexCLI_Embed_AlwaysUnsupported(t *testing.T) {
	p := providers.NewCodexCLI(providers.CodexCLIOpts{Binary: "/bin/true"})
	_, err := p.Embed(context.Background(), ai.EmbedRequest{Model: "x", Input: "y"})
	if !errors.Is(err, ai.ErrUnsupported) {
		t.Errorf("want ErrUnsupported, got %v", err)
	}
}

func TestCodexCLI_NameAndModels(t *testing.T) {
	p := providers.NewCodexCLI(providers.CodexCLIOpts{})
	if p.Name() != "codex-cli" {
		t.Errorf("Name() = %q", p.Name())
	}
	if len(p.Models()) == 0 {
		t.Error("Models() empty")
	}
}
