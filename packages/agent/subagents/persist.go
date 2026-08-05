package subagents

// On-disk persistence for subagent agents.
//
// Every Spawn writes a meta.json next to the agent's events.jsonl and
// session.json. The file captures the immutable identity bits (id,
// task, branch, dir) plus the paths the runner needs to resume the
// agent later. On a fresh zot launch, Supervisor.Reload() walks
// <root>/agents/*/meta.json and re-registers every agent it finds in
// StatusDetached so the user can see, view, resume, or remove them
// from the dashboard.
//
// We don't try to keep meta.json in sync with mutable state (status,
// activity, transcript). Those live in the events log (durable) and
// in-memory Agent fields (rebuilt by Reload from the log tail).
// Keeping meta.json immutable means we never have to worry about
// concurrent writers stomping on each other and the file matters
// only on the spawn/reload boundary.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// agentMeta is the durable identity record for one agent. Only fields
// the supervisor needs to rebuild an Agent after a restart live here.
// Adding a field leaves it zero when absent; removing or renaming a field
// changes the persisted contract.
//
// Historical fields like `branch` and `isolated` are silently dropped
// by encoding/json's permissive decoder when an older meta.json is
// loaded; we don't need to keep them in the struct.
type agentMeta struct {
	ID               string        `json:"id"`
	Task             string        `json:"task"`
	OriginalTask     string        `json:"original_task,omitempty"`
	Dir              string        `json:"dir"`
	RepositoryRoot   string        `json:"repository_root,omitempty"`
	Started          time.Time     `json:"started"`
	ParentID         string        `json:"parent_id,omitempty"`
	BatchID          string        `json:"batch_id,omitempty"`
	RootSessionID    string        `json:"root_session_id,omitempty"`
	Model            string        `json:"model,omitempty"`
	Provider         string        `json:"provider,omitempty"`
	BaseURL          string        `json:"base_url,omitempty"`
	InsecureTLS      bool          `json:"insecure_tls,omitempty"`
	Reasoning        string        `json:"reasoning,omitempty"`
	FastMode         *bool         `json:"fast_mode,omitempty"`
	Subagent         string        `json:"subagent,omitempty"`
	WorkspaceMode    WorkspaceMode `json:"workspace_mode,omitempty"`
	WorkspacePath    string        `json:"workspace_path,omitempty"`
	WorkspaceBase    string        `json:"workspace_base,omitempty"`
	WorkspaceCapture CaptureMode   `json:"workspace_capture,omitempty"`
	MaxTurns         int           `json:"max_turns,omitempty"`
	Timeout          time.Duration `json:"timeout,omitempty"`
	Tools            []string      `json:"tools,omitempty"`
	ProcessState     ProcessState  `json:"process_state,omitempty"`
	TurnState        TurnState     `json:"turn_state,omitempty"`
	CurrentTurnID    string        `json:"current_turn_id,omitempty"`
	Attempt          int           `json:"attempt,omitempty"`
	ProcessPID       int           `json:"process_pid,omitempty"`
	UpdatedAt        time.Time     `json:"updated_at,omitempty"`
	LastActivity     time.Time     `json:"last_activity,omitempty"`
	ResultRef        string        `json:"result_ref,omitempty"`
	PatchRef         string        `json:"patch_ref,omitempty"`
	ChangedFiles     []string      `json:"changed_files,omitempty"`
	InboxPath        string        `json:"inbox_path"`
	EventLogPath     string        `json:"event_log_path"`
	SessionPath      string        `json:"session_path"`

	// SessionID, when non-empty, scopes the agent to a particular
	// host zot session: the dashboard only shows agents whose
	// SessionID matches the active session. Older meta.json files
	// (and agents spawned outside of any session, e.g. by tests or
	// scripted callers that didn't call SetActiveSession) have an
	// empty SessionID and are visible from every session as a
	// unscoped-agent fallback.
	SessionID string `json:"session_id,omitempty"`
}

func metaPath(stateDir string) string { return filepath.Join(stateDir, "meta.json") }

