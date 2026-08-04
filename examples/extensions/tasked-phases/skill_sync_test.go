package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBundledSkillMatchesStandaloneCopy(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	extensionSkill := filepath.Join(filepath.Dir(file), "skills", "tasked-phases", "SKILL.md")
	standaloneSkill := filepath.Join(filepath.Dir(file), "..", "..", "skills", "tasked-phases", "SKILL.md")
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
