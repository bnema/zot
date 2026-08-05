package modes

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"

	"github.com/patriceckhart/zot/packages/agent/subagents"
	"github.com/patriceckhart/zot/packages/tui"
)

func assertRowsFitWidth(t *testing.T, rows []string, width int) {
	t.Helper()
	for i, row := range rows {
		if got := runewidth.StringWidth(stripANSIBytes(row)); got > width {
			t.Errorf("row %d width = %d, want <= %d: %q", i, got, width, stripANSIBytes(row))
		}
	}
}

func TestBtwDialogWrapsAssistantAndKeepsCursorAligned(t *testing.T) {
	const width = 80
	response := "This response is deliberately long enough to require several wrapped display rows without losing any words."
	d := &btwDialog{
		active:    true,
		lineInput: true,
		turns: []btwTurn{{
			User:      "check wrapping",
			Assistant: response,
		}},
		editor: tui.NewEditor(""),
	}

	rows := d.Render(tui.Theme{}, width)
	assertRowsFitWidth(t, rows, width)
	plain := strings.Join(rows, "\n")
	for _, word := range strings.Fields(response) {
		if !strings.Contains(plain, word) {
			t.Fatalf("wrapped response lost word %q:\n%s", word, plain)
		}
	}

	cursorRow, _ := d.CursorPos(width)
	if want := len(rows) - 3; cursorRow != want {
		t.Fatalf("cursor row = %d, want editor row %d", cursorRow, want)
	}
}

func TestSupervisorTranscriptWrapsAssistantAndDiagnosticRows(t *testing.T) {
	const width = 80
	long := strings.Repeat("long response text ", 8)
	rows := renderSupervisorTranscriptBlocks([]string{
		long,
		"stderr: " + long,
		"error: " + long,
	}, tui.Theme{}, width, false)

	assertRowsFitWidth(t, rows, width)
	plain := strings.Join(rows, "\n")
	for _, want := range []string{"long", "stderr", "✖"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("wrapped transcript missing %q:\n%s", want, plain)
		}
	}
}

func TestSubagentsDialogViewingWrapsSpawnedAgentResponse(t *testing.T) {
	const width = 80
	response := strings.Repeat("spawned agent response ", 6)
	rows := []subagents.AgentSnapshot{{
		ID:     "agent-1",
		Status: subagents.StatusDone,
		Lines:  []string{response},
	}}
	d := newSubagentsDialog()
	d.Open(staticSnapshots(rows...), nil, nil, nil, nil, nil, "")
	_ = d.Render(tui.Theme{}, width)
	d.HandleKey(tui.Key{Kind: tui.KeyEnter})

	rendered := d.Render(tui.Theme{}, width)
	assertRowsFitWidth(t, rendered, width)
	if !strings.Contains(strings.Join(rendered, "\n"), "response") {
		t.Fatal("spawned agent response missing from transcript")
	}
}
