package modes

import (
	"fmt"
	"strings"

	"github.com/patriceckhart/zot/packages/agent/skills"
)

const skillCommandPrefix = "/skill:"

func isSkillCommand(text string) bool {
	text = strings.TrimSpace(text)
	return len(text) >= len(skillCommandPrefix) && strings.EqualFold(text[:len(skillCommandPrefix)], skillCommandPrefix)
}

// expandSkillCommand expands /skill:<name> [request] into a prompt containing
// the complete skill instructions. The boolean reports whether text uses the
// skill-command syntax.
func expandSkillCommand(text string, available []*skills.Skill) (prompt string, recognized bool, err error) {
	text = strings.TrimSpace(text)
	if !isSkillCommand(text) {
		return "", false, nil
	}

	head := text
	args := ""
	if idx := strings.IndexAny(text, " \t\n"); idx >= 0 {
		head = text[:idx]
		args = strings.TrimSpace(text[idx:])
	}
	name := head[len(skillCommandPrefix):]
	if name == "" {
		return "", true, fmt.Errorf("usage: /skill:<name> [request]")
	}

	skill := skills.FindByName(available, name)
	if skill == nil {
		return "", true, fmt.Errorf("unknown skill %q; run /skills to see what is available", name)
	}
	return skills.InvocationPrompt(skill, args), true, nil
}
