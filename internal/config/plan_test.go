package config

import "testing"

func TestPlanConfig_FiveHourCap(t *testing.T) {
	cases := []struct {
		name string
		in   PlanConfig
		want int64
	}{
		{"empty", PlanConfig{}, 0},
		{"pro", PlanConfig{Tier: PlanTierPro}, 44_000_000},
		{"max-5x", PlanConfig{Tier: PlanTierMax5x}, 220_000_000},
		{"max-20x", PlanConfig{Tier: PlanTierMax20x}, 880_000_000},
		{"team", PlanConfig{Tier: PlanTierTeam}, 220_000_000},
		{"custom non-zero", PlanConfig{Tier: PlanTierCustom, CustomCap: 1_500_000}, 1_500_000},
		{"custom zero", PlanConfig{Tier: PlanTierCustom, CustomCap: 0}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.FiveHourCap(); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParsePlanTier(t *testing.T) {
	cases := []struct {
		in    string
		want  PlanTier
		isErr bool
	}{
		{"pro", PlanTierPro, false},
		{"PRO", PlanTierPro, false},
		{" max-5x ", PlanTierMax5x, false},
		{"max5x", PlanTierMax5x, false},
		{"max-20x", PlanTierMax20x, false},
		{"team", PlanTierTeam, false},
		{"custom", PlanTierCustom, false},
		{"skip", PlanTierEmpty, false},
		{"", PlanTierEmpty, false},
		{"none", PlanTierEmpty, false},
		{"weird", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParsePlanTier(tc.in)
			if tc.isErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %v", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPlanConfig_IsConfigured(t *testing.T) {
	if (PlanConfig{}).IsConfigured() {
		t.Fatalf("empty plan should not be configured")
	}
	if !(PlanConfig{Tier: PlanTierPro}).IsConfigured() {
		t.Fatalf("pro plan should be configured")
	}
	if (PlanConfig{Tier: PlanTierCustom, CustomCap: 0}).IsConfigured() {
		t.Fatalf("custom plan with 0 cap should not be configured")
	}
	if !(PlanConfig{Tier: PlanTierCustom, CustomCap: 1000}).IsConfigured() {
		t.Fatalf("custom plan with non-zero cap should be configured")
	}
}
