package handlers

import (
	"net/http"
	"os/exec"

	"github.com/klyne-ai/klyne/internal/api"
)

// EnginesHandler serves GET /api/engines — the AI engines available on
// this machine, so the productivity page offers a Claude/Codex choice and
// only shows engines the user actually has installed (the locked
// auto-detect decision; see the 2026-06-03 multi-engine design).
type EnginesHandler struct{}

// NewEnginesHandler constructs the stateless handler.
func NewEnginesHandler() *EnginesHandler { return &EnginesHandler{} }

// claudeAvailable reports whether the claude CLI is on PATH.
func claudeAvailable() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

// Get handles GET /api/engines. Detection is best-effort and cheap
// (exec.LookPath + a stat for the Codex.app fallback); both engines are
// always listed so the UI can explain a missing one, but only Available
// entries are offered in the picker.
func (h *EnginesHandler) Get(w http.ResponseWriter, r *http.Request) {
	resp := api.EnginesResponse{
		Engines: []api.EngineOption{
			{
				ID:        "claude",
				Label:     "Claude",
				Available: claudeAvailable(),
				Models: []api.EngineModelOption{
					{Key: "sonnet", Label: "Sonnet"},
					{Key: "opus", Label: "Opus"},
				},
			},
			{
				ID:        "codex",
				Label:     "Codex",
				Available: codexAvailable(),
				Models: []api.EngineModelOption{
					{Key: "codex", Label: "Codex (GPT)"},
				},
			},
		},
	}
	writeJSON(w, http.StatusOK, resp)
}
