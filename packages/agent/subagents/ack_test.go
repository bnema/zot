package subagents

import (
	"testing"
	"time"
)

func TestResumePromptAcknowledgementRequiresDurableUserMessageAndTurnStart(t *testing.T) {
	acceptedAt := time.Now().Add(-time.Second)
	prompt := "follow-up prompt"
	user := NewEvent("user_message", map[string]any{
		"content": []any{map[string]any{"type": "text", "text": prompt}},
	})
	started := NewEvent(EventTurnStarted, map[string]any{
		"lifetime_turns":    2,
		"current_run_turns": 1,
	})

	if resumePromptAcknowledged([]Event{started}, prompt, acceptedAt) {
		t.Fatal("turn.started alone acknowledged a pending prompt")
	}
	if resumePromptAcknowledged([]Event{user}, prompt, acceptedAt) {
		t.Fatal("crash point after durable user message acknowledged before turn.started")
	}
	if !resumePromptAcknowledged([]Event{user, started}, prompt, acceptedAt) {
		t.Fatal("matching durable user message followed by delegated turn.started was not acknowledged")
	}
}

func TestResumePromptAcknowledgementIgnoresNestedTurnStart(t *testing.T) {
	acceptedAt := time.Now().Add(-time.Second)
	prompt := "follow-up prompt"
	user := NewEvent("user_message", map[string]any{
		"content": []any{map[string]any{"type": "text", "text": prompt}},
	})
	nested := NewEvent(EventTurnStarted, map[string]any{"nested_turn": true})
	if resumePromptAcknowledged([]Event{user, nested}, prompt, acceptedAt) {
		t.Fatal("nested turn.started acknowledged a pending delegated prompt")
	}
}
