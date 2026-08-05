package agent

import (
	"testing"

	"github.com/bnema/zut/packages/agent/extproto"
	"github.com/bnema/zut/packages/agent/modes"
)

func TestInteractiveExtHooksBufferStartupChrome(t *testing.T) {
	hooks := &interactiveExtHooks{}
	hooks.SetStatus("tasked-phases", "progress", "success", "2/4")
	hooks.SetWidget("tasked-phases", "plan", "above_input", "Plan", []string{"one"})
	hooks.SetWidget("tasked-phases", "cleared", "above_input", "old", []string{"old"})
	hooks.ClearWidget("tasked-phases", "cleared")
	hooks.SetStatus("tasked-phases", "removed", "info", "old")
	hooks.SetStatus("tasked-phases", "removed", "info", "")

	hooks.mu.Lock()
	if len(hooks.pendingStatuses) != 1 || len(hooks.pendingWidgets) != 1 {
		t.Fatalf("pending chrome = statuses %d widgets %d, want one each", len(hooks.pendingStatuses), len(hooks.pendingWidgets))
	}
	hooks.mu.Unlock()

	hooks.attachInteractive(&modes.Interactive{})
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if len(hooks.pendingStatuses) != 0 || len(hooks.pendingWidgets) != 0 {
		t.Fatalf("pending chrome was not flushed: statuses=%v widgets=%v", hooks.pendingStatuses, hooks.pendingWidgets)
	}
}

func TestInteractiveExtHooksBufferStartupAlerts(t *testing.T) {
	hooks := &interactiveExtHooks{}
	hooks.Alert("question-ext", extproto.AlertRequest{Kind: extproto.AlertKindBell, Reason: "question_ready"})
	for n := 0; n < maxBufferedInteractiveAlerts+8; n++ {
		hooks.Alert("question-ext", extproto.AlertRequest{Kind: extproto.AlertKindBell, Reason: "flood"})
	}

	hooks.mu.Lock()
	pending := len(hooks.pendingAlerts)
	hooks.mu.Unlock()
	if pending != maxBufferedInteractiveAlerts {
		t.Fatalf("pending startup alerts = %d, want bounded capacity %d", pending, maxBufferedInteractiveAlerts)
	}

	iv := &modes.Interactive{}
	hooks.attachInteractive(iv)
	hooks.mu.Lock()
	pending = len(hooks.pendingAlerts)
	attached := hooks.interactive == iv
	hooks.mu.Unlock()
	if pending != 0 || !attached {
		t.Fatalf("after attach: pending=%d attached=%v, want no pending alert and attached host", pending, attached)
	}
}
