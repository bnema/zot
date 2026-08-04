package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSplitFrontmatter(t *testing.T) {
	in := "---\nname: foo\ndescription: bar\n---\nbody text\n"
	front, body := splitFrontmatter(in)
	if front != "name: foo\ndescription: bar" {
		t.Errorf("front = %q", front)
	}
	if body != "body text\n" {
		t.Errorf("body = %q", body)
	}

	front2, body2 := splitFrontmatter("no frontmatter here")
	if front2 != "" || body2 != "no frontmatter here" {
		t.Errorf("expected pass-through, got front=%q body=%q", front2, body2)
	}
}

func TestParseFrontmatter(t *testing.T) {
	front := `name: code-review
description: "Review a recent change."
disable-model-invocation: true
allowed-tools: [read, bash]
permissions:
  bash: ["git diff*", "git log*"]
`
	s := &Skill{}
	parseFrontmatter(front, s)
	if s.Name != "code-review" {
		t.Errorf("name = %q", s.Name)
	}
	if s.Description != "Review a recent change." {
		t.Errorf("description = %q", s.Description)
	}
	if !s.DisableModelInvocation {
		t.Error("disable-model-invocation was not parsed")
	}
	if got := s.AllowedTools; len(got) != 2 || got[0] != "read" || got[1] != "bash" {
		t.Errorf("allowed-tools = %v", got)
	}
	if got := s.Permissions["bash"]; len(got) != 2 || got[0] != "git diff*" || got[1] != "git log*" {
		t.Errorf("permissions[bash] = %v", got)
	}
}

func TestDiscoverProjectAndGlobalPriorityAndDedup(t *testing.T) {
	t.Setenv("ZOT_AGENT_SKILLS", "")
	tmp := t.TempDir()
	zotHome := filepath.Join(tmp, "home")
	cwd := filepath.Join(tmp, "proj")

	mk := func(dir, name, desc string) {
		full := filepath.Join(dir, name)
		os.MkdirAll(full, 0o755)
		body := "---\nname: " + name + "\ndescription: " + desc + "\n---\n# " + name + "\n"
		os.WriteFile(filepath.Join(full, "SKILL.md"), []byte(body), 0o644)
	}

	// Same skill name in BOTH project and global; project should win.
	mk(filepath.Join(cwd, ".zot", "skills"), "shared", "project version")
	mk(filepath.Join(zotHome, "skills"), "shared", "global version")
	// Unique skill in global only.
	mk(filepath.Join(zotHome, "skills"), "global-only", "from global")

	skills, errs := Discover(zotHome, cwd, "", true /* includeUser */)
	if len(errs) > 0 {
		t.Fatalf("errs: %v", errs)
	}
	// Expect the two user skills + every built-in shipped with the
	// binary (currently the write-zot-extension authoring guide).
	builtins := loadBuiltins()
	want := 2 + len(builtins)
	if len(skills) != want {
		t.Fatalf("expected %d skills (2 user + %d built-in), got %d (%v)", want, len(builtins), len(skills), skills)
	}
	shared := FindByName(skills, "shared")
	if shared == nil || shared.Description != "project version" {
		t.Errorf("expected project to win for 'shared', got %v", shared)
	}
	if FindByName(skills, "global-only") == nil {
		t.Errorf("global-only skill missing")
	}
	// At least one built-in should have made it through.
	for _, b := range builtins {
		if FindByName(skills, b.Name) == nil {
			t.Errorf("built-in skill %q missing from Discover output", b.Name)
		}
	}
}

func TestDiscoverWithBundledSkillsPrecedence(t *testing.T) {
	t.Setenv("ZOT_AGENT_SKILLS", "")
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "project")
	userDir := filepath.Join(cwd, ".zot", "skills")
	bundleA := filepath.Join(tmp, "ext-a", "skills")
	bundleB := filepath.Join(tmp, "ext-b", "skills")
	write := func(root, name, description string) {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\ndescription: " + description + "\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(userDir, "shared", "user")
	write(bundleA, "shared", "bundle-a")
	write(bundleA, "only-a", "only a")
	write(bundleB, "only-a", "bundle-b")
	write(bundleB, "only-b", "only b")

	list, errs := DiscoverWithBundled("", cwd, "", true, []BundledSkillDir{
		{Dir: bundleA, Source: "extension a"},
		{Dir: bundleB, Source: "extension b"},
	})
	if len(errs) > 0 {
		t.Fatalf("errs: %v", errs)
	}
	if got := FindByName(list, "shared"); got == nil || got.Description != "user" {
		t.Fatalf("user precedence = %#v", got)
	}
	if got := FindByName(list, "only-a"); got == nil || got.Description != "only a" || got.Source != "extension a" {
		t.Fatalf("bundle declaration precedence = %#v", got)
	}
	if got := FindByName(list, "only-b"); got == nil || got.Source != "extension b" {
		t.Fatalf("second bundle skill = %#v", got)
	}
	builtins := loadBuiltins()
	if len(builtins) == 0 {
		t.Fatal("expected at least one builtin skill")
	}
	write(bundleA, builtins[0].Name, "bundled override")
	list, errs = DiscoverWithBundled("", cwd, "", true, []BundledSkillDir{{Dir: bundleA, Source: "extension a"}})
	if len(errs) > 0 {
		t.Fatalf("builtin override errors: %v", errs)
	}
	if got := FindByName(list, builtins[0].Name); got == nil || got.Description != "bundled override" || got.Builtin {
		t.Fatalf("bundled builtin override = %#v", got)
	}
}

func TestVisibleSkillsHidesBuiltins(t *testing.T) {
	in := []*Skill{
		{Name: "user-one"},
		{Name: "built-one", Builtin: true},
		{Name: "user-two"},
	}
	out := VisibleSkills(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 visible skills, got %d (%v)", len(out), out)
	}
	for _, s := range out {
		if s.Builtin {
			t.Errorf("built-in %q leaked into visible set", s.Name)
		}
	}
}

func TestSystemPromptAddendum(t *testing.T) {
	skills := []*Skill{
		{Name: "built-a", Description: "Do A.", Builtin: true},
		{Name: "user-b", Description: "Do B.", Path: "/tmp/skills/user-b/SKILL.md"},
		{Name: "manual-c", Description: "Do C.", DisableModelInvocation: true},
	}
	out := SystemPromptAddendum(skills)
	if !contains(out, "- built-a [builtin]: Do A.\n") {
		t.Errorf("builtin entry missing or wrong:\n%s", out)
	}
	if !contains(out, "- user-b [/tmp/skills/user-b/SKILL.md]: Do B.\n") {
		t.Errorf("user entry missing path pointer:\n%s", out)
	}
	if contains(out, "manual-c") {
		t.Errorf("manual skill leaked into system prompt:\n%s", out)
	}
}

func TestSystemPromptAddendumEmptyWhenAllSkillsDisableModelInvocation(t *testing.T) {
	out := SystemPromptAddendum([]*Skill{{Name: "manual", DisableModelInvocation: true}})
	if out != "" {
		t.Fatalf("SystemPromptAddendum() = %q, want empty", out)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(sub) > 0 && stringIndex(s, sub) >= 0))
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
