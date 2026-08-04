//go:build windows

package swarm

import (
	"errors"

	"golang.org/x/sys/windows"
)

func acquireAgentLease(stateDir string) (*agentLease, error) {
	file, err := openAgentLeaseFile(stateDir)
	if err != nil {
		return nil, err
	}
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, nil); err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
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
	unlockErr := windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, nil)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
