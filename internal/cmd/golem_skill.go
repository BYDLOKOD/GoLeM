// Package cmd implements the glm CLI sub-commands.
package cmd

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// golemSkillContent is the global Claude Code skill that documents how to drive
// golems. It is embedded at build time so the operating manual ships inside the
// binary. Installed to <skillsDir>/golem/SKILL.md, it loads on demand (triggered
// by "golem"/"голем" or delegation intent) instead of being permanently injected
// into ~/.claude/CLAUDE.md.
//
//go:embed golem_skill.md
var golemSkillContent string

// golemSkillName is the directory and skill name under the skills root.
const golemSkillName = "golem"

// WriteGolemSkill installs the golem skill at skillsDir/golem/SKILL.md.
func WriteGolemSkill(skillsDir string) error {
	dir := filepath.Join(skillsDir, golemSkillName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create skill dir: %w", err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(golemSkillContent), 0o644); err != nil {
		return fmt.Errorf("write skill: %w", err)
	}
	return nil
}

// RemoveGolemSkill removes the golem skill directory. It no-ops when absent.
func RemoveGolemSkill(skillsDir string) error {
	dir := filepath.Join(skillsDir, golemSkillName)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove skill dir: %w", err)
	}
	return nil
}
