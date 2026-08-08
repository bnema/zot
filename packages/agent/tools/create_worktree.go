package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bnema/zut/packages/core"
	"github.com/bnema/zut/packages/provider"
)

const (
	worktreesIgnoreEntry           = "/.worktrees/"
	worktreesUnanchoredIgnoreEntry = ".worktrees/"
	defaultWorktreeRoot            = ".worktrees"
	worktreeConfigKey              = "zut.worktrees.path"
	maxCreateWorktreeGitOutput     = 64 * 1024
	maxCreateWorktreeIgnoreBytes   = 1 << 20
)

var createWorktreeOperationMu sync.Mutex

// CreateWorktreeTool creates a persistent branch checkout. A repository that
// has neither a local worktree configuration nor an existing .worktrees
// directory returns a bootstrap request without changing any files.
type CreateWorktreeTool struct {
	CWD     string
	Sandbox *Sandbox

	previewMu sync.Mutex
	preview   *createWorktreePreview
}

type createWorktreeArgs struct {
	Branch        string `json:"branch"`
	BootstrapRoot string `json:"bootstrap_root,omitempty"`
}

const createWorktreeSchema = `{
  "type":"object",
  "properties":{
    "branch":{
      "type":"string",
      "description":"Name of the new Git branch."
    },
    "bootstrap_root":{
      "type":"string",
      "description":"Only for an unconfigured repository: use .worktrees to initialize the repository-root default, or provide an absolute external worktree root. The choice is stored in local Git config. Omit it on the first call to request user guidance instead of changing files."
    }
  },
  "required":["branch"]
}`

func (t *CreateWorktreeTool) Name() string { return "create_worktree" }
func (t *CreateWorktreeTool) Description() string {
	return "Create a persistent Git branch worktree. An unconfigured repository returns bootstrap guidance; ask the user for .worktrees or an absolute external root, then call again with bootstrap_root."
}
func (t *CreateWorktreeTool) Schema() json.RawMessage { return json.RawMessage(createWorktreeSchema) }

// Preview reports the exact bootstrap request or checkout that Execute will
// produce without modifying the repository.
func (t *CreateWorktreeTool) Preview(ctx context.Context, raw json.RawMessage) (core.ToolResult, error) {
	plan, err := t.plan(ctx, raw)
	if err != nil {
		return core.ToolResult{}, err
	}
	t.previewMu.Lock()
	t.preview = &createWorktreePreview{raw: string(raw), plan: plan}
	t.previewMu.Unlock()
	return plan.preview, nil
}

func (t *CreateWorktreeTool) Execute(ctx context.Context, raw json.RawMessage, progress func(string)) (core.ToolResult, error) {
	createWorktreeOperationMu.Lock()
	defer createWorktreeOperationMu.Unlock()

	plan, err := t.planForExecute(ctx, raw)
	if err != nil {
		return core.ToolResult{}, err
	}
	if plan.bootstrapRequired {
		return plan.preview, nil
	}
	return executeCreateWorktreePlan(ctx, plan, progress)
}

