// Package policy loads the risky-command pattern list and matches
// incoming tool-call commands against it.
package policy

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

// Severity is the risk level assigned to a matched pattern.
type Severity string

const (
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// Pattern is one entry from risky_commands.json.
type Pattern struct {
	ID                string   `json:"id"`
	Regex             string   `json:"regex"`
	Severity          Severity `json:"severity"`
	Snapshot          bool     `json:"snapshot"`
	BlockUnlessConfirm bool    `json:"block_unless_confirm"`
}

// policy is the on-disk JSON shape.
type policy struct {
	Version  int       `json:"version"`
	Patterns []Pattern `json:"patterns"`
}

// MatchResult is the output of a single Match call.
type MatchResult struct {
	Matched            bool
	PatternID          string
	Severity           Severity
	Snapshot           bool
	BlockUnlessConfirm bool
}

// Matcher holds a compiled set of patterns for fast repeated matching.
type Matcher struct {
	patterns []compiledPattern
}

type compiledPattern struct {
	Pattern
	re *regexp.Regexp
}

// LoadFile reads and compiles the policy from the given JSON file path.
func LoadFile(path string) (*Matcher, error) {
	body, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("policy: read %s: %w", path, err)
	}
	return LoadJSON(body)
}

// LoadJSON parses and compiles a policy from raw JSON bytes.
func LoadJSON(data []byte) (*Matcher, error) {
	var p policy
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("policy: parse json: %w", err)
	}
	m := &Matcher{patterns: make([]compiledPattern, 0, len(p.Patterns))}
	for _, pat := range p.Patterns {
		re, err := regexp.Compile(pat.Regex)
		if err != nil {
			return nil, fmt.Errorf("policy: compile regex for %q: %w", pat.ID, err)
		}
		m.patterns = append(m.patterns, compiledPattern{Pattern: pat, re: re})
	}
	return m, nil
}

// Match checks command against every compiled pattern and returns the first
// match. The command should be the raw shell command string (e.g. the
// "command" field from a Bash tool-call input).
func (m *Matcher) Match(command string) MatchResult {
	for _, cp := range m.patterns {
		if cp.re.MatchString(command) {
			return MatchResult{
				Matched:            true,
				PatternID:          cp.ID,
				Severity:           cp.Severity,
				Snapshot:           cp.Snapshot,
				BlockUnlessConfirm: cp.BlockUnlessConfirm,
			}
		}
	}
	return MatchResult{}
}
