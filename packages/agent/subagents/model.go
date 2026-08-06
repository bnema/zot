package subagents

import "time"

// ProcessState describes the lifetime of the supervised child process. It is
// intentionally independent from TurnState: an alive worker can be idle
// between turns, and a disconnected supervisor does not imply that the
// worker's current turn failed.
type ProcessState string

const (
	ProcessPending  ProcessState = "pending"
	ProcessStarting ProcessState = "starting"
	ProcessAlive    ProcessState = "alive"
	ProcessDetached ProcessState = "detached"
	ProcessExited   ProcessState = "exited"
	ProcessKilled   ProcessState = "killed"
)

// TurnState describes one delegated prompt inside a worker process.
type TurnState string

const (
	TurnIdle      TurnState = "idle"
	TurnQueued    TurnState = "queued"
	TurnRunning   TurnState = "running"
	TurnCanceling TurnState = "canceling"
	TurnSucceeded TurnState = "succeeded"
	TurnFailed    TurnState = "failed"
	TurnCanceled  TurnState = "canceled"
)

// WorkspaceMode selects how a child is allowed to access the repository.
type WorkspaceMode string

const (
	WorkspaceShared   WorkspaceMode = "shared"
	WorkspaceWorktree WorkspaceMode = "worktree"
)

// CaptureMode controls which durable worktree output is collected.
type CaptureMode string

const (
	CapturePatch CaptureMode = "patch"
	CaptureDiff  CaptureMode = "diff"
)

// SubagentPolicy contains manager-owned safety and resource limits. normalize
// replaces zero MaxOutputBytes, MaxOutputLines, QueueTimeout, and
// DefaultTimeout values with positive manager defaults. Other zero values retain
// their documented semantics.
type SubagentPolicy struct {
	MaxConcurrent          int
	MaxConcurrentPerParent int
	QueueTimeout           time.Duration
	DefaultTimeout         time.Duration
	MaxTurns               int
	MaxOutputBytes         int
	MaxOutputLines         int
	AllowedTools           []string
	AllowedRoots           []string
	HeartbeatInterval      time.Duration
	IdleTimeout            time.Duration
	ReconnectTimeout       time.Duration
	CancelGracePeriod      time.Duration
}

func (p *SubagentPolicy) normalize() {
	if p.MaxConcurrent <= 0 {
		p.MaxConcurrent = 8
	}
	if p.MaxConcurrentPerParent <= 0 {
		p.MaxConcurrentPerParent = 4
	}
	if p.QueueTimeout <= 0 {
		p.QueueTimeout = 30 * time.Minute
	}
	if p.DefaultTimeout <= 0 {
		p.DefaultTimeout = 20 * time.Minute
	}
	if p.MaxTurns <= 0 {
		p.MaxTurns = 3
	}
	if p.MaxOutputBytes <= 0 {
		p.MaxOutputBytes = 500_000
	}
	if p.MaxOutputLines <= 0 {
		p.MaxOutputLines = 5_000
	}
	if p.HeartbeatInterval <= 0 {
		p.HeartbeatInterval = 10 * time.Second
	}
	if p.IdleTimeout <= 0 {
		p.IdleTimeout = 7 * time.Minute
	}
	if p.ReconnectTimeout <= 0 {
		p.ReconnectTimeout = 5 * time.Second
	}
	if p.CancelGracePeriod <= 0 {
		p.CancelGracePeriod = 10 * time.Second
	}
}

func (p SubagentPolicy) allowedTool(name string) bool {
	if len(p.AllowedTools) == 0 {
		return true
	}
	for _, allowed := range p.AllowedTools {
		if allowed == name {
			return true
		}
	}
	return false
}
