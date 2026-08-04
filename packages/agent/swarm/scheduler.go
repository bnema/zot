package swarm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

func (f *Swarm) validateSpawnRequest(req SpawnRequest) error {
	if err := f.validateSpawnOptions(req); err != nil {
		return err
	}
	mode := req.WorkspaceMode
	if mode == "" {
		mode = WorkspaceShared
	}
	if mode != WorkspaceShared && mode != WorkspaceWorktree {
		return fmt.Errorf("subagents: unknown workspace mode %q", mode)
	}
	if req.WorkspaceCapture != "" && req.WorkspaceCapture != CapturePatch && req.WorkspaceCapture != CaptureDiff {
		return fmt.Errorf("subagents: unknown workspace capture mode %q", req.WorkspaceCapture)
	}

	f.mu.Lock()
	root := f.cfg.RepoRoot
	allowedRoots := append([]string(nil), f.cfg.Policy.AllowedRoots...)
	f.mu.Unlock()
	return validateWorkspaceRoot(root, mode, allowedRoots)
}

func (f *Swarm) validateSpawnOptions(req SpawnRequest) error {
	p := f.cfg.Policy
	if req.MaxTurns < 0 || req.MaxTurns > p.MaxTurns {
		return fmt.Errorf("subagents: max_turns must be between 1 and %d", p.MaxTurns)
	}
	if req.Timeout < 0 {
		return errorsInvalid("timeout must not be negative")
	}
	for _, tool := range req.Tools {
		name := strings.TrimSpace(tool)
		if name == "" {
			continue
		}
		if !p.allowedTool(name) {
			return fmt.Errorf("subagents: tool %q is not allowed by policy", name)
		}
	}
	return nil
}

func validateWorkspaceRoot(root string, mode WorkspaceMode, allowedRoots []string) error {
	if root == "" {
		return errorsInvalid("repository root is empty")
	}
	if len(allowedRoots) > 0 {
		ok := false
		for _, allowed := range allowedRoots {
			if pathWithin(root, allowed) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("subagents: repository root is outside allowed roots")
		}
	}
	if mode == WorkspaceWorktree {
		if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
			// Worktrees can be attached to repositories with a .git file or
			// a parent checkout. Let the workspace adapter provide the
			// detailed Git error, but fail obvious non-checkouts here.
			if _, statErr := os.Stat(filepath.Join(root, "HEAD")); statErr != nil {
				return fmt.Errorf("subagents: worktree mode requires a Git checkout: %w", err)
			}
		}
	}
	return nil
}

func errorsInvalid(message string) error { return fmt.Errorf("subagents: %s", message) }

func pathWithin(path, root string) bool {
	pathAbs, err := containmentPath(path)
	if err != nil {
		return false
	}
	rootAbs, err := containmentPath(root)
	if err != nil {
		return false
	}
	pathAbs = filepath.Clean(pathAbs)
	rootAbs = filepath.Clean(rootAbs)
	if pathAbs == rootAbs {
		return true
	}
	prefix := rootAbs + string(filepath.Separator)
	return strings.HasPrefix(pathAbs, prefix)
}

func containmentPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if evaluated, evalErr := filepath.EvalSymlinks(absolute); evalErr == nil {
		return evaluated, nil
	}
	return filepath.Clean(absolute), nil
}

func (f *Swarm) schedule() {
	var starts []*Agent
	f.mu.Lock()
	for len(f.queue) > 0 && f.active < f.cfg.Policy.MaxConcurrent {
		idx := f.nextRunnableIndexLocked()
		if idx < 0 {
			break
		}
		a := f.queue[idx]
		f.queue = append(f.queue[:idx], f.queue[idx+1:]...)

		a.mu.Lock()
		if a.status != StatusPending {
			a.mu.Unlock()
			continue
		}
		a.status = StatusRunning
		a.activity = "starting"
		a.mu.Unlock()
		a.setProcessState(ProcessStarting)
		a.setTurnState(TurnIdle, "")
		a.incrementAttempt()

		f.active++
		key := parentKey(a)
		f.activeByParent[key]++
		if a.BatchID != "" {
			f.activeByBatch[a.BatchID]++
		}
		starts = append(starts, a)
	}
	f.mu.Unlock()

	for _, a := range starts {
		f.persistAgent(a)
		f.startLifecycleMonitor(a)
		go f.run(a)
	}
}

func (f *Swarm) startLifecycleMonitor(a *Agent) {
	if a == nil || f.cfg.Policy.IdleTimeout <= 0 {
		return
	}
	interval := f.cfg.Policy.HeartbeatInterval / 2
	if interval <= 0 || interval > time.Minute {
		interval = time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		heartbeatGrace := f.cfg.Policy.HeartbeatInterval + f.cfg.Policy.ReconnectTimeout
		for {
			select {
			case <-a.done:
				return
			case <-ticker.C:
				if a.ProcessState() != ProcessAlive {
					continue
				}
				last := a.LastActivity()
				if a.protocolReady() && !last.IsZero() && heartbeatGrace > 0 && time.Since(last) >= heartbeatGrace {
					if err := f.Ping(a.ID); err != nil {
						a.setProcessState(ProcessDetached)
						a.setActivity("detached")
						f.persistAgent(a)
						continue
					}
				}
				if a.TurnState() == TurnIdle && !last.IsZero() && time.Since(last) >= f.cfg.Policy.IdleTimeout {
					_ = f.Stop(a.ID)
					return
				}
			}
		}
	}()
}

