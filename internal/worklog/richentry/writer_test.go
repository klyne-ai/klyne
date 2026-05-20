package richentry

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/klyne-ai/klyne/internal/ai"
)

// scriptedAI returns scripted replies in order, one per Chat call.
// errs is index-aligned with replies; a non-nil errs[i] short-circuits.
type scriptedAI struct {
	replies []string
	errs    []error
	calls   int
}

func (s *scriptedAI) Chat(_ context.Context, _ ai.ChatRequest) (*ai.ChatResponse, error) {
	i := s.calls
	s.calls++
	if i < len(s.errs) && s.errs[i] != nil {
		return nil, s.errs[i]
	}
	if i >= len(s.replies) {
		return nil, errors.New("scripted AI exhausted")
	}
	return &ai.ChatResponse{Text: s.replies[i]}, nil
}

// validReply is a minimal entry that only cites tokens present in
// fixtureInputs() — must pass the validator without retry.
const validReply = `{
  "schema_version": 1,
  "categories": {
    "features_worked_on": [{"summary":"chh_no field","repo":"operations-app","refs":["e2850e2"]}],
    "shipped": [{"summary":"PR #400","repo":"operations-app","refs":["#400","e9a1b2a"],"ticket":"CLI-1325"}],
    "features_picked": [], "bugs_found": [], "bugs_fixed": [],
    "investigations": [], "decisions": [], "config_changes": [],
    "blockers": [], "blocked_on": [], "pending": [],
    "followups_for_others": [], "must_remember": [],
    "mistakes_or_dead_ends": [], "reviews_given": []
  }
}`

// invalidReply cites a fake SHA — must trigger validator rejection
// on the first attempt.
const invalidReply = `{
  "schema_version": 1,
  "categories": {
    "features_worked_on": [{"summary":"made-up","refs":["deadbeef"]}],
    "features_picked": [], "shipped": [], "bugs_found": [], "bugs_fixed": [],
    "investigations": [], "decisions": [], "config_changes": [],
    "blockers": [], "blocked_on": [], "pending": [],
    "followups_for_others": [], "must_remember": [],
    "mistakes_or_dead_ends": [], "reviews_given": []
  }
}`

func TestWrite_HappyPath_VerdictCarried(t *testing.T) {
	llm := &scriptedAI{replies: []string{validReply}}
	out, err := Write(context.Background(), llm, fixtureInputs(), "admitted-heuristic")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out.Verdict != "admitted-heuristic" {
		t.Errorf("verdict: got %q want admitted-heuristic", out.Verdict)
	}
	if out.Retries != 0 {
		t.Errorf("retries: got %d want 0", out.Retries)
	}
	if len(out.Entry.Categories["shipped"]) != 1 {
		t.Errorf("entry lost the shipped item: %+v", out.Entry.Categories["shipped"])
	}
}

func TestWrite_AdmittedLLM_VerdictPreserved(t *testing.T) {
	llm := &scriptedAI{replies: []string{validReply}}
	out, _ := Write(context.Background(), llm, fixtureInputs(), "admitted-llm")
	if out.Verdict != "admitted-llm" {
		t.Errorf("verdict: got %q want admitted-llm", out.Verdict)
	}
}

func TestWrite_FirstFailsValidation_RetrySucceeds(t *testing.T) {
	llm := &scriptedAI{replies: []string{invalidReply, validReply}}
	out, err := Write(context.Background(), llm, fixtureInputs(), "admitted-heuristic")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out.Verdict != "admitted-heuristic" {
		t.Errorf("verdict: got %q want admitted-heuristic", out.Verdict)
	}
	if out.Retries != 1 {
		t.Errorf("retries: got %d want 1", out.Retries)
	}
	if llm.calls != 2 {
		t.Errorf("LLM calls: got %d want 2", llm.calls)
	}
}

func TestWrite_BothAttemptsFail_SkippedValidator(t *testing.T) {
	llm := &scriptedAI{replies: []string{invalidReply, invalidReply}}
	out, err := Write(context.Background(), llm, fixtureInputs(), "admitted-heuristic")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out.Verdict != "skipped-validator" {
		t.Errorf("verdict: got %q want skipped-validator", out.Verdict)
	}
	if out.Retries != 1 {
		t.Errorf("retries: got %d want 1", out.Retries)
	}
	// Empty entry must still have all 15 categories present as [] so
	// the reflection consumer can iterate without nil-checks.
	for _, cat := range []string{
		"features_worked_on", "features_picked", "shipped",
		"bugs_found", "bugs_fixed", "investigations", "decisions",
		"config_changes", "blockers", "blocked_on", "pending",
		"followups_for_others", "must_remember",
		"mistakes_or_dead_ends", "reviews_given",
	} {
		if _, ok := out.Entry.Categories[cat]; !ok {
			t.Errorf("empty entry missing category %q", cat)
		}
	}
}

func TestWrite_LLMError_PropagatesUnchanged(t *testing.T) {
	llm := &scriptedAI{errs: []error{errors.New("api timeout")}}
	out, err := Write(context.Background(), llm, fixtureInputs(), "admitted-heuristic")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "api timeout") {
		t.Errorf("err: got %v", err)
	}
	// Outcome verdict should be empty — caller (worker) decides
	// whether to set 'pending' for retry or move on.
	if out.Verdict != "" {
		t.Errorf("verdict on LLM error: got %q want ''", out.Verdict)
	}
}

func TestWrite_FencedJSON_StripsAndParses(t *testing.T) {
	// Some providers wrap JSON in ```json fences despite the prompt
	// saying not to. The parser must strip + still validate.
	fenced := "```json\n" + validReply + "\n```"
	llm := &scriptedAI{replies: []string{fenced}}
	out, err := Write(context.Background(), llm, fixtureInputs(), "admitted-heuristic")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out.Verdict != "admitted-heuristic" {
		t.Errorf("verdict: got %q want admitted-heuristic", out.Verdict)
	}
}

func TestWrite_RetryPromptIncludesRejectionDetails(t *testing.T) {
	// Probe what gets sent on the second call by recording the prompts.
	probe := &probingAI{good: validReply}
	out, _ := Write(context.Background(), probe, fixtureInputs(), "admitted-heuristic")
	if out.Retries != 1 {
		t.Fatalf("expected one retry; got %d", out.Retries)
	}
	if len(probe.prompts) != 2 {
		t.Fatalf("expected 2 prompts captured; got %d", len(probe.prompts))
	}
	retry := probe.prompts[1]
	if !strings.Contains(retry, "INVALID citations") {
		t.Errorf("retry prompt missing the rejection header; got:\n%s", retry)
	}
	if !strings.Contains(retry, "deadbeef") {
		t.Errorf("retry prompt missing the specific rejected ref; got:\n%s", retry)
	}
}

// probingAI returns invalidReply first, then `good` — used to inspect
// the retry prompt the writer sends.
type probingAI struct {
	good    string
	calls   int
	prompts []string
}

func (p *probingAI) Chat(_ context.Context, req ai.ChatRequest) (*ai.ChatResponse, error) {
	prompt := ""
	if len(req.Messages) > 0 {
		prompt = req.Messages[0].Content
	}
	p.prompts = append(p.prompts, prompt)
	i := p.calls
	p.calls++
	if i == 0 {
		return &ai.ChatResponse{Text: invalidReply}, nil
	}
	return &ai.ChatResponse{Text: p.good}, nil
}
