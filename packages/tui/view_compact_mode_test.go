package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bnema/zut/packages/provider"
)

func compactModeUserMsg(text string) provider.Message {
	return provider.Message{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: text}},
	}
}

func countGutterRows(plain string) int {
	n := 0
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "▌") {
			n++
		}
	}
	return n
}

func TestCompactModeDropsUserBubblePadding(t *testing.T) {
	spacious := View{Theme: Dark, Messages: []provider.Message{compactModeUserMsg("hello")}}
	spaciousPlain := stripANSI(strings.Join(spacious.Build(80), "\n"))
	if got := countGutterRows(spaciousPlain); got != 3 {
		t.Fatalf("default user bubble should be 3 gutter rows, got %d:\n%s", got, spaciousPlain)
	}

	compact := View{Theme: Dark, CompactMode: true, Messages: []provider.Message{compactModeUserMsg("hello")}}
	compactPlain := stripANSI(strings.Join(compact.Build(80), "\n"))
	if got := countGutterRows(compactPlain); got != 1 {
		t.Fatalf("compact mode user message should be 1 gutter row, got %d:\n%s", got, compactPlain)
	}
	if !strings.Contains(compactPlain, "▌ hello") {
		t.Fatalf("compact mode lost user text or gutter:\n%s", compactPlain)
	}
}

func TestCompactModeDropsToolBoxBorders(t *testing.T) {
	args := json.RawMessage(`{"command":"echo hi"}`)
	compact := View{
		Theme:       Dark,
		CompactMode: true,
		ToolCalls: []ToolCallView{
			{ID: "toolu_1", Name: "bash", Args: ShortArgs("bash", args), Result: "hi\n"},
		},
	}
	compactPlain := stripANSI(strings.Join(compact.Build(80), "\n"))
	if strings.ContainsAny(compactPlain, "┌└│┐┘") {
		t.Fatalf("compact mode tool render still contains box glyphs:\n%s", compactPlain)
	}
	if !strings.Contains(compactPlain, "bash") || !strings.Contains(compactPlain, "hi") {
		t.Fatalf("compact mode tool render lost header or output:\n%s", compactPlain)
	}

	spacious := View{
		Theme: Dark,
		ToolCalls: []ToolCallView{
			{ID: "toolu_1", Name: "bash", Args: ShortArgs("bash", args), Result: "hi\n"},
		},
	}
	spaciousPlain := stripANSI(strings.Join(spacious.Build(80), "\n"))
	if !strings.ContainsAny(spaciousPlain, "┌└│┐┘") {
		t.Fatalf("default tool render should contain box glyphs:\n%s", spaciousPlain)
	}
}
