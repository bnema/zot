package subagents

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Agent is one supervised task. Public fields are immutable after
// Spawn; mutable state (status, activity, transcript) lives behind
// the embedded mutex.
type turnEndNotice struct {
	step   int
	errMsg string
}

type Agent struct {
	ID      string
	Task    string
	Dir     string // shared checkout or isolated worktree used by the child.
	Started time.Time

	// ParentID and RootSessionID identify the supervisor/session that owns
	// this worker. Empty ParentID means the main supervisor created it;
	// child-originated spawning is rejected in v1.
	ParentID         string
	BatchID          string
	RootSessionID    string
	OriginalTask     string
	RepositoryRoot   string
	WorkspaceMode    WorkspaceMode
	WorkspacePath    string
	WorkspaceBase    string
	WorkspaceCapture CaptureMode
	MaxTurns         int
	// Timeout is the effective per-agent lifetime, persisted so a resumed
	// worker keeps the timeout selected at its spawn boundary.
	Timeout           time.Duration
	HeartbeatInterval time.Duration
	Tools             []string

	// Model and Provider, when non-empty, override the child
	// subprocess's model resolution. BaseURL and InsecureTLS carry
	// the effective provider connection settings from the supervisor.
	// Empty values inherit whatever the child resolves on its own from
	// config / env / flags. All four settings are persisted in meta.json
	// so Resume keeps using the same provider configuration across zut
	// restarts.
	Model       string
	Provider    string
	BaseURL     string
	InsecureTLS bool
	Reasoning   string
	FastMode    bool
	Subagent    string

	// SessionID, when non-empty, scopes the agent to a particular
	// host zut session: the dashboard only surfaces agents whose
	// SessionID matches the active session. Empty means "unscoped" and
	// applies to agents spawned without a session context such as in tests. Set at
	// Spawn time from Supervisor.activeSession and persisted in
	// meta.json so the scope survives restarts.
	SessionID string

	// Resuming is true when this Agent struct was built by Resume
	// rather than Spawn. The runner consults it to decide whether
	// to pass the original Task as a positional argv to the child:
	// on first Spawn we want the child to run the task immediately;
	// on Resume the task was already run last time and replaying it
	// would produce a second turn that collides ("agent busy; send
	// 'cancel' first") with whatever the user types next. Not
	// persisted — every Resume sets it explicitly.
	Resuming bool

	// InboxPath is the unix socket the child agent listens on for
	// follow-up prompts and control messages. The supervisor opens
	// an Inbox at this path; the child opens a Listener.
	InboxPath string

	// Attempt increments whenever the supervisor starts a process for this
	// agent. It is persisted so recovery can distinguish a resumed worker
	// from a first launch.
	Attempt int

	// ProcessPID identifies the current worker when known. It is persisted
	// so a reloaded supervisor can force-kill a detached worker after a
	// graceful shutdown deadline.
	ProcessPID int

	// EventLogPath is the durable JSONL event log for this agent.
	// The runner appends every well-formed event from the child
	// (plus lifecycle events of its own) here. /subagents open in any
	// zut process reads from this file to replay the full history.
	EventLogPath string

	// SessionPath is the child's persistent session file. Surfaced
	// so the dashboard / /subagents open can resume the agent later.
	SessionPath string

	// inbox is the supervisor-side socket handle. Populated by
	// Supervisor.Spawn so callers do not need to manage the socket directly.
	inbox *Inbox

	mu            sync.Mutex
	status        Status
	activity      string
	transcript    []string
	lastAssistant string
	finished      time.Time
	lastErr       error

	lifecycleMu      sync.Mutex
	processState     ProcessState
	turnState        TurnState
	currentTurnID    string
	updatedAt        time.Time
	lastActivity     time.Time
	result           *TurnResult
	resultRef        string
	patchRef         string
	changedFiles     []string
	outputBytes      int
	outputLines      int
	outputTruncated  bool
	maxOutputBytes   int
	maxOutputLines   int
	persistFn        func(*Agent)
	stateDir         string
	workspaceCleanup func() error
	workspaceCapture func() (WorkspaceCapture, error)

	// OnTurnEnd, if set, fires once per prompt-level turn_end event
	// emitted by the subagent daemon wrapper. Provider/tool-loop
	// turn_end events (for example stop=tool_use) are ignored by the
	// runner because they do not mean the sub-agent task finished.
	// Used by auto-subagents watchers to detect that a sub-agent's first
	// (or n-th) task has finished without waiting for the long-lived
	// daemon itself to exit — sub-agents keep running on the inbox
	// even after the initial task completes, so Wait() never unblocks
	// for them.
	OnTurnEnd       func(step int, errMsg string)
	pendingTurnEnds []turnEndNotice

	ctx    context.Context
	cancel context.CancelFunc
	runner Runner
	lease  *agentLease

	// launchState is an admission gate for the scheduler's goroutine:
	// 0=not admitted, 1=runner admitted, 2=stopped before admission. It
	// prevents an immediate Stop from starting a runner after cancellation.
	launchState int32

	// done closes when the run goroutine finalises the agent's
	// status (done / failed / killed). Wait blocks on this so
	// callers don't have to poll.
	done        chan struct{}
	turnResults chan *TurnResult
	doneOnce    sync.Once
}

