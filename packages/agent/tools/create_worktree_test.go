package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bnema/zut/packages/provider"
)

func TestCreateWorktreeBootstrapsDefaultAtRepositoryRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	nested := filepath.Join(repo, "nested", "dir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("*.local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	branchBefore := gitTestOutput(t, repo, "branch", "--show-current")
	sandbox := NewSandbox(repo)
	sandbox.Lock()

	tool := &CreateWorktreeTool{CWD: nested, Sandbox: sandbox}
	args, err := json.Marshal(map[string]string{"branch": "feature/new-checkout", "bootstrap_root": ".worktrees"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), args, nil); err != nil {
		t.Fatal(err)
	}

	worktree := filepath.Join(repo, ".worktrees", "feature", "new-checkout")
	if info, err := os.Stat(worktree); err != nil || !info.IsDir() {
		t.Fatalf("worktree %q was not created: %v", worktree, err)
	}
	if got := gitTestOutput(t, worktree, "branch", "--show-current"); got != "feature/new-checkout" {
		t.Fatalf("worktree branch = %q, want %q", got, "feature/new-checkout")
	}
	if got := gitTestOutput(t, repo, "branch", "--show-current"); got != branchBefore {
		t.Fatalf("source branch changed from %q to %q", branchBefore, got)
	}
	ignore, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(ignore), "/.worktrees/\n"); count != 1 {
		t.Fatalf("/.worktrees/ ignore entries = %d, want 1:\n%s", count, ignore)
	}
	if got := gitTestOutput(t, repo, "config", "--local", "--get", worktreeConfigKey); got != defaultWorktreeRoot {
		t.Fatalf("saved worktree root = %q, want %q", got, defaultWorktreeRoot)
	}
	followupArgs := mustJSON(t, map[string]string{
		"branch":         "feature/default-followup",
		"bootstrap_root": defaultWorktreeRoot,
	})
	if _, err := tool.Preview(context.Background(), followupArgs); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), followupArgs, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, defaultWorktreeRoot, "feature", "default-followup")); err != nil {
		t.Fatalf("configured default root was not reused: %v", err)
	}
}

func TestCreateWorktreeRequiresBootstrapForUnconfiguredRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	tool := newCreateWorktreeTestTool(repo)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"branch":"feature/needs-bootstrap"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(provider.TextBlock).Text
	if !strings.Contains(text, "Worktree bootstrap is required") {
		t.Fatalf("bootstrap result = %q", text)
	}
	if state, _ := result.Details.(map[string]any)["state"].(string); state != "bootstrap_required" {
		t.Fatalf("bootstrap state = %q", state)
	}
	for _, path := range []string{".worktrees", ".gitignore"} {
		if _, statErr := os.Stat(filepath.Join(repo, path)); !os.IsNotExist(statErr) {
			t.Fatalf("bootstrap created %s: %v", path, statErr)
		}
	}
	if got := gitTestOutputAllowExit(t, repo, "config", "--local", "--get", worktreeConfigKey); got.exitCode != 1 {
		t.Fatalf("local worktree config exit = %d, want 1", got.exitCode)
	}
	if got := gitTestOutputAllowExit(t, repo, "show-ref", "--verify", "--quiet", "refs/heads/feature/needs-bootstrap"); got.exitCode != 1 {
		t.Fatalf("bootstrap created branch: %#v", got)
	}
}

