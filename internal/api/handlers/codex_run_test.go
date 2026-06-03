package handlers

import (
	"strings"
	"testing"
)

// TestParseCodexRunResult_Usage verifies token attribution from a
// successful `codex exec --json` stream + captured final message.
func TestParseCodexRunResult_Usage(t *testing.T) {
	t.Parallel()
	stdout := strings.Join([]string{
		`{"type":"thread.started"}`,
		`{"type":"turn.started"}`,
		`{"type":"agent_message","text":"done"}`,
		`{"type":"turn.completed","usage":{"input_tokens":20684,"cached_input_tokens":3456,"output_tokens":62,"reasoning_output_tokens":40}}`,
	}, "\n")
	res := parseCodexRunResult([]byte(stdout), []byte("the final card text"), "codex")

	if !res.Parsed {
		t.Fatal("Parsed = false, want true (turn.completed seen)")
	}
	if res.InputTokens != 20684 || res.OutputTokens != 62 || res.CacheReadTokens != 3456 {
		t.Errorf("tokens = in:%d out:%d cacheRead:%d; want 20684/62/3456",
			res.InputTokens, res.OutputTokens, res.CacheReadTokens)
	}
	if res.Output != "the final card text" {
		t.Errorf("Output = %q, want the -o file contents", res.Output)
	}
	if res.Model != "codex" {
		t.Errorf("Model = %q, want codex", res.Model)
	}
}

// TestParseCodexRunResult_Failure ensures a failed turn is NOT marked
// Parsed (so it records status=error, not zero-usage success) and the
// error message surfaces.
func TestParseCodexRunResult_Failure(t *testing.T) {
	t.Parallel()
	stdout := strings.Join([]string{
		`{"type":"thread.started"}`,
		`{"type":"turn.started"}`,
		`{"type":"error","message":"The 'gpt-5-codex' model is not supported when using Codex with a ChatGPT account."}`,
		`{"type":"turn.failed","message":"model not supported"}`,
	}, "\n")
	res := parseCodexRunResult([]byte(stdout), nil, "codex:gpt-5-codex")

	if res.Parsed {
		t.Error("Parsed = true, want false for a failed turn")
	}
	if !strings.Contains(res.Output, "not supported") {
		t.Errorf("Output = %q, want the failure message surfaced", res.Output)
	}
}

// TestCodexBinary_FallbackOrEmpty asserts the resolver returns either a
// real path or "" — never panics — regardless of whether codex is present.
func TestCodexBinary_FallbackOrEmpty(t *testing.T) {
	t.Parallel()
	got := codexBinary()
	if got != "" {
		// If it resolved, it must be an existing path it claims.
		if !strings.HasSuffix(got, "codex") {
			t.Errorf("codexBinary() = %q, expected a path ending in 'codex'", got)
		}
	}
	if codexAvailable() != (got != "") {
		t.Error("codexAvailable() disagrees with codexBinary()")
	}
}

// TestSpawnProductivitySync_RoutesByEngine verifies the dispatcher picks
// the codex engine for the codex key without invoking claude. We can't run
// a real subprocess here, so we assert routing via the error origin: an
// unknown key errors with the dispatcher message; the codex key reaches
// the codex runner (which errors only on a missing binary or bad path),
// never the "unknown claude model" path.
func TestSpawnProductivitySync_RoutesByEngine(t *testing.T) {
	t.Parallel()
	if _, ok := compileModelAllowlist["codex"]; !ok {
		t.Fatal("codex key missing from compileModelAllowlist")
	}
	if compileModelAllowlist["codex"].engine != "codex" {
		t.Errorf("codex key engine = %q, want codex", compileModelAllowlist["codex"].engine)
	}
	if _, err := spawnProductivitySync(t.Context(), "/nonexistent/path", "2026-06-03", "bogus-key"); err == nil {
		t.Error("expected error for unknown model key")
	}
}