func executeCreateWorktreePlan(ctx context.Context, plan createWorktreePlan, progress func(string)) (core.ToolResult, error) {
	configSaved := false
	var createdWorktreeParents []string
	if plan.setConfig {
		if _, err := createWorktreeGitOutput(ctx, plan.repoRoot, "config", "--local", "--replace-all", worktreeConfigKey, plan.configValue); err != nil {
			return core.ToolResult{}, fmt.Errorf("create_worktree: save worktree root: %w", err)
		}
		configSaved = true
	}
	rollback := func(cause error) error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := plan.cleanupFailedCheckout(cleanupCtx); err != nil {
			cause = fmt.Errorf("%w; cleanup worktree: %v", cause, err)
		}
		if err := removeCreatedWorktreeParents(createdWorktreeParents); err != nil {
			cause = fmt.Errorf("%w; remove created worktree directories: %v", cause, err)
		}
		if err := plan.ignore.restore(); err != nil {
			cause = fmt.Errorf("%w; restore .gitignore: %v", cause, err)
		}
		if configSaved {
			if err := plan.removeBootstrapConfig(cleanupCtx); err != nil {
				cause = fmt.Errorf("%w; remove local worktree configuration: %v", cause, err)
			}
		}
		return cause
	}
	if err := plan.ignore.apply(); err != nil {
		return core.ToolResult{}, rollback(fmt.Errorf("create_worktree: update .gitignore: %w", err))
	}

	createdWorktreeParents, err := missingWorktreeParents(plan.worktreePath)
	if err != nil {
		return core.ToolResult{}, rollback(fmt.Errorf("create_worktree: inspect worktree directories: %w", err))
	}
	if err := os.MkdirAll(filepath.Dir(plan.worktreePath), 0o755); err != nil {
		return core.ToolResult{}, rollback(fmt.Errorf("create_worktree: make worktree directory: %w", err))
	}
	if progress != nil {
		progress("Creating Git worktree...\n")
	}
	if _, err := createWorktreeGitOutput(ctx, plan.repoRoot, "worktree", "add", "-b", plan.branch, plan.worktreePath, plan.base); err != nil {
		return core.ToolResult{}, rollback(fmt.Errorf("create_worktree: create worktree: %w", err))
	}

	result := plan.preview
	previewDetails, _ := plan.preview.Details.(map[string]any)
	details := make(map[string]any, len(previewDetails)+1)
	for key, value := range previewDetails {
		details[key] = value
	}
	details["state"] = "created"
	result.Details = details
	result.Content = []provider.Content{provider.TextBlock{Text: strings.Replace(plan.previewText, "create worktree", "created worktree", 1)}}
	return result, nil
}

type createWorktreePreview struct {
	raw  string
	plan createWorktreePlan
}

func (t *CreateWorktreeTool) planForExecute(ctx context.Context, raw json.RawMessage) (createWorktreePlan, error) {
	t.previewMu.Lock()
	cached := t.preview
	t.preview = nil
	t.previewMu.Unlock()

	plan, err := t.plan(ctx, raw)
	if err != nil {
		return createWorktreePlan{}, err
	}
	if cached != nil && cached.raw == string(raw) && !sameCreateWorktreePlan(cached.plan, plan) {
		return createWorktreePlan{}, errors.New("create_worktree: repository state changed since preview; inspect and retry")
	}
	return plan, nil
}

type createWorktreePlan struct {
	repoRoot          string
	branch            string
	base              string
	worktreeRoot      string
	worktreePath      string
	rootSource        string
	configValue       string
	setConfig         bool
	bootstrapRequired bool
	previewText       string
	preview           core.ToolResult
	ignore            worktreesIgnoreUpdate
}

