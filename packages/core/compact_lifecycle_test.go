package core

import (
	"context"
	"strings"
	"testing"

	"github.com/bnema/zut/packages/provider"
)

type compactLifecycleClient struct {
	hadLifecycle bool
	req          provider.Request
	summary      string
}

func (c *compactLifecycleClient) Name() string { return "compact-lifecycle" }

func (c *compactLifecycleClient) Stream(_ context.Context, req provider.Request) (<-chan provider.Event, error) {
	c.req = req
	c.hadLifecycle = req.Lifecycle != nil
	if req.Lifecycle != nil {
		req.Lifecycle.RequestAttempt(1, 1)
	}
	summary := c.summary
	if summary == "" {
		summary = "summary"
	}
	out := make(chan provider.Event, 2)
	out <- provider.EventTextDelta{Delta: summary}
	out <- provider.EventDone{Stop: provider.StopEnd, Message: provider.Message{
		Role:    provider.RoleAssistant,
		Content: []provider.Content{provider.TextBlock{Text: summary}},
	}}
	close(out)
	return out, nil
}

func TestCompactWithEventsEmitsRequestLifecycle(t *testing.T) {
	client := &compactLifecycleClient{}
	agent := NewAgent(client, "compact-model", "system", Registry{})
	agent.SetMessages([]provider.Message{{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "source"}},
	}})

	var events []AgentEvent
	summary, err := agent.CompactWithEvents(context.Background(), 0, func(event AgentEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("CompactWithEvents: %v", err)
	}
	if summary != "summary" {
		t.Fatalf("summary = %q, want summary", summary)
	}
	if !client.hadLifecycle {
		t.Fatal("compaction request did not receive a lifecycle observer")
	}

	if len(events) != 4 {
		t.Fatalf("events = %#v, want agent request, provider request, assistant start, text delta", events)
	}
	agentRequest, ok := events[0].(EvRequestStarted)
	if !ok || agentRequest.Scope != RetryScopeAgent || agentRequest.Attempt != 1 || agentRequest.MaxAttempts != 1 {
		t.Fatalf("first event = %#v, want initial agent request", events[0])
	}
	providerRequest, ok := events[1].(EvRequestStarted)
	if !ok || providerRequest.Scope != RetryScopeProvider || providerRequest.Attempt != 1 || providerRequest.MaxAttempts != 1 {
		t.Fatalf("second event = %#v, want initial provider request", events[1])
	}
	if _, ok := events[2].(EvAssistantStart); !ok {
		t.Fatalf("third event = %#v, want assistant start", events[2])
	}
	if delta, ok := events[3].(EvTextDelta); !ok || delta.Delta != "summary" {
		t.Fatalf("fourth event = %#v, want summary text delta", events[3])
	}
}

func TestCompactPromptPreservesActiveInstructions(t *testing.T) {
	const activeInstruction = "delegate independent work to subagents"
	client := &compactLifecycleClient{summary: "Active instruction still in force: " + activeInstruction}
	agent := NewAgent(client, "compact-model", "system", Registry{})
	agent.SetMessages([]provider.Message{
		{
			Role:    provider.RoleUser,
			Content: []provider.Content{provider.TextBlock{Text: "When coding, delegate independent work to subagents."}},
		},
		{
			Role:    provider.RoleAssistant,
			Content: []provider.Content{provider.TextBlock{Text: "I will do that."}},
		},
	})

	if _, err := agent.Compact(context.Background(), 0, nil); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	messages := agent.Messages()
	if len(messages) != 1 {
		t.Fatalf("compacted messages = %d, want 1", len(messages))
	}
	if messages[0].Role != provider.RoleUser {
		t.Fatalf("compacted message role = %q, want user", messages[0].Role)
	}
	if messages[0].Meta["compaction"] != "true" {
		t.Fatalf("compacted message meta = %#v, want compaction=true", messages[0].Meta)
	}
	compacted, ok := messages[0].Content[0].(provider.TextBlock)
	if !ok {
		t.Fatalf("compacted message content = %#v, want TextBlock", messages[0].Content[0])
	}
	if !strings.Contains(compacted.Text, "## Context Summary (compacted)") || !strings.Contains(compacted.Text, activeInstruction) {
		t.Fatalf("compacted transcript did not preserve active instruction:\n%s", compacted.Text)
	}
	if !strings.Contains(client.req.System, "Preserve active user instructions") {
		t.Fatalf("compaction system prompt does not preserve active instructions: %q", client.req.System)
	}
	if len(client.req.Messages) != 1 {
		t.Fatalf("compaction request messages = %d, want 1", len(client.req.Messages))
	}
	text, ok := client.req.Messages[0].Content[0].(provider.TextBlock)
	if !ok {
		t.Fatalf("compaction request content = %#v, want TextBlock", client.req.Messages[0].Content[0])
	}
	for _, want := range []string{"## Active Instructions & Preferences", "delegation/subagent", activeInstruction} {
		if !strings.Contains(text.Text, want) {
			t.Fatalf("compaction prompt missing %q:\n%s", want, text.Text)
		}
	}
}
