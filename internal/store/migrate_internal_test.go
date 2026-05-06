// Package store internal tests exercise unexported migration helpers.
package store

import (
	"testing"
)

// TestVersionFromFilename_Valid exercises valid filenames.
func TestVersionFromFilename_Valid(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		{"001_init.sql", 1},
		{"002_fts.sql", 2},
		{"003_summaries.sql", 3},
		{"100_foo.sql", 100},
	}
	for _, tc := range cases {
		got, err := versionFromFilename(tc.name)
		if err != nil {
			t.Errorf("versionFromFilename(%q) error: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("versionFromFilename(%q) = %d; want %d", tc.name, got, tc.want)
		}
	}
}

// TestVersionFromFilename_Invalid exercises filenames that don't start with a
// numeric prefix — covers the nil-match error branch.
func TestVersionFromFilename_Invalid(t *testing.T) {
	invalids := []string{
		"init.sql",
		"_001_init.sql",
		"abc_init.sql",
		"",
	}
	for _, name := range invalids {
		if _, err := versionFromFilename(name); err == nil {
			t.Errorf("versionFromFilename(%q) expected error, got nil", name)
		}
	}
}

// TestIsNoSuchTableErr covers the nil-input branch and the false branch.
func TestIsNoSuchTableErr(t *testing.T) {
	// nil → false
	if isNoSuchTableErr(nil) {
		t.Error("isNoSuchTableErr(nil) = true; want false")
	}

	// real "no such table" error string → true
	type sqlErr struct{ msg string }
	// Use a simple error wrapper that contains the relevant substring.
	errNoTable := &noSuchTableError{}
	if !isNoSuchTableErr(errNoTable) {
		t.Error("isNoSuchTableErr(noSuchTableError) = false; want true")
	}

	// unrelated error → false
	errOther := &unrelatedError{}
	if isNoSuchTableErr(errOther) {
		t.Error("isNoSuchTableErr(unrelatedError) = true; want false")
	}
}

type noSuchTableError struct{}

func (e *noSuchTableError) Error() string {
	return "sqlite: no such table: schema_migrations"
}

type unrelatedError struct{}

func (e *unrelatedError) Error() string {
	return "some other database error"
}
