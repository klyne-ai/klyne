package claude

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// loadFixture parses every line from a JSONL fixture file and returns the
// canonical messages in order. Lines that fail to parse are silently skipped
// (same behaviour as the Watch loop).
func loadFixture(t *testing.T, name string) []*connectors.Message {
	t.Helper()
	// Fixture lives at examples/sample-jsonl/claude/<name> relative to the
	// module root. Walk up from the package directory.
	path := filepath.Join("..", "..", "..", "examples", "sample-jsonl", "claude", name)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture %s: %v", name, err)
	}
	defer f.Close()

	var msgs []*connectors.Message
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		m, err := Parse(line, path)
		if err != nil {
			// Skip unknown-type lines (e.g. "system" init line).
			continue
		}
		msgs = append(msgs, m)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan fixture %s: %v", name, err)
	}
	return msgs
}

// TestCompact_DetectedFromFixture feeds session-003-with-compact.jsonl through
// the detector and asserts that exactly one compact event fires.
func TestCompact_DetectedFromFixture(t *testing.T) {
	msgs := loadFixture(t, "session-003-with-compact.jsonl")
	if len(msgs) == 0 {
		t.Fatal("no messages parsed from fixture")
	}

	det := NewCompactDetector()
	detections := 0
	var detectedTs int64

	for _, m := range msgs {
		if ok, ts := det.Observe(m); ok {
			detections++
			detectedTs = ts
		}
	}

	if detections != 1 {
		t.Errorf("expected 1 detection, got %d", detections)
	}
	if detectedTs == 0 {
		t.Error("detected ts should be non-zero")
	}
}

// TestCompact_NotTriggeredWithoutMarker uses session-001-simple.jsonl which has
// no summary line and therefore should never trigger the detector.
func TestCompact_NotTriggeredWithoutMarker(t *testing.T) {
	msgs := loadFixture(t, "session-001-simple.jsonl")

	det := NewCompactDetector()
	for _, m := range msgs {
		if ok, _ := det.Observe(m); ok {
			t.Error("unexpected compact detection on fixture without summary marker")
		}
	}
}

// TestCompact_NotTriggeredOnTokenDropAlone verifies that a token drop without
// a preceding summary marker does not trigger detection.
func TestCompact_NotTriggeredOnTokenDropAlone(t *testing.T) {
	det := NewCompactDetector()

	// Feed a high-token assistant message.
	highToken := &connectors.Message{
		Role:     connectors.RoleAssistant,
		TokensIn: 2000,
		Content:  "Hello",
		Ts:       1000,
	}
	if ok, _ := det.Observe(highToken); ok {
		t.Error("unexpected detection on first assistant message")
	}

	// Feed a low-token assistant message (huge drop) but no summary marker.
	lowToken := &connectors.Message{
		Role:     connectors.RoleAssistant,
		TokensIn: 100, // < 20% of 2000
		Content:  "World",
		Ts:       2000,
	}
	if ok, _ := det.Observe(lowToken); ok {
		t.Error("expected no detection: token drop alone is insufficient without marker")
	}
}

// TestCompact_SingleSignalMode verifies that when no prior assistant message
// exists the detector fires on the summary marker alone (ParentUUID must be set).
func TestCompact_SingleSignalMode(t *testing.T) {
	// A fresh detector with no prior assistant messages.
	det := NewCompactDetector()
	summaryMsg := &connectors.Message{
		Role:       connectors.RoleSystem,
		Content:    "This is a summary of the conversation",
		ParentUUID: "leaf-uuid-0016", // must be non-empty to be recognised as summary
		Ts:         5000,
	}
	ok, ts := det.Observe(summaryMsg)
	if !ok {
		t.Error("expected detection in single-signal mode when no prior assistant exists")
	}
	if ts != 5000 {
		t.Errorf("expected ts=5000, got %d", ts)
	}
}

// TestCompact_SystemInitLineIgnored verifies that a system init line
// (ParentUUID="") does NOT trigger detection, even with non-empty content.
func TestCompact_SystemInitLineIgnored(t *testing.T) {
	det := NewCompactDetector()
	initMsg := &connectors.Message{
		Role:       connectors.RoleSystem,
		Content:    "Session initialized",
		ParentUUID: "", // no leafUUID — this is an init line, not a summary
		Ts:         1000,
	}
	if ok, _ := det.Observe(initMsg); ok {
		t.Error("unexpected detection on system init line (empty ParentUUID)")
	}
}

// TestCompact_TwoSignalRequiredWhenPriorAssistantExists verifies that a summary
// marker alone (with a prior assistant) does not fire — it waits for the token drop.
func TestCompact_TwoSignalRequiredWhenPriorAssistantExists(t *testing.T) {
	det := NewCompactDetector()

	// Establish prior assistant context.
	priorAssistant := &connectors.Message{
		Role:     connectors.RoleAssistant,
		TokensIn: 1000,
		Content:  "Prior response",
		Ts:       1000,
	}
	if ok, _ := det.Observe(priorAssistant); ok {
		t.Error("unexpected detection on prior assistant")
	}

	// Feed summary marker — should NOT fire yet.
	summaryMsg := &connectors.Message{
		Role:       connectors.RoleSystem,
		Content:    "Summary of conversation",
		ParentUUID: "leaf-uuid-before-compact", // must be non-empty
		Ts:         2000,
	}
	if ok, _ := det.Observe(summaryMsg); ok {
		t.Error("expected no detection on summary marker alone when prior assistant exists")
	}

	// Feed a low-token assistant message to confirm compaction.
	postMsg := &connectors.Message{
		Role:     connectors.RoleAssistant,
		TokensIn: 100, // < 20% of 1000
		Content:  "Post-compact reply",
		Ts:       3000,
	}
	ok, ts := det.Observe(postMsg)
	if !ok {
		t.Error("expected detection after summary marker + token drop")
	}
	if ts != 2000 { // ts should be the summary marker's ts
		t.Errorf("expected ts=2000 (summary ts), got %d", ts)
	}
}

// TestCompact_NoFireWhenDropInsufficient verifies that if the token drop is
// not large enough (>= threshold), compaction is not detected.
func TestCompact_NoFireWhenDropInsufficient(t *testing.T) {
	det := NewCompactDetector()

	priorAssistant := &connectors.Message{
		Role:     connectors.RoleAssistant,
		TokensIn: 1000,
		Content:  "Prior response",
		Ts:       1000,
	}
	det.Observe(priorAssistant) //nolint:errcheck

	summaryMsg := &connectors.Message{
		Role:       connectors.RoleSystem,
		Content:    "Summary",
		ParentUUID: "leaf-uuid", // must be non-empty to be recognized as summary
		Ts:         2000,
	}
	det.Observe(summaryMsg) //nolint:errcheck

	// 600/1000 = 60% — well above threshold, should NOT fire.
	postMsg := &connectors.Message{
		Role:     connectors.RoleAssistant,
		TokensIn: 600,
		Content:  "Still large context",
		Ts:       3000,
	}
	if ok, _ := det.Observe(postMsg); ok {
		t.Error("expected no detection: token drop is insufficient (60% of prior)")
	}
}
