package worklog

import "testing"

func TestSignature(t *testing.T) {
	files := []string{"a.go", "b.go"}
	a := Signature("commit", "abc", files)
	b := Signature("commit", "abc", []string{"b.go", "a.go"}) // order-insensitive
	if a != b {
		t.Errorf("must be order-insensitive: %s vs %s", a, b)
	}
	if len(a) != 40 {
		t.Errorf("expected 40-char sha1, got %d", len(a))
	}
	if Signature("commit", "abc", nil) == Signature("commit", "def", nil) {
		t.Errorf("must distinguish commit SHA")
	}
	if Signature("commit", "abc", nil) == Signature("stop", "abc", nil) {
		t.Errorf("must distinguish close reason")
	}
	if Signature("stop", "", nil) == "" {
		t.Errorf("must not be empty")
	}
}
