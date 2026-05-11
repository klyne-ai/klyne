package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// loader.go — JSON fixture loader.
//
// JSON was chosen over YAML because the standard library handles it
// without an extra dependency, the project's go.mod has no YAML
// importer today, and the fixture schema is small enough that the
// terseness gap is irrelevant.
//
// A fixture file may contain either a single fixture object or a
// top-level array of fixture objects. Files are loaded in lexical
// order so the resulting slice is deterministic.

// LoadFixtures walks the given directory and returns every fixture
// declared by every .json file underneath it. Files are read in
// lexical filename order; within a single file, array entries are
// preserved in source order.
//
// Returns an error wrapping the offending path on the FIRST malformed
// file — the runner fails fast rather than skip silently, so a typo
// in a fixture surfaces immediately.
func LoadFixtures(dir string) ([]LabelledFixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read fixture dir %s: %w", dir, err)
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	sort.Strings(paths)

	var out []LabelledFixture
	seen := map[string]bool{}
	for _, p := range paths {
		batch, err := loadFile(p)
		if err != nil {
			return nil, err
		}
		for _, fx := range batch {
			if fx.Name == "" {
				return nil, fmt.Errorf("fixture in %s missing required field 'name'", p)
			}
			if seen[fx.Name] {
				return nil, fmt.Errorf("duplicate fixture name %q (second occurrence in %s)", fx.Name, p)
			}
			seen[fx.Name] = true
			out = append(out, fx)
		}
	}
	return out, nil
}

// loadFile parses one JSON file. Accepts either a single object or
// a top-level array of objects.
func loadFile(path string) ([]LabelledFixture, error) {
	body, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	// Try array first, then single object. The two shapes are
	// disambiguated by the first non-whitespace byte.
	trimmed := skipSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var arr []LabelledFixture
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, fmt.Errorf("parse %s (array): %w", path, err)
		}
		return arr, nil
	}
	var one LabelledFixture
	if err := json.Unmarshal(body, &one); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return []LabelledFixture{one}, nil
}

// skipSpace returns body with leading ASCII whitespace removed. Tiny
// helper kept inline to avoid pulling in unicode for what is always
// JSON input.
func skipSpace(body []byte) []byte {
	for i, b := range body {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return body[i:]
		}
	}
	return nil
}
