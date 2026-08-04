package subagents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverLoadsGlobalAgentsProfilesAndPiFallback(t *testing.T) {
	home := t.TempDir()
	agentsDir := filepath.Join(home, ".agents", "agents")
	piDir := filepath.Join(home, ".pi", "agent", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(piDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProfile(t, filepath.Join(agentsDir, "reviewer.md"), `---
name: reviewer
description: Read-only code reviewer
tools: read
model: openai-codex/gpt-5.6-luna
thinking: max
systemPromptMode: replace
inheritProjectContext: false
inheritSkills: false
---
Review the requested scope without editing it.
`)
	writeProfile(t, filepath.Join(piDir, "implementer.md"), `---
name: implementer
description: Primary implementation agent
tools: [read, bash, edit, write]
model: openai-codex/gpt-5.6-luna
thinking: xhigh
---
Implement the requested change.
`)

	profiles, errs := Discover("", home)
	if len(errs) != 0 {
		t.Fatalf("Discover errors = %v", errs)
	}
	if len(profiles) != 2 {
		t.Fatalf("profiles = %d, want 2: %#v", len(profiles), profiles)
	}
	if profiles[0].Name != "implementer" || profiles[1].Name != "reviewer" {
		t.Fatalf("profiles not sorted by name: %#v", profiles)
	}
	reviewer := Find(profiles, "reviewer")
	if reviewer == nil {
		t.Fatal("reviewer profile missing")
	}
	if reviewer.SystemPromptMode != "replace" {
		t.Fatalf("system prompt mode = %q, want replace", reviewer.SystemPromptMode)
	}
	if reviewer.Model != "openai-codex/gpt-5.6-luna" || reviewer.Thinking != "max" {
		t.Fatalf("profile metadata = %#v", reviewer)
	}
	if reviewer.InheritProjectContext == nil || *reviewer.InheritProjectContext {
		t.Fatalf("inherit project context = %#v, want false", reviewer.InheritProjectContext)
	}
	if reviewer.InheritSkills == nil || *reviewer.InheritSkills {
		t.Fatalf("inherit skills = %#v, want false", reviewer.InheritSkills)
	}
}

func TestDiscoverConfiguredDirectoryWinsAndRejectsUnsafeNames(t *testing.T) {
	configured := t.TempDir()
	home := t.TempDir()
	t.Setenv("ZOT_AGENT_PROFILES", configured)
	writeProfile(t, filepath.Join(configured, "reviewer.md"), `---
name: reviewer
description: Configured reviewer
---
Configured instructions.
`)
	writeProfile(t, filepath.Join(filepath.Join(home, ".agents", "agents"), "reviewer.md"), `---
name: reviewer
description: Global reviewer
---
Global instructions.
`)
	writeProfile(t, filepath.Join(configured, "unsafe.md"), `---
name: ../unsafe
description: Should not load
---
No.
`)

	profiles, errs := Discover("", home)
	if len(errs) != 0 {
		t.Fatalf("Discover errors = %v", errs)
	}
	if len(profiles) != 1 || profiles[0].Description != "Configured reviewer" {
		t.Fatalf("configured precedence = %#v", profiles)
	}
}

func TestLoadRejectsUnclosedFrontmatter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.md")
	if err := os.WriteFile(path, []byte("---\nname: reviewer\nInstructions without a closing delimiter.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := load(path, "test"); err == nil || !strings.Contains(err.Error(), "missing closing delimiter") {
		t.Fatalf("load error = %v, want missing closing delimiter", err)
	}
}

func TestLoadRejectsInvalidClosedSchemaMetadata(t *testing.T) {
	cases := []struct {
		name  string
		field string
	}{
		{name: "invalid-thinking", field: "thinking: extreme"},
		{name: "invalid-reasoning", field: "reasoning: extreme"},
		{name: "invalid-prompt-mode", field: "systemPromptMode: merge"},
		{name: "invalid-project-context", field: "inheritProjectContext: sometimes"},
		{name: "invalid-skills", field: "inheritSkills: sometimes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "profile.md")
			writeProfile(t, path, fmt.Sprintf("---\nname: test\n%s\n---\nInstructions.\n", tc.field))
			if _, err := load(path, "test"); err == nil {
				t.Fatalf("load succeeded for invalid metadata %q", tc.field)
			}
		})
	}
}

func TestProfileModelSelection(t *testing.T) {
	profile := &Profile{Model: "openrouter/anthropic/claude-sonnet"}
	provider, model := profile.ModelSelection()
	if provider != "openrouter" || model != "anthropic/claude-sonnet" {
		t.Fatalf("selection = (%q, %q)", provider, model)
	}

	profile = &Profile{Provider: "openai", Model: "gpt-5"}
	provider, model = profile.ModelSelection()
	if provider != "openai" || model != "gpt-5" {
		t.Fatalf("explicit selection = (%q, %q)", provider, model)
	}
}

func TestSystemPromptAddendumIsCompactAndDoesNotExposeBodyOrPath(t *testing.T) {
	profiles := []*Profile{{
		Name:         "reviewer",
		Description:  "Read-only\nreviewer",
		SystemPrompt: "secret instructions that belong only to the child",
		Model:        "openai-codex/gpt-5",
		Thinking:     "max",
		Tools:        []string{"read", "bash"},
		Path:         "/private/profile.md",
	}}
	got := SystemPromptAddendum(profiles)
	for _, want := range []string{"[subagents_list]", "[/subagents_list]", "reviewer", "Read-only reviewer", "model=openai-codex/gpt-5", "thinking=max"} {
		if !strings.Contains(got, want) {
			t.Fatalf("addendum missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"secret instructions", "/private/profile.md", "\nreviewer"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("addendum contains %q:\n%s", unwanted, got)
		}
	}
}

func writeProfile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