func TestCreateWorktreeAcceptsDefaultBootstrapRootWhenDefaultRootExists(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	if err := os.Mkdir(filepath.Join(repo, defaultWorktreeRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := newCreateWorktreeTestTool(repo)
	args := mustJSON(t, map[string]string{
		"branch":         "feature/existing-default-root",
		"bootstrap_root": defaultWorktreeRoot,
	})

	if _, err := tool.Preview(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(context.Background(), args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, defaultWorktreeRoot, "feature", "existing-default-root")); err != nil {
		t.Fatalf("worktree was not created: %v", err)
	}
	if source, _ := result.Details.(map[string]any)["root_source"].(string); source != "existing .worktrees directory" {
		t.Fatalf("root source = %q", source)
	}
	if got := gitTestOutputAllowExit(t, repo, "config", "--local", "--get", worktreeConfigKey); got.exitCode != 1 {
		t.Fatalf("existing default root saved config: %#v", got)
	}
}

func TestCreateWorktreeRejectsConflictingBootstrapRootWhenDefaultRootExists(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	if err := os.Mkdir(filepath.Join(repo, defaultWorktreeRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := newCreateWorktreeTestTool(repo)

	_, err := tool.Execute(context.Background(), mustJSON(t, map[string]string{
		"branch":         "feature/conflicting-bootstrap",
		"bootstrap_root": t.TempDir(),
	}), nil)
	if err == nil || !strings.Contains(err.Error(), "already established") {
		t.Fatalf("error = %v, want established-root error", err)
	}
	if got := gitTestOutputAllowExit(t, repo, "config", "--local", "--get", worktreeConfigKey); got.exitCode != 1 {
		t.Fatalf("conflicting bootstrap saved config: %#v", got)
	}
	if got := gitTestOutputAllowExit(t, repo, "show-ref", "--verify", "--quiet", "refs/heads/feature/conflicting-bootstrap"); got.exitCode != 1 {
		t.Fatalf("conflicting bootstrap created branch: %#v", got)
	}
}

func TestCreateWorktreeRejectsConflictingBootstrapRootWhenRootIsConfigured(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	gitTestOutput(t, repo, "config", "--local", worktreeConfigKey, defaultWorktreeRoot)
	tool := newCreateWorktreeTestTool(repo)
	args := mustJSON(t, map[string]string{
		"branch":         "feature/conflicting-configured-bootstrap",
		"bootstrap_root": t.TempDir(),
	})

	if _, err := tool.Preview(context.Background(), args); err == nil || !strings.Contains(err.Error(), "already configured") {
		t.Fatalf("preview error = %v, want configured-root error", err)
	}
	_, err := tool.Execute(context.Background(), args, nil)
	if err == nil || !strings.Contains(err.Error(), "already configured") {
		t.Fatalf("error = %v, want configured-root error", err)
	}
	if got := gitTestOutputAllowExit(t, repo, "show-ref", "--verify", "--quiet", "refs/heads/feature/conflicting-configured-bootstrap"); got.exitCode != 1 {
		t.Fatalf("conflicting bootstrap created branch: %#v", got)
	}
}

func TestCreateWorktreeBootstrapsExternalRootWithoutChangingGitignore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	externalRoot, err := canonicalOrParent(filepath.Join(t.TempDir(), "company-worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	tool := newCreateWorktreeTestTool(repo)
	args := mustJSON(t, map[string]string{"branch": "feature/external", "bootstrap_root": externalRoot})

	result, err := tool.Execute(context.Background(), args, nil)
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(externalRoot, "feature", "external")
	if info, err := os.Stat(worktree); err != nil || !info.IsDir() {
		t.Fatalf("external worktree %q was not created: %v", worktree, err)
	}
	if got := gitTestOutput(t, worktree, "branch", "--show-current"); got != "feature/external" {
		t.Fatalf("external branch = %q", got)
	}
	if got := gitTestOutput(t, repo, "config", "--local", "--get", worktreeConfigKey); got != externalRoot {
		t.Fatalf("saved external root = %q, want %q", got, externalRoot)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("external bootstrap changed .gitignore: %v", err)
	}
	if source, _ := result.Details.(map[string]any)["root_source"].(string); source != "bootstrap selection" {
		t.Fatalf("root source = %q", source)
	}
	if state, _ := result.Details.(map[string]any)["state"].(string); state != "created" {
		t.Fatalf("state = %q, want created", state)
	}
	followupArgs := mustJSON(t, map[string]string{
		"branch":         "feature/external-followup",
		"bootstrap_root": externalRoot,
	})
	if _, err := tool.Preview(context.Background(), followupArgs); err != nil {
		t.Fatal(err)
	}
	followup, err := tool.Execute(context.Background(), followupArgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(externalRoot, "feature", "external-followup")); err != nil {
		t.Fatalf("configured external root was not reused: %v", err)
	}
	if source, _ := followup.Details.(map[string]any)["root_source"].(string); source != "local Git config" {
		t.Fatalf("follow-up root source = %q", source)
	}
}

func TestCreateWorktreeRejectsInvalidBootstrapRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	tool := newCreateWorktreeTestTool(repo)
	for _, root := range []string{"relative-worktrees", filepath.Join(repo, "company-worktrees")} {
		_, err := tool.Execute(context.Background(), mustJSON(t, map[string]string{"branch": "feature/invalid", "bootstrap_root": root}), nil)
		if err == nil || !strings.Contains(err.Error(), "bootstrap_root") {
			t.Fatalf("bootstrap_root %q error = %v", root, err)
		}
	}
	if got := gitTestOutputAllowExit(t, repo, "config", "--local", "--get", worktreeConfigKey); got.exitCode != 1 {
		t.Fatalf("invalid bootstrap saved config: %#v", got)
	}
}

func TestCreateWorktreeRejectsBranchSyntaxThatGitCanInterpret(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	tool := newCreateWorktreeTestTool(repo)
	for _, branch := range []string{"-option", "topic@{1}"} {
		_, err := tool.Execute(context.Background(), mustJSON(t, map[string]string{"branch": branch}), nil)
		if err == nil || !strings.Contains(err.Error(), "invalid branch") {
			t.Fatalf("branch %q error = %v", branch, err)
		}
	}
}

func TestCreateWorktreeRejectsConfiguredExternalRootResolvingInsideRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	link := filepath.Join(t.TempDir(), "repository-link")
	if err := os.Symlink(repo, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	root := filepath.Join(link, "external-worktrees")
	gitTestOutput(t, repo, "config", "--local", worktreeConfigKey, root)
	tool := newCreateWorktreeTestTool(repo)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"branch":"feature/inside-repository"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "external worktree root must be outside the repository") {
		t.Fatalf("error = %v, want external-root error", err)
	}
}

func TestCreateWorktreeDisablesPostCheckoutHook(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Git hook fixture uses a POSIX shell")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	if err := os.Mkdir(filepath.Join(repo, ".worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(repo, "hook-ran")
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	script := "#!/bin/sh\ntouch " + shellQuoteForTest(sentinel) + "\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := newCreateWorktreeTestTool(repo)

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"branch":"feature/no-hook"}`), nil); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(repo, ".worktrees", "feature", "no-hook")
	if info, err := os.Stat(worktree); err != nil || !info.IsDir() {
		t.Fatalf("worktree %q was not created: %v", worktree, err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("post-checkout hook ran: %v", err)
	}
}

func TestCreateWorktreeCleanupRemovesPartialCheckoutAndBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	worktreePath := filepath.Join(repo, ".worktrees", "partial")
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		t.Fatal(err)
	}
	base := gitTestOutput(t, repo, "rev-parse", "HEAD")
	gitTestOutput(t, repo, "worktree", "add", "-b", "feature/partial", worktreePath, base)
	plan := createWorktreePlan{
		repoRoot:     repo,
		branch:       "feature/partial",
		base:         base,
		worktreePath: worktreePath,
	}

	if err := plan.cleanupFailedCheckout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("partial worktree remains: %v", err)
	}
	if got := gitTestOutputAllowExit(t, repo, "show-ref", "--verify", "--quiet", "refs/heads/feature/partial"); got.exitCode != 1 {
		t.Fatalf("partial branch remains: %#v", got)
	}
}

func TestExecuteCreateWorktreePlanRemovesCreatedParentsOnCheckoutFailure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	branch := "feature/checkout-failure"
	plan := createWorktreePlan{
		repoRoot:     repo,
		branch:       branch,
		base:         "not-a-commit",
		worktreePath: filepath.Join(repo, defaultWorktreeRoot, "feature", "checkout-failure"),
		configValue:  defaultWorktreeRoot,
		setConfig:    true,
	}

	_, err := executeCreateWorktreePlan(context.Background(), plan, nil)
	if err == nil || !strings.Contains(err.Error(), "create worktree") {
		t.Fatalf("error = %v, want worktree creation error", err)
	}
	if _, err := os.Stat(filepath.Join(repo, defaultWorktreeRoot)); !os.IsNotExist(err) {
		t.Fatalf("failed checkout left worktree directories: %v", err)
	}
	if got := gitTestOutputAllowExit(t, repo, "config", "--local", "--get", worktreeConfigKey); got.exitCode != 1 {
		t.Fatalf("failed checkout kept local config: %#v", got)
	}
	if got := gitTestOutputAllowExit(t, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); got.exitCode != 1 {
		t.Fatalf("failed checkout created branch: %#v", got)
	}
}

func TestExecuteCreateWorktreePlanRollsBackBootstrapConfigOnIgnoreFailure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	ignorePath := filepath.Join(repo, ".gitignore")
	before := []byte("generated/\n")
	if err := os.WriteFile(ignorePath, before, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignorePath, []byte("changed by another process\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := gitTestOutput(t, repo, "rev-parse", "HEAD")
	plan := createWorktreePlan{
		repoRoot:     repo,
		branch:       "feature/ignore-failure",
		base:         base,
		worktreePath: filepath.Join(repo, ".worktrees", "feature", "ignore-failure"),
		configValue:  defaultWorktreeRoot,
		setConfig:    true,
		ignore: worktreesIgnoreUpdate{
			path:    ignorePath,
			before:  before,
			after:   append(append([]byte(nil), before...), []byte(worktreesIgnoreEntry+"\n")...),
			existed: true,
			mode:    0o644,
			changed: true,
		},
	}

	_, err := executeCreateWorktreePlan(context.Background(), plan, nil)
	if err == nil || !strings.Contains(err.Error(), ".gitignore changed since preview") {
		t.Fatalf("error = %v, want stale .gitignore error", err)
	}
	if got := gitTestOutputAllowExit(t, repo, "config", "--local", "--get", worktreeConfigKey); got.exitCode != 1 {
		t.Fatalf("failed bootstrap kept local config: %#v", got)
	}
	if got := gitTestOutputAllowExit(t, repo, "show-ref", "--verify", "--quiet", "refs/heads/feature/ignore-failure"); got.exitCode != 1 {
		t.Fatalf("failed bootstrap created branch: %#v", got)
	}
	contents, err := os.ReadFile(ignorePath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(contents), "changed by another process\n"; got != want {
		t.Fatalf(".gitignore = %q, want %q", got, want)
	}
}

func TestCreateWorktreePreservesGitignoreLineEndings(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	ignorePath := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(ignorePath, []byte("generated/\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := newCreateWorktreeTestTool(repo)

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"branch":"feature/crlf","bootstrap_root":".worktrees"}`), nil); err != nil {
		t.Fatal(err)
	}
	ignore, err := os.ReadFile(ignorePath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(ignore), "generated/\r\n/.worktrees/\r\n"; got != want {
		t.Fatalf(".gitignore = %q, want %q", got, want)
	}
}

func TestCreateWorktreeRejectsGitignoreSymbolicLink(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	target := filepath.Join(repo, "ignore-target")
	if err := os.WriteFile(target, []byte("generated/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ignorePath := filepath.Join(repo, ".gitignore")
	if err := os.Symlink(target, ignorePath); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	tool := newCreateWorktreeTestTool(repo)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"branch":"feature/ignore-symlink","bootstrap_root":".worktrees"}`), nil)
	if err == nil || !strings.Contains(err.Error(), ".gitignore must not be a symbolic link") {
		t.Fatalf("error = %v, want symbolic-link error", err)
	}
	info, err := os.Lstat(ignorePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("tool replaced .gitignore symbolic link")
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".worktrees")); !os.IsNotExist(statErr) {
		t.Fatalf("symbolic-link rejection created .worktrees: %v", statErr)
	}
}

func TestCreateWorktreeRejectsNonRegularGitignore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	if err := os.Mkdir(filepath.Join(repo, ".gitignore"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := newCreateWorktreeTestTool(repo)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"branch":"feature/nonregular","bootstrap_root":".worktrees"}`), nil)
	if err == nil || !strings.Contains(err.Error(), ".gitignore must be a regular file") {
		t.Fatalf("error = %v, want regular-file error", err)
	}
	if got := gitTestOutputAllowExit(t, repo, "config", "--local", "--get", worktreeConfigKey); got.exitCode != 1 {
		t.Fatalf("non-regular ignore saved config: %#v", got)
	}
}