// Inbox exposes the supervisor-side socket handle. Returns nil for
// agents without inboxes (e.g. tests using a custom Runner that
// doesn't speak the daemon protocol).
func (a *Agent) Inbox() *Inbox { return a.inbox }

// Status returns the current high-level status. Cheap; safe from any goroutine.
func (a *Agent) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// Activity returns the current one-line activity string.
func (a *Agent) Activity() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.activity
}

// ProcessState returns the child process lifecycle state.
func (a *Agent) ProcessState() ProcessState {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	return a.processState
}

// TurnState returns the current delegated-turn state.
func (a *Agent) TurnState() TurnState {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	return a.turnState
}

// CurrentTurnID returns the stable id of the current or most recent turn.
func (a *Agent) CurrentTurnID() string {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	return a.currentTurnID
}

// AttemptValue returns the current process attempt number.
func (a *Agent) AttemptValue() int {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	return a.Attempt
}

// ProcessPIDValue returns the current worker pid, when known.
func (a *Agent) ProcessPIDValue() int {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	return a.ProcessPID
}

// LastActivity returns the last heartbeat or meaningful child event time.
func (a *Agent) LastActivity() time.Time {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	return a.lastActivity
}

// Result returns a copy of the most recent structured turn result.
func (a *Agent) Result() *TurnResult {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	return cloneTurnResult(a.result)
}

func (a *Agent) setProcessState(state ProcessState) {
	a.lifecycleMu.Lock()
	a.processState = state
	a.updatedAt = time.Now()
	a.lifecycleMu.Unlock()
}

func (a *Agent) incrementAttempt() int {
	a.lifecycleMu.Lock()
	a.Attempt++
	a.updatedAt = time.Now()
	attempt := a.Attempt
	a.lifecycleMu.Unlock()
	return attempt
}

func (a *Agent) setProcessPID(pid int) {
	a.lifecycleMu.Lock()
	a.ProcessPID = pid
	a.updatedAt = time.Now()
	a.lifecycleMu.Unlock()
}

func (a *Agent) releaseLease() error {
	a.lifecycleMu.Lock()
	lease := a.lease
	a.lease = nil
	a.lifecycleMu.Unlock()
	if lease == nil {
		return nil
	}
	return lease.Close()
}

func (a *Agent) setTurnState(state TurnState, turnID string) {
	a.lifecycleMu.Lock()
	a.turnState = state
	if turnID != "" {
		a.currentTurnID = turnID
	}
	a.updatedAt = time.Now()
	a.lifecycleMu.Unlock()
}

func (a *Agent) markActivity(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	a.lifecycleMu.Lock()
	a.lastActivity = now
	a.updatedAt = now
	a.lifecycleMu.Unlock()
}

func (a *Agent) setResult(result *TurnResult) {
	a.lifecycleMu.Lock()
	a.result = cloneTurnResult(result)
	a.updatedAt = time.Now()
	a.lifecycleMu.Unlock()
}

// Transcript returns a copy of the running transcript.
func (a *Agent) Transcript() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.transcript))
	copy(out, a.transcript)
	return out
}

// Err returns the runner's terminal error, if any.
func (a *Agent) Err() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastErr
}

