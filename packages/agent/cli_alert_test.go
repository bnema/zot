package agent

import (
	"testing"

	"github.com/patriceckhart/zot/packages/agent/extproto"
	"github.com/patriceckhart/zot/packages/agent/modes"
)

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