func TestCreateWorktreeGitOutputKeepsStderrOutOfSuccessfulOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Git fixture uses a POSIX shell")
	}
	binDir := t.TempDir()
	gitPath := filepath.Join(binDir, "git")
	if err := os.WriteFile(gitPath, []byte("#!/bin/sh\nprintf 'value\\n'\nprintf 'warning\\n' >&2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := createWorktreeGitOutput(context.Background(), t.TempDir(), "status")
	if err != nil {
		t.Fatal(err)
	}
	if output != "value" {
		t.Fatalf("output = %q, want value", output)
	}
}

func TestCreateWorktreeGitCancelsDescendants(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group fixture uses a POSIX shell")
	}
	binDir := t.TempDir()
	started := filepath.Join(t.TempDir(), "git-started")
	childPID := filepath.Join(t.TempDir(), "child-pid")
	gitPath := filepath.Join(binDir, "git")
	script := "#!/bin/sh\nsleep 30 &\nprintf '%s' \"$!\" > " + shellQuoteForTest(childPID) + "\nprintf started > " + shellQuoteForTest(started) + "\nwait\n"
	if err := os.WriteFile(gitPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cwd := t.TempDir()
	done := make(chan error, 1)
	go func() {
		_, err := createWorktreeGitOutput(ctx, cwd, "status")
		done <- err
	}()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		if _, err := os.Stat(started); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case <-deadline.C:
			t.Fatal("fake git did not start")
		case <-poll.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Git process group did not exit after cancellation")
	}
	pid, err := os.ReadFile(childPID)
	if err != nil {
		t.Fatal(err)
	}
	exitDeadline := time.NewTimer(5 * time.Second)
	defer exitDeadline.Stop()
	for {
		err := exec.Command("kill", "-0", strings.TrimSpace(string(pid))).Run()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				break
			}
			t.Fatalf("check child process: %v", err)
		}
		select {
		case <-exitDeadline.C:
			t.Fatalf("Git descendant %q remained after cancellation", pid)
		case <-poll.C:
		}
	}
}