// Ping asks a live worker for a heartbeat. A detached or terminal worker
// returns the same not-ready error as other inbox operations.
func (f *Swarm) Ping(id string) error {
	a := f.Get(id)
	if a == nil {
		return fmt.Errorf("subagents: no such agent %q", id)
	}
	if a.inbox == nil {
		return fmt.Errorf("subagents: agent %s has no inbox", a.ID)
	}
	return a.inbox.SendCommand(NewCommand(CommandAgentPing, a.ID, a.CurrentTurnID(), AgentPingPayload{}))
}

func (f *Swarm) nextRunnableIndexLocked() int {
	if f.active >= f.cfg.Policy.MaxConcurrent {
		return -1
	}
	for i, a := range f.queue {
		if a == nil {
			continue
		}
		a.mu.Lock()
		status := a.status
		a.mu.Unlock()
		if status != StatusPending {
			continue
		}
		if f.cfg.Policy.MaxConcurrentPerParent > 0 && f.activeByParent[parentKey(a)] >= f.cfg.Policy.MaxConcurrentPerParent {
			continue
		}
		if a.BatchID != "" {
			if batch := f.batches[a.BatchID]; batch != nil && batch.MaxConcurrent > 0 && f.activeByBatch[a.BatchID] >= batch.MaxConcurrent {
				continue
			}
		}
		return i
	}
	return -1
}

func parentKey(a *Agent) string {
	if a == nil || strings.TrimSpace(a.ParentID) == "" {
		return "supervisor"
	}
	return a.ParentID
}

func (f *Swarm) armQueueTimeout(a *Agent) {
	if a == nil {
		return
	}
	if d := f.cfg.Policy.QueueTimeout; d > 0 {
		time.AfterFunc(d, func() { f.expireQueued(a.ID) })
	}
	go func() {
		<-a.ctx.Done()
		f.cancelQueued(a.ID, a.ctx.Err())
	}()
}

func (f *Swarm) expireQueued(id string) { f.cancelQueued(id, context.DeadlineExceeded) }

func (f *Swarm) removeQueued(a *Agent) {
	if a == nil {
		return
	}
	f.mu.Lock()
	for i, queued := range f.queue {
		if queued == a {
			f.queue = append(f.queue[:i], f.queue[i+1:]...)
			break
		}
	}
	f.mu.Unlock()
}

func (f *Swarm) cancelQueued(id string, cause error) {
	a := f.Get(id)
	if a == nil {
		return
	}

	// schedule and Stop use f.mu as the admission lock. Make queued
	// cancellation take the same lock while it changes status and removes the
	// entry, so a timeout/cancel cannot finalize an agent concurrently with
	// scheduler admission.
	f.mu.Lock()
	a.mu.Lock()
	if a.status != StatusPending {
		a.mu.Unlock()
		f.mu.Unlock()
		return
	}
	a.status = StatusFailed
	atomic.CompareAndSwapInt32(&a.launchState, 0, 2)
	if cause == context.Canceled {
		a.activity = "cancelled while queued"
	} else {
		a.activity = "queue timeout"
	}
	a.lastErr = cause
	a.finished = f.cfg.Now()
	for i, queued := range f.queue {
		if queued == a {
			f.queue = append(f.queue[:i], f.queue[i+1:]...)
			break
		}
	}
	a.mu.Unlock()
	f.mu.Unlock()
	a.setProcessState(ProcessExited)
	if cause == context.Canceled {
		a.setTurnState(TurnCanceled, "")
	} else {
		a.setTurnState(TurnFailed, "")
	}
	f.captureWorkspace(a)
	f.ensureResult(a, StatusFailed, cause)
	if a.cancel != nil {
		a.cancel()
	}
	f.persistAgent(a)
	if a.workspaceCleanup != nil {
		_ = a.workspaceCleanup()
	}
	_ = a.releaseLease()
	a.closeDone()
	f.schedule()
}

func (f *Swarm) releaseCapacity(a *Agent) {
	f.mu.Lock()
	if f.active > 0 {
		f.active--
	}
	key := parentKey(a)
	if f.activeByParent[key] > 0 {
		f.activeByParent[key]--
	}
	if a != nil && a.BatchID != "" && f.activeByBatch[a.BatchID] > 0 {
		f.activeByBatch[a.BatchID]--
	}
	f.mu.Unlock()
	f.schedule()
}

func (f *Swarm) persistAgent(a *Agent) {
	if a == nil {
		return
	}
	stateDir := a.stateDirectory(f.cfg.Root)
	if _, err := os.Stat(stateDir); err != nil {
		return
	}
	_ = writeAgentMeta(stateDir, a)
}
