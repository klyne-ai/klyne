package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/api/handlers"
	"github.com/klyne-ai/klyne/internal/config"
)

// newWizardRouter builds a chi router with wizard routes wired.
func newWizardRouter(cfg *config.Config) http.Handler {
	r := chi.NewRouter()
	h := handlers.NewWizardHandler(cfg, nil)
	r.Get(api.RouteWizardDetect, h.Detect)
	r.Post(api.RouteWizardComplete, h.Complete)
	return r
}

// TestWizardDetect_AllProviderCombinations tests the wizard detect endpoint
// across different env permutations.
func TestWizardDetect_AllProviderCombinations(t *testing.T) {
	tests := []struct {
		name              string
		anthropicKey      string
		openaiKey         string
		geminiKey         string
		wantAnthropic     bool
		wantOpenAI        bool
		wantGemini        bool
		wantRecCount      int // minimum recommendations expected
	}{
		{
			name:          "no_providers",
			wantRecCount:  0, // no providers → no recommendations
		},
		{
			name:          "anthropic_only",
			anthropicKey:  "sk-ant-test",
			wantAnthropic: true,
			wantRecCount:  2, // summarize + title
		},
		{
			name:          "openai_only",
			openaiKey:     "sk-openai-test",
			wantOpenAI:    true,
			wantRecCount:  2,
		},
		{
			name:          "gemini_only",
			geminiKey:     "test-gemini-key",
			wantGemini:    true,
			wantRecCount:  2, // gemini covers summarize + title
		},
		{
			name:          "all_providers",
			anthropicKey:  "sk-ant-test",
			openaiKey:     "sk-openai-test",
			geminiKey:     "test-gemini-key",
			wantAnthropic: true,
			wantOpenAI:    true,
			wantGemini:    true,
			wantRecCount:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Isolate env vars.
			t.Setenv("ANTHROPIC_API_KEY", tt.anthropicKey)
			t.Setenv("OPENAI_API_KEY", tt.openaiKey)
			t.Setenv("GEMINI_API_KEY", tt.geminiKey)

			cfg := config.Defaults()
			router := newWizardRouter(cfg)
			srv := httptest.NewServer(router)
			t.Cleanup(srv.Close)

			resp, err := http.Get(srv.URL + "/wizard/detect")
			if err != nil {
				t.Fatalf("GET /wizard/detect: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200, got %d", resp.StatusCode)
			}

			var body api.WizardDetectResponse
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}

			if body.Providers.Anthropic != tt.wantAnthropic {
				t.Errorf("providers.anthropic: got %v, want %v", body.Providers.Anthropic, tt.wantAnthropic)
			}
			if body.Providers.OpenAI != tt.wantOpenAI {
				t.Errorf("providers.openai: got %v, want %v", body.Providers.OpenAI, tt.wantOpenAI)
			}
			if body.Providers.Gemini != tt.wantGemini {
				t.Errorf("providers.gemini: got %v, want %v", body.Providers.Gemini, tt.wantGemini)
			}
			if len(body.Recommendations) < tt.wantRecCount {
				t.Errorf("recommendations: got %d, want >= %d", len(body.Recommendations), tt.wantRecCount)
			}
		})
	}
}

// TestWizardDetect_ConnectorRoots verifies that the connector root detection
// correctly reports existing vs. non-existing directories.
func TestWizardDetect_ConnectorRoots(t *testing.T) {
	// Create a temp dir to act as the Claude root.
	tmpDir := t.TempDir()
	claudeRoot := filepath.Join(tmpDir, "claude-projects")
	if err := os.MkdirAll(claudeRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cfg := config.Defaults()
	cfg.Connectors.Claude.Root = claudeRoot
	cfg.Connectors.Codex.Root = filepath.Join(tmpDir, "codex-sessions") // does not exist

	router := newWizardRouter(cfg)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/wizard/detect")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	var body api.WizardDetectResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !body.Connectors.ClaudeOK {
		t.Errorf("expected claude_ok=true for existing dir %s", claudeRoot)
	}
	if body.Connectors.CodexOK {
		t.Error("expected codex_ok=false for non-existing dir")
	}
	if body.Connectors.ClaudeRoot != claudeRoot {
		t.Errorf("claude_root: got %q, want %q", body.Connectors.ClaudeRoot, claudeRoot)
	}
}

