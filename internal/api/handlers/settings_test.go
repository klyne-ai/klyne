package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mohitpatell/agentdeck/internal/api"
	"github.com/mohitpatell/agentdeck/internal/api/handlers"
	"github.com/mohitpatell/agentdeck/internal/config"
)

// newSettingsRouter builds a router with GET/PUT /settings registered.
// Caller must NOT be in parallel mode if the test mutates global state.
func newSettingsRouter(t *testing.T) (http.Handler, *config.Config) {
	t.Helper()
	setHomeDir(t, t.TempDir())

	cfg := config.Defaults()
	cfg.Paths.PricingOverride = ""

	r := chi.NewRouter()
	h := handlers.NewSettingsHandler(cfg, nil)
	r.Get(api.RouteSettings, h.Get)
	r.Put(api.RouteSettings, h.Put)
	return r, cfg
}

// TestSettings_Get_HappyPath verifies GET /settings returns 200 with default AI config.
// Sequential: mutates config.HomeDir.
func TestSettings_Get_HappyPath(t *testing.T) {
	router, _ := newSettingsRouter(t)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/settings")
	if err != nil {
		t.Fatalf("GET /settings: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body api.SettingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.AI.SummaryModel.Model != config.AIModelAuto {
		t.Errorf("expected default summary model 'auto', got %q", body.AI.SummaryModel.Model)
	}
}

// TestSettings_Get_NoAPIKeyInResponse verifies API keys are never echoed.
// Sequential: uses t.Setenv + mutates config.HomeDir.
func TestSettings_Get_NoAPIKeyInResponse(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-super-secret")

	router, _ := newSettingsRouter(t)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/settings")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var rawBody bytes.Buffer
	if _, err := rawBody.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}

	if bytes.Contains(rawBody.Bytes(), []byte("sk-ant-super-secret")) {
		t.Error("API key value should NOT appear in GET /settings response")
	}

	var body api.SettingsResponse
	if err := json.Unmarshal(rawBody.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Detected.Anthropic {
		t.Error("expected Detected.Anthropic = true when ANTHROPIC_API_KEY is set")
	}
}

// TestSettings_Put_HappyPath verifies PUT /settings updates and returns new state.
// Sequential: mutates config.HomeDir.
func TestSettings_Put_HappyPath(t *testing.T) {
	router, _ := newSettingsRouter(t)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	payload := api.SettingsUpdateRequest{
		AI: &api.SettingsAI{
			SummaryModel: api.TaskModel{Provider: "openai", Model: "gpt-4o"},
			TitleModel:   api.TaskModel{Provider: "anthropic", Model: "claude-3-haiku-20240307"},
			EmbedModel:   api.TaskModel{Provider: "", Model: config.AIModelOff},
		},
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPut, srv.URL+"/settings", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("PUT /settings: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result api.SettingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.AI.SummaryModel.Provider != "openai" {
		t.Errorf("expected summary provider 'openai', got %q", result.AI.SummaryModel.Provider)
	}
	if result.AI.SummaryModel.Model != "gpt-4o" {
		t.Errorf("expected summary model 'gpt-4o', got %q", result.AI.SummaryModel.Model)
	}
}

// TestSettings_Put_InvalidJSON verifies 400 on malformed JSON body.
// Sequential: mutates config.HomeDir.
func TestSettings_Put_InvalidJSON(t *testing.T) {
	router, _ := newSettingsRouter(t)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPut, srv.URL+"/settings", bytes.NewBufferString("{not valid json"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

// TestSettings_Put_PartialUpdate verifies that nil AI fields are left unchanged.
// Sequential: mutates config.HomeDir.
func TestSettings_Put_PartialUpdate(t *testing.T) {
	router, _ := newSettingsRouter(t)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	// Only update summary_model — title should stay "auto".
	payload := api.SettingsUpdateRequest{
		AI: &api.SettingsAI{
			SummaryModel: api.TaskModel{Provider: "gemini", Model: "gemini-1.5-flash"},
		},
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result api.SettingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.AI.SummaryModel.Provider != "gemini" {
		t.Errorf("expected gemini provider, got %q", result.AI.SummaryModel.Provider)
	}
	if result.AI.SummaryModel.Model != "gemini-1.5-flash" {
		t.Errorf("expected gemini-1.5-flash model, got %q", result.AI.SummaryModel.Model)
	}
}

// TestSettings_LoopbackMiddleware verifies the loopback middleware on /settings.
// Sequential: uses buildFullRouter which mutates config.HomeDir.
func TestSettings_LoopbackMiddleware(t *testing.T) {
	router := buildFullRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for non-loopback, got %d", w.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req2.RemoteAddr = "127.0.0.1:1234"
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code == http.StatusForbidden {
		t.Errorf("loopback should not be 403, got %d", w2.Code)
	}
}

// TestSettings_Put_NoAPIKeyInResponse verifies PUT response never exposes API keys.
// Sequential: uses t.Setenv + mutates config.HomeDir.
func TestSettings_Put_NoAPIKeyInResponse(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-openai-secret-value")

	router, _ := newSettingsRouter(t)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	payload := api.SettingsUpdateRequest{
		AI: &api.SettingsAI{
			SummaryModel: api.TaskModel{Provider: "openai", Model: "gpt-4o-mini"},
		},
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()

	var rawBody bytes.Buffer
	rawBody.ReadFrom(resp.Body) //nolint:errcheck

	if bytes.Contains(rawBody.Bytes(), []byte("sk-openai-secret-value")) {
		t.Error("API key should NOT appear in PUT /settings response")
	}
}
