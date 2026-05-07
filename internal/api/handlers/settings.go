package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/mohitpatell/agentdeck/internal/ai/providers"
	"github.com/mohitpatell/agentdeck/internal/api"
	"github.com/mohitpatell/agentdeck/internal/config"
)

// SettingsHandler handles GET /settings and PUT /settings.
type SettingsHandler struct {
	cfg    *config.Config
	logger *slog.Logger
}

// NewSettingsHandler constructs a SettingsHandler.
func NewSettingsHandler(cfg *config.Config, logger *slog.Logger) *SettingsHandler {
	return &SettingsHandler{cfg: cfg, logger: logger}
}

// Get handles GET /settings.
// Returns the current AI configuration and detected provider availability.
// API keys are NEVER returned in the response.
func (h *SettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	detected := detectProviders(r)
	writeJSON(w, http.StatusOK, h.buildResponse(detected))
}

// Put handles PUT /settings.
// Accepts a SettingsUpdateRequest (partial), patches the in-memory config,
// persists via config.Save, then returns the new state.
// API keys are NEVER echoed back.
func (h *SettingsHandler) Put(w http.ResponseWriter, r *http.Request) {
	var req api.SettingsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Apply partial patch — only fields present in the request are updated.
	if req.AI != nil {
		if req.AI.SummaryModel.Provider != "" || req.AI.SummaryModel.Model != "" {
			h.cfg.AI.SummaryModel = taskModelToString(req.AI.SummaryModel)
		}
		if req.AI.TitleModel.Provider != "" || req.AI.TitleModel.Model != "" {
			h.cfg.AI.TitleModel = taskModelToString(req.AI.TitleModel)
		}
		if req.AI.EmbedModel.Provider != "" || req.AI.EmbedModel.Model != "" {
			h.cfg.AI.EmbedModel = taskModelToString(req.AI.EmbedModel)
		}
	}

	if err := config.Save(h.cfg); err != nil {
		h.logger.Error("settings: save config", "err", err)
		http.Error(w, "failed to save settings", http.StatusInternalServerError)
		return
	}

	detected := detectProviders(r)
	writeJSON(w, http.StatusOK, h.buildResponse(detected))
}

// buildResponse assembles a SettingsResponse from the current config state.
func (h *SettingsHandler) buildResponse(detected api.DetectedProviders) api.SettingsResponse {
	return api.SettingsResponse{
		AI: api.SettingsAI{
			SummaryModel: parseTaskModel(h.cfg.AI.SummaryModel),
			TitleModel:   parseTaskModel(h.cfg.AI.TitleModel),
			EmbedModel:   parseTaskModel(h.cfg.AI.EmbedModel),
		},
		Detected: detected,
	}
}

// detectProviders calls providers.DetectAvailable and maps to DetectedProviders.
func detectProviders(r *http.Request) api.DetectedProviders {
	infos := providers.DetectAvailable(r.Context())
	dp := api.DetectedProviders{}
	for _, info := range infos {
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
	return dp
}

// parseTaskModel converts a config AI model string (e.g. "openai/gpt-4o")
// into a TaskModel DTO. Sentinel values ("auto", "off") are returned with
// empty Provider and the sentinel as Model.
func parseTaskModel(s string) api.TaskModel {
	if s == "" || s == config.AIModelAuto || s == config.AIModelOff {
		return api.TaskModel{Provider: "", Model: s}
	}
	// Format is "provider/model" — split on first slash only.
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return api.TaskModel{Provider: s[:i], Model: s[i+1:]}
		}
	}
	// No slash — treat the whole string as model with empty provider.
	return api.TaskModel{Provider: "", Model: s}
}

// taskModelToString converts a TaskModel DTO back to the config string format.
func taskModelToString(tm api.TaskModel) string {
	if tm.Provider == "" {
		return tm.Model
	}
	return tm.Provider + "/" + tm.Model
}
