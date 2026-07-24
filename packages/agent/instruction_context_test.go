package agent

import "testing"

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
