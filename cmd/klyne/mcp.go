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
		// Prefer the lightweight klyne-hook stub when it's installed
		// alongside the main binary. The stub forwards hook events
		// to the long-running daemon over a Unix socket and only
		// falls back to spawning the full binary when the daemon is
		// unreachable — see internal/hookrpc for the full design.
		// When the stub isn't installed (older releases, manual go-
		// build), we transparently use the main binary like before.
		hookExe := resolveHookBinary(exe)
		if hookExe != exe {
			fmt.Fprintf(cmd.OutOrStdout(), "hook stub: using %s (low-memory path)\n", hookExe)
		}

		report, err := mcpserver.InstallAdvisorHook(hookExe)
		if err != nil {
			return fmt.Errorf("install advisor hook: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "advisor: %s — %s\n",
			report.Action, report.Path)

		// Install the PreToolUse safety-net hook so klyne snapshots
		// working-tree state before risky commands run.
		ptReport, err := mcpserver.InstallPreToolHook(hookExe)
		if err != nil {
			return fmt.Errorf("install pretool hook: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "safety-net: %s — %s\n",
			ptReport.Action, ptReport.Path)

		// Install the PreCompact compact-shield hook so klyne intercepts
		// native compact events and blocks them when a snapshot is armed.
		csReport, err := mcpserver.InstallPreCompactHook(hookExe)
		if err != nil {
			return fmt.Errorf("install compact-shield hook: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "compact-shield: %s — %s\n",
			csReport.Action, csReport.Path)

		// Stop hook for the deterministic session-end summary. Same
		// settings.json file, separate event ("Stop" vs
		// "UserPromptSubmit"). Idempotent — re-running only rewrites
		// when the entry would actually change.
		stopReport, err := mcpserver.InstallStopHook(hookExe)
		if err != nil {
			return fmt.Errorf("install stop hook: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "session-end hook: %s — %s\n",
			stopReport.Action, stopReport.Path)

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

		// Agent-driven Skills (~/.claude/skills/klyne-*) intentionally
		// NOT installed. They duplicated the static slash commands
		// installed above — same name, same handler, but shown as
		// `/klyne-<name>` in the slash UI alongside `/klyne:<name>`.
		// The static slash commands are the single user-facing surface;
		// the AI still gets auto-activation via the underlying
		// mcp__klyne__* tool registrations.

		fmt.Fprintln(cmd.OutOrStdout(),
			"klyne: advisor active — you'll see inline warnings in Claude Code when sessions drift, accelerate, or approach your 5-hour cap.")
		fmt.Fprintln(cmd.OutOrStdout(),
			"To enable the 5-hour-window advisor, run: klyne config set plan <pro|max-5x|max-20x|team>")
	}

	printRestartBanner(cmd, targets)
	return nil
}

// printRestartBanner emits a visually distinctive footer reminding the
// user that MCP servers and hook bindings only load at session start —
// so the host CLI must be restarted before any newly-installed klyne
// surfaces take effect. Tailors the body to the platforms that were
// actually installed.
func printRestartBanner(cmd *cobra.Command, targets []mcpserver.Platform) {
	var hasClaude, hasCodex bool
	for _, p := range targets {
		switch p {
		case mcpserver.PlatformClaude:
			hasClaude = true
		case mcpserver.PlatformCodex:
			hasCodex = true
		}
	}
	if !hasClaude && !hasCodex {
		return
	}

	out := cmd.OutOrStdout()
	fmt.Fprintln(out)
	fmt.Fprintln(out, "═══════════════════════════════════════════════════════════════")
	fmt.Fprintln(out, "  ⚠  RESTART YOUR AI CLI NOW")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  MCP servers and hooks only load at session start.")
	fmt.Fprintln(out, "  Until you restart, /klyne:* slash commands will fail and")
	fmt.Fprintln(out, "  the advisor/snapshot/compact-shield hooks won't fire.")
	if hasClaude {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Claude Code: quit and relaunch the CLI.")
	}
	if hasCodex {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "  Codex CLI: re-run `codex` to pick up the new MCP server.")
	}
	fmt.Fprintln(out, "═══════════════════════════════════════════════════════════════")
}

// resolveHookBinary picks the binary klyne mcp install should wire
// into Claude Code's hook entries. It prefers a `klyne-hook` stub
// sitting next to the main klyne binary (the layout `make install`
// produces) because the stub is ~4 MB resident vs the full klyne's
// ~100 MB — small enough that the macOS jetsam killer doesn't
// reflexively terminate it under memory pressure. When the stub
// isn't present, we fall back to klyneExe so older installations
// keep working exactly as before.
//
// The stub forwards every event to the long-running daemon over a
// Unix socket and exec's the full klyne binary when the daemon is
// unreachable, so users see no behavioural difference — only fewer
// "Failed with non-blocking status code" hook errors.
func resolveHookBinary(klyneExe string) string {
	dir := klyneExe
	for len(dir) > 0 && dir[len(dir)-1] != '/' && dir[len(dir)-1] != '\\' {
		dir = dir[:len(dir)-1]
	}
	candidate := dir + "klyne-hook"
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return klyneExe
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
