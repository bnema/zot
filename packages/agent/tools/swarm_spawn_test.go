package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patriceckhart/zot/packages/agent/subagents"
	"github.com/patriceckhart/zot/packages/agent/swarm"
	"github.com/patriceckhart/zot/packages/provider"
)

type noopSwarmRunner struct{}

func (noopSwarmRunner) Run(context.Context, swarm.Sink) error { return nil }

func newTestSwarm(t *testing.T) *swarm.Swarm {
	t.Helper()
	root := t.TempDir()
	return swarm.New(swarm.Config{
		Root:     filepath.Join(root, "swarm"),
		RepoRoot: root,
		NewRunner: func(*swarm.Agent) swarm.Runner {
			return noopSwarmRunner{}
		},
	})
}

func TestSwarmSpawnInheritsHostModelAndProviderWhenOmitted(t *testing.T) {
	tool := &SwarmSpawnTool{
		Swarm:            newTestSwarm(t),
		Enabled:          func() bool { return true },
		DefaultModel:     func() string { return "gpt-5" },
		DefaultProvider:  func() string { return "openai-codex" },
		DefaultReasoning: func() string { return "medium" },
	}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"research docs"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", textResult(res.Content))
	}
	details, ok := res.Details.(map[string]any)
	if !ok {
		t.Fatalf("details type = %T, want map[string]any", res.Details)
	}
	if got := details["model"]; got != "gpt-5" {
		t.Fatalf("model detail = %v, want gpt-5", got)
	}
	if got := details["provider"]; got != "openai-codex" {
		t.Fatalf("provider detail = %v, want openai-codex", got)
	}
	if got := details["reasoning"]; got != "medium" {
		t.Fatalf("reasoning detail = %v, want medium", got)
	}
	text := textResult(res.Content)
	if !strings.Contains(text, "model: gpt-5") || !strings.Contains(text, "provider: openai-codex") || !strings.Contains(text, "reasoning: medium") {
		t.Fatalf("result text missing inherited model/provider:\n%s", text)
	}

	agents := tool.Swarm.List()
	if len(agents) != 1 {
		t.Fatalf("spawned agents = %d, want 1", len(agents))
	}
	if agents[0].Model != "gpt-5" || agents[0].Provider != "openai-codex" || agents[0].Reasoning != "medium" {
		t.Fatalf("agent model/provider/reasoning = %q/%q/%q, want gpt-5/openai-codex/medium", agents[0].Model, agents[0].Provider, agents[0].Reasoning)
	}
}

func TestSwarmSpawnSelectsProfileAndReasoning(t *testing.T) {
	tool := &SwarmSpawnTool{
		Swarm:   newTestSwarm(t),
		Enabled: func() bool { return true },
		ResolveSubagent: func(name string) (*subagents.Profile, error) {
			if name != "reviewer" {
				return nil, nil
			}
			return &subagents.Profile{
				Name:     "reviewer",
				Model:    "openai-codex/gpt-5.6-luna",
				Thinking: "low",
			}, nil
		},
	}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"review auth","agent":"reviewer","reasoning":"high"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", textResult(res.Content))
	}
	text := textResult(res.Content)
	for _, want := range []string{"agent: reviewer", "model: gpt-5.6-luna", "provider: openai-codex", "reasoning: high"} {
		if !strings.Contains(text, want) {
			t.Fatalf("result text missing %q:\n%s", want, text)
		}
	}
	agents := tool.Swarm.List()
	if len(agents) != 1 || agents[0].Subagent != "reviewer" || agents[0].Reasoning != "high" {
		t.Fatalf("agent profile/reasoning = %#v", agents)
	}
}

func TestSwarmSpawnRejectsUnknownProfile(t *testing.T) {
	tool := &SwarmSpawnTool{
		Swarm:   newTestSwarm(t),
		Enabled: func() bool { return true },
		ResolveSubagent: func(string) (*subagents.Profile, error) {
			return nil, nil
		},
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"review auth","agent":"missing"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(textResult(res.Content), "unknown subagent profile") {
		t.Fatalf("result = %#v, want unknown profile error", res)
	}
	if got := len(tool.Swarm.List()); got != 0 {
		t.Fatalf("spawned agents = %d, want 0", got)
	}
}

func TestSwarmSpawnRejectsPartialModelProviderOverride(t *testing.T) {
	tool := &SwarmSpawnTool{
		Swarm:           newTestSwarm(t),
		Enabled:         func() bool { return true },
		DefaultModel:    func() string { return "gpt-5" },
		DefaultProvider: func() string { return "openai-codex" },
	}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"research docs","provider":"openai"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected partial override to fail")
	}
	if got := textResult(res.Content); !strings.Contains(got, "omit both model/provider") {
		t.Fatalf("error text = %q", got)
	}
	if got := len(tool.Swarm.List()); got != 0 {
		t.Fatalf("spawned agents = %d, want 0", got)
	}
}

func textResult(content []provider.Content) string {
	if len(content) == 0 {
		return ""
	}
	if tb, ok := content[0].(provider.TextBlock); ok {
		return tb.Text
	}
	return ""
}
