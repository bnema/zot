package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patriceckhart/zot/packages/agent/subagents"
	"github.com/patriceckhart/zot/packages/provider"
)

type noopSupervisorRunner struct{}

func (noopSupervisorRunner) Run(context.Context, subagents.Sink) error { return nil }

func newTestSupervisor(t *testing.T) *subagents.Supervisor {
	t.Helper()
	root := t.TempDir()
	manager := subagents.New(subagents.Config{
		Root:     filepath.Join(root, "subagents"),
		RepoRoot: root,
		NewRunner: func(*subagents.Agent) subagents.Runner {
			return noopSupervisorRunner{}
		},
	})
	t.Cleanup(manager.StopAll)
	return manager
}

func TestSubagentSpawnInheritsHostModelAndProviderWhenOmitted(t *testing.T) {
	tool := &SubagentSpawnTool{
		Supervisor:       newTestSupervisor(t),
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

	agents := tool.Supervisor.List()
	if len(agents) != 1 {
		t.Fatalf("spawned agents = %d, want 1", len(agents))
	}
	if agents[0].Model != "gpt-5" || agents[0].Provider != "openai-codex" || agents[0].Reasoning != "medium" {
		t.Fatalf("agent model/provider/reasoning = %q/%q/%q, want gpt-5/openai-codex/medium", agents[0].Model, agents[0].Provider, agents[0].Reasoning)
	}
	if agents[0].MaxTurns != 3 {
		t.Fatalf("omitted max_turns = %d, want default 3", agents[0].MaxTurns)
	}
}

func TestSubagentSpawnDetailsUseEffectiveTimeoutAndTurns(t *testing.T) {
	root := t.TempDir()
	manager := subagents.New(subagents.Config{
		Root:     filepath.Join(root, "subagents"),
		RepoRoot: root,
		Policy: subagents.SubagentPolicy{
			DefaultTimeout: 37 * time.Minute,
			MaxTurns:       7,
		},
		NewRunner: func(*subagents.Agent) subagents.Runner {
			return noopSupervisorRunner{}
		},
	})
	t.Cleanup(manager.StopAll)
	tool := &SubagentSpawnTool{
		Supervisor: manager,
		Enabled:    func() bool { return true },
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
	if got := details["timeout"]; got != (37 * time.Minute).String() {
		t.Fatalf("omitted timeout detail = %v, want %s", got, (37 * time.Minute).String())
	}
	if got := details["max_turns"]; got != 7 {
		t.Fatalf("max_turns detail = %v, want 7", got)
	}
}

func TestSubagentSpawnRejectsExplicitZeroMaxTurns(t *testing.T) {
	tool := &SubagentSpawnTool{
		Supervisor: newTestSupervisor(t),
		Enabled:    func() bool { return true },
	}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"research docs","max_turns":0}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(textResult(res.Content), "max_turns must be positive") {
		t.Fatalf("result = %#v, want max_turns validation error", res)
	}
	if got := len(tool.Supervisor.List()); got != 0 {
		t.Fatalf("spawned agents = %d, want 0", got)
	}
}

func TestSubagentSpawnSelectsProfileAndReasoning(t *testing.T) {
	tool := &SubagentSpawnTool{
		Supervisor: newTestSupervisor(t),
		Enabled:    func() bool { return true },
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
	agents := tool.Supervisor.List()
	if len(agents) != 1 || agents[0].Subagent != "reviewer" || agents[0].Reasoning != "high" {
		t.Fatalf("agent profile/reasoning = %#v", agents)
	}
	if agents[0].Model != "gpt-5.6-luna" || agents[0].Provider != "openai-codex" {
		t.Fatalf("agent model/provider = %q/%q, want gpt-5.6-luna/openai-codex", agents[0].Model, agents[0].Provider)
	}
}

func TestSubagentSpawnAppliesProfileFastModeRestriction(t *testing.T) {
	root := t.TempDir()
	profileFastMode := false
	f := subagents.New(subagents.Config{
		Root:     filepath.Join(root, "subagents"),
		RepoRoot: root,
		FastMode: true,
		NewRunner: func(*subagents.Agent) subagents.Runner {
			return noopSupervisorRunner{}
		},
	})
	t.Cleanup(f.StopAll)
	tool := &SubagentSpawnTool{
		Supervisor: f,
		Enabled:    func() bool { return true },
		ResolveSubagent: func(name string) (*subagents.Profile, error) {
			if name != "reviewer" {
				return nil, nil
			}
			return &subagents.Profile{Name: name, FastMode: &profileFastMode}, nil
		},
	}

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"review auth","agent":"reviewer"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", textResult(res.Content))
	}
	agents := f.List()
	if len(agents) != 1 || agents[0].FastMode {
		t.Fatalf("agent fast mode = %#v, want disabled by profile", agents)
	}
	if got := res.Details.(map[string]any)["fast_mode"]; got != false {
		t.Fatalf("fast_mode detail = %v, want false", got)
	}
}

func TestSubagentSpawnUsesProfileReasoningWhenOmitted(t *testing.T) {
	tool := &SubagentSpawnTool{
		Supervisor: newTestSupervisor(t),
		Enabled:    func() bool { return true },
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

	res, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"review auth","agent":"reviewer"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", textResult(res.Content))
	}
	agents := tool.Supervisor.List()
	if len(agents) != 1 || agents[0].Reasoning != "low" {
		t.Fatalf("agent reasoning = %#v, want low", agents)
	}
}

func TestSubagentSpawnRejectsUnknownProfile(t *testing.T) {
	tool := &SubagentSpawnTool{
		Supervisor: newTestSupervisor(t),
		Enabled:    func() bool { return true },
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
	if got := len(tool.Supervisor.List()); got != 0 {
		t.Fatalf("spawned agents = %d, want 0", got)
	}
}

func TestSubagentSpawnRejectsPartialModelProviderOverride(t *testing.T) {
	tool := &SubagentSpawnTool{
		Supervisor:      newTestSupervisor(t),
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
	if got := len(tool.Supervisor.List()); got != 0 {
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
