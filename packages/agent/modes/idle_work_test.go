package modes

import "testing"

func TestDeferUntilIdleRunsImmediatelyWhenIdle(t *testing.T) {
	interactive := &Interactive{}
	ran := false
	interactive.DeferUntilIdle(func() { ran = true })
	if !ran {
		t.Fatal("idle callback did not run immediately")
	}
}

func TestDeferUntilIdleQueuesWhileBusy(t *testing.T) {
	interactive := &Interactive{}
	interactive.busy = true
	ran := false
	interactive.DeferUntilIdle(func() { ran = true })
	if ran {
		t.Fatal("busy callback ran before the operation became idle")
	}

	interactive.mu.Lock()
	interactive.busy = false
	work := interactive.takePendingIdleWorkLocked()
	interactive.mu.Unlock()
	runPendingIdleWork(work)
	if !ran {
		t.Fatal("queued idle callback did not run after the operation became idle")
	}
}
