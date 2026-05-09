package config

import (
	"fmt"
	"strings"
)

// plan.go — plan-tier enum and cap lookup for the 5-hour-window
// advisory.
//
// Anthropic does NOT expose the user's exact 5-hour token cap via
// any public API. The values below are calibrated against publicly
// reported community numbers and the maintainer's own observed
// sessions. Honest framing in product copy: "estimated against
// your configured plan."
//
// Users on a non-standard plan can run:
//
//	klyne config set plan custom --cap=<tokens>
//
// to override the lookup with their own measured value.

// PlanTier is the user's Claude / Codex subscription tier.
type PlanTier string

const (
	// PlanTierEmpty means the user has not configured a plan yet.
	// The 5-hour-window advisor stays silent in this state.
	PlanTierEmpty PlanTier = ""
	// PlanTierPro is the Claude Pro plan.
	PlanTierPro PlanTier = "pro"
	// PlanTierMax5x is the Claude Max 5× plan.
	PlanTierMax5x PlanTier = "max-5x"
	// PlanTierMax20x is the Claude Max 20× plan.
	PlanTierMax20x PlanTier = "max-20x"
	// PlanTierTeam is the Claude Team plan (per-seat allowance).
	PlanTierTeam PlanTier = "team"
	// PlanTierCustom defers to PlanConfig.CustomCap rather than the
	// built-in lookup table. Used when the user's plan does not match
	// any of the canned tiers, or when they want to override the
	// default with a personally measured value.
	PlanTierCustom PlanTier = "custom"
)

// PlanConfig is the [plan] table in ~/.klyne/config.toml.
type PlanConfig struct {
	// Tier names the subscription tier. Empty means "skip the
	// 5-hour-window advisory."
	Tier PlanTier `toml:"tier" json:"tier"`
	// CustomCap is the user-supplied 5-hour effective-token cap when
	// Tier is PlanTierCustom. Ignored otherwise. Zero means
	// "unconfigured" — same effect as PlanTierEmpty.
	CustomCap int64 `toml:"custom_cap" json:"custom_cap"`
}

// FiveHourCap returns the effective per-window token cap for the
// configured plan, or 0 when the plan is unset / custom-but-zero.
// A zero return value tells the advisor to silently skip the
// 5-hour-window trigger.
//
// The numbers in the lookup table reflect community-reported
// 5-hour effective input ceilings as of 2026-05. They are
// intentionally conservative — better to fire the advisory a touch
// early than to miss the actual cap.
func (p PlanConfig) FiveHourCap() int64 {
	switch p.Tier {
	case PlanTierPro:
		return 44_000_000
	case PlanTierMax5x:
		return 220_000_000
	case PlanTierMax20x:
		return 880_000_000
	case PlanTierTeam:
		return 220_000_000
	case PlanTierCustom:
		return p.CustomCap
	default:
		return 0
	}
}

// IsConfigured returns true when the user has selected a plan that
// produces a non-zero cap.
func (p PlanConfig) IsConfigured() bool {
	return p.FiveHourCap() > 0
}

// ParsePlanTier turns a CLI argument like "max-5x" into a PlanTier.
// Returns an error when the value does not match any known tier.
func ParsePlanTier(s string) (PlanTier, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pro":
		return PlanTierPro, nil
	case "max-5x", "max5x":
		return PlanTierMax5x, nil
	case "max-20x", "max20x":
		return PlanTierMax20x, nil
	case "team":
		return PlanTierTeam, nil
	case "custom":
		return PlanTierCustom, nil
	case "skip", "off", "none", "":
		return PlanTierEmpty, nil
	default:
		return "", fmt.Errorf("unknown plan tier %q (valid: pro, max-5x, max-20x, team, custom, skip)", s)
	}
}

// SupportedPlanTiers returns the canonical strings the user can pass
// to `klyne config set plan <tier>`. Useful for help output.
func SupportedPlanTiers() []string {
	return []string{"pro", "max-5x", "max-20x", "team", "custom", "skip"}
}
