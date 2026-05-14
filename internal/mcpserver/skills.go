package mcpserver

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Skill install
// =============
// Claude Code v2.x discovers user-level Skills under ~/.claude/skills/.
// Each skill lives in its own directory with a SKILL.md file carrying
// `name` + `description` frontmatter that the model matches against the
// current situation. When the description fits, Claude auto-invokes the
// skill via the Skill tool — no `/klyne:` typing required.
//
// klyne ships skill bundles alongside the existing slash commands so a
// degrading session can trigger the right rescue surface even when the
// user hasn't reached for it. The slash commands stay the user-driven
// path; skills are the agent-driven path.
//
// Bundle layout under internal/mcpserver/skills/:
//
//	skills/
//	  klyne-health/
//	    SKILL.md
//	  klyne-<future>/
//	    SKILL.md
//	    references/        (optional; copied verbatim)
//
// The embed walker preserves the tree exactly. Adding a new skill is
// "drop a directory under skills/" — no Go changes required.
//
// Idempotent: re-running install overwrites with the current bundled
// content. Existing user-owned files under ~/.claude/skills/ that don't
// match a klyne bundle are left untouched.

//go:embed skills/*
var skillsFS embed.FS

// SkillsReport carries the outcome of InstallSkills.
type SkillsReport struct {
	// Dir is the destination root the skills were written under
	// (typically ~/.claude/skills).
	Dir string
	// Action is "added", "updated", or "already-installed".
	Action InstallAction
	// Files is the count of files written or verified across all
	// bundled skills (SKILL.md plus any reference files).
	Files int
	// Skills is the list of skill names that were processed.
	Skills []string
}

// claudeSkillsDir returns ~/.claude/skills. Returns ("", error) when
// HOME is unresolvable — same contract as the other path helpers.
func claudeSkillsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".claude", "skills"), nil
}

// InstallSkills writes every embedded skill bundle under
// ~/.claude/skills/<skill-name>/. Creates the directory tree when
// missing. Returns a report indicating whether the destination was
// freshly created (added), had at least one file rewritten (updated),
// or matched the bundled content byte-for-byte (already-installed).
func InstallSkills() (*SkillsReport, error) {
	dest, err := claudeSkillsDir()
	if err != nil {
		return nil, err
	}

	// Track whether any klyne-* directory existed before this install
	// so we can report "added" vs "updated" precisely. We only count
	// klyne-owned bundles — a pre-existing ~/.claude/skills/ with
	// unrelated user skills should still show as "added" for klyne.
	rewroteAny := false
	preexistedAny := false
	files := 0
	skills := []string{}

	err = fs.WalkDir(skillsFS, "skills", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "skills" {
			return nil
		}
		// Skill name = the first path segment after "skills/".
		rel, err := filepath.Rel("skills", path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			parts := splitFirstSegment(rel)
			if parts[1] == "" {
				skills = append(skills, parts[0])
				if _, statErr := os.Stat(filepath.Join(dest, parts[0])); statErr == nil {
					preexistedAny = true
				}
			}
			return os.MkdirAll(filepath.Join(dest, rel), 0o755)
		}
		content, err := fs.ReadFile(skillsFS, path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}
		target := filepath.Join(dest, rel)
		existing, readErr := os.ReadFile(target)
		switch {
		case os.IsNotExist(readErr):
			// New file.
		case readErr != nil:
			return fmt.Errorf("read %s: %w", target, readErr)
		case bytes.Equal(existing, content):
			files++
			return nil
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		rewroteAny = true
		files++
		return nil
	})
	if err != nil {
		return nil, err
	}

	action := InstallActionAlreadyInstalled
	switch {
	case !preexistedAny:
		action = InstallActionAdded
	case rewroteAny:
		action = InstallActionUpdated
	}
	return &SkillsReport{Dir: dest, Action: action, Files: files, Skills: skills}, nil
}

// splitFirstSegment returns [first-segment, rest]. For "klyne-health"
// returns ["klyne-health", ""]; for "klyne-health/references/foo.md"
// returns ["klyne-health", "references/foo.md"].
func splitFirstSegment(p string) [2]string {
	for i := 0; i < len(p); i++ {
		if p[i] == filepath.Separator || p[i] == '/' {
			return [2]string{p[:i], p[i+1:]}
		}
	}
	return [2]string{p, ""}
}
