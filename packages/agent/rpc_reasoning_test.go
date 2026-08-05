package agent

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bnema/zut/packages/core"
)

func TestRPCSetReasoningMax(t *testing.T) {
	var out bytes.Buffer
	s := &rpcServer{agent: &core.Agent{}, out: &out}
	s.dispatch("set_reasoning", "1", []byte(`{"reasoning":"max"}`))

	if s.agent.Reasoning != "max" {
		t.Fatalf("reasoning = %q, want max", s.agent.Reasoning)
	}
	if !strings.Contains(out.String(), `"reasoning":"max"`) {
		t.Fatalf("response = %q", out.String())
	}
}

func TestRPCSetReasoningRejectsUnknownLevel(t *testing.T) {
	var out bytes.Buffer
	s := &rpcServer{agent: &core.Agent{}, out: &out}
	s.dispatch("set_reasoning", "1", []byte(`{"reasoning":"extreme"}`))

	if s.agent.Reasoning != "" {
		t.Fatalf("reasoning = %q, want unchanged", s.agent.Reasoning)
	}
	if !strings.Contains(out.String(), `"success":false`) {
		t.Fatalf("response = %q", out.String())
	}
}