func (t *CreateWorktreeTool) plan(ctx context.Context, raw json.RawMessage) (createWorktreePlan, error) {
	var args createWorktreeArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return createWorktreePlan{}, fmt.Errorf("invalid args: %w", err)
	}
	branchInput := strings.TrimSpace(args.Branch)
	if branchInput == "" {
		return createWorktreePlan{}, errors.New("branch is required")
	}
	if strings.HasPrefix(branchInput, "-") || strings.Contains(branchInput, "@{") {
		return createWorktreePlan{}, fmt.Errorf("create_worktree: invalid branch %q", branchInput)
	}
	if t.Sandbox == nil {
		return createWorktreePlan{}, errors.New("create_worktree: sandbox is required")
	}
	if err := t.Sandbox.CheckBashPermission("git"); err != nil {
		return createWorktreePlan{}, fmt.Errorf("create_worktree: %w", err)
	}

	cwd, err := t.accessibleCWD()
	if err != nil {
		return createWorktreePlan{}, err
	}
	candidateRoot, err := t.findGitRootCandidate(cwd)
	if err != nil {
		return createWorktreePlan{}, err
	}
	repoRoot, err := createWorktreeGitOutput(ctx, candidateRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return createWorktreePlan{}, fmt.Errorf("create_worktree: verify repository: %w", err)
	}
	repoRoot, err = canonical(repoRoot)
	if err != nil {
		return createWorktreePlan{}, fmt.Errorf("create_worktree: resolve repository root: %w", err)
	}
	if err := t.checkReadAccess(repoRoot); err != nil {
		return createWorktreePlan{}, err
	}
	branch, err := createWorktreeGitOutput(ctx, repoRoot, "check-ref-format", "--branch", branchInput)
	if err != nil {
		return createWorktreePlan{}, fmt.Errorf("create_worktree: invalid branch %q: %w", branchInput, err)
	}
	if branch != branchInput {
		return createWorktreePlan{}, fmt.Errorf("create_worktree: invalid branch %q", branchInput)
	}
	base, err := createWorktreeGitOutput(ctx, repoRoot, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return createWorktreePlan{}, fmt.Errorf("create_worktree: resolve current commit: %w", err)
	}
	commonDir, err := createWorktreeGitOutput(ctx, repoRoot, "rev-parse", "--git-common-dir")
	if err != nil {
		return createWorktreePlan{}, fmt.Errorf("create_worktree: resolve Git metadata: %w", err)
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repoRoot, commonDir)
	}
	commonDir, err = canonical(commonDir)
	if err != nil {
		return createWorktreePlan{}, fmt.Errorf("create_worktree: resolve Git metadata: %w", err)
	}
	if err := t.checkReadAccess(commonDir); err != nil {
		return createWorktreePlan{}, err
	}

	configuredRoot, configured, err := createWorktreeGitConfigValue(ctx, repoRoot, worktreeConfigKey)
	if err != nil {
		return createWorktreePlan{}, fmt.Errorf("create_worktree: read local worktree configuration: %w", err)
	}
	bootstrapRoot := strings.TrimSpace(args.BootstrapRoot)

	rootSource := "existing .worktrees directory"
	configValue := ""
	setConfig := false
	defaultRoot := false
	var worktreeRoot string
	if configured {
		worktreeRoot, defaultRoot, err = resolveConfiguredWorktreeRoot(repoRoot, configuredRoot)
		if err != nil {
			return createWorktreePlan{}, err
		}
		if bootstrapRoot != "" {
			requestedRoot, requestedDefault, _, err := resolveBootstrapWorktreeRoot(repoRoot, bootstrapRoot)
			if err != nil {
				return createWorktreePlan{}, err
			}
			configuredCanonical, err := canonicalOrParent(worktreeRoot)
			if err != nil {
				return createWorktreePlan{}, fmt.Errorf("create_worktree: resolve configured worktree root: %w", err)
			}
			requestedCanonical, err := canonicalOrParent(requestedRoot)
			if err != nil {
				return createWorktreePlan{}, fmt.Errorf("create_worktree: resolve bootstrap_root: %w", err)
			}
			if defaultRoot != requestedDefault || configuredCanonical != requestedCanonical {
				return createWorktreePlan{}, errors.New("create_worktree: worktree root is already configured; change the local Git configuration explicitly before bootstrapping again")
			}
		}
		rootSource = "local Git config"
	} else {
		defaultPath := filepath.Join(repoRoot, defaultWorktreeRoot)
		exists, err := inspectWorktreeRoot(defaultPath)
		if err != nil {
			return createWorktreePlan{}, err
		}
		if !exists && bootstrapRoot == "" {
			return bootstrapRequiredPlan(branch, repoRoot), nil
		}
		if exists {
			if bootstrapRoot != "" && bootstrapRoot != defaultWorktreeRoot {
				return createWorktreePlan{}, errors.New("create_worktree: worktree root is already established; omit bootstrap_root")
			}
			worktreeRoot = defaultPath
			defaultRoot = true
		} else {
			worktreeRoot, defaultRoot, configValue, err = resolveBootstrapWorktreeRoot(repoRoot, bootstrapRoot)
			if err != nil {
				return createWorktreePlan{}, err
			}
			setConfig = true
			rootSource = "bootstrap selection"
		}
	}

	worktreeRoot, err = validateWorktreeRoot(worktreeRoot, defaultRoot)
	if err != nil {
		return createWorktreePlan{}, err
	}
	worktreePath := filepath.Join(worktreeRoot, filepath.FromSlash(branch))
	rootCanonical, err := canonicalOrParent(worktreeRoot)
	if err != nil {
		return createWorktreePlan{}, fmt.Errorf("create_worktree: resolve worktree root: %w", err)
	}
	if !defaultRoot && isUnder(repoRoot, rootCanonical) {
		return createWorktreePlan{}, errors.New("create_worktree: external worktree root must be outside the repository")
	}
	pathCanonical, err := canonicalOrParent(worktreePath)
	if err != nil {
		return createWorktreePlan{}, fmt.Errorf("create_worktree: resolve worktree path: %w", err)
	}
	if !isUnder(rootCanonical, pathCanonical) {
		return createWorktreePlan{}, errors.New("create_worktree: branch path escapes configured worktree root")
	}

	ignorePath := ""
	accessPaths := []string{repoRoot, commonDir, worktreeRoot, worktreePath}
	if defaultRoot {
		ignorePath = filepath.Join(repoRoot, ".gitignore")
		accessPaths = append(accessPaths, ignorePath)
	}
	if err := t.checkReadWriteAccess(accessPaths...); err != nil {
		return createWorktreePlan{}, err
	}
	if _, err := os.Lstat(worktreePath); err == nil {
		return createWorktreePlan{}, fmt.Errorf("create_worktree: worktree path %q already exists", filepath.ToSlash(worktreePath))
	} else if !os.IsNotExist(err) {
		return createWorktreePlan{}, fmt.Errorf("create_worktree: inspect worktree path: %w", err)
	}
	exists, err := createWorktreeBranchExists(ctx, repoRoot, branch)
	if err != nil {
		return createWorktreePlan{}, fmt.Errorf("create_worktree: check branch: %w", err)
	}
	if exists {
		return createWorktreePlan{}, fmt.Errorf("create_worktree: branch %q already exists", branch)
	}

	ignore := worktreesIgnoreUpdate{}
	ignoreAction := "not modified (external worktree root)"
	if defaultRoot {
		ignore, err = newWorktreesIgnoreUpdate(ignorePath)
		if err != nil {
			return createWorktreePlan{}, fmt.Errorf("create_worktree: read .gitignore: %w", err)
		}
		ignoreAction = "already ignores " + defaultWorktreeRoot + "/"
		if ignore.changed {
			ignoreAction = "add " + worktreesIgnoreEntry
		}
	}

	displayRoot := worktreeRoot
	if defaultRoot {
		displayRoot = defaultWorktreeRoot
	}
	displayPath := displayWorktreePath(repoRoot, worktreePath, defaultRoot)
	configAction := ""
	if setConfig {
		configAction = fmt.Sprintf("local config: set %s=%s\n", worktreeConfigKey, configValue)
	}
	previewText := fmt.Sprintf("create worktree\nbranch: %s\nbase: %s\nworktree root: %s\nroot source: %s\npath: %s\n%s.gitignore: %s\n", branch, base, filepath.ToSlash(displayRoot), rootSource, displayPath, configAction, ignoreAction)
	return createWorktreePlan{
		repoRoot:     repoRoot,
		branch:       branch,
		base:         base,
		worktreeRoot: worktreeRoot,
		worktreePath: worktreePath,
		rootSource:   rootSource,
		configValue:  configValue,
		setConfig:    setConfig,
		previewText:  previewText,
		preview: core.ToolResult{
			Content: []provider.Content{provider.TextBlock{Text: previewText}},
			Details: map[string]any{
				"state":           "ready",
				"branch":          branch,
				"base":            base,
				"worktree_root":   displayRoot,
				"root_source":     rootSource,
				"path":            displayPath,
				"bootstrap_saved": setConfig,
			},
		},
		ignore: ignore,
	}, nil
}

