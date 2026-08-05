package subagents

import (
	"errors"
	"testing"
)

func TestAgentLeaseIsExclusive(t *testing.T) {
	stateDir := t.TempDir()
	first, err := acquireAgentLease(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := acquireAgentLease(stateDir)
	if !errors.Is(err, ErrAgentLeaseHeld) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second lease = %v, want ErrAgentLeaseHeld", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := acquireAgentLease(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}
