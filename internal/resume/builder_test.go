package resume

import (
	"testing"
)

// TestResumeBuilder_PerOS exercises ClaudeCmd and CodexCmd across different
// OS values injected via the package-level osName variable.
func TestResumeBuilder_PerOS(t *testing.T) {
	t.Run("ClaudeCmd", func(t *testing.T) {
		tests := []struct {
			name        string
			goos        string
			sessionID   string
			projectPath string
			want        string
		}{
			{
				name:        "unix_no_spaces",
				goos:        "linux",
				sessionID:   "abc-123",
				projectPath: "/home/user/myproject",
				want:        "claude --resume abc-123 -p '/home/user/myproject'",
			},
			{
				name:        "unix_with_spaces",
				goos:        "darwin",
				sessionID:   "abc-123",
				projectPath: "/home/user/my project",
				want:        "claude --resume abc-123 -p '/home/user/my project'",
			},
			{
				name:        "unix_with_single_quote",
				goos:        "linux",
				sessionID:   "abc-123",
				projectPath: "/home/user/it's here",
				want:        `claude --resume abc-123 -p '/home/user/it'\''s here'`,
			},
			{
				name:        "unix_empty_path",
				goos:        "linux",
				sessionID:   "abc-123",
				projectPath: "",
				want:        "claude --resume abc-123",
			},
			{
				name:        "windows_no_spaces",
				goos:        "windows",
				sessionID:   "abc-123",
				projectPath: `C:\Users\user\myproject`,
				want:        `claude --resume abc-123 -p C:\Users\user\myproject`,
			},
			{
				name:        "windows_with_spaces",
				goos:        "windows",
				sessionID:   "abc-123",
				projectPath: `C:\Users\user\my project`,
				want:        `claude --resume abc-123 -p "C:\Users\user\my project"`,
			},
			{
				name:        "windows_empty_path",
				goos:        "windows",
				sessionID:   "xyz-999",
				projectPath: "",
				want:        "claude --resume xyz-999",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// Inject OS name.
				orig := osName
				osName = tt.goos
				defer func() { osName = orig }()

				got := ClaudeCmd(tt.sessionID, tt.projectPath)
				if got != tt.want {
					t.Errorf("ClaudeCmd(%q, %q) with GOOS=%q\n  got:  %q\n  want: %q",
						tt.sessionID, tt.projectPath, tt.goos, got, tt.want)
				}
			})
		}
	})

	t.Run("CodexCmd", func(t *testing.T) {
		// CodexCmd always returns the same string regardless of OS or args.
		tests := []struct {
			name        string
			goos        string
			sessionID   string
			projectPath string
		}{
			{"linux", "linux", "abc-123", "/home/user/project"},
			{"darwin", "darwin", "xyz-456", "/Users/user/project"},
			{"windows", "windows", "win-789", `C:\Users\user\project`},
			{"empty_args", "linux", "", ""},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				orig := osName
				osName = tt.goos
				defer func() { osName = orig }()

				got := CodexCmd(tt.sessionID, tt.projectPath)
				want := "codex resume --last"
				if got != want {
					t.Errorf("CodexCmd() = %q, want %q", got, want)
				}
			})
		}
	})
}

// TestQuoteUnix verifies the Unix path quoting helper directly.
func TestQuoteUnix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/simple/path", "'/simple/path'"},
		{"/path with spaces", "'/path with spaces'"},
		{"/path/with'quote", `'/path/with'\''quote'`},
		{"", "''"},
		{"/a/b/c", "'/a/b/c'"},
	}

	for _, tt := range tests {
		got := quoteUnix(tt.input)
		if got != tt.want {
			t.Errorf("quoteUnix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestQuoteWindows verifies the Windows path quoting helper directly.
func TestQuoteWindows(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`C:\simple\path`, `C:\simple\path`},
		{`C:\path with spaces`, `"C:\path with spaces"`},
		{`C:\tab	path`, `"C:\tab	path"`},
		{"", ""},
	}

	for _, tt := range tests {
		got := quoteWindows(tt.input)
		if got != tt.want {
			t.Errorf("quoteWindows(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
