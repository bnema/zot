package modes

import (
	"strings"
	"testing"

	"github.com/patriceckhart/zot/packages/agent/skills"
)

func TestExpandSkillCommandForcesNamedSkill(t *testing.T) {
	available := []*skills.Skill{{
		Name: "review",
		Body: "Inspect the complete diff.",
	}}

	got, recognized, err := expandSkillCommand("/skill:review focus on security", available)
	if err != nil {
		t.Fatal(err)
	}
	if !recognized {
		t.Fatal("skill command was not recognized")
	}
	for _, want := range []string{"# Skill: review", "Inspect the complete diff.", "User request:\nfocus on security"} {
		if !strings.Contains(got, want) {
			t.Errorf("expanded prompt missing %q:\n%s", want, got)
		}
	}
}

func TestExpandSkillCommandRejectsUnknownSkill(t *testing.T) {
	_, recognized, err := expandSkillCommand("/skill:missing", nil)
	if !recognized {
		t.Fatal("skill command was not recognized")
	}
	if err == nil || !strings.Contains(err.Error(), "unknown skill") {
		t.Fatalf("error = %v, want unknown skill", err)
	}
}

func TestSkillCommandRoutesAsSlashCommand(t *testing.T) {
	for _, text := range []string{"/skill:review", "/skill:review focus here", "/SKILL:review"} {
		if !looksLikeSlashCommand(text) {
			t.Errorf("looksLikeSlashCommand(%q) = false", text)
		}
		if !isKnownSlashCommand(text) {
			t.Errorf("isKnownSlashCommand(%q) = false", text)
		}
	}
}
