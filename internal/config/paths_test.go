package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

// withTempHome replaces HomeDir for the duration of fn and restores the
// original afterwards. Returns the temporary directory it injected.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := HomeDir
	HomeDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { HomeDir = orig })
	return dir
}

func TestConfigDir_MacLinux(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("macOS/Linux path test skipped on Windows")
	}
	home := withTempHome(t)
	got := ConfigDir()
	want := filepath.Join(home, ".klyne")
	if got != want {
		t.Errorf("ConfigDir() = %q; want %q", got, want)
	}
}

func TestConfigFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipped on Windows")
	}
	home := withTempHome(t)
	got := ConfigFile()
	want := filepath.Join(home, ".klyne", "config.toml")
	if got != want {
		t.Errorf("ConfigFile() = %q; want %q", got, want)
	}
}

func TestDBPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipped on Windows")
	}
	home := withTempHome(t)
	got := DBPath()
	want := filepath.Join(home, ".klyne", "klyne.db")
	if got != want {
		t.Errorf("DBPath() = %q; want %q", got, want)
	}
}

func TestPricingOverridePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipped on Windows")
	}
	home := withTempHome(t)
	got := PricingOverridePath()
	want := filepath.Join(home, ".klyne", "pricing.json")
	if got != want {
		t.Errorf("PricingOverridePath() = %q; want %q", got, want)
	}
}

// TestPaths_PerOS is a table-driven test verifying that the suffix
// produced by ConfigDir() matches expectations per runtime.GOOS.
// Cases that don't match the current host OS are skipped.
func TestPaths_PerOS(t *testing.T) {
	cases := []struct {
		os     string
		suffix string
	}{
		{"darwin", ".klyne"},
		{"linux", ".klyne"},
		{"windows", ".klyne"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.os, func(t *testing.T) {
			if runtime.GOOS != tc.os {
				t.Skipf("host is %q, not %q", runtime.GOOS, tc.os)
			}
			if runtime.GOOS == "windows" {
				// On Windows we rely on USERPROFILE; just check the suffix.
				got := ConfigDir()
				if filepath.Base(got) != tc.suffix {
					t.Errorf("ConfigDir() base = %q; want %q", filepath.Base(got), tc.suffix)
				}
				return
			}
			home := withTempHome(t)
			got := ConfigDir()
			want := filepath.Join(home, tc.suffix)
			if got != want {
				t.Errorf("ConfigDir() = %q; want %q", got, want)
			}
		})
	}
}
