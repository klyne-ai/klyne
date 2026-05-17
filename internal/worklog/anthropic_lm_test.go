package worklog

import "testing"

func TestNewAnthropicLM_NoKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	lm, err := NewAnthropicLM()
	if err == nil {
		t.Errorf("expected error when key absent, got nil")
	}
	if lm != nil {
		t.Errorf("expected nil LM, got %+v", lm)
	}
}

func TestNewAnthropicLM_WithKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "dummy-key")
	lm, err := NewAnthropicLM()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lm == nil {
		t.Errorf("expected non-nil LM")
	}
}
