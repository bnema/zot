//go:build windows

package swarm

import (
	"os/exec"
	"strconv"
	"syscall"
)

// configureWorkerProcess makes CommandContext cancellation terminate the
// worker and its descendants on Windows. Windows does not provide a Unix-like
// process-group kill operation, so killProcessGroup uses taskkill's tree mode.
func configureWorkerProcess(cmd *exec.Cmd) {
	setProcessGroup(cmd)
	cmd.Cancel = func() error {
		return killProcessGroup(cmd)
	}
}

// setProcessGroup gives the worker its own Windows process group. The group
// flag prevents it from being attached to the supervisor's console group;
// taskkill /T below is still needed because Windows group creation alone does
// not terminate descendants.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// killProcessGroup terminates the worker process tree. taskkill is part of
// Windows and /T follows the parent-child relationships to include workers
// that spawned their own subprocesses.
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
	return exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}
