package mcpserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klyne-ai/klyne/internal/connectors"
)

// codexSessionsDir returns the absolute path to ~/.codex/sessions, the
// root under which Codex CLI writes date-partitioned rollout files.
// Returns ("", error) when HOME cannot be resolved — every call site
// must surface that error rather than guess.
func codexSessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

// codexListSessionsForCWD scans every rollout-*.jsonl under
// ~/.codex/sessions/YYYY/MM/DD/, reads each file's session_meta line
// for the launch cwd, and returns candidates whose sessionCwd is the
// nearest ancestor of (or equal to) the requested cwd.
//
// "Nearest ancestor" is the longest sessionCwd that is an ancestor of
// cwd. This matches Claude's "walk up to nearest project dir" rule:
// if the user has Codex sessions in /tmp/parent AND /tmp/parent/sub,
// asking for /tmp/parent/sub/inner returns only the sub sessions.
//
// Returns nil with no error when ~/.codex/sessions does not exist
// (Codex not installed) or when no sessions match cwd.
func codexListSessionsForCWD(cwd string) ([]SessionCandidate, error) {
	root, err := codexSessionsDir()
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat codex sessions dir: %w", err)
	}
	if !info.IsDir() {
		return nil, nil
	}

	files, err := walkCodexRolloutFiles(root)
	if err != nil {
		return nil, fmt.Errorf("walk codex rollouts: %w", err)
	}

	type pooled struct {
		cand       SessionCandidate
		sessionCwd string
	}
	var (
		pool    []pooled
		bestLen int
		now     = time.Now()
	)
	for _, path := range files {
		meta := readCodexCandidateMeta(path)
		if meta.cwd == "" {
			continue
		}
		if !isAncestorOrEqual(meta.cwd, cwd) {
			continue
		}
		fi, statErr := os.Stat(path)
		if statErr != nil {
			continue
		}
		sessionID := meta.sessionID
		if sessionID == "" {
			// Fall back to the rollout filename's tail (after rollout-
			// prefix and before .jsonl) when the meta line was malformed.
			base := filepath.Base(path)
			base = strings.TrimPrefix(base, "rollout-")
			base = strings.TrimSuffix(base, ".jsonl")
			sessionID = base
		}
		cand := SessionCandidate{
			SessionID: sessionID,
			Path:      path,
			ModTime:   fi.ModTime(),
			MsgCount:  meta.msgCount,
			Preview:   meta.preview,
			IsActive:  now.Sub(fi.ModTime()) <= activeWindow,
			CLI:       connectors.CLICodex,
		}
		pool = append(pool, pooled{cand: cand, sessionCwd: meta.cwd})
		if l := len(meta.cwd); l > bestLen {
			bestLen = l
		}
	}
	if len(pool) == 0 {
		return nil, nil
	}
	out := make([]SessionCandidate, 0, len(pool))
	for _, p := range pool {
		if len(p.sessionCwd) == bestLen {
			out = append(out, p.cand)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModTime.After(out[j].ModTime)
	})
	return out, nil
}

// walkCodexRolloutFiles returns absolute paths to every
// rollout-*.jsonl under root's YYYY/MM/DD partitioning. Bounded depth
// is intentional — we do not recurse arbitrarily into other
// subdirectories Codex may add later, to keep the scan O(sessions).
func walkCodexRolloutFiles(root string) ([]string, error) {
	yearEntries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, ye := range yearEntries {
		if !ye.IsDir() {
			continue
		}
		yearPath := filepath.Join(root, ye.Name())
		monthEntries, err := os.ReadDir(yearPath)
		if err != nil {
			continue
		}
		for _, me := range monthEntries {
			if !me.IsDir() {
				continue
			}
			monthPath := filepath.Join(yearPath, me.Name())
			dayEntries, err := os.ReadDir(monthPath)
			if err != nil {
				continue
			}
			for _, de := range dayEntries {
				if !de.IsDir() {
					continue
				}
				dayPath := filepath.Join(monthPath, de.Name())
				entries, err := os.ReadDir(dayPath)
				if err != nil {
					continue
				}
				for _, e := range entries {
					if e.IsDir() {
						continue
					}
					name := e.Name()
					if strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl") {
						files = append(files, filepath.Join(dayPath, name))
					}
				}
			}
		}
	}
	return files, nil
}

// codexCandidateMeta is the bounded-scan result for one rollout file.
type codexCandidateMeta struct {
	sessionID string
	cwd       string
	preview   string
	msgCount  int
}

// readCodexCandidateMeta opens a Codex rollout file and extracts the
// session id, launch cwd, first user-message preview, and total line
// count. One bounded scan; tolerates malformed lines silently. Bounded
// at 16 MiB per token to match the audit/Claude scanners (some Codex
// turn_context blocks can run hundreds of KB).
func readCodexCandidateMeta(path string) codexCandidateMeta {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return codexCandidateMeta{}
	}
	defer f.Close()

	const maxScanToken = 16 * 1024 * 1024
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxScanToken)

	var meta codexCandidateMeta
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		meta.msgCount++
		if meta.sessionID != "" && meta.cwd != "" && meta.preview != "" {
			continue
		}
		var env struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}
		switch env.Type {
		case "session_meta":
			if meta.sessionID != "" && meta.cwd != "" {
				continue
			}
			var p struct {
				ID  string `json:"id"`
				CWD string `json:"cwd"`
			}
			if json.Unmarshal(env.Payload, &p) == nil {
				if meta.sessionID == "" {
					meta.sessionID = p.ID
				}
				if meta.cwd == "" {
					meta.cwd = p.CWD
				}
			}
		case "response_item":
			if meta.preview != "" {
				continue
			}
			var p struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				continue
			}
			if p.Type != "message" || p.Role != "user" {
				continue
			}
			for _, c := range p.Content {
				if c.Type == "input_text" && c.Text != "" {
					meta.preview = truncatePreview(strings.TrimSpace(c.Text))
					break
				}
			}
		}
	}
	return meta
}

// isAncestorOrEqual reports whether `ancestor` equals or is a parent
// directory of `descendant`. Both inputs are expected to be absolute
// paths; trailing slashes are normalised.
func isAncestorOrEqual(ancestor, descendant string) bool {
	if ancestor == "" || descendant == "" {
		return false
	}
	ancestor = filepath.Clean(ancestor)
	descendant = filepath.Clean(descendant)
	if ancestor == descendant {
		return true
	}
	// Prefix match must terminate at a path separator so /tmp/a does
	// not match /tmp/abc.
	return strings.HasPrefix(descendant, ancestor+string(filepath.Separator))
}
