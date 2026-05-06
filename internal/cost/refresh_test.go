package cost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// decodePricingJSON tests
// ---------------------------------------------------------------------------

func TestDecodePricingJSON_Valid(t *testing.T) {
	input := PricingFile{
		Version: 1,
		Models: map[string]PerTokenRates{
			"test-model": {
				PromptPerMtok:     1.0,
				CompletionPerMtok: 2.0,
			},
		},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	pf, err := decodePricingJSON(data)
	if err != nil {
		t.Fatalf("decodePricingJSON: %v", err)
	}
	if pf.Version != 1 {
		t.Errorf("Version: got %d, want 1", pf.Version)
	}
	rates, ok := pf.Models["test-model"]
	if !ok {
		t.Fatal("test-model not found")
	}
	if rates.PromptPerMtok != 1.0 {
		t.Errorf("PromptPerMtok: got %v, want 1.0", rates.PromptPerMtok)
	}
}

func TestDecodePricingJSON_InvalidJSON(t *testing.T) {
	_, err := decodePricingJSON([]byte("not valid json {{{"))
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestDecodePricingJSON_UnsupportedVersion(t *testing.T) {
	data := []byte(`{"version": 99, "models": {}}`)
	_, err := decodePricingJSON(data)
	if err == nil {
		t.Error("expected error for unsupported version, got nil")
	}
}

func TestDecodePricingJSON_NilModels(t *testing.T) {
	data := []byte(`{"version": 1}`)
	pf, err := decodePricingJSON(data)
	if err != nil {
		t.Fatalf("decodePricingJSON: %v", err)
	}
	if pf.Models == nil {
		t.Error("Models should be initialised to empty map, not nil")
	}
}

// ---------------------------------------------------------------------------
// loadOverride tests
// ---------------------------------------------------------------------------

func TestLoadOverride_FileNotExist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	pf, err := loadOverride(path)
	if err != nil {
		t.Errorf("loadOverride(nonexistent): expected nil error, got %v", err)
	}
	if pf != nil {
		t.Errorf("loadOverride(nonexistent): expected nil PricingFile, got %+v", pf)
	}
}

func TestLoadOverride_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pricing.json")

	override := PricingFile{
		Version: 1,
		Models: map[string]PerTokenRates{
			"override-model": {PromptPerMtok: 5.0, CompletionPerMtok: 10.0},
		},
	}
	data, _ := json.Marshal(override)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	pf, err := loadOverride(path)
	if err != nil {
		t.Fatalf("loadOverride: %v", err)
	}
	if pf == nil {
		t.Fatal("expected non-nil PricingFile")
	}
	rates, ok := pf.Models["override-model"]
	if !ok {
		t.Fatal("override-model not found in loaded file")
	}
	if rates.PromptPerMtok != 5.0 {
		t.Errorf("PromptPerMtok: got %v, want 5.0", rates.PromptPerMtok)
	}
}

func TestLoadOverride_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pricing.json")

	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := loadOverride(path)
	if err == nil {
		t.Error("expected error for invalid JSON in override file, got nil")
	}
}

func TestLoadOverride_UnreadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pricing.json")

	// Write valid content first, then make it unreadable.
	data := []byte(`{"version":1,"models":{}}`)
	if err := os.WriteFile(path, data, 0o000); err != nil {
		t.Fatalf("write: %v", err)
	}

	// On some CI environments running as root, permission checks are skipped.
	// Skip the test in that case.
	if os.Getuid() == 0 {
		t.Skip("running as root, permission check skipped")
	}

	_, err := loadOverride(path)
	if err == nil {
		t.Error("expected error for unreadable file, got nil")
	}
}

// ---------------------------------------------------------------------------
// Embedded pricing.json coverage tests
// ---------------------------------------------------------------------------

func TestEmbeddedPricingJSON_AllRequiredModels(t *testing.T) {
	pf, err := decodePricingJSON(embeddedPricing)
	if err != nil {
		t.Fatalf("decodePricingJSON(embedded): %v", err)
	}

	required := []string{
		"claude-sonnet-4-5",
		"claude-haiku-4",
		"claude-opus-4-6",
		"gpt-5",
		"gpt-5-mini",
		"gpt-5-nano",
		"gemini-2.5-flash",
		"gemini-2.5-flash-lite",
		"text-embedding-3-small",
		"llama3.1:8b",
	}

	for _, model := range required {
		rates, ok := pf.Models[model]
		if !ok {
			t.Errorf("required model %q not found in embedded pricing.json", model)
			continue
		}
		// All paid models must have non-zero prompt rate.
		if model != "llama3.1:8b" && rates.PromptPerMtok == 0 {
			t.Errorf("model %q: PromptPerMtok is 0 (expected non-zero for paid model)", model)
		}
		// llama3.1:8b must be free (all zeros).
		if model == "llama3.1:8b" {
			if rates.PromptPerMtok != 0 || rates.CompletionPerMtok != 0 {
				t.Errorf("llama3.1:8b: expected all-zero rates for free model")
			}
		}
	}
}

func TestEmbeddedPricingJSON_Version(t *testing.T) {
	pf, err := decodePricingJSON(embeddedPricing)
	if err != nil {
		t.Fatalf("decodePricingJSON(embedded): %v", err)
	}
	if pf.Version != 1 {
		t.Errorf("embedded pricing.json version: got %d, want 1", pf.Version)
	}
}