func bootstrapRequiredPlan(branch, repoRoot string) createWorktreePlan {
	text := fmt.Sprintf("Worktree bootstrap is required.\nbranch: %s\nrepository: %s\nChoose a location with the user, then call create_worktree again with bootstrap_root set to .worktrees for the repository default or to an absolute external directory. This call made no changes.\n", branch, filepath.ToSlash(repoRoot))
	return createWorktreePlan{
		branch:            branch,
		bootstrapRequired: true,
		previewText:       text,
		preview: core.ToolResult{
			Content: []provider.Content{provider.TextBlock{Text: text}},
			Details: map[string]any{
				"state":                  "bootstrap_required",
				"branch":                 branch,
				"default_bootstrap_root": defaultWorktreeRoot,
			},
		},
	}
}

func (t *CreateWorktreeTool) accessibleCWD() (string, error) {
	cwd := t.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("create_worktree: working directory: %w", err)
		}
	}
	cwd, err := canonical(cwd)
	if err != nil {
		return "", fmt.Errorf("create_worktree: resolve working directory: %w", err)
	}
	if err := t.checkReadAccess(cwd); err != nil {
		return "", err
	}
	return cwd, nil
}

func (t *CreateWorktreeTool) findGitRootCandidate(cwd string) (string, error) {
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if err := t.checkReadAccess(dir); err != nil {
			return "", err
		}
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("create_worktree: inspect Git metadata: %w", err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return "", errors.New("create_worktree: working directory is not a Git repository")
}

func (t *CreateWorktreeTool) checkReadAccess(paths ...string) error {
	for _, path := range paths {
		if err := t.Sandbox.CheckReadPath(path); err != nil {
			return fmt.Errorf("create_worktree: %w", err)
		}
	}
	return nil
}

func (t *CreateWorktreeTool) checkReadWriteAccess(paths ...string) error {
	for _, path := range paths {
		if err := t.Sandbox.CheckReadPath(path); err != nil {
			return fmt.Errorf("create_worktree: %w", err)
		}
		if err := t.Sandbox.CheckWritePath(path); err != nil {
			return fmt.Errorf("create_worktree: %w", err)
		}
	}
	return nil
}

func resolveConfiguredWorktreeRoot(repoRoot, value string) (string, bool, error) {
	value = strings.TrimSpace(value)
	if value == defaultWorktreeRoot {
		return filepath.Join(repoRoot, defaultWorktreeRoot), true, nil
	}
	if !filepath.IsAbs(value) {
		return "", false, errors.New("create_worktree: local worktree configuration must be .worktrees or an absolute external path")
	}
	return value, false, nil
}

func resolveBootstrapWorktreeRoot(repoRoot, value string) (string, bool, string, error) {
	if value == defaultWorktreeRoot {
		return filepath.Join(repoRoot, defaultWorktreeRoot), true, defaultWorktreeRoot, nil
	}
	if !filepath.IsAbs(value) {
		return "", false, "", errors.New("create_worktree: bootstrap_root must be .worktrees or an absolute external path")
	}
	root, err := canonicalOrParent(value)
	if err != nil {
		return "", false, "", fmt.Errorf("create_worktree: resolve bootstrap_root: %w", err)
	}
	if isUnder(repoRoot, root) {
		return "", false, "", errors.New("create_worktree: external bootstrap_root must be outside the repository")
	}
	return root, false, root, nil
}

func validateWorktreeRoot(path string, defaultRoot bool) (string, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return path, nil
	}
	if err != nil {
		return "", fmt.Errorf("create_worktree: inspect worktree root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("create_worktree: worktree root must not be a symbolic link")
	}
	if !info.IsDir() {
		return "", errors.New("create_worktree: worktree root exists but is not a directory")
	}
	if defaultRoot && filepath.Base(path) != defaultWorktreeRoot {
		return "", errors.New("create_worktree: invalid default worktree root")
	}
	return path, nil
}

func inspectWorktreeRoot(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create_worktree: inspect .worktrees: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("create_worktree: .worktrees must not be a symbolic link")
	}
	if !info.IsDir() {
		return false, errors.New("create_worktree: .worktrees exists but is not a directory")
	}
	return true, nil
}

