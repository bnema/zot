package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/bnema/zut/packages/provider"
)

// boxGlyphs are the runes that only appear when a tool call is drawn
// inside the bordered panel. Flat mode must emit none of them.
const boxGlyphs = "┌└│┐┘"

func assertNoBoxGlyphs(t *testing.T, plain string) {
	t.Helper()
	if strings.ContainsAny(plain, boxGlyphs) {
		t.Fatalf("flat render still contains box glyphs:\n%s", plain)
	}
}

func TestFlatToolRenderDropsBorders(t *testing.T) {
	args := json.RawMessage(`{"command":"echo hi"}`)
	v := View{
		Theme:     Dark,
		FlatTools: true,
		ToolCalls: []ToolCallView{
			{ID: "toolu_1", Name: "bash", Args: ShortArgs("bash", args), Result: "hi\n"},
		},
	}
	plain := stripANSI(strings.Join(v.Build(80), "\n"))
	assertNoBoxGlyphs(t, plain)
	if !strings.Contains(plain, "bash") {
		t.Fatalf("flat header lost the tool name:\n%s", plain)
	}
	if !strings.Contains(plain, "hi") {
		t.Fatalf("flat render lost the output:\n%s", plain)
	}
}

// The box render of the same call still has borders — the toggle is
// what changes, not the data.
func TestBoxToolRenderKeepsBorders(t *testing.T) {
	args := json.RawMessage(`{"command":"echo hi"}`)
	v := View{
		Theme: Dark,
		ToolCalls: []ToolCallView{
			{ID: "toolu_1", Name: "bash", Args: ShortArgs("bash", args), Result: "hi\n"},
		},
	}
	plain := stripANSI(strings.Join(v.Build(80), "\n"))
	if !strings.ContainsAny(plain, boxGlyphs) {
		t.Fatalf("box render should contain border glyphs:\n%s", plain)
	}
}

func TestFlatToolRenderLiveBodyHasNoBorders(t *testing.T) {
	args := json.RawMessage(`{"command":"printf 'start' && sleep 60"}`)
	v := View{
		Theme:     Dark,
		FlatTools: true,
		ToolCalls: []ToolCallView{
			{ID: "toolu_1", Name: "bash", Args: ShortArgs("bash", args), RawJSONBuf: string(args)},
		},
	}
	plain := stripANSI(strings.Join(v.BuildLive(100), "\n"))
	assertNoBoxGlyphs(t, plain)
	if !strings.Contains(plain, "$ printf 'start'") {
		t.Fatalf("flat live body lost the streamed command:\n%s", plain)
	}
}

// Truncation + the ctrl+o expand footer must survive flat mode.
func TestFlatToolRenderKeepsTruncationFooter(t *testing.T) {
	var b strings.Builder
	for i := 0; i < ToolCollapseLines+50; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	args := json.RawMessage(`{"command":"seq 999"}`)
	collapsed := View{
		Theme:     Dark,
		FlatTools: true,
		ToolCalls: []ToolCallView{
			{ID: "toolu_1", Name: "bash", Args: ShortArgs("bash", args), Result: b.String()},
		},
	}
	plain := stripANSI(strings.Join(collapsed.Build(80), "\n"))
	assertNoBoxGlyphs(t, plain)
	if !strings.Contains(plain, "ctrl+o to expand") {
		t.Fatalf("flat render dropped the truncation footer:\n%s", plain)
	}

	// ExpandAll shows everything and still has no borders.
	collapsed.ExpandAll = true
	full := stripANSI(strings.Join(collapsed.Build(80), "\n"))
	assertNoBoxGlyphs(t, full)
	if strings.Contains(full, "ctrl+o to expand") {
		t.Fatalf("expanded flat render should not show the footer:\n%s", full)
	}
	if !strings.Contains(full, fmt.Sprintf("line %d", ToolCollapseLines+40)) {
		t.Fatalf("expanded flat render is missing later lines:\n%s", full)
	}
}

// A finished tool result that comes back as a RoleTool message (the
// transcript path, distinct from the in-flight ToolCalls path) also
// renders flat.
func TestFlatToolRenderTranscriptResult(t *testing.T) {
	callArgs := json.RawMessage(`{"command":"echo done"}`)
	v := View{
		Theme:     Dark,
		FlatTools: true,
		Messages: []provider.Message{
			{
				Role: provider.RoleAssistant,
				Content: []provider.Content{
					provider.ToolCallBlock{ID: "toolu_1", Name: "bash", Arguments: callArgs},
				},
			},
			{
				Role: provider.RoleTool,
				Content: []provider.Content{
					provider.ToolResultBlock{CallID: "toolu_1", Content: []provider.Content{
						provider.TextBlock{Text: "done"},
					}},
				},
			},
		},
	}
	plain := stripANSI(strings.Join(v.Build(80), "\n"))
	assertNoBoxGlyphs(t, plain)
	if !strings.Contains(plain, "bash") || !strings.Contains(plain, "done") {
		t.Fatalf("flat transcript result lost header or output:\n%s", plain)
	}
}
