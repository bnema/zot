package agent

import (
	"testing"

	"github.com/patriceckhart/zot/packages/agent/modes"
	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
)

func TestLiveInteractiveAgentUsesReplacementAgentForSessionResume(t *testing.T) {
	startup := core.NewAgent(nil, "startup-model", "", nil)
	startup.SetMessages([]provider.Message{{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "startup transcript"}},
	}})

	replacement := core.NewAgent(nil, "replacement-model", "", nil)
	iv := modes.NewInteractive(modes.InteractiveConfig{Agent: replacement})

	resumed := []provider.Message{{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "resumed transcript"}},
	}}
	liveInteractiveAgent(iv, startup).SetMessages(resumed)

	if got := firstMessageText(replacement.Messages()); got != "resumed transcript" {
		t.Fatalf("replacement agent transcript = %q, want resumed transcript", got)
	}
	if got := firstMessageText(startup.Messages()); got != "startup transcript" {
		t.Fatalf("startup agent transcript changed to %q", got)
	}
}

func TestLiveInteractiveAgentFallsBackBeforeInteractiveConstruction(t *testing.T) {
	startup := core.NewAgent(nil, "startup-model", "", nil)
	if got := liveInteractiveAgent(nil, startup); got != startup {
		t.Fatalf("fallback agent = %p, want %p", got, startup)
	}
}

func firstMessageText(messages []provider.Message) string {
	if len(messages) == 0 || len(messages[0].Content) == 0 {
		return ""
	}
	text, _ := messages[0].Content[0].(provider.TextBlock)
	return text.Text
}
