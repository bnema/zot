package agent

import (
	"github.com/bnema/zut/packages/agent/extproto"
	"github.com/bnema/zut/packages/core"
)

// confirmationEventConfirmer decorates an interactive confirmer with an
// extension lifecycle event. ConfirmGate only invokes this decorator when a
// call actually needs user input, after remembered approvals have been checked.
type confirmationEventConfirmer struct {
	inner core.Confirmer
	emit  func(extproto.EventFromHost)
}

func (c *confirmationEventConfirmer) Confirm(toolName, preview string) core.ConfirmDecision {
	call := core.ToolCallConfirmation{Name: toolName, Summary: preview}
	c.emitRequest(call)
	return c.inner.Confirm(toolName, preview)
}

func (c *confirmationEventConfirmer) ConfirmToolCall(call core.ToolCallConfirmation) core.ConfirmDecision {
	c.emitRequest(call)
	if detailed, ok := c.inner.(core.ToolCallConfirmer); ok {
		return detailed.ConfirmToolCall(call)
	}
	return c.inner.Confirm(call.Name, call.Summary)
}

func (c *confirmationEventConfirmer) emitRequest(call core.ToolCallConfirmation) {
	if c.emit == nil {
		return
	}
	c.emit(extproto.EventFromHost{
		Event:       "tool_confirmation_requested",
		ToolID:      call.ID,
		ToolName:    call.Name,
		ToolPreview: call.Summary,
	})
}
