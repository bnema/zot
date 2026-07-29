package skills

import (
	"fmt"
	"path/filepath"
	"strings"
)

// InvocationPrompt expands a user-invoked skill into a prompt that includes
// its complete instructions. Arguments are appended as the request to perform.
func InvocationPrompt(skill *Skill, args string) string {
	if skill == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Use the following skill for this request. Follow its instructions.\n\n")
	fmt.Fprintf(&sb, "# Skill: %s\n\n", skill.Name)
	if desc := strings.TrimSpace(skill.Description); desc != "" {
		sb.WriteString(desc)
		sb.WriteString("\n\n")
	}
	if skill.Path != "" {
		fmt.Fprintf(&sb, "Skill directory: %s\n\n", filepath.Dir(skill.Path))
	}
	sb.WriteString("---\n\n")
	sb.WriteString(strings.TrimSpace(skill.Body))
	if args = strings.TrimSpace(args); args != "" {
		sb.WriteString("\n\n---\n\nUser request:\n")
		sb.WriteString(args)
	}
	return sb.String()
}