func TestCreateWorktreePreviewDoesNotModifyRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	tool := newCreateWorktreeTestTool(repo)
	args := json.RawMessage(`{"branch":"feature/preview"}`)

	preview, err := tool.Preview(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	text := preview.Content[0].(provider.TextBlock).Text
	for _, want := range []string{"Worktree bootstrap is required", "feature/preview", "bootstrap_root"} {
		if !strings.Contains(text, want) {
			t.Fatalf("preview missing %q:\n%s", want, text)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".worktrees")); !os.IsNotExist(err) {
		t.Fatalf("preview created .worktrees: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("preview created .gitignore: %v", err)
	}
	cmd := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/feature/preview")
	if err := cmd.Run(); err == nil {
		t.Fatal("preview created the branch")
	}
}

func TestCreateWorktreeRejectsStateChangedAfterPreview(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	if err := os.Mkdir(filepath.Join(repo, ".worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := newCreateWorktreeTestTool(repo)
	args := json.RawMessage(`{"branch":"feature/stale-preview"}`)
	if _, err := tool.Preview(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "after-preview.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestOutput(t, repo, "add", "after-preview.txt")
	gitTestOutput(t, repo, "commit", "-qm", "change after preview")

	_, err := tool.Execute(context.Background(), args, nil)
	if err == nil || !strings.Contains(err.Error(), "state changed since preview") {
		t.Fatalf("error = %v, want stale-preview error", err)
	}
	if got := gitTestOutputAllowExit(t, repo, "show-ref", "--verify", "--quiet", "refs/heads/feature/stale-preview"); got.exitCode != 1 {
		t.Fatalf("stale preview created branch: %#v", got)
	}
}

func TestCreateWorktreeRejectsNonRepository(t *testing.T) {
	dir := t.TempDir()
	tool := newCreateWorktreeTestTool(dir)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"branch":"feature/new"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "not a Git repository") {
		t.Fatalf("error = %v, want repository error", err)
	}
	for _, path := range []string{".worktrees", ".gitignore"} {
		if _, statErr := os.Stat(filepath.Join(dir, path)); !os.IsNotExist(statErr) {
			t.Fatalf("non-repository call created %s: %v", path, statErr)
		}
	}
}

func TestCreateWorktreeRequiresSandbox(t *testing.T) {
	tool := &CreateWorktreeTool{CWD: t.TempDir()}

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"branch":"feature/no-sandbox"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "sandbox is required") {
		t.Fatalf("error = %v, want sandbox error", err)
	}
}

func TestCreateWorktreeRejectsExistingBranchWithoutChangingGitignore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	tool := newCreateWorktreeTestTool(repo)
	bootstrapArgs := json.RawMessage(`{"branch":"feature/existing","bootstrap_root":".worktrees"}`)
	if _, err := tool.Execute(context.Background(), bootstrapArgs, nil); err != nil {
		t.Fatal(err)
	}
	ignoreBefore, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = tool.Execute(context.Background(), json.RawMessage(`{"branch":"feature/existing"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v, want collision error", err)
	}
	ignoreAfter, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if string(ignoreAfter) != string(ignoreBefore) {
		t.Fatalf("collision changed .gitignore:\nbefore: %q\nafter: %q", ignoreBefore, ignoreAfter)
	}
}

func TestCreateWorktreeRespectsJail(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	nested := filepath.Join(repo, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	sandbox := NewSandbox(nested)
	sandbox.Lock()
	tool := &CreateWorktreeTool{CWD: nested, Sandbox: sandbox}

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"branch":"feature/jailed"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "jailed:") {
		t.Fatalf("error = %v, want jailed error", err)
	}
	for _, path := range []string{".worktrees", ".gitignore"} {
		if _, statErr := os.Stat(filepath.Join(repo, path)); !os.IsNotExist(statErr) {
			t.Fatalf("jailed call created %s: %v", path, statErr)
		}
	}
}

func TestCreateWorktreeRejectsJailedAncestorBeforeGitDiscovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Git invocation fixture uses a POSIX shell")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	nested := filepath.Join(repo, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(repo, "git-was-invoked")
	binDir := t.TempDir()
	fakeGit := filepath.Join(binDir, "git")
	if err := os.WriteFile(fakeGit, []byte("#!/bin/sh\ntouch "+shellQuoteForTest(sentinel)+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	sandbox := NewSandbox(nested)
	sandbox.Lock()
	tool := &CreateWorktreeTool{CWD: nested, Sandbox: sandbox}

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"branch":"feature/jailed-discovery"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "jailed:") {
		t.Fatalf("error = %v, want jailed error", err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("Git ran before jailed ancestor was rejected: %v", err)
	}
}

func TestCreateWorktreeRequiresFilesystemPermissions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	sandbox := NewSandbox(repo)
	permissions := &PermissionSet{}
	permissions.Bash.Mode = "allowlist"
	permissions.Bash.Allow = []string{"git"}
	sandbox.SetPermissions(permissions)
	tool := &CreateWorktreeTool{CWD: repo, Sandbox: sandbox}

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"branch":"feature/restricted"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "no filesystem read permission") {
		t.Fatalf("error = %v, want filesystem permission error", err)
	}
	for _, path := range []string{".worktrees", ".gitignore"} {
		if _, statErr := os.Stat(filepath.Join(repo, path)); !os.IsNotExist(statErr) {
			t.Fatalf("restricted call created %s: %v", path, statErr)
		}
	}
}

func TestCreateWorktreeRequiresGitBashPermission(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	sandbox := NewSandbox(repo)
	permissions := &PermissionSet{}
	permissions.FS.Read = []string{repo}
	permissions.FS.Write = []string{repo}
	sandbox.SetPermissions(permissions)
	tool := &CreateWorktreeTool{CWD: repo, Sandbox: sandbox}

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"branch":"feature/no-git"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "no bash permission") {
		t.Fatalf("error = %v, want git permission error", err)
	}
	for _, path := range []string{".worktrees", ".gitignore"} {
		if _, statErr := os.Stat(filepath.Join(repo, path)); !os.IsNotExist(statErr) {
			t.Fatalf("restricted call created %s: %v", path, statErr)
		}
	}
}

func TestCreateWorktreeRejectsInvalidWorktreePathBeforeGitignoreUpdate(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	repo := initWorktreeTestRepo(t)
	worktrees := filepath.Join(repo, ".worktrees")
	if err := os.Mkdir(worktrees, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktrees, "blocked"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := newCreateWorktreeTestTool(repo)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"branch":"blocked/new"}`), nil)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error = %v, want non-directory path error", err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".gitignore")); !os.IsNotExist(statErr) {
		t.Fatalf("invalid path left .gitignore behind: %v", statErr)
	}
	cmd := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/blocked/new")
	if err := cmd.Run(); err == nil {
		t.Fatal("invalid path created the branch")
	}
}

func newCreateWorktreeTestTool(cwd string) *CreateWorktreeTool {
	return &CreateWorktreeTool{CWD: cwd, Sandbox: NewSandbox(cwd)}
}

func initWorktreeTestRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	repo := t.TempDir()
	gitTestOutput(t, repo, "init", "-q")
	gitTestOutput(t, repo, "config", "user.email", "test@example.invalid")
	gitTestOutput(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestOutput(t, repo, "add", "README.md")
	gitTestOutput(t, repo, "commit", "-qm", "initial")
	return repo
}

func shellQuoteForTest(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

type gitTestResult struct {
	output   string
	exitCode int
}

func gitTestOutputAllowExit(t *testing.T, dir string, args ...string) gitTestResult {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return gitTestResult{output: strings.TrimSpace(string(output))}
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return gitTestResult{output: strings.TrimSpace(string(output)), exitCode: exitErr.ExitCode()}
	}
	t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	return gitTestResult{}
}

func gitTestOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
