package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/mohitpatell/agentdeck/internal/ai"
	"github.com/mohitpatell/agentdeck/internal/ai/providers"
	"github.com/mohitpatell/agentdeck/internal/api"
	"github.com/mohitpatell/agentdeck/internal/config"
)

// WizardCompleteRequest is the body for POST /wizard/complete.
// Summary/TitleModel accept "auto" or "<provider>:<model>" format.
// If claude_enabled / codex_enabled are set, the connector is toggled.
type WizardCompleteRequest struct {
	SummaryModel   string `json:"summary_model"`
	TitleModel     string `json:"title_model"`
	ClaudeEnabled  bool   `json:"claude_enabled"`
	CodexEnabled   bool   `json:"codex_enabled"`
}

// WizardHandler handles GET /wizard/detect and POST /wizard/complete.
type WizardHandler struct {
	cfg    *config.Config
	logger *slog.Logger
}

// NewWizardHandler constructs a WizardHandler.
func NewWizardHandler(cfg *config.Config, logger *slog.Logger) *WizardHandler {
	return &WizardHandler{cfg: cfg, logger: logger}
}

// Detect handles GET /wizard/detect.
//
// Returns:
//   - Connector roots presence (does ~/.claude/projects/ exist? ~/.codex/sessions?)
//   - Provider availability (booleans only — no key material)
//   - Selector recommendations for Summarize and Title tasks
func (h *WizardHandler) Detect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Resolve connector root paths from config.
	claudeRoot := h.cfg.Connectors.Claude.Root
	codexRoot := h.cfg.Connectors.Codex.Root

	claudeRoot = expandTilde(claudeRoot)
	codexRoot = expandTilde(codexRoot)

	claudeOK := dirExists(claudeRoot)
	codexOK := dirExists(codexRoot)

	// Detect available providers.
	available := providers.DetectAvailable(ctx)

	// Build DetectedProviders DTO.
	dp := api.DetectedProviders{}
	for _, info := range available {
		switch info.Name {
		case "anthropic":
			dp.Anthropic = info.Available
		case "openai":
			dp.OpenAI = info.Available
		case "gemini":
			dp.Gemini = info.Available
		case "ollama":
			dp.Ollama = info.Available
		}
	}

	// Build recommendations using the selector.
	// Use the current config overrides (or "auto" if unset).
	summaryOverride := h.cfg.AI.SummaryModel
	if summaryOverride == config.AIModelAuto || summaryOverride == "" {
		summaryOverride = ""
	}
	titleOverride := h.cfg.AI.TitleModel
	if titleOverride == config.AIModelAuto || titleOverride == "" {
		titleOverride = ""
	}

	recommendations := buildRecommendations(available, summaryOverride, titleOverride)

	resp := api.WizardDetectResponse{
		Connectors: api.WizardConnectors{
			ClaudeRoot: claudeRoot,
			ClaudeOK:   claudeOK,
			CodexRoot:  codexRoot,
			CodexOK:    codexOK,
		},
		Providers:       dp,
		Recommendations: recommendations,
	}

	writeJSON(w, http.StatusOK, resp)
}

// Complete handles POST /wizard/complete.
//
// Loads the current config, applies the fields from the request body,
// and saves via config.Save. Returns 204 No Content on success.
func (h *WizardHandler) Complete(w http.ResponseWriter, r *http.Request) {
	var req WizardCompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Apply model selections.
	if req.SummaryModel != "" {
		h.cfg.AI.SummaryModel = req.SummaryModel
	}
	if req.TitleModel != "" {
		h.cfg.AI.TitleModel = req.TitleModel
	}

	// Apply connector toggles.
	h.cfg.Connectors.Claude.Enabled = req.ClaudeEnabled
	h.cfg.Connectors.Codex.Enabled = req.CodexEnabled

	if err := config.Save(h.cfg); err != nil {
		h.logger.Error("wizard: save config", "err", err)
		http.Error(w, "failed to save settings", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// buildRecommendations calls the selector for Summarize and Title tasks
// and returns the WizardRecommendation slice.
func buildRecommendations(available []ai.ProviderInfo, summaryOverride, titleOverride string) []api.WizardRecommendation {
	var recs []api.WizardRecommendation

	summarizeChoice, err := ai.Pick(ai.TaskSummarize, available, summaryOverride)
	if err == nil {
		recs = append(recs, api.WizardRecommendation{
			Task: "summary",
			Selected: api.TaskModel{
				Provider: summarizeChoice.Provider,
				Model:    summarizeChoice.Model,
			},
			Reason: summarizeChoice.Reason,
		})
	}

	titleChoice, err := ai.Pick(ai.TaskTitle, available, titleOverride)
	if err == nil {
		recs = append(recs, api.WizardRecommendation{
			Task: "title",
			Selected: api.TaskModel{
				Provider: titleChoice.Provider,
				Model:    titleChoice.Model,
			},
			Reason: titleChoice.Reason,
		})
	}

	if recs == nil {
		recs = []api.WizardRecommendation{}
	}
	return recs
}

// expandTilde replaces a leading "~" with the user's home directory.
func expandTilde(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}

// dirExists returns true if the given path is an existing directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
