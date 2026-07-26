package modes

import (
	"testing"

	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
	"github.com/patriceckhart/zot/packages/tui"
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
