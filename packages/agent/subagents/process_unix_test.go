//go:build !windows

package subagents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWorkerCancellationKillsProcessGroup(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is not available")
	}

	pidPath := t.TempDir() + "/child.pid"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, shell, "-c", fmt.Sprintf("sleep 30 & echo $! > %q; wait", pidPath))
	configureWorkerProcess(cmd)
	if cmd.WaitDelay != workerProcessWaitDelay {
		t.Fatalf("WaitDelay = %s, want %s", cmd.WaitDelay, workerProcessWaitDelay)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	childPID := waitForWorkerChildPID(t, pidPath)
	cancel()

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not exit after context cancellation")
	case err := <-waitDone:
		if err == nil {
			t.Fatal("worker exited successfully after context cancellation")
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && processExists(childPID) {
		time.Sleep(10 * time.Millisecond)
	}
	if processExists(childPID) {
		t.Fatalf("worker descendant pid %d survived process-group cancellation", childPID)
	}
}

func TestWorkerCancellationClosesOutputPipes(t *testing.T) {
	cmd := exec.Command("sh")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	defer stderr.Close()

	configureWorkerProcess(cmd)
	if err := cmd.Cancel(); err != nil {
		t.Fatal(err)
	}
	for name, pipe := range map[string]io.Reader{"stdout": stdout, "stderr": stderr} {
		data := make([]byte, 1)
		if _, err := pipe.Read(data); err != io.EOF {
			t.Errorf("%s read error = %v, want EOF", name, err)
		}
	}
}

func waitForWorkerChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for child pid in %s", path)
	return 0
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
