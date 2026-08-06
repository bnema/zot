package modes

import (
	"testing"
	"time"

	"github.com/bnema/zut/packages/core"
)

func TestEventToJSONActivityLifecycle(t *testing.T) {
	request := EventToJSON(core.EvRequestStarted{
		Provider:    "anthropic",
		Model:       "claude-test",
		Scope:       core.RetryScopeProvider,
		Attempt:     2,
		MaxAttempts: 3,
	})
	if got, want := request["type"], "request_started"; got != want {
		t.Fatalf("type = %q, want %q", got, want)
	}
	for key, want := range map[string]any{
		"provider":     "anthropic",
		"model":        "claude-test",
		"scope":        "provider",
		"attempt":      2,
		"max_attempts": 3,
	} {
		if got := request[key]; got != want {
			t.Errorf("request[%q] = %#v, want %#v", key, got, want)
		}
	}

	retry := EventToJSON(core.EvRetryScheduled{
		Scope: core.RetryScopeAgent, Attempt: 2, MaxAttempts: 4, Delay: 1500 * time.Millisecond,
	})
	if got, want := retry["delay_ms"], int64(1500); got != want {
		t.Fatalf("delay_ms = %#v, want %#v", got, want)
	}
	if _, exists := retry["delay"]; exists {
		t.Fatal("retry event must not serialize a duration")
	}

	tool := EventToJSON(core.EvToolExecutionStarted{ID: "call-1", Name: "read"})
	if got, want := tool["type"], "tool_execution_started"; got != want {
		t.Fatalf("type = %q, want %q", got, want)
	}
	if tool["id"] != "call-1" || tool["name"] != "read" {
		t.Fatalf("tool event = %#v", tool)
	}
}