// writeAgentMeta serialises a's identity into stateDir/meta.json. The
// write is atomic (tmp + rename) so a crash mid-write can't leave a
// half-parsable file that fails Reload.
func writeAgentMeta(stateDir string, a *Agent) error {
	fastMode := a.FastMode
	a.lifecycleMu.Lock()
	processState := a.processState
	turnState := a.turnState
	currentTurnID := a.currentTurnID
	attempt := a.Attempt
	processPID := a.ProcessPID
	updatedAt := a.updatedAt
	lastActivity := a.lastActivity
	resultRef := a.resultRef
	patchRef := a.patchRef
	changedFiles := append([]string(nil), a.changedFiles...)
	a.lifecycleMu.Unlock()
	m := agentMeta{
		ID:               a.ID,
		Task:             a.Task,
		OriginalTask:     a.OriginalTask,
		Dir:              a.Dir,
		RepositoryRoot:   a.RepositoryRoot,
		Started:          a.Started,
		ParentID:         a.ParentID,
		BatchID:          a.BatchID,
		RootSessionID:    a.RootSessionID,
		Model:            a.Model,
		Provider:         a.Provider,
		BaseURL:          a.BaseURL,
		InsecureTLS:      a.InsecureTLS,
		Reasoning:        a.Reasoning,
		FastMode:         &fastMode,
		Subagent:         a.Subagent,
		WorkspaceMode:    a.WorkspaceMode,
		WorkspacePath:    a.WorkspacePath,
		WorkspaceBase:    a.WorkspaceBase,
		WorkspaceCapture: a.WorkspaceCapture,
		MaxTurns:         a.MaxTurns,
		Timeout:          a.Timeout,
		Tools:            append([]string(nil), a.Tools...),
		ProcessState:     processState,
		TurnState:        turnState,
		CurrentTurnID:    currentTurnID,
		Attempt:          attempt,
		ProcessPID:       processPID,
		UpdatedAt:        updatedAt,
		LastActivity:     lastActivity,
		ResultRef:        resultRef,
		PatchRef:         patchRef,
		ChangedFiles:     changedFiles,
		InboxPath:        a.InboxPath,
		EventLogPath:     a.EventLogPath,
		SessionPath:      a.SessionPath,
		SessionID:        a.SessionID,
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("subagent meta marshal: %w", err)
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("subagents meta dir: %w", err)
	}
	final := metaPath(stateDir)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("subagents meta write: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("subagent meta rename: %w", err)
	}
	return nil
}

// readAgentMeta loads one meta.json. Returns os.ErrNotExist (wrapped)
// when the file is missing so callers can distinguish "no such agent"
// from "corrupt metadata".
func readAgentMeta(stateDir string) (agentMeta, error) {
	var m agentMeta
	b, err := os.ReadFile(metaPath(stateDir))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("subagent meta parse %s: %w", stateDir, err)
	}
	if m.ID == "" {
		return m, fmt.Errorf("subagent meta %s: missing id", stateDir)
	}
	return m, nil
}

func safeAgentID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id && !strings.ContainsAny(id, `/\\`)
}

func (f *Supervisor) sanitizeReloadMeta(stateDir, root string, m *agentMeta) error {
	if m == nil {
		return errors.New("subagents reload: nil metadata")
	}
	if m.EventLogPath == "" {
		m.EventLogPath = filepath.Join(stateDir, "events.jsonl")
	}
	if m.SessionPath == "" {
		m.SessionPath = filepath.Join(stateDir, "session.json")
	}
	if filepath.Base(m.EventLogPath) != "events.jsonl" || !pathWithin(m.EventLogPath, stateDir) {
		return fmt.Errorf("subagents reload %s: event log path escapes agent state", stateDir)
	}
	if filepath.Base(m.SessionPath) != "session.json" || !pathWithin(m.SessionPath, stateDir) {
		return fmt.Errorf("subagents reload %s: session path escapes agent state", stateDir)
	}
	if m.InboxPath == "" || !validRuntimeInboxPath(m.InboxPath, m.ID) {
		// Old metadata may point at a runtime directory from a previous
		// environment. Recompute that transient path rather than ever
		// dialing an arbitrary socket supplied by metadata.
		expectedInbox, err := inboxSocketPath(root, m.ID)
		if err != nil {
			return fmt.Errorf("subagents reload %s: inbox path: %w", stateDir, err)
		}
		m.InboxPath = expectedInbox
	}
	return nil
}

