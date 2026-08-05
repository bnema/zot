//go:build !windows

package subagents

import (
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// configureWorkerProcess makes cancellation target the worker's process
// group rather than only the worker itself. CommandContext calls cmd.Cancel
// from its context watcher, so descendants that inherited the worker's group
// are terminated before execRunner waits for their output pipes to close.
func configureWorkerProcess(cmd *exec.Cmd, pipes ...io.Closer) {
	setProcessGroup(cmd)
	cmd.WaitDelay = workerProcessWaitDelay
	var closePipes sync.Once
	cmd.Cancel = func() error {
		err := killProcessGroup(cmd)
		closePipes.Do(func() { closeWorkerPipes(pipes) })
		return err
	}
}

const workerProcessWaitDelay = 5 * time.Second

// closeWorkerPipes closes the parent-side readers returned by StdoutPipe and
// StderrPipe. Closing these descriptors lets the custom reader goroutines exit
// if a descendant has outlived the worker process.
func closeWorkerPipes(pipes []io.Closer) {
	for _, pipe := range pipes {
		if pipe != nil {
			_ = pipe.Close()
		}
	}
}

// setProcessGroup puts a worker in a process group of its own. This keeps a
// canceled worker from sharing a group with the supervising zut process.
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