func displayWorktreePath(repoRoot, path string, defaultRoot bool) string {
	if defaultRoot {
		relative, err := filepath.Rel(repoRoot, path)
		if err == nil {
			return filepath.ToSlash(relative)
		}
	}
	return filepath.ToSlash(path)
}

func sameCreateWorktreePlan(a, b createWorktreePlan) bool {
	return a.bootstrapRequired == b.bootstrapRequired &&
		a.repoRoot == b.repoRoot &&
		a.branch == b.branch &&
		a.base == b.base &&
		a.worktreeRoot == b.worktreeRoot &&
		a.worktreePath == b.worktreePath &&
		a.rootSource == b.rootSource &&
		a.configValue == b.configValue &&
		a.setConfig == b.setConfig &&
		a.ignore.path == b.ignore.path &&
		a.ignore.existed == b.ignore.existed &&
		a.ignore.changed == b.ignore.changed &&
		bytes.Equal(a.ignore.before, b.ignore.before)
}

func missingWorktreeParents(worktreePath string) ([]string, error) {
	var missing []string
	for directory := filepath.Dir(worktreePath); ; directory = filepath.Dir(directory) {
		info, err := os.Lstat(directory)
		if err == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("%q is not a directory", directory)
			}
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		missing = append(missing, directory)
		parent := filepath.Dir(directory)
		if parent == directory {
			return nil, errors.New("no existing parent directory")
		}
	}
	for left, right := 0, len(missing)-1; left < right; left, right = left+1, right-1 {
		missing[left], missing[right] = missing[right], missing[left]
	}
	return missing, nil
}

