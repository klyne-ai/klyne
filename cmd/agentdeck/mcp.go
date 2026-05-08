package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/mcpserver"
)

// newMcpCmd registers `agentdeck mcp`.
//
// The command runs the agentdeck MCP server over stdio, the standard
// transport that Claude Code (and other MCP hosts) use to spawn local
// subprocess servers. To wire it into Claude Code, add to .mcp.json:
//
//	{
//	  "mcpServers": {
//	    "agentdeck": {
//	      "command": "agentdeck",
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
		Short: "Run the agentdeck MCP server over stdio",
		Long: `Run the agentdeck MCP server over stdio.

Designed to be spawned by Claude Code (or any other MCP host) as a
subprocess. Stdout is the JSON-RPC channel; logs go to stderr. The
agentdeck daemon does NOT need to be running.

Tools (auto-invoked by the AI):
  list_sessions             — enumerate Claude+Codex sessions in the cwd's project
  search_messages           — full-text search across every indexed session
                              (REQUIRES the agentdeck daemon to be running)
  get_context_health        — verdict + bloat scorecard
  generate_handoff          — paste-ready Markdown handoff
  get_pre_compact_context   — recover what /compact ate (Claude) or
                              decode replacement_history (Codex)

Slash prompts (user-triggered via /):
  /mcp__agentdeck__health      → live get_context_health
  /mcp__agentdeck__sessions    → live list_sessions
  /mcp__agentdeck__search      → live search_messages
  /mcp__agentdeck__handoff     → live generate_handoff
  /mcp__agentdeck__precompact  → live get_pre_compact_context

To register the server with each host, run: agentdeck mcp install`,
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

// newMcpInstallCmd registers `agentdeck mcp install`.
//
// Defaults to auto-detect: writes the agentdeck entry into every host
// config file present on disk (~/.claude.json, ~/.codex/config.toml).
// `--platform` overrides the auto-detect for users who only want one
// host updated. The command is idempotent — safe to re-run after every
// `go install`.
func newMcpInstallCmd() *cobra.Command {
	var platform string
	c := &cobra.Command{
		Use:   "install",
		Short: "Register the agentdeck MCP server in Claude Code and/or Codex configs",
		Long: `Write (or update) the agentdeck MCP server entry in each detected host's config file.

Without --platform, installs into every host whose config exists on disk:
  Claude Code:  ~/.claude.json (mcpServers.agentdeck)
  Codex CLI:    ~/.codex/config.toml ([mcp_servers.agentdeck])

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

// runMcpInstall resolves the agentdeck binary path, decides which
// platforms to install into, and prints a one-line outcome per platform.
// Errors short-circuit (we don't continue past the first install failure
// — partial install state is harder to reason about than "rerun after
// fixing the error").
func runMcpInstall(cmd *cobra.Command, platformFlag string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve agentdeck binary path: %w", err)
	}

	targets, err := resolveInstallTargets(platformFlag)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(),
			"No host configs detected at ~/.claude.json or ~/.codex/config.toml.\n"+
				"Run Claude Code or Codex CLI once to create their config, then re-run `agentdeck mcp install`,\n"+
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
	return nil
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
	log.SetPrefix("agentdeck-mcp: ")

	ctx := cmd.Context()
	if err := mcpserver.Run(ctx); err != nil {
		log.Printf("server stopped: %v", err)
		return err
	}
	return nil
}
