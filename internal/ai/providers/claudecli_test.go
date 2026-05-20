package providers_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/klyne-ai/klyne/internal/ai"
	"github.com/klyne-ai/klyne/internal/ai/providers"
)

// writeStubCLI writes a tiny shell-script "claude" stub into a tempdir
// and returns its absolute path. The stub echoes whatever args/stdin
// it's told to. Each test passes the script body via $KLYNE_STUB_OUT.
func writeStubCLI(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude-stub.sh")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

func TestClaudeCLI_Chat_HappyPath(t *testing.T) {
	stub := writeStubCLI(t, "#!/bin/sh\necho 'hello from the stub'\n")
	p := providers.NewClaudeCLI(providers.ClaudeCLIOpts{Binary: stub})

	resp, err := p.Chat(context.Background(), ai.ChatRequest{
		Model: "claude-haiku-4",
		Messages: []ai.Message{
			{Role: "user", Content: "say hi"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text != "hello from the stub" {
		t.Errorf("Text = %q, want %q", resp.Text, "hello from the stub")
	}
	if resp.Model != "claude-haiku-4" {
		t.Errorf("Model echoed: %q", resp.Model)
	}
}

func TestClaudeCLI_Chat_PassesPromptArgs(t *testing.T) {
	// Stub echoes back its OWN argv so we can verify the flags klyne sent.
	stub := writeStubCLI(t, `#!/bin/sh
for a in "$@"; do echo "ARG:$a"; done
`)
	p := providers.NewClaudeCLI(providers.ClaudeCLIOpts{Binary: stub})

	resp, err := p.Chat(context.Background(), ai.ChatRequest{
		Model:        "claude-sonnet-4-6",
		SystemPrompt: "you are terse",
		Messages: []ai.Message{
			{Role: "user", Content: "the prompt body"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	out := resp.Text
	for _, want := range []string{
		"ARG:--print",
		"ARG:--model",
		"ARG:claude-sonnet-4-6",
		"ARG:--append-system-prompt",
		"ARG:you are terse",
		"ARG:--",
		"ARG:the prompt body",
	} {
		if !contains(out, want) {
			t.Errorf("stub argv missing %q in:\n%s", want, out)
		}
	}
}

func TestClaudeCLI_Chat_NonZeroExitIsUnavailable(t *testing.T) {
	stub := writeStubCLI(t, "#!/bin/sh\necho 'claude: bad request' >&2\nexit 2\n")
	p := providers.NewClaudeCLI(providers.ClaudeCLIOpts{Binary: stub})

	_, err := p.Chat(context.Background(), ai.ChatRequest{
		Messages: []ai.Message{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error on non-zero exit")
	}
	if !errors.Is(err, ai.ErrProviderUnavailable) {
		t.Errorf("want ErrProviderUnavailable, got %v", err)
	}
}

func TestClaudeCLI_Chat_BinaryMissingIsUnavailable(t *testing.T) {
	p := providers.NewClaudeCLI(providers.ClaudeCLIOpts{Binary: "/no/such/claude"})
	_, err := p.Chat(context.Background(), ai.ChatRequest{
		Messages: []ai.Message{{Role: "user", Content: "x"}},
	})
	if err == nil {
		t.Fatal("expected error when binary is missing")
	}
	if !errors.Is(err, ai.ErrProviderUnavailable) {
		t.Errorf("want ErrProviderUnavailable, got %v", err)
	}
}

func TestClaudeCLI_Chat_EmptyPromptRejected(t *testing.T) {
	p := providers.NewClaudeCLI(providers.ClaudeCLIOpts{Binary: "/bin/true"})
	_, err := p.Chat(context.Background(), ai.ChatRequest{})
	if err == nil {
		t.Fatal("expected error on empty prompt")
	}
}

func TestClaudeCLI_Embed_AlwaysUnsupported(t *testing.T) {
	p := providers.NewClaudeCLI(providers.ClaudeCLIOpts{Binary: "/bin/true"})
	_, err := p.Embed(context.Background(), ai.EmbedRequest{Model: "x", Input: "y"})
	if !errors.Is(err, ai.ErrUnsupported) {
		t.Errorf("want ErrUnsupported, got %v", err)
	}
}

func TestClaudeCLI_NameAndModels(t *testing.T) {
	p := providers.NewClaudeCLI(providers.ClaudeCLIOpts{})
	if p.Name() != "claude-cli" {
		t.Errorf("Name() = %q, want %q", p.Name(), "claude-cli")
	}
	if len(p.Models()) == 0 {
		t.Error("Models() empty")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
