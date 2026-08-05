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
