package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBundledSkillMatchesStandaloneCopy(t *testing.T) {
	extensionSkill := filepath.Join("skills", "tasked-phases", "SKILL.md")
	standaloneSkill := filepath.Join("..", "..", "skills", "tasked-phases", "SKILL.md")
	extension, err := os.ReadFile(extensionSkill)
	if err != nil {
		t.Fatal(err)
	}
	standalone, err := os.ReadFile(standaloneSkill)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(extension, standalone) {
		t.Fatal("bundled and standalone tasked-phases skills have diverged")
	}
}
