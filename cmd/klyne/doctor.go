package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/ai/providers"
	"github.com/klyne-ai/klyne/internal/config"
	"github.com/klyne-ai/klyne/internal/store"
)

// newDoctorCmd registers `klyne doctor`.
//
// Output: a single JSON document on stdout.
// Exit status:
//   - 0  when at least one provider is available AND at least one connector
//     root exists on disk (the "green" state).
//   - 1  on any "red" state (no providers, no connector roots, DB unopenable, …).
//
// Never echoes API key values — providers report a boolean only.
func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Print a diagnostic report (paths, providers, schema version)",
		// Silence the auto-printed "Error: …" because doctor's non-OK
		// state is communicated through the JSON document itself.
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE:          runDoctor,
	}
}

// doctorReport is the structured output of `klyne doctor`.
type doctorReport struct {
	OK            bool                       `json:"ok"`
	Version       string                     `json:"version"`
	SchemaVersion int                        `json:"schema_version"`
	ConfigPath    string                     `json:"config_path"`
	DBPath        string                     `json:"db_path"`
	DBSizeBytes   int64                      `json:"db_size_bytes"`
	Connectors    map[string]connectorStatus `json:"connectors"`
	Providers     map[string]bool            `json:"providers"`
}

type connectorStatus struct {
	Enabled bool   `json:"enabled"`
	Root    string `json:"root"`
	Exists  bool   `json:"exists"`
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	report, ok := buildDoctorReport()

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return fmt.Errorf("klyne doctor: encode report: %w", err)
	}

	if !ok {
		// Cobra would normally print the error message. We have already
		// emitted JSON; signal exit-1 by returning a sentinel error that
		// Cobra suppresses (SilenceUsage is true on the root command).
		return errors.New("klyne doctor: not green")
	}
	return nil
}

// buildDoctorReport gathers diagnostic information without starting
// servers. It opens the DB read-only enough to read schema_version, then
// closes it immediately.
func buildDoctorReport() (doctorReport, bool) {
	r := doctorReport{
		Version:    version,
		ConfigPath: config.ConfigFile(),
		Connectors: map[string]connectorStatus{},
		Providers:  map[string]bool{},
	}

	// --- Config ----------------------------------------------------------
	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		// Continue with defaults so the JSON is still useful.
		cfg = config.Defaults()
	}

	dbPath := expandHome(cfg.Paths.DB)
	if dbPath == "" {
		dbPath = config.DBPath()
	}
	r.DBPath = dbPath

	// --- DB --------------------------------------------------------------
	dbOK := false
	if info, err := os.Stat(dbPath); err == nil {
		r.DBSizeBytes = info.Size()
	}

	// Try to open and read schema version. Use a bounded ctx so a stuck
	// SQLite lock can't make `klyne doctor` hang.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if db, err := store.Open(ctx, dbPath); err == nil {
		ver, vErr := db.SchemaVersion(ctx)
		if vErr == nil {
			r.SchemaVersion = ver
			dbOK = true
		}
		_ = db.Close()
	}

	// --- Connectors ------------------------------------------------------
	claudeRoot := expandHome(cfg.Connectors.Claude.Root)
	r.Connectors["claude"] = connectorStatus{
		Enabled: cfg.Connectors.Claude.Enabled,
		Root:    claudeRoot,
		Exists:  pathExists(claudeRoot),
	}
	codexRoot := expandHome(cfg.Connectors.Codex.Root)
	r.Connectors["codex"] = connectorStatus{
		Enabled: cfg.Connectors.Codex.Enabled,
		Root:    codexRoot,
		Exists:  pathExists(codexRoot),
	}

	anyRootExists := r.Connectors["claude"].Exists || r.Connectors["codex"].Exists

	// --- Providers -------------------------------------------------------
	detectCtx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	infos := providers.DetectAvailable(detectCtx)
	anyProvider := false
	for _, p := range infos {
		r.Providers[p.Name] = p.Available
		if p.Available {
			anyProvider = true
		}
	}

	// "ok" = green:
	//   * config loaded (or defaulted) without panic
	//   * DB opens
	//   * at least one connector root exists
	//   * at least one provider available
	r.OK = dbOK && anyRootExists && anyProvider && cfgErr == nil
	return r, r.OK
}

// expandHome expands a leading "~" to the current user's home directory.
// Used by doctor to display the resolved paths.
func expandHome(p string) string {
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := config.HomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// pathExists reports whether path exists on the filesystem (any type).
func pathExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}