// Wait blocks until the agent reaches a terminal state. Used by tests
// and by /subagents wait <id>.
func (a *Agent) Wait() { <-a.done }

// WaitContext waits for the agent to finish or for ctx cancellation.
func (a *Agent) WaitContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-a.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Agent) waitForTurnResult(ctx context.Context) (*TurnResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case result := <-a.turnResults:
		if result == nil {
			return nil, fmt.Errorf("subagents: nil turn result for %s", a.ID)
		}
		return cloneTurnResult(result), nil
	case <-a.done:
		if result := a.Result(); result != nil {
			return result, nil
		}
		return nil, fmt.Errorf("subagents: agent %s exited without a turn result", a.ID)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (a *Agent) closeDone() { a.doneOnce.Do(func() { close(a.done) }) }

func (a *Agent) stateDirectory(defaultRoot string) string {
	if a.stateDir != "" {
		return a.stateDir
	}
	return filepath.Join(defaultRoot, "agents", a.ID)
}

// SetOnTurnEnd installs (or clears, with nil) the callback fired
// from the runner when the child daemon emits a prompt-level
// turn_end event with a step field. Safe to call from any goroutine:
// the runner reads the callback under the same mutex.
func (a *Agent) SetOnTurnEnd(fn func(step int, errMsg string)) {
	a.mu.Lock()
	a.OnTurnEnd = fn
	pending := append([]turnEndNotice(nil), a.pendingTurnEnds...)
	a.pendingTurnEnds = nil
	a.mu.Unlock()
	if fn != nil {
		for _, notice := range pending {
			fn(notice.step, notice.errMsg)
		}
	}
}

func (a *Agent) setActivity(msg string) {
	a.mu.Lock()
	a.activity = strings.TrimSpace(msg)
	a.mu.Unlock()
	a.markActivity(time.Now())
}

func (a *Agent) appendTranscript(chunk string) {
	a.appendTranscriptLocked(chunk, "", false)
}

func (a *Agent) appendUserMessage(text string) {
	a.appendTranscriptLocked(text, "user: ", false)
}

func (a *Agent) appendAssistantMessage(text string) {
	a.appendTranscriptLocked(text, "", true)
}

func (a *Agent) appendTranscriptLocked(chunk, linePrefix string, assistant bool) {
	message := chunk
	chunk = strings.TrimRight(chunk, "\n")
	if chunk == "" {
		return
	}
	a.mu.Lock()
	for _, line := range strings.Split(chunk, "\n") {
		line = linePrefix + line
		a.transcript = append(a.transcript, line)
		a.outputBytes += len(line) + 1
		a.outputLines++
	}
	if assistant {
		a.lastAssistant = boundInlineText(message, a.maxOutputBytes, a.maxOutputLines)
	}
	// Bound the inline dashboard copy. The durable event log and session
	// remain available through the logical history reference.
	lineCap := a.maxOutputLines
	if lineCap <= 0 {
		lineCap = 2_000
	}
	byteCap := a.maxOutputBytes
	if byteCap <= 0 {
		byteCap = 500_000
	}
	for a.outputLines > lineCap {
		line := a.transcript[0]
		a.transcript = a.transcript[1:]
		a.outputBytes -= len(line) + 1
		a.outputLines--
		a.outputTruncated = true
	}
	for a.outputLines > 0 && a.outputBytes > byteCap {
		line := a.transcript[0]
		a.transcript = a.transcript[1:]
		a.outputBytes -= len(line) + 1
		a.outputLines--
		a.outputTruncated = true
	}
	if a.outputLines == 0 {
		a.outputBytes = 0
	}
	a.mu.Unlock()
	a.markActivity(time.Now())
}

// newAgentID returns a short, mostly-collision-free identifier of the
// form "<slug>-<nano>". The slug is derived from the task text so
// dashboards stay readable; the nano suffix guarantees uniqueness
// even when two agents are spawned in the same millisecond.
func newAgentID(task string, now time.Time) string {
	slug := taskSlug(task)
	return fmt.Sprintf("%s-%d", slug, now.UnixNano()%1_000_000)
}

func taskSlug(task string) string {
	task = strings.ToLower(task)
	var b strings.Builder
	dash := false
	for _, r := range task {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
		if b.Len() >= 24 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "agent"
	}
	return out
}
