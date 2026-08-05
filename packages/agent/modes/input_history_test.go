package modes

import (
	"testing"

	"github.com/bnema/zut/packages/core"
	"github.com/bnema/zut/packages/provider"
	"github.com/bnema/zut/packages/tui"
)

func TestInputHistoryUsesUpDown(t *testing.T) {
	ag := core.NewAgent(nil, "", "", nil)
	ag.SetMessages([]provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "first"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "reply"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "second"}}},
	})
	i := &Interactive{
		agent:             ag,
		ed:                tui.NewEditor(""),
		inputHistoryIndex: -1,
	}

	if !i.handleInputHistoryKey(tui.Key{Kind: tui.KeyUp}) {
		t.Fatal("expected Up to enter history")
	}
	if got := i.ed.Value(); got != "second" {
		t.Fatalf("Up loaded %q, want newest history item", got)
	}

	if !i.handleInputHistoryKey(tui.Key{Kind: tui.KeyUp}) {
		t.Fatal("expected repeated Up to stay in history")
	}
	if got := i.ed.Value(); got != "first" {
		t.Fatalf("second Up loaded %q, want older history item", got)
	}

	if !i.handleInputHistoryKey(tui.Key{Kind: tui.KeyDown}) {
		t.Fatal("expected Down to move forward in history")
	}
	if got := i.ed.Value(); got != "second" {
		t.Fatalf("Down loaded %q, want newer history item", got)
	}

	if !i.handleInputHistoryKey(tui.Key{Kind: tui.KeyDown}) {
		t.Fatal("expected Down at newest item to clear editor")
	}
	if got := i.ed.Value(); got != "" {
		t.Fatalf("Down past newest loaded %q, want empty editor", got)
	}
}

func TestInputHistorySkipsShellEscapeContext(t *testing.T) {
	ag := core.NewAgent(nil, "", "", nil)
	ag.SetMessages([]provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "prompt"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "$ pwd\n\n/tmp\n\n[exit 0]"}}, Meta: map[string]string{shellEscapeMetaKey: "true"}},
	})
	i := &Interactive{agent: ag}

	history := i.inputHistory()
	if len(history) != 1 || history[0] != "prompt" {
		t.Fatalf("inputHistory() = %q, want [prompt]", history)
	}
}

func TestInputHistorySkipsCompactionContext(t *testing.T) {
	ag := core.NewAgent(nil, "", "", nil)
	ag.SetMessages([]provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "prompt"}}},
		{Role: provider.RoleUser, Meta: map[string]string{"compaction": "true"}, Content: []provider.Content{provider.TextBlock{Text: "## Context Summary (compacted)"}}},
		{Role: provider.RoleUser, Meta: map[string]string{autoCompactContinueMetaKey: "true"}, Content: []provider.Content{provider.TextBlock{Text: "resume internally"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "new prompt"}}},
	})
	i := &Interactive{agent: ag}

	history := i.inputHistory()
	if len(history) != 2 || history[0] != "prompt" || history[1] != "new prompt" {
		t.Fatalf("inputHistory() = %q, want [prompt new prompt]", history)
	}
}

func TestJumpTargetsSkipInternalUserContext(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "prompt"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.ToolCallBlock{ID: "call-1", Name: "read"}}},
		{Role: provider.RoleTool, Content: []provider.Content{provider.ToolResultBlock{CallID: "call-1"}}},
		{Role: provider.RoleUser, Meta: map[string]string{"compaction": "true"}, Content: []provider.Content{provider.TextBlock{Text: "## Context Summary (compacted)"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "new prompt"}}},
	}

	targets := buildJumpTargets(msgs)
	if len(targets) != 2 {
		t.Fatalf("jump target count = %d, want 2", len(targets))
	}
	if targets[0].TurnNo != 1 || targets[0].ToolCount != 1 || targets[1].TurnNo != 2 {
		t.Fatalf("jump targets = %+v, want real turns and tool count", targets)
	}
}

func TestInputHistoryNoLongerUsesLeftRight(t *testing.T) {
	ag := core.NewAgent(nil, "", "", nil)
	ag.SetMessages([]provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "prompt"}}},
	})
	i := &Interactive{
		agent:             ag,
		ed:                tui.NewEditor(""),
		inputHistoryIndex: -1,
	}

	if i.handleInputHistoryKey(tui.Key{Kind: tui.KeyLeft}) {
		t.Fatal("Left should not browse input history")
	}
	if i.handleInputHistoryKey(tui.Key{Kind: tui.KeyRight}) {
		t.Fatal("Right should not browse input history")
	}
	if got := i.ed.Value(); got != "" {
		t.Fatalf("left/right changed editor to %q", got)
	}
}

func TestInputHistoryDoesNotStealNonEmptyEditor(t *testing.T) {
	ag := core.NewAgent(nil, "", "", nil)
	ag.SetMessages([]provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "prompt"}}},
	})
	i := &Interactive{
		agent:             ag,
		ed:                tui.NewEditor(""),
		inputHistoryIndex: -1,
	}
	i.ed.SetValue("draft")

	if i.handleInputHistoryKey(tui.Key{Kind: tui.KeyUp}) {
		t.Fatal("Up should not browse history while editing a draft")
	}
	if got := i.ed.Value(); got != "draft" {
		t.Fatalf("editor changed to %q, want draft", got)
	}
}
