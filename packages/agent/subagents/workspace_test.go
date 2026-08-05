package subagents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitWorktreeWorkspaceIsolatesAndCapturesPatch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initTestRepo(t)

	state := t.TempDir()
	handle, err := PrepareWorkspace(context.Background(), WorkspaceRequest{
		Mode: WorkspaceWorktree, RepositoryRoot: repo, StateDir: state, AgentID: "worker-1", Base: "HEAD", Capture: CapturePatch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if handle.Mode() != WorkspaceWorktree || handle.Dir() == repo {
		t.Fatalf("handle = mode %s dir %q repo %q", handle.Mode(), handle.Dir(), repo)
	}
	if err := os.WriteFile(filepath.Join(handle.Dir(), "README.md"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(repo, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != "before\n" {
		t.Fatalf("host checkout changed: %q", before)
	}
	capture, err := handle.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Patch) == 0 || !strings.Contains(string(capture.Patch), "after") {
		t.Fatalf("patch = %q", capture.Patch)
	}
	if len(capture.ChangedFiles) != 1 || capture.ChangedFiles[0] != "README.md" {
		t.Fatalf("changed files = %#v", capture.ChangedFiles)
	}
	if err := handle.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(handle.Dir()); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
}

func TestGitWorktreeWorkspaceReusesDetachedCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initTestRepo(t)

	state := t.TempDir()
	first, err := PrepareWorkspace(context.Background(), WorkspaceRequest{
		Mode: WorkspaceWorktree, RepositoryRoot: repo, StateDir: state, AgentID: "worker-1", Base: "HEAD", Capture: CapturePatch,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first.Dir(), "draft.txt"), []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resumed, err := PrepareWorkspace(context.Background(), WorkspaceRequest{
		Mode: WorkspaceWorktree, RepositoryRoot: repo, StateDir: state, AgentID: "worker-1", Base: "HEAD", Capture: CapturePatch,
		ExistingPath: first.Dir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Dir() != first.Dir() {
		t.Fatalf("resume dir = %q, want %q", resumed.Dir(), first.Dir())
	}
	if _, err := os.Stat(filepath.Join(resumed.Dir(), "draft.txt")); err != nil {
		t.Fatalf("resumed worktree lost uncommitted file: %v", err)
	}
	if err := resumed.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestGitWorktreeWorkspaceCaptureIncludesUntrackedFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initTestRepo(t)
	state := t.TempDir()
	handle, err := PrepareWorkspace(context.Background(), WorkspaceRequest{
		Mode: WorkspaceWorktree, RepositoryRoot: repo, StateDir: state, Base: "HEAD", Capture: CapturePatch,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Cleanup(context.Background()) }()

	name := "new file – 日本語.txt"
	if err := os.WriteFile(filepath.Join(handle.Dir(), name), []byte("created\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	capture, err := handle.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.ChangedFiles) != 1 || capture.ChangedFiles[0] != name {
		t.Fatalf("changed files = %#v, want [%q]", capture.ChangedFiles, name)
	}
	if !strings.Contains(string(capture.Patch), "created") {
		t.Fatalf("patch = %q", capture.Patch)
	}
}

func TestChangedFilesFromStatus(t *testing.T) {
	status := []byte(" M path with spaces.txt\x00R  renamed – 新.txt\x00original name – старый.txt\x00?? untracked – 文件.txt\x00")
	got := changedFilesFromStatus(status)
	want := []string{"path with spaces.txt", "renamed – 新.txt", "untracked – 文件.txt"}
	if len(got) != len(want) {
		t.Fatalf("changed files = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("changed files[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSharedWorkspaceUsesRepositoryRoot(t *testing.T) {
	root := t.TempDir()
	handle, err := PrepareWorkspace(context.Background(), WorkspaceRequest{Mode: WorkspaceShared, RepositoryRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if handle.Dir() != root || handle.RepositoryRoot() != root || handle.Mode() != WorkspaceShared {
		t.Fatalf("shared handle = %#v", handle)
	}
	if err := handle.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config"))
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	unsetenvForTest(t, "GIT_DIR")
	unsetenvForTest(t, "GIT_WORK_TREE")

	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.invalid")
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "initial")
	return repo
}

func unsetenvForTest(t *testing.T, name string) {
	t.Helper()
	value, ok := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(name, value)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
