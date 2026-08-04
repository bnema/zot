//go:build !windows

package swarm

import (
	"os/exec"
	"syscall"
)

// configureWorkerProcess makes cancellation target the worker's process
// group rather than only the worker itself. CommandContext calls cmd.Cancel
// from its context watcher, so descendants that inherited the worker's group
// are terminated before execRunner waits for their output pipes to close.
func configureWorkerProcess(cmd *exec.Cmd) {
	setProcessGroup(cmd)
	cmd.Cancel = func() error {
		return killProcessGroup(cmd)
	}
}

// setProcessGroup puts a worker in a process group of its own. This keeps a
// canceled worker from sharing a group with the supervising zot process.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup forcefully terminates the worker group. Cancellation is a
// forceful path: the supervisor already offers a graceful shutdown through
// the worker inbox, while SIGKILL ensures a descendant cannot keep the
// worker's stdout or stderr pipe open indefinitely.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return forceKillProcess(cmd.Process.Pid)
}

func forceKillProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	// With Setpgid, the worker's PID is also the process-group ID. A negative
	// PID addresses the whole group rather than just the worker process.
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}
