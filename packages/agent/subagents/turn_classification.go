package subagents

import "fmt"

// turnEventClass identifies the boundary that owns a turn event. The worker
// daemon's turn.started event is the delegated message boundary; provider
// turn_start events are nested model-loop activity. New workers carry stable
// lifetime/current counters on the delegated boundary and nested_turn on the
// provider boundary. The fallbacks preserve protocol-v1 logs, which used
// turn.started without counters for delegated turns and turn_start for nested
// turns.
type turnEventClass uint8

const (
	turnEventUnknown turnEventClass = iota
	turnEventDelegated
	turnEventNested
)

func classifyTurnEvent(ev Event) turnEventClass {
	if nested, ok := ev.Data["nested_turn"].(bool); ok && nested {
		return turnEventNested
	}
	switch ev.Type {
	case "turn_start":
		return turnEventNested
	case EventTurnStarted:
		if _, lifetimeOK := eventCounter(ev.Data, "lifetime_turns"); lifetimeOK {
			if _, currentOK := eventCounter(ev.Data, "current_run_turns"); currentOK {
				return turnEventDelegated
			}
		}
		// v1 turn.started without counters is the delegated boundary. A
		// current worker marks nested canonical events explicitly above.
		return turnEventDelegated
	case "turn_end":
		if _, ok := eventCounter(ev.Data, "step"); ok {
			return turnEventDelegated
		}
		return turnEventNested
	}
	return turnEventUnknown
}

func isDelegatedTurnStart(ev Event) bool {
	return ev.Type == EventTurnStarted && classifyTurnEvent(ev) == turnEventDelegated
}

// IsDelegatedTurnStart is shared with the worker's replay bootstrap so live,
// replayed, and protocol-v1 event handling use the same boundary rules.
func IsDelegatedTurnStart(ev Event) bool {
	return isDelegatedTurnStart(ev)
}

func isDelegatedTurnEnd(ev Event) bool {
	return ev.Type == "turn_end" && classifyTurnEvent(ev) == turnEventDelegated
}

func eventTurnID(ev Event) string {
	if ev.TurnID != "" {
		return ev.TurnID
	}
	if step, ok := eventCounter(ev.Data, "step"); ok {
		return fmt.Sprintf("turn-%d", step)
	}
	return ""
}

func userMessageText(ev Event) string {
	if ev.Type != "user_message" {
		return ""
	}
	content := ev.Data["content"]
	var text string
	appendBlock := func(fields map[string]any) {
		if typ, _ := fields["type"].(string); typ == "text" {
			if value, _ := fields["text"].(string); value != "" {
				if text != "" {
					text += "\n"
				}
				text += value
			}
		}
	}
	switch blocks := content.(type) {
	case []any:
		for _, block := range blocks {
			if fields, ok := block.(map[string]any); ok {
				appendBlock(fields)
			}
		}
	case []map[string]any:
		for _, fields := range blocks {
			appendBlock(fields)
		}
	}
	return text
}
