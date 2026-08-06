package modes

import (
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bnema/zut/packages/core"
)

// activityKind describes a user-visible operation. It is deliberately local
// to interactive modes: public consumers receive factual core events instead
// of rendered UI labels.
type activityKind string

const (
	activityIdle                 activityKind = "idle"
	activityPreparingRequest     activityKind = "preparing_request"
	activitySendingRequest       activityKind = "sending_request"
	activityWaitingForResponse   activityKind = "waiting_for_response"
	activityReceivingResponse    activityKind = "receiving_response"
	activityPreparingTool        activityKind = "preparing_tool"
	activityAwaitingConfirmation activityKind = "awaiting_confirmation"
	activityRunningTool          activityKind = "running_tool"
	activitySendingToolResults   activityKind = "sending_tool_results"
	activityRetryingRequest      activityKind = "retrying_request"
	activityCompactingHistory    activityKind = "compacting_history"
	activityCondensingHistory    activityKind = "condensing_history"
	activityRunningShellCommand  activityKind = "running_shell_command"
)

type activity struct {
	kind     activityKind
	provider string
	model    string
	tool     string
	attempt  int
	maxTries int
	retryIn  time.Duration
}

func (a activity) label() string {
	switch a.kind {
	case activityPreparingRequest:
		return "Preparing request"
	case activitySendingRequest:
		return "Sending request to " + activityText(a.provider, "provider")
	case activityWaitingForResponse:
		return "Waiting for " + activityText(a.model, "model") + " to respond"
	case activityReceivingResponse:
		return "Receiving response from " + activityText(a.model, "model")
	case activityPreparingTool:
		return "Preparing tool: " + activityText(a.tool, "tool")
	case activityAwaitingConfirmation:
		return "Waiting for approval: " + activityText(a.tool, "tool")
	case activityRunningTool:
		return "Running tool: " + activityText(a.tool, "tool")
	case activitySendingToolResults:
		return "Sending tool results to " + activityText(a.provider, "provider")
	case activityRetryingRequest:
		if a.attempt <= 0 || a.maxTries <= 0 {
			return "Retrying request"
		}
		return "Retrying request in " + a.retryIn.Round(time.Millisecond).String() + " (attempt " + strconv.Itoa(a.attempt) + "/" + strconv.Itoa(a.maxTries) + ")"
	case activityCompactingHistory:
		return "Compacting conversation history"
	case activityCondensingHistory:
		return "Condensing conversation history"
	case activityRunningShellCommand:
		return "Running shell command"
	default:
		return ""
	}
}

func activityText(value, fallback string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

// agentActivity reduces agent lifecycle facts into one current activity. It
// tracks all pending tool calls so a multi-tool round does not claim results
// are being sent while another tool still needs to run.
type agentActivity struct {
	activity
	pendingToolCalls map[string]string
	toolOrder        []string
}

func newAgentActivity(provider, model string) agentActivity {
	return agentActivity{activity: activity{
		kind:     activityPreparingRequest,
		provider: provider,
		model:    model,
	}}
}

func (a *agentActivity) apply(ev core.AgentEvent) {
	if a == nil {
		return
	}
	switch e := ev.(type) {
	case core.EvTurnStart:
		a.activity.kind = activityPreparingRequest
		a.pendingToolCalls = nil
		a.toolOrder = nil
	case core.EvRequestStarted:
		a.activity.kind = activitySendingRequest
		if e.Provider != "" {
			a.activity.provider = e.Provider
		}
		if e.Model != "" {
			a.activity.model = e.Model
		}
	case core.EvAssistantStart:
		a.activity.kind = activityWaitingForResponse
	case core.EvTextDelta:
		a.activity.kind = activityReceivingResponse
	case core.EvToolUseStart:
		a.activity.kind = activityPreparingTool
		a.activity.tool = e.Name
	case core.EvToolCall:
		if a.pendingToolCalls == nil {
			a.pendingToolCalls = make(map[string]string)
		}
		if _, exists := a.pendingToolCalls[e.ID]; !exists {
			a.toolOrder = append(a.toolOrder, e.ID)
		}
		a.pendingToolCalls[e.ID] = e.Name
		a.activity.kind = activityPreparingTool
		a.activity.tool = e.Name
	case core.EvToolExecutionStarted:
		a.activity.kind = activityRunningTool
		a.activity.tool = e.Name
	case core.EvToolResult:
		if _, exists := a.pendingToolCalls[e.ID]; !exists {
			return
		}
		delete(a.pendingToolCalls, e.ID)
		if len(a.pendingToolCalls) == 0 {
			a.activity.kind = activitySendingToolResults
			a.activity.tool = ""
			return
		}
		for _, id := range a.toolOrder {
			if name, ok := a.pendingToolCalls[id]; ok {
				a.activity.kind = activityPreparingTool
				a.activity.tool = name
				return
			}
		}
	case core.EvRetryScheduled:
		// Agent retries discard the preceding partial assistant message.
		// Its proposed tool calls cannot run, so they must not affect the
		// next successful attempt's batch accounting.
		if e.Scope == core.RetryScopeAgent {
			a.pendingToolCalls = nil
			a.toolOrder = nil
		}
		a.activity.kind = activityRetryingRequest
		a.activity.attempt = e.Attempt
		a.activity.maxTries = e.MaxAttempts
		a.activity.retryIn = e.Delay
	}
}
