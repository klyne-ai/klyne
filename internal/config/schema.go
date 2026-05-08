// Package config defines the typed shape of ~/.klyne/config.toml.
// The loader (read, parse, expand "~", validate) lives in W6.
//
// W0-FROZEN CONTRACT
// ------------------
// Every exported struct + field below is part of the cross-workstream
// contract. Field names are TOML-tagged; renaming a tag is a breaking
// change for users and requires a `contract-change` PR.
//
// Spec references:
//   - docs/plan/04-shared-contracts.md §7 (TOML schema)
//   - spec §8 (BYOK matrix → AI section)
//   - spec §11 (paths section)
package config

// Config is the root TOML document.
type Config struct {
	Server     ServerConfig     `toml:"server"     json:"server"`
	Paths      PathsConfig      `toml:"paths"      json:"paths"`
	Connectors ConnectorsConfig `toml:"connectors" json:"connectors"`
	AI         AIConfig         `toml:"ai"         json:"ai"`
}

// ServerConfig is the [server] table.
type ServerConfig struct {
	// Addr is the host:port the daemon listens on. Default:
	// "127.0.0.1:7878" (loopback by design — spec §17 non-goals).
	Addr string `toml:"addr" json:"addr"`
}

// PathsConfig is the [paths] table.
type PathsConfig struct {
	// DB is the SQLite file path; "~" is expanded by the loader (W6).
	DB string `toml:"db" json:"db"`
	// PricingOverride is the optional path to a user-supplied pricing.json
	// (LiteLLM-style; see internal/cost/pricing_schema.go).
	PricingOverride string `toml:"pricing_override" json:"pricing_override"`
}

// ConnectorsConfig is the [connectors] table — one sub-table per CLI.
type ConnectorsConfig struct {
	Claude ClaudeConnectorConfig `toml:"claude" json:"claude"`
	Codex  CodexConnectorConfig  `toml:"codex"  json:"codex"`
}

// ClaudeConnectorConfig is [connectors.claude].
type ClaudeConnectorConfig struct {
	Enabled bool   `toml:"enabled" json:"enabled"`
	// Root is the directory the watcher recurses. Default:
	// "~/.claude/projects".
	Root string `toml:"root" json:"root"`
}

// CodexConnectorConfig is [connectors.codex].
type CodexConnectorConfig struct {
	Enabled bool   `toml:"enabled" json:"enabled"`
	// Root is the directory the watcher recurses. Default:
	// "~/.codex/sessions".
	Root string `toml:"root" json:"root"`
}

// AIConfig is the [ai] table — see spec §8.
//
// Each model field is either a literal provider/model string
// (e.g. "openai/gpt-5-mini") or one of the sentinel values:
//
//	"auto" — let the smart selector pick (default for summary, title)
//	"off"  — disable this internal task entirely (default for embed in v1)
type AIConfig struct {
	SummaryModel string `toml:"summary_model" json:"summary_model"`
	TitleModel   string `toml:"title_model"   json:"title_model"`
	EmbedModel   string `toml:"embed_model"   json:"embed_model"`
}

// Sentinel values for the AI* fields.
const (
	AIModelAuto = "auto"
	AIModelOff  = "off"
)

// Defaults returns a *Config populated with the documented v1 defaults
// (see docs/plan/04-shared-contracts.md §7). The loader (W6) starts from
// Defaults() and overlays parsed TOML on top.
func Defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Addr: "127.0.0.1:7878",
		},
		Paths: PathsConfig{
			DB:              "~/.klyne/klyne.db",
			PricingOverride: "~/.klyne/pricing.json",
		},
		Connectors: ConnectorsConfig{
			Claude: ClaudeConnectorConfig{
				Enabled: true,
				Root:    "~/.claude/projects",
			},
			Codex: CodexConnectorConfig{
				Enabled: true,
				Root:    "~/.codex/sessions",
			},
		},
		AI: AIConfig{
			SummaryModel: AIModelAuto,
			TitleModel:   AIModelAuto,
			EmbedModel:   AIModelOff, // v1.1 feature, off by default
		},
	}
}