// TestWizardDetect_RecommendationsShape verifies the recommendations shape.
func TestWizardDetect_RecommendationsShape(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")

	cfg := config.Defaults()
	router := newWizardRouter(cfg)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/wizard/detect")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	var body api.WizardDetectResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, rec := range body.Recommendations {
		if rec.Task == "" {
			t.Error("recommendation has empty task")
		}
		if rec.Selected.Provider == "" {
			t.Errorf("recommendation %q has empty provider", rec.Task)
		}
		if rec.Selected.Model == "" {
			t.Errorf("recommendation %q has empty model", rec.Task)
		}
		if rec.Reason == "" {
			t.Errorf("recommendation %q has empty reason", rec.Task)
		}
	}
}

// TestWizardComplete_WritesConfig verifies that POST /wizard/complete
// writes the selected models and connector flags to the config file on disk.
func TestWizardComplete_WritesConfig(t *testing.T) {
	// Use a temp home dir so config.Save writes to a temp file.
	tmpHome := t.TempDir()
	setHomeDir(t, tmpHome)

	cfg := config.Defaults()
	router := newWizardRouter(cfg)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	reqBody := handlers.WizardCompleteRequest{
		SummaryModel:  "openai:gpt-5-mini",
		TitleModel:    "gemini:gemini-2.5-flash-lite",
		ClaudeEnabled: true,
		CodexEnabled:  false,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	resp, err := http.Post(srv.URL+"/wizard/complete", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("POST /wizard/complete: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// Verify config was written to disk.
	configPath := filepath.Join(tmpHome, ".agentdeck", "config.toml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatalf("config file not found at %s", configPath)
	}

	// Verify in-memory config was updated.
	if cfg.AI.SummaryModel != "openai:gpt-5-mini" {
		t.Errorf("summary_model: got %q, want %q", cfg.AI.SummaryModel, "openai:gpt-5-mini")
	}
	if cfg.AI.TitleModel != "gemini:gemini-2.5-flash-lite" {
		t.Errorf("title_model: got %q, want %q", cfg.AI.TitleModel, "gemini:gemini-2.5-flash-lite")
	}
	if !cfg.Connectors.Claude.Enabled {
		t.Error("expected claude_enabled=true")
	}
	if cfg.Connectors.Codex.Enabled {
		t.Error("expected codex_enabled=false")
	}
}

// TestWizardComplete_InvalidBody verifies 400 is returned for invalid JSON.
func TestWizardComplete_InvalidBody(t *testing.T) {
	t.Parallel()
	cfg := config.Defaults()
	router := newWizardRouter(cfg)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/wizard/complete", "application/json",
		bytes.NewReader([]byte("{invalid json")))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

// TestWizardMounter_RegistersRoutes verifies that WizardMounter.Mount
// registers the expected routes on the chi router.
func TestWizardMounter_RegistersRoutes(t *testing.T) {
	t.Parallel()
	db := newTestStore(t)
	cfg := config.Defaults()

	mounter := handlers.NewWizardMounter(handlers.WizardMounterDeps{
		DB:  db,
		Cfg: cfg,
	})

	r := chi.NewRouter()
	mounter.Mount(r)

	// Verify restore route is registered.
	testSrv := httptest.NewServer(r)
	t.Cleanup(testSrv.Close)

	// GET /sessions/unknown/restore should return 404 (session not found) not
	// 405 Method Not Allowed (route not registered).
	resp, err := http.Get(testSrv.URL + "/sessions/unknown/restore")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusMethodNotAllowed {
		t.Error("restore route not registered by WizardMounter.Mount")
	}

	// GET /wizard/detect should return 200.
	resp2, err := http.Get(testSrv.URL + "/wizard/detect")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("wizard/detect: expected 200, got %d", resp2.StatusCode)
	}
}
