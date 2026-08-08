package subagents

import (
	"testing"
	"time"
)

func TestTurnClassificationUsesStableBoundaryFieldsAcrossProtocolVersions(t *testing.T) {
	cases := []struct {
		name      string
		event     Event
		class     turnEventClass
		delegated bool
	}{
		{
			name: "current delegated",
			event: NewEvent(EventTurnStarted, map[string]any{
				"lifetime_turns":    4,
				"current_run_turns": 2,
			}),
			class:     turnEventDelegated,
			delegated: true,
		},
		{
			name: "current nested",
			event: NewEvent(EventTurnStarted, map[string]any{
				"nested_turn": true,
				"step":        3,
			}),
			class: turnEventNested,
		},
		{
			name:      "protocol v1 delegated fallback",
			event:     Event{Type: EventTurnStarted, Version: 1, Time: time.Now()},
			class:     turnEventDelegated,
			delegated: true,
		},
		{
			name:  "protocol v1 nested turn_start",
			event: Event{Type: "turn_start", Version: 1, Time: time.Now()},
			class: turnEventNested,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyTurnEvent(tc.event); got != tc.class {
				t.Fatalf("classifyTurnEvent = %d, want %d", got, tc.class)
			}
			if got := isDelegatedTurnStart(tc.event); got != tc.delegated {
				t.Fatalf("isDelegatedTurnStart = %v, want %v", got, tc.delegated)
			}
		})
	}
}

func TestTurnClassificationKeepsNestedAndDelegatedEndsSeparate(t *testing.T) {
	if isDelegatedTurnEnd(NewEvent("turn_end", map[string]any{"stop": "tool_use"})) {
		t.Fatal("provider turn_end was classified as delegated")
	}
	if !isDelegatedTurnEnd(NewEvent("turn_end", map[string]any{"step": 2})) {
		t.Fatal("worker turn_end was not classified as delegated")
	}
}