func removeCreatedWorktreeParents(parents []string) error {
	for index := len(parents) - 1; index >= 0; index-- {
		if err := os.Remove(parents[index]); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (p createWorktreePlan) removeBootstrapConfig(ctx context.Context) error {
	value, configured, err := createWorktreeGitConfigValue(ctx, p.repoRoot, worktreeConfigKey)
	if err != nil {
		return err
	}
	if !configured {
		return nil
	}
	if value != p.configValue {
		return errors.New("local worktree configuration changed during creation")
	}
	_, err = createWorktreeGitOutput(ctx, p.repoRoot, "config", "--local", "--unset-all", worktreeConfigKey)
	return err
}

func (p createWorktreePlan) cleanupFailedCheckout(ctx context.Context) error {
	registered, err := createWorktreeRegistered(ctx, p.repoRoot, p.worktreePath)
	if err != nil {
		return err
	}
	var cleanupErr error
	if registered {
		if _, err := createWorktreeGitOutput(ctx, p.repoRoot, "worktree", "remove", "--force", p.worktreePath); err != nil {
			cleanupErr = fmt.Errorf("remove registered worktree: %w", err)
		}
	}
	branchHead, exists, err := createWorktreeGitRef(ctx, p.repoRoot, "refs/heads/"+p.branch)
	if err == nil && exists && branchHead == p.base {
		if _, err := createWorktreeGitOutput(ctx, p.repoRoot, "branch", "-D", p.branch); err != nil && cleanupErr == nil {
			cleanupErr = fmt.Errorf("remove branch: %w", err)
		}
	}
	if err != nil && cleanupErr == nil {
		cleanupErr = err
	}
	return cleanupErr
}

func createWorktreeRegistered(ctx context.Context, repoRoot, worktreePath string) (bool, error) {
	output, err := createWorktreeGitOutput(ctx, repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return false, err
	}
	want, err := canonicalOrParent(worktreePath)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		path, err := canonicalOrParent(strings.TrimPrefix(line, "worktree "))
		if err == nil && path == want {
			return true, nil
		}
	}
	return false, nil
}

func createWorktreeBranchExists(ctx context.Context, repoRoot, branch string) (bool, error) {
	_, err := createWorktreeGitOutput(ctx, repoRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err == nil {
		return true, nil
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if createWorktreeGitExitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

func createWorktreeGitRef(ctx context.Context, repoRoot, ref string) (string, bool, error) {
	output, err := createWorktreeGitOutput(ctx, repoRoot, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err == nil {
		return output, true, nil
	}
	if createWorktreeGitExitCode(err) == 1 {
		return "", false, nil
	}
	return "", false, err
}

func createWorktreeGitConfigValue(ctx context.Context, repoRoot, key string) (string, bool, error) {
	output, err := createWorktreeGitOutput(ctx, repoRoot, "config", "--local", "--get", key)
	if err == nil {
		return output, true, nil
	}
	if createWorktreeGitExitCode(err) == 1 {
		return "", false, nil
	}
	return "", false, err
}

type createWorktreeGitError struct {
	args   []string
	output string
	err    error
}

func (e *createWorktreeGitError) Error() string {
	message := strings.TrimSpace(e.output)
	if message == "" {
		message = e.err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.args, " "), message)
}

func (e *createWorktreeGitError) Unwrap() error { return e.err }

func createWorktreeGitExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func createWorktreeGitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmdArgs := append([]string{"-c", "core.hooksPath=" + os.DevNull, "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = createWorktreeGitEnv()
	setProcessGroup(cmd)

	var stdout, output cappedGitOutput
	cmd.Stdout = io.MultiWriter(&stdout, &output)
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start git %s: %w", strings.Join(args, " "), err)
	}
	watchDone := make(chan struct{})
	watchStopped := make(chan struct{})
	go func() {
		defer close(watchStopped)
		select {
		case <-ctx.Done():
			killProcessGroup(cmd)
		case <-watchDone:
		}
	}()
	waitErr := cmd.Wait()
	close(watchDone)
	<-watchStopped
	if waitErr != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", &createWorktreeGitError{args: args, output: output.String(), err: waitErr}
	}
	return strings.TrimSpace(stdout.String()), nil
}

type cappedGitOutput struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	truncated bool
}

func (b *cappedGitOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := maxCreateWorktreeGitOutput - b.buffer.Len(); room > 0 {
		if len(p) > room {
			_, _ = b.buffer.Write(p[:room])
			b.truncated = true
		} else {
			_, _ = b.buffer.Write(p)
		}
	} else {
		b.truncated = true
	}
	return len(p), nil
}

func (b *cappedGitOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	output := b.buffer.String()
	if b.truncated {
		output += "\n... [git output truncated]"
	}
	return output
}

func createWorktreeGitEnv() []string {
	env := make([]string, 0, len(os.Environ())+5)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GIT_") || key == "SSH_ASKPASS" {
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
	)
}

type worktreesIgnoreUpdate struct {
	path    string
	before  []byte
	after   []byte
	existed bool
	mode    os.FileMode
	changed bool
}

func newWorktreesIgnoreUpdate(path string) (worktreesIgnoreUpdate, error) {
	contents, mode, existed, err := readWorktreesIgnore(path)
	if err != nil {
		return worktreesIgnoreUpdate{}, err
	}
	for _, line := range strings.Split(string(contents), "\n") {
		switch strings.TrimSuffix(line, "\r") {
		case worktreesIgnoreEntry, worktreesUnanchoredIgnoreEntry:
			return worktreesIgnoreUpdate{path: path, before: contents, existed: existed, mode: mode}, nil
		}
	}
	lineEnding := "\n"
	if bytes.Contains(contents, []byte("\r\n")) {
		lineEnding = "\r\n"
	}
	after := append([]byte(nil), contents...)
	if len(after) > 0 && !strings.HasSuffix(string(after), "\n") {
		after = append(after, lineEnding...)
	}
	after = append(after, []byte(worktreesIgnoreEntry+lineEnding)...)
	return worktreesIgnoreUpdate{path: path, before: contents, after: after, existed: existed, mode: mode, changed: true}, nil
}

func (u worktreesIgnoreUpdate) apply() error {
	if !u.changed {
		return nil
	}
	current, _, existed, err := readWorktreesIgnore(u.path)
	if err != nil {
		return err
	}
	if existed != u.existed || !bytes.Equal(current, u.before) {
		return errors.New(".gitignore changed since preview; inspect it and retry")
	}
	return writeWorktreesIgnore(u.path, u.after, u.mode)
}

func (u worktreesIgnoreUpdate) restore() error {
	if !u.changed {
		return nil
	}
	current, _, existed, err := readWorktreesIgnore(u.path)
	if err != nil {
		return err
	}
	if !existed || !bytes.Equal(current, u.after) {
		return nil
	}
	if !u.existed {
		if err := os.Remove(u.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeWorktreesIgnore(u.path, u.before, u.mode)
}

func readWorktreesIgnore(path string) ([]byte, os.FileMode, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, 0o644, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, 0, false, errors.New(".gitignore must not be a symbolic link")
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, errors.New(".gitignore must be a regular file")
	}
	if info.Size() > maxCreateWorktreeIgnoreBytes {
		return nil, 0, false, fmt.Errorf(".gitignore exceeds %d bytes", maxCreateWorktreeIgnoreBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maxCreateWorktreeIgnoreBytes+1))
	if err != nil {
		return nil, 0, false, err
	}
	if len(contents) > maxCreateWorktreeIgnoreBytes {
		return nil, 0, false, fmt.Errorf(".gitignore exceeds %d bytes", maxCreateWorktreeIgnoreBytes)
	}
	return contents, info.Mode().Perm(), true, nil
}

func writeWorktreesIgnore(path string, contents []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".zut-gitignore-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
