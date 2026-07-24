package agent

import (
	"reflect"
	"testing"

	"github.com/patriceckhart/zot/packages/agent/extensions"
	"github.com/patriceckhart/zot/packages/agent/skills"
)

func TestInstructionContextPathsPreserveLoadOrder(t *testing.T) {
	files := []ContextFile{
		{Path: "/home/user/AGENTS.md", Content: "global rule"},
		{Path: "/repo/AGENTS.md", Content: "project rule"},
	}

	paths := instructionContextPaths(files)
	if len(paths) != len(files) {
		t.Fatalf("got %d startup paths, want %d", len(paths), len(files))
	}
	for idx, path := range paths {
		if path != files[idx].Path {
			t.Fatalf("path %d = %q, want %q", idx, path, files[idx].Path)
		}
	}
}

func TestStartupExtensionNamesAreSortedAndEnabled(t *testing.T) {
	disabled := false
	exts := []*extensions.Extension{
		{Manifest: extensions.Manifest{Name: "zeta"}},
		{Manifest: extensions.Manifest{Name: "disabled", Enabled: &disabled}},
		{Manifest: extensions.Manifest{Name: "alpha"}},
		nil,
	}
	if got, want := startupExtensionNames(exts), []string{"alpha", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("startupExtensionNames() = %v, want %v", got, want)
	}
}

func TestStartupSkillNamesExcludeBuiltins(t *testing.T) {
	discovered := []*skills.Skill{
		{Name: "zeta"},
		{Name: "internal", Builtin: true},
		{Name: "alpha"},
	}
	if got, want := startupSkillNames(discovered), []string{"alpha", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("startupSkillNames() = %v, want %v", got, want)
	}
}
