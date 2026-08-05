package subagents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpawnWorktreeCapturesBeforeCleanup(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.invalid")
	runGit(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "main.txt"), []byte("host\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "main.txt")
	runGit(t, repo, "commit", "-m", "initial")

	state := t.TempDir()
	f := New(Config{
		Root: state, RepoRoot: repo,
		Policy: SubagentPolicy{DefaultTimeout: 0, IdleTimeout: 0},
		NewRunner: func(a *Agent) Runner {
			return RunnerFunc(func(context.Context, Sink) error {
				if err := os.WriteFile(filepath.Join(a.Dir, "main.txt"), []byte("child\n"), 0o600); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(a.Dir, "new.txt"), []byte("created\n"), 0o600)
			})
		},
	})
	a, err := f.SpawnReq(context.Background(), SpawnRequest{Task: "isolate", WorkspaceMode: WorkspaceWorktree, WorkspaceBase: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	a.Wait()
	result, err := f.ReadResult(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ResultSucceeded || len(result.ChangedFiles) != 2 || result.ChangedFiles[0] != "main.txt" || result.ChangedFiles[1] != "new.txt" {
		t.Fatalf("result = %#v", result)
	}
	patch, err := os.ReadFile(filepath.Join(state, "agents", a.ID, "patch.diff"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(patch), "child") || !strings.Contains(string(patch), "created") {
		t.Fatalf("patch = %q", patch)
	}
	host, err := os.ReadFile(filepath.Join(repo, "main.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(host) != "host\n" {
		t.Fatalf("host checkout changed: %q", host)
	}
	if _, err := os.Stat(a.WorkspacePath); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
}
