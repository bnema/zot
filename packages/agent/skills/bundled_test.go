package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadBundledLoadsDeclaredDirectoriesInOrder(t *testing.T) {
	tmp := t.TempDir()
	collectionA := filepath.Join(tmp, "extension-a", "skills")
	collectionB := filepath.Join(tmp, "extension-b", "skills")
	direct := filepath.Join(tmp, "extension-c", "one-skill")

	writeSkill := func(dir, name, description string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\ndescription: " + description + "\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeSkill(filepath.Join(collectionA, "shared"), "shared", "first declaration")
	writeSkill(filepath.Join(collectionA, "alpha"), "alpha", "from a")
	writeSkill(filepath.Join(collectionB, "shared"), "shared", "later declaration")
	writeSkill(filepath.Join(collectionB, "beta"), "beta", "from b")
	writeSkill(direct, "direct", "from a direct skill directory")

	got, errs := LoadBundled([]BundledSkillDir{
		{Dir: collectionA, Source: "extension alpha"},
		{Dir: collectionB, Source: "extension beta"},
		{Dir: direct, Source: "extension direct"},
	})
	if len(errs) != 0 {
		t.Fatalf("LoadBundled() errors = %v", errs)
	}

	if len(got) != 4 {
		t.Fatalf("LoadBundled() returned %d skills, want 4: %#v", len(got), got)
	}
	if shared := FindByName(got, "shared"); shared == nil {
		t.Fatal("shared skill was not loaded")
	} else {
		if shared.Description != "first declaration" || shared.Source != "extension alpha" {
			t.Fatalf("shared skill = %#v, want first declaration and source label", shared)
		}
	}
	if beta := FindByName(got, "beta"); beta == nil || beta.Source != "extension beta" {
		t.Fatalf("beta skill = %#v, want extension beta source", beta)
	}
	if directSkill := FindByName(got, "direct"); directSkill == nil {
		t.Fatal("direct skill directory was not loaded")
	} else if directSkill.Path != filepath.Join(direct, "SKILL.md") {
		t.Fatalf("direct skill path = %q, want %q", directSkill.Path, filepath.Join(direct, "SKILL.md"))
	}
}

func TestLoadBundledRejectsSkillFilesOutsideRoot(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "extension")
	bundle := filepath.Join(root, "skills")
	outside := filepath.Join(tmp, "outside", "SKILL.md")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("---\nname: escape\ndescription: outside\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, errs := LoadBundled([]BundledSkillDir{{Dir: filepath.Dir(outside), Root: root, Source: "extension test"}})
	if len(errs) == 0 || len(got) != 0 {
		t.Fatalf("outside bundle was accepted: skills=%#v errors=%v", got, errs)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Symlink(outside, filepath.Join(bundle, "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	got, errs = LoadBundled([]BundledSkillDir{{Dir: bundle, Root: root, Source: "extension test"}})
	if len(errs) == 0 || len(got) != 0 {
		t.Fatalf("outside skill was accepted: skills=%#v errors=%v", got, errs)
	}
}

func TestLoadBundledDoesNotIncludeUserOrBuiltinSkills(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "skills", "declared")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: declared\ndescription: declared only\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, errs := LoadBundled([]BundledSkillDir{{Dir: filepath.Dir(dir), Source: "extension test"}})
	if len(errs) != 0 {
		t.Fatalf("LoadBundled() errors = %v", errs)
	}
	if FindByName(got, "declared") == nil {
		t.Fatalf("declared skill missing from %#v", got)
	}
	for _, skill := range got {
		if skill.Builtin {
			t.Fatalf("LoadBundled() returned builtin skill %q", skill.Name)
		}
		if skill.Name != "declared" {
			t.Fatalf("LoadBundled() returned unexpected skill %q", skill.Name)
		}
	}
}
