package skills

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestInvocationPromptIncludesInstructionsLocationAndRequest(t *testing.T) {
	skill := &Skill{
		Name:        "code-review",
		Description: "Review a change.",
		Body:        "1. Read the diff.\n2. Report findings.",
		Path:        filepath.Join("tmp", "skills", "code-review", "SKILL.md"),
	}

	got := InvocationPrompt(skill, "review the current branch")
	for _, want := range []string{
		"Use the following skill for this request.",
		"# Skill: code-review",
		"Skill directory: " + filepath.Join("tmp", "skills", "code-review"),
		"1. Read the diff.",
		"User request:\nreview the current branch",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("InvocationPrompt() missing %q:\n%s", want, got)
		}
	}
}
