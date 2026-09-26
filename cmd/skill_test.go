package cmd

import (
	"os"
	"strings"
	"testing"
)

// The skill ships twice: embedded in the binary (skill install) and in
// .claude/skills for Claude Code sessions inside this repo. They must match.
func TestSkillCopiesMatch(t *testing.T) {
	repo, err := os.ReadFile("../.claude/skills/datadog/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(repo) != string(skillMD) {
		t.Fatal(".claude/skills/datadog/SKILL.md differs from cmd/skill_data/SKILL.md: copy one over the other")
	}
	if !strings.HasPrefix(string(skillMD), "---\nname: datadog\ndescription: ") {
		t.Fatal("the skill needs its frontmatter (name, description)")
	}
}