func validRuntimeInboxPath(path, id string) bool {
	if !filepath.IsAbs(path) || (filepath.Base(path) != id+".sock" && filepath.Base(path) != shortHash(id)+".sock") {
		return false
	}
	dir := filepath.Dir(path)
	if !strings.HasPrefix(filepath.Base(dir), "zot-subagents-") {
		return false
	}
	bases := []string{os.TempDir(), "/tmp"}
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(runtimeDir) {
		bases = append(bases, runtimeDir)
	}
	for _, base := range bases {
		if pathWithin(dir, filepath.Clean(base)) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// Reload scans <root>/agents/*/meta.json and re-registers every
// previously-spawned agent as a StatusDetached entry. Agents already
// present in memory are left alone (Reload is idempotent and safe to
// call after Spawn, though in practice the cli invokes it exactly
// once just after New()).
//
// The reloaded agents have no live Runner; the user can:
//   - view their transcript (the dashboard reads from EventLogPath),
//   - resume them via Supervisor.Resume (starts a fresh subprocess on the
//     same worktree / session / inbox path),
//   - remove them (worktree + meta + events log gone).
//
// Reload returns the number of agents loaded plus any per-directory
// error encountered. Malformed entries are skipped rather than
// failing the whole reload — one bad meta.json shouldn't hide the
// rest of the subagents.
func (f *Supervisor) Reload() (loaded int, errs []error) {
	root := filepath.Clean(f.cfg.Root)
	if root == "." {
		return 0, nil
	}
	loaded, errs = f.reloadRoot(root)
	errs = append(errs, f.reloadBatches(root)...)
	return loaded, errs
}

func (f *Supervisor) reloadRoot(root string) (loaded int, errs []error) {
	agentsDir := filepath.Join(root, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, []error{fmt.Errorf("subagents reload %s: %w", root, err)}
	}

	// Sort by directory name so the load order is stable across runs.
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		stateDir := filepath.Join(agentsDir, name)
		m, err := readAgentMeta(stateDir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			errs = append(errs, err)
			continue
		}
		if filepath.Base(stateDir) != m.ID || !safeAgentID(m.ID) {
			errs = append(errs, fmt.Errorf("subagents reload %s: unsafe or mismatched agent id", stateDir))
			continue
		}
		if err := f.sanitizeReloadMeta(stateDir, root, &m); err != nil {
			errs = append(errs, err)
			continue
		}

		f.mu.Lock()
		if _, exists := f.agents[m.ID]; exists {
			f.mu.Unlock()
			continue
		}
		a := f.buildDetachedAgent(m)
		f.agents[m.ID] = a
		f.order = append(f.order, m.ID)
		f.mu.Unlock()
		loaded++
	}
	return loaded, errs
}

// buildDetachedAgent constructs an Agent from a meta.json with no
// running Runner. The agent's transcript is populated from the tail
// of its event log so the dashboard immediately shows recent output;
// activity is inferred from the last lifecycle event.
//
// The returned Agent has a closed `done` channel because Wait should
// return instantly: there is nothing to wait for.
func (f *Supervisor) buildDetachedAgent(m agentMeta) *Agent {
	// Metadata without a repository identity uses the current RepoRoot;
	// records with one retain their persisted repository so a restart from
	// another checkout cannot silently redirect the child.
	dir := m.Dir
	if m.WorkspaceMode != WorkspaceWorktree && m.RepositoryRoot == "" && f.cfg.RepoRoot != "" {
		// Preserve the best-effort migration only for records without an
		// explicit repository identity.
		dir = f.cfg.RepoRoot
	}
	if dir == "" {
		dir = firstNonEmpty(m.RepositoryRoot, f.cfg.RepoRoot)
	}
	fastMode := f.cfg.FastMode
	if m.FastMode != nil {
		fastMode = *m.FastMode
	}
	turnState := m.TurnState
	if turnState == "" {
		turnState = TurnIdle
	}
	lastActivity := m.LastActivity
	if lastActivity.IsZero() {
		lastActivity = m.Started
	}
	processState := ProcessDetached
	activity := "detached"
	if inboxLive(m.InboxPath) {
		processState = ProcessAlive
		activity = "live (detached)"
	}
	agentStateDir := f.agentStateDir(m.ID)
	if m.EventLogPath != "" {
		// EventLogPath is <state-dir>/events.jsonl; keep the reloaded
		// agent's state scoped to its own directory. Using the parent
		// agents/ directory here would make Remove delete sibling agents
		// and would make resumed result/patch writes collide.
		agentStateDir = filepath.Dir(m.EventLogPath)
	}
	a := &Agent{
		ID:               m.ID,
		Task:             m.Task,
		OriginalTask:     firstNonEmpty(m.OriginalTask, m.Task),
		Dir:              dir,
		RepositoryRoot:   firstNonEmpty(m.RepositoryRoot, f.cfg.RepoRoot),
		Started:          m.Started,
		ParentID:         m.ParentID,
		BatchID:          m.BatchID,
		RootSessionID:    m.RootSessionID,
		Model:            m.Model,
		Provider:         m.Provider,
		BaseURL:          m.BaseURL,
		InsecureTLS:      m.InsecureTLS,
		Reasoning:        m.Reasoning,
		FastMode:         fastMode,
		Subagent:         m.Subagent,
		WorkspaceMode:    m.WorkspaceMode,
		WorkspacePath:    m.WorkspacePath,
		WorkspaceBase:    m.WorkspaceBase,
		WorkspaceCapture: m.WorkspaceCapture,
		MaxTurns:         m.MaxTurns,
		Timeout:          m.Timeout,
		Tools:            append([]string(nil), m.Tools...),
		Attempt:          m.Attempt,
		ProcessPID:       m.ProcessPID,
		InboxPath:        m.InboxPath,
		EventLogPath:     m.EventLogPath,
		SessionPath:      m.SessionPath,
		SessionID:        m.SessionID,
		inbox:            NewInbox(m.InboxPath),
		status:           StatusDetached,
		activity:         activity,
		processState:     processState,
		turnState:        turnState,
		currentTurnID:    m.CurrentTurnID,
		updatedAt:        m.UpdatedAt,
		lastActivity:     lastActivity,
		stateDir:         agentStateDir,
		resultRef:        m.ResultRef,
		patchRef:         m.PatchRef,
		changedFiles:     append([]string(nil), m.ChangedFiles...),
		maxOutputBytes:   f.cfg.Policy.MaxOutputBytes,
		maxOutputLines:   f.cfg.Policy.MaxOutputLines,
		done:             make(chan struct{}),
		turnResults:      make(chan *TurnResult, 16),
	}
	if a.updatedAt.IsZero() {
		a.updatedAt = lastActivity
	}
	a.closeDone()

	// Recover transcript + activity hints from the event log. Best
	// effort: a missing or unreadable log just leaves the agent
	// detached with an empty transcript.
	if a.EventLogPath != "" {
		if evs, err := ReadEventLog(a.EventLogPath); err == nil {
			replayEventsIntoAgent(a, evs)
		}
	}
	// The append-only log may contain a terminal event from an earlier
	// attempt. A live socket is stronger evidence for the current process;
	// keep the reloaded entry detached/live so Stop and Remove cannot treat a
	// resumed worker as already finished.
	if inboxLive(a.InboxPath) {
		a.mu.Lock()
		a.status = StatusDetached
		a.activity = "live (detached)"
		a.mu.Unlock()
		a.setProcessState(ProcessAlive)
	}
	resultDir := f.agentStateDir(a.ID)
	if a.EventLogPath != "" {
		resultDir = filepath.Dir(a.EventLogPath)
	}
	if result, err := readTurnResult(resultDir); err == nil && validateTurnResultAgent(result, a.ID) == nil {
		a.setResult(result.Bounded(f.cfg.Policy.MaxOutputBytes, f.cfg.Policy.MaxOutputLines))
	}
	return a
}

// replayEventsIntoAgent re-derives an agent's transcript and last
// known status hint from its event log. Mirrors applyEventToSink in
// runner.go but writes directly to the Agent fields (no Sink because
// the agent isn't being driven by a runner yet).
//
// Status precedence: explicit lifecycle events (agent_stopped) win
// over inferred ones (assistant_message → idle). If the log contains
// no terminator we keep status=StatusDetached so the user can resume.
func replayEventsIntoAgent(a *Agent, evs []Event) {
	terminal := false
	for _, ev := range evs {
		switch ev.Type {
		case EventTurnStarted, "turn_start":
			turnID := ev.TurnID
			if turnID == "" {
				if step, ok := ev.Data["step"].(float64); ok {
					turnID = fmt.Sprintf("turn-%d", int(step))
				}
			}
			a.setTurnState(TurnRunning, turnID)
		case EventTurnResult, "turn_result":
			if result, err := decodeTurnResultEvent(ev, a.ID, a.maxOutputBytes, a.maxOutputLines); err == nil {
				a.setResult(result)
				if result.Status == ResultFailed {
					a.setTurnState(TurnFailed, result.TurnID)
				} else if result.Status == ResultCanceled {
					a.setTurnState(TurnCanceled, result.TurnID)
				} else {
					a.setTurnState(TurnSucceeded, result.TurnID)
				}
			}
		case EventAgentIdle, "agent_idle":
			a.setTurnState(TurnIdle, ev.TurnID)
		case EventAgentExited, "agent_exited":
			terminal = true
			a.mu.Lock()
			a.status = StatusDone
			a.activity = "done (offline)"
			a.mu.Unlock()
		case "assistant_message", "user_message":
			var text []string
			if c, ok := ev.Data["content"].([]any); ok {
				for _, blk := range c {
					m, _ := blk.(map[string]any)
					if t, _ := m["type"].(string); t == "text" {
						if txt, _ := m["text"].(string); txt != "" {
							text = append(text, txt)
						}
					}
				}
			}
			message := strings.Join(text, "\n")
			if ev.Type == "assistant_message" {
				a.appendAssistantMessage(message)
			} else {
				a.appendUserMessage(message)
			}
		case "stdout":
			if txt, _ := ev.Data["text"].(string); txt != "" {
				a.appendTranscript(txt)
			}
		case "stderr":
			if txt, _ := ev.Data["text"].(string); txt != "" {
				a.appendTranscript("stderr: " + txt)
			}
		case "error":
			if msg, _ := ev.Data["message"].(string); msg != "" {
				a.appendTranscript("error: " + msg)
			}
		case "agent_stopped":
			terminal = true
			reason, _ := ev.Data["reason"].(string)
			a.mu.Lock()
			switch reason {
			case "cancelled":
				a.status = StatusKilled
				a.activity = "cancelled (offline)"
			case "shutdown":
				a.status = StatusDone
				a.activity = "shutdown (offline)"
			case "exit":
				if code, ok := ev.Data["code"].(float64); ok && code != 0 {
					a.status = StatusFailed
					a.activity = fmt.Sprintf("exit %d (offline)", int(code))
				} else {
					a.status = StatusDone
					a.activity = "done (offline)"
				}
			default:
				a.status = StatusDone
				a.activity = "stopped (offline)"
			}
			a.mu.Unlock()
		}
	}
	if !terminal {
		// Non-terminal log means the previous parent died mid-run.
		// Leave status=StatusDetached but record a hint so the
		// dashboard shows something useful.
		a.mu.Lock()
		if a.activity == "detached" && len(a.transcript) > 0 {
			a.activity = "detached (resume to continue)"
		}
		a.mu.Unlock()
	}
}

// Resume re-attaches a Runner to a previously-spawned agent. Durable
// session and event files are kept, while the transient inbox path is
// recalculated for the current runtime environment. Use this to
// continue a subagent session across zot restarts:
//
//	subagentMgr.Reload()
//	a, err := subagentMgr.Resume(ctx, "alpha-12345")
//	subagentMgr.SendUserTurn(a.ID, "where were we?")
//
// The agent must be in a non-running state (Detached, Done, Failed,
// Killed). Resuming a still-running agent returns an error so two
// runners don't race for the same session.
func (f *Supervisor) Resume(ctx context.Context, id string) (*Agent, error) {
	return f.resume(ctx, id, true)
}

// ResumeSession continues the existing child session without replaying the
// original task. It is the explicit canonical spelling for Resume.
func (f *Supervisor) ResumeSession(ctx context.Context, id string) (*Agent, error) {
	return f.resume(ctx, id, true)
}

// RestartTask intentionally runs the stored original task again in a fresh
// worker attempt. Callers should use ResumeSession for normal recovery.
func (f *Supervisor) RestartTask(ctx context.Context, id string) (*Agent, error) {
	return f.resume(ctx, id, false)
}

func (f *Supervisor) resume(ctx context.Context, id string, resuming bool) (*Agent, error) {
	f.operationMu.Lock()
	defer f.operationMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	existing := f.Get(id)
	if existing == nil {
		return nil, fmt.Errorf("subagent: no such agent %q", id)
	}
	existing.mu.Lock()
	st := existing.status
	existing.mu.Unlock()
	if st == StatusRunning || st == StatusPending {
		return nil, fmt.Errorf("subagent: agent %s is still %s; stop it first", existing.ID, st)
	}
	select {
	case <-existing.done:
	default:
		return nil, fmt.Errorf("subagent: agent %s is still finalizing; wait before resuming", existing.ID)
	}
	// A reloaded worker may still be alive even though this supervisor has no
	// in-process Runner (the old host can disappear after the child daemon
	// starts). Fence resume before touching the shared session/worktree: a
	// second worker must never own the same inbox and files concurrently.
	if inboxLive(existing.InboxPath) {
		return nil, fmt.Errorf("subagent: agent %s still has a live worker; send shutdown or stop it first", existing.ID)
	}
	stateDir := existing.stateDirectory(f.cfg.Root)
	lease, err := acquireAgentLease(stateDir)
	if err != nil {
		return nil, fmt.Errorf("subagents resume lease: %w", err)
	}
	leaseOwned := true
	defer func() {
		if leaseOwned {
			_ = lease.Close()
		}
	}()
	existingSnapshot := existing.Snapshot()

	// Rebuild from the meta record so we don't carry stale runner
	// state from a previous incarnation. The inbox is transient and
	// may have been persisted under an incompatible filesystem by an
	// older zot version, so always select it again on resume.
	inboxPath, err := inboxSocketPath(f.cfg.Root, existing.ID)
	if err != nil {
		return nil, fmt.Errorf("subagent inbox path: %w", err)
	}
	fastMode := existing.FastMode
	m := agentMeta{
		ID: existing.ID, Task: existing.Task,
		OriginalTask: existing.OriginalTask,
		Dir:          existing.Dir, RepositoryRoot: existing.RepositoryRoot, Started: existing.Started,
		ParentID: existing.ParentID, BatchID: existing.BatchID, RootSessionID: existing.RootSessionID,
		Model: existing.Model, Provider: existing.Provider,
		BaseURL: existing.BaseURL, InsecureTLS: existing.InsecureTLS,
		Reasoning: existing.Reasoning, FastMode: &fastMode,
		Subagent:      existing.Subagent,
		WorkspaceMode: existing.WorkspaceMode, WorkspacePath: existing.WorkspacePath,
		WorkspaceBase: existing.WorkspaceBase, WorkspaceCapture: existing.WorkspaceCapture,
		MaxTurns: existing.MaxTurns, Timeout: existing.Timeout, Tools: append([]string(nil), existing.Tools...),
		CurrentTurnID: existingSnapshot.CurrentTurnID, Attempt: existingSnapshot.Attempt,
		SessionID: existing.SessionID,
		InboxPath: inboxPath, EventLogPath: existing.EventLogPath,
		SessionPath: existing.SessionPath,
	}

	if len(m.Tools) == 0 && len(f.cfg.Policy.AllowedTools) > 0 {
		m.Tools = append([]string(nil), f.cfg.Policy.AllowedTools...)
	}
	if err := f.validateSpawnOptions(SpawnRequest{MaxTurns: m.MaxTurns, Tools: m.Tools}); err != nil {
		return nil, err
	}
	workspaceMode := m.WorkspaceMode
	if workspaceMode == "" {
		workspaceMode = WorkspaceShared
	}
	if workspaceMode != WorkspaceShared && workspaceMode != WorkspaceWorktree {
		return nil, fmt.Errorf("subagents: unknown workspace mode %q", workspaceMode)
	}
	if m.WorkspaceCapture != "" && m.WorkspaceCapture != CapturePatch && m.WorkspaceCapture != CaptureDiff {
		return nil, fmt.Errorf("subagents: unknown workspace capture mode %q", m.WorkspaceCapture)
	}
	f.mu.Lock()
	allowedRoots := append([]string(nil), f.cfg.Policy.AllowedRoots...)
	f.mu.Unlock()
	repositoryRoot := firstNonEmpty(m.RepositoryRoot, m.Dir, f.cfg.RepoRoot)
	if err := validateWorkspaceRoot(repositoryRoot, workspaceMode, allowedRoots); err != nil {
		return nil, err
	}
	now := f.cfg.Now()
	maxTurns := m.MaxTurns
	if maxTurns <= 0 {
		maxTurns = f.cfg.Policy.MaxTurns
	}
	existingWorkspacePath := ""
	if workspaceMode == WorkspaceWorktree {
		existingWorkspacePath = m.WorkspacePath
	}
	workspace, err := PrepareWorkspace(ctx, WorkspaceRequest{
		Mode:           workspaceMode,
		RepositoryRoot: repositoryRoot,
		StateDir:       stateDir,
		AgentID:        existing.ID,
		Base:           m.WorkspaceBase,
		Capture:        m.WorkspaceCapture,
		AllowedRoots:   allowedRoots,
		ExistingPath:   existingWorkspacePath,
	})
	if err != nil {
		return nil, err
	}
	m.Dir = workspace.Dir()
	m.RepositoryRoot = workspace.RepositoryRoot()
	m.WorkspacePath = workspace.Dir()
	m.WorkspaceMode = workspace.Mode()
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = f.cfg.Policy.DefaultTimeout
	}
	runCtx, cancel := f.workerContext(timeout)
	a := &Agent{
		ID:                m.ID,
		Task:              m.Task,
		OriginalTask:      firstNonEmpty(m.OriginalTask, m.Task),
		Dir:               m.Dir,
		RepositoryRoot:    firstNonEmpty(m.RepositoryRoot, f.cfg.RepoRoot),
		Started:           m.Started,
		ParentID:          m.ParentID,
		BatchID:           m.BatchID,
		RootSessionID:     m.RootSessionID,
		Model:             m.Model,
		Provider:          m.Provider,
		BaseURL:           m.BaseURL,
		InsecureTLS:       m.InsecureTLS,
		Reasoning:         m.Reasoning,
		FastMode:          fastMode,
		Subagent:          m.Subagent,
		SessionID:         m.SessionID,
		WorkspaceMode:     m.WorkspaceMode,
		WorkspacePath:     m.WorkspacePath,
		WorkspaceBase:     m.WorkspaceBase,
		WorkspaceCapture:  m.WorkspaceCapture,
		MaxTurns:          maxTurns,
		Timeout:           timeout,
		Attempt:           m.Attempt,
		HeartbeatInterval: f.cfg.Policy.HeartbeatInterval,
		Tools:             append([]string(nil), m.Tools...),
		InboxPath:         m.InboxPath,
		EventLogPath:      m.EventLogPath,
		SessionPath:       m.SessionPath,
		Resuming:          resuming,
		inbox:             NewInbox(m.InboxPath),
		status:            StatusPending,
		activity:          "resuming",
		processState:      ProcessPending,
		turnState:         TurnQueued,
		updatedAt:         now,
		lastActivity:      now,
		currentTurnID:     m.CurrentTurnID,
		stateDir:          stateDir,
		lease:             lease,
		maxOutputBytes:    f.cfg.Policy.MaxOutputBytes,
		maxOutputLines:    f.cfg.Policy.MaxOutputLines,
		done:              make(chan struct{}),
		turnResults:       make(chan *TurnResult, 16),
	}
	// Carry the previous transcript forward so the dashboard doesn't
	// flash empty between resume and the first new event.
	prev := existing.Transcript()
	if len(prev) > 0 {
		a.appendTranscript(strings.Join(prev, "\n"))
	}
	a.ctx, a.cancel = runCtx, cancel
	a.persistFn = f.persistAgent
	a.workspaceCleanup = func() error { return workspace.Cleanup(context.Background()) }
	a.workspaceCapture = func() (WorkspaceCapture, error) { return workspace.Capture(context.Background()) }
	a.runner = f.cfg.NewRunner(a)
	if err := writeAgentMeta(a.stateDirectory(f.cfg.Root), a); err != nil {
		if a.cancel != nil {
			a.cancel()
		}
		_ = workspace.Cleanup(context.Background())
		return nil, fmt.Errorf("subagents resume metadata: %w", err)
	}

	f.mu.Lock()
	f.agents[a.ID] = a
	f.queue = append(f.queue, a)
	// Keep the agent's slot in f.order; replacing in-place avoids
	// reshuffling the dashboard's row ordering on resume.
	found := false
	for _, k := range f.order {
		if k == a.ID {
			found = true
			break
		}
	}
	if !found {
		f.order = append(f.order, a.ID)
	}
	f.mu.Unlock()
	leaseOwned = false

	// Refreshing metadata happened before queue admission, so a later
	// supervisor can reconstruct this resumed attempt even if the runner
	// has not reached its first event yet.
	f.armQueueTimeout(a)
	f.schedule()
	return a, nil
}
