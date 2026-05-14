package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/mcpserver"
)

// newMcpCmd registers `klyne mcp`.
//
// The command runs the klyne MCP server over stdio, the standard
// transport that Claude Code (and other MCP hosts) use to spawn local
// subprocess servers. To wire it into Claude Code, add to .mcp.json:
//
//	{
//	  "mcpServers": {
//	    "klyne": {
//	      "command": "klyne",
//	      "args": ["mcp"]
//	    }
//	  }
//	}
//
// The daemon does NOT need to be running. Every tool reads JSONL
// transcripts directly so it works in fresh sessions before the
// daemon has had a chance to ingest them.
func newMcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the klyne MCP server over stdio",
		Long: `Run the klyne MCP server over stdio.

Designed to be spawned by Claude Code (or any other MCP host) as a
subprocess. Stdout is the JSON-RPC channel; logs go to stderr. The
klyne daemon does NOT need to be running.

Tools (auto-invoked by the AI):
  list_sessions             — enumerate Claude+Codex sessions in the cwd's project
  search_messages           — full-text search across every indexed session
                              (REQUIRES the klyne daemon to be running)
  get_context_health        — verdict + bloat scorecard
  generate_handoff          — paste-ready Markdown handoff
  get_pre_compact_context   — recover what /compact ate (Claude) or
                              decode replacement_history (Codex)

Slash prompts (user-triggered via /):
  /klyne:health      → live get_context_health
  /klyne:sessions    → live list_sessions
  /klyne:search      → live search_messages
  /klyne:handoff     → live generate_handoff
  /klyne:precompact  → live get_pre_compact_context

To register the server with each host, run: klyne mcp install`,
		RunE: runMcp,
		// Suppress cobra's built-in usage-on-error output. An MCP host
		// would interpret it as garbage on stderr — we'd rather see a
		// single log line.
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newMcpInstallCmd())
	return cmd
}

// newMcpInstallCmd registers `klyne mcp install`.
//
// Defaults to auto-detect: writes the klyne entry into every host
// config file present on disk (~/.claude.json, ~/.codex/config.toml).
// `--platform` overrides the auto-detect for users who only want one
// host updated. The command is idempotent — safe to re-run after every
// `go install`.
func newMcpInstallCmd() *cobra.Command {
	var platform string
	c := &cobra.Command{
		Use:   "install",
		Short: "Register the klyne MCP server in Claude Code and/or Codex configs",
		Long: `Write (or update) the klyne MCP server entry in each detected host's config file.

Without --platform, installs into every host whose config exists on disk:
  Claude Code:  ~/.claude.json (mcpServers.klyne)
  Codex CLI:    ~/.codex/config.toml ([mcp_servers.klyne])

Pass --platform claude or --platform codex to scope the install.

Idempotent: safe to re-run on every binary upgrade. The command picks up
the current binary's path from os.Executable().`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMcpInstall(cmd, platform)
		},
		SilenceUsage: true,
	}
	c.Flags().StringVar(&platform, "platform", "", "claude | codex | all (default: auto-detect)")
	return c
}

// runMcpInstall resolves the klyne binary path, decides which
// platforms to install into, and prints a one-line outcome per platform.
// Errors short-circuit (we don't continue past the first install failure
// — partial install state is harder to reason about than "rerun after
// fixing the error").
func runMcpInstall(cmd *cobra.Command, platformFlag string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve klyne binary path: %w", err)
	}

	targets, err := resolveInstallTargets(platformFlag)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(),
			"No host configs detected at ~/.claude.json or ~/.codex/config.toml.\n"+
				"Run Claude Code or Codex CLI once to create their config, then re-run `klyne mcp install`,\n"+
				"or pass --platform claude|codex to create the config explicitly.")
		return nil
	}

	for _, p := range targets {
		report, err := mcpserver.InstallForPlatform(p, exe)
		if err != nil {
			return fmt.Errorf("install %s: %w", p, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %s — %s\n",
			report.Platform, report.Action, report.Path)
	}

	// Always attempt to install the advisor hook into Claude
	// Code's settings.json so the proactive session advisor works
	// out of the box. Codex CLI does not yet expose an equivalent
	// hook surface; the web cockpit advisory is the v1 fallback
	// for Codex-only users.
	if shouldInstallAdvisorHook(targets) {
		report, err := mcpserver.InstallAdvisorHook(exe)
		if err != nil {
			return fmt.Errorf("install advisor hook: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "advisor: %s — %s\n",
			report.Action, report.Path)

		// Unpack the bundled markdown slash commands under
		// ~/.claude/commands/klyne/. Claude Code surfaces these as
		// /klyne:<name> in every project on the host — independent
		// of the MCP-prompt path, which has a v2.1.x bug that drops
		// no-arg server prompts in the slash UI.
		slashReport, err := mcpserver.InstallSlashCommands()
		if err != nil {
			return fmt.Errorf("install slash commands: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "slash commands: %s — %d files in %s\n",
			slashReport.Action, slashReport.Files, slashReport.Dir)

		// Unpack agent-driven Skills under ~/.claude/skills/. Slash
		// commands are user-typed (/klyne:health); skills are Claude-
		// invoked when their description matches the situation — e.g.
		// klyne-health fires when an advisory mentions context fill,
		// after a /compact, or before loading a large file.
		skillsReport, err := mcpserver.InstallSkills()
		if err != nil {
			return fmt.Errorf("install skills: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "skills: %s — %d files (%d skills) in %s\n",
			skillsReport.Action, skillsReport.Files, len(skillsReport.Skills), skillsReport.Dir)

		fmt.Fprintln(cmd.OutOrStdout(),
			"klyne: advisor active — you'll see inline warnings in Claude Code when sessions drift, accelerate, or approach your 5-hour cap.")
		fmt.Fprintln(cmd.OutOrStdout(),
			"To enable the 5-hour-window advisor, run: klyne config set plan <pro|max-5x|max-20x|team>")
	}
	return nil
}

// shouldInstallAdvisorHook reports whether the install should also
// register the advisor hook. The hook lives inside Claude Code's
// settings.json, so we only install when Claude is one of the
// install targets.
func shouldInstallAdvisorHook(targets []mcpserver.Platform) bool {
	for _, p := range targets {
		if p == mcpserver.PlatformClaude {
			return true
		}
	}
	return false
}

// resolveInstallTargets turns the --platform flag into the concrete
// list of platforms to write to. Empty / "all" → auto-detect; "claude"
// or "codex" → just that one (creates the config if it does not yet
// exist).
func resolveInstallTargets(flag string) ([]mcpserver.Platform, error) {
	switch strings.ToLower(strings.TrimSpace(flag)) {
	case "", "all":
		return mcpserver.DetectAvailablePlatforms(), nil
	case "claude":
		return []mcpserver.Platform{mcpserver.PlatformClaude}, nil
	case "codex":
		return []mcpserver.Platform{mcpserver.PlatformCodex}, nil
	default:
		return nil, fmt.Errorf("--platform must be one of: claude, codex, all (got %q)", flag)
	}
}

func runMcp(cmd *cobra.Command, _ []string) error {
	// CRITICAL: redirect logging to stderr. The default `log` already
	// uses stderr, but the explicit assignment below documents the
	// invariant for every future maintainer who might be tempted to
	// fmt.Println(...) something for debugging.
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("klyne-mcp: ")

	ctx := cmd.Context()
	if err := mcpserver.Run(ctx); err != nil {
		log.Printf("server stopped: %v", err)
		return err
	}
	return nil
}
