package sdk

import (
	"testing"

	"github.com/bnema/zut/packages/core"
)

func TestRuntimeSetReasoningMax(t *testing.T) {
	r := &Runtime{agent: &core.Agent{}}
	if err := r.SetReasoning("max"); err != nil {
		t.Fatal(err)
	}
	if r.agent.Reasoning != "max" {
		t.Fatalf("reasoning = %q, want max", r.agent.Reasoning)
	}
}

func TestRuntimeSetReasoningRejectsUnknownLevel(t *testing.T) {
	r := &Runtime{agent: &core.Agent{}}
	if err := r.SetReasoning("extreme"); err == nil {
		t.Fatal("expected invalid reasoning error")
	}
}

func TestNewRequiresExplicitWebSearchTool(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "test-key")

	withoutWeb, err := New(Config{Provider: "openai", Model: "gpt-5", Tools: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	defer withoutWeb.Close()
	if _, ok := withoutWeb.agent.ToolsSnapshot()["web_search"]; ok {
		t.Fatal("SDK registry inherited web_search without explicit Config.Tools opt-in")
	}

	withWeb, err := New(Config{Provider: "openai", Model: "gpt-5", Tools: []string{"web_search"}})
	if err != nil {
		t.Fatal(err)
	}
	defer withWeb.Close()
	if _, ok := withWeb.agent.ToolsSnapshot()["web_search"]; !ok {
		t.Fatal("SDK registry omitted explicitly requested web_search")
	}
}
