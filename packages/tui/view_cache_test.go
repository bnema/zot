package tui

import (
	"strings"
	"testing"

	"github.com/bnema/zut/packages/provider"
)

func TestViewCacheInvalidatesToolOutputExpansionAndWidth(t *testing.T) {
	messages := []provider.Message{
		{
			Role: provider.RoleAssistant,
			Content: []provider.Content{provider.ToolCallBlock{
				ID: "call-1", Name: "bash", Arguments: []byte(`{"command":"cat file"}`),
			}},
		},
		{
			Role: provider.RoleTool,
			Content: []provider.Content{provider.ToolResultBlock{
				CallID:  "call-1",
				Content: []provider.Content{provider.TextBlock{Text: cacheTestResult("old")}},
			}},
		},
	}
	view := &View{Theme: Dark, Messages: messages, MessagesRevision: 1}
	collapsed, _ := view.BuildWithAnchors(40)
	if len(collapsed) == 0 || !strings.Contains(strings.Join(collapsed, "\n"), "more lines") {
		t.Fatal("long tool result did not use the collapsed presentation")
	}

	view.ExpandAll = true
	expanded, _ := view.BuildWithAnchors(40)
	if len(expanded) <= len(collapsed) {
		t.Fatalf("expansion did not invalidate rows: collapsed=%d expanded=%d", len(collapsed), len(expanded))
	}

	wide, _ := view.BuildWithAnchors(120)
	if len(wide) >= len(expanded) {
		// The wide render may have the same row count for this fixture, but it
		// must still be rebuilt for the new wrapping width.
		if strings.Join(wide, "\n") == strings.Join(expanded, "\n") {
			t.Fatal("resize reused rows from the old width")
		}
	}

	view.ExpandAll = false
	view.Messages[1].Content = []provider.Content{provider.ToolResultBlock{
		CallID:  "call-1",
		Content: []provider.Content{provider.TextBlock{Text: cacheTestResult("new marker")}},
	}}
	view.MessagesRevision = 2
	updated, _ := view.BuildWithAnchors(40)
	if !strings.Contains(strings.Join(updated, "\n"), "new marker") {
		t.Fatal("tool output revision reused stale cached rows")
	}
}

func cacheTestResult(prefix string) string {
	var b strings.Builder
	for n := 0; n < 32; n++ {
		b.WriteString(prefix)
		b.WriteString(" line ")
		b.WriteString(string(rune('a' + n%26)))
		b.WriteByte('\n')
	}
	return b.String()
}
