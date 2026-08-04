//go:build !windows

package swarm

import (
	"errors"
	"syscall"
)

func acquireAgentLease(stateDir string) (*agentLease, error) {
	file, err := openAgentLeaseFile(stateDir)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrAgentLeaseHeld
		}
		return nil, err
	}
	return &agentLease{file: file}, nil
}

func (l *agentLease) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
