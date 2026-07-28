package agent

import (
	"reflect"
	"testing"

	"github.com/patriceckhart/zot/packages/agent/extproto"
	"github.com/patriceckhart/zot/packages/core"
)

type rememberingDetailedConfirmer struct {
	calls []core.ToolCallConfirmation
}

func (c *rememberingDetailedConfirmer) Confirm(string, string) core.ConfirmDecision {
	panic("Confirm called instead of ConfirmToolCall")
}

func (c *rememberingDetailedConfirmer) ConfirmToolCall(call core.ToolCallConfirmation) core.ConfirmDecision {
	c.calls = append(c.calls, call)
	return core.ConfirmDecision{Allow: true, RememberTool: true}
}

func TestConfirmationEventConfirmerEmitsOnlyWhenPrompting(t *testing.T) {
	inner := &rememberingDetailedConfirmer{}
	var events []extproto.EventFromHost
	notifier := &confirmationEventConfirmer{
		inner: inner,
		emit: func(event extproto.EventFromHost) {
			events = append(events, event)
		},
	}
	gate := core.NewConfirmGate(notifier)
	call := core.ToolCallConfirmation{
		ID:      "call-1",
		Name:    "edit",
		Summary: "main.go",
		Content: "-old\n+new\n",
	}

	if allowed, _, _ := gate.CheckToolCall(call); !allowed {
		t.Fatal("first call should be allowed")
	}
	if allowed, _, _ := gate.CheckToolCall(core.ToolCallConfirmation{Name: "edit", Summary: "other.go"}); !allowed {
		t.Fatal("remembered tool should be allowed")
	}

	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	want := extproto.EventFromHost{
		Event:       "tool_confirmation_requested",
		ToolID:      "call-1",
		ToolName:    "edit",
		ToolPreview: "main.go",
	}
	if !reflect.DeepEqual(events[0], want) {
		t.Fatalf("event = %+v, want %+v", events[0], want)
	}
	if len(inner.calls) != 1 || inner.calls[0] != call {
		t.Fatalf("inner calls = %+v, want [%+v]", inner.calls, call)
	}
}
