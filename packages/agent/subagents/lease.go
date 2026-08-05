package subagents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrAgentLeaseHeld means another supervisor currently owns an agent's
// durable state. The lease is advisory; socket probing remains the fence for
// workers that are still live.
var ErrAgentLeaseHeld = errors.New("subagents: agent state is owned by another supervisor")

type agentLease struct {
	file *os.File
}

func openAgentLeaseFile(stateDir string) (*os.File, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("subagents lease directory: %w", err)
	}
	path := filepath.Join(stateDir, "owner.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("subagents lease open: %w", err)
	}
	_ = file.Chmod(0o600)
	return file, nil
}
