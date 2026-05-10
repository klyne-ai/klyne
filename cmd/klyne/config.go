package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/klyne-ai/klyne/internal/config"
)

// config.go — `klyne config` subcommands.
//
// v1 surfaces a single key: the user's plan tier, which drives the
// 5-hour-window advisory's denominator. Future work can expand this
// surface, but the contract here is intentionally minimal so we
// don't accumulate per-key flags before we know which configuration
// surfaces matter.

// newConfigCmd registers `klyne config` with subcommands for
// reading and writing fields users care about today.
func newConfigCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Read or update klyne configuration",
		Long: `Read or update klyne configuration values stored in ~/.klyne/config.toml.

Subcommands:
  klyne config set plan <tier>         Configure the plan tier driving the 5-hour-window advisor.
  klyne config set plan custom --cap=<tokens>
                                       Set a user-defined effective-token cap when no canonical tier matches.
  klyne config get plan                Print the current plan tier and effective cap.
  klyne config show                    Print every configured value (TOML).

Plan tiers (case-insensitive):
  pro      ~44M effective tokens / 5h
  max-5x   ~220M
  max-20x  ~880M
  team     ~220M
  custom   user-supplied via --cap
  skip     disable the 5-hour-window advisor (default)`,
		SilenceUsage: true,
	}
	c.AddCommand(newConfigSetCmd())
	c.AddCommand(newConfigGetCmd())
	c.AddCommand(newConfigShowCmd())
	return c
}

func newConfigSetCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "set",
		Short: "Set a configuration value",
		Args:  cobra.MinimumNArgs(1),
	}
	c.AddCommand(newConfigSetPlanCmd())
	c.AddCommand(newConfigSetAdvisorCmd())
	return c
}

// newConfigSetAdvisorCmd registers `klyne config set advisor on|off`.
// Toggles the UserPromptSubmit hook's kill switch without disturbing
// any other configuration. Persisted to ~/.klyne/config.toml so the
// next hook invocation honours it.
func newConfigSetAdvisorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "advisor <on|off>",
		Short: "Enable or disable the proactive advisor hook",
		Long: `Toggle the proactive advisor's UserPromptSubmit hook.

When set to "off", klyne advise exits silently for every prompt.
The hook entry stays installed in ~/.claude/settings.json — the
gate lives in klyne itself, so re-enabling does not require
re-running klyne mcp install.

Useful when:
  - Developing klyne (the relevance trigger fires on klyne's own
    transcripts during dogfooding).
  - Working in a tightly-scoped session where you do not need
    proactive nudges.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			val := strings.ToLower(strings.TrimSpace(args[0]))
			var disabled bool
			switch val {
			case "on", "enabled", "true", "yes":
				disabled = false
			case "off", "disabled", "false", "no":
				disabled = true
			default:
				return fmt.Errorf("advisor must be one of: on, off (got %q)", args[0])
			}
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			cfg.Advisor.Disabled = disabled
			if err := config.Save(cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			if disabled {
				fmt.Fprintln(cmd.OutOrStdout(),
					"Advisor disabled. The UserPromptSubmit hook stays installed but will exit silently.")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(),
					"Advisor enabled. Inline warnings will fire when sessions drift, accelerate, or approach your 5-hour cap.")
			}
			return nil
		},
		SilenceUsage: true,
	}
}

func newConfigSetPlanCmd() *cobra.Command {
	var customCap int64
	c := &cobra.Command{
		Use:   "plan <tier>",
		Short: "Set the plan tier",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tier, err := config.ParsePlanTier(args[0])
			if err != nil {
				return err
			}
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			cfg.Plan.Tier = tier
			if tier == config.PlanTierCustom {
				if customCap <= 0 {
					return fmt.Errorf("plan custom requires --cap=<tokens> (positive integer)")
				}
				cfg.Plan.CustomCap = customCap
			} else {
				cfg.Plan.CustomCap = 0
			}
			if err := config.Save(cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			cap := cfg.Plan.FiveHourCap()
			if cap <= 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Plan tier set to %q. The 5-hour-window advisor will stay silent.\n", tier)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Plan tier set to %q (~%s effective tokens / 5h). 5-hour-window advisor is active.\n",
					tier, humanTokens(cap))
			}
			return nil
		},
	}
	c.Flags().Int64Var(&customCap, "cap", 0,
		"effective-token cap for tier=custom; ignored otherwise")
	return c
}

func newConfigGetCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "get",
		Short: "Read a configuration value",
		Args:  cobra.MinimumNArgs(1),
	}
	c.AddCommand(&cobra.Command{
		Use:   "plan",
		Short: "Print the current plan tier and effective cap",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			tier := cfg.Plan.Tier
			if tier == config.PlanTierEmpty {
				fmt.Fprintln(cmd.OutOrStdout(), "plan: (unset) — run `klyne config set plan <tier>` to enable the 5-hour advisor.")
				return nil
			}
			cap := cfg.Plan.FiveHourCap()
			fmt.Fprintf(cmd.OutOrStdout(), "plan: %s (estimated cap ~%s effective tokens / 5h)\n",
				tier, humanTokens(cap))
			return nil
		},
	})
	c.AddCommand(&cobra.Command{
		Use:   "advisor",
		Short: "Print whether the proactive advisor hook is on or off",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			state := "on"
			if cfg.Advisor.Disabled {
				state = "off"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "advisor: %s\n", state)
			return nil
		},
	})
	return c
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print every configured value (TOML)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			body, err := toml.Marshal(cfg)
			if err != nil {
				return fmt.Errorf("marshal config: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), strings.TrimRight(string(body), "\n"))
			return nil
		},
	}
}

// humanTokens renders a token count like 220_000_000 as "220M" so
// the CLI output stays readable at a glance.
func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return formatFloat(float64(n)/1e9) + "B"
	case n >= 1_000_000:
		return formatFloat(float64(n)/1e6) + "M"
	case n >= 1_000:
		return formatFloat(float64(n)/1e3) + "K"
	default:
		return strconv.FormatInt(n, 10)
	}
}

func formatFloat(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(f, 'f', 1, 64), "0"), ".")
}
