package modes

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/patriceckhart/zot/packages/tui"
)

func newNotesTestInteractive() *Interactive {
	i := &Interactive{dirty: make(chan struct{}, 1)}
	i.cfg.Theme = tui.Theme{
		Muted: tui.Color256(8), Warning: tui.Color256(3), Error: tui.Color256(1),
		Tool: tui.Color256(2), Accent: tui.Color256(4),
	}
	return i
}

func TestClearNotesRemovesOnlyOwnerNotes(t *testing.T) {
	i := newNotesTestInteractive()

	i.Notify("kagi", "info", "pending")
	i.Notify("kagi", "success", "approved")
	i.Notify("other", "info", "keep me")

	if len(i.extNotes) != 3 {
		t.Fatalf("expected 3 notes, got %d", len(i.extNotes))
	}

	i.ClearNotes("kagi")

	if len(i.extNotes) != 1 {
		t.Fatalf("expected 1 note after clear, got %d: %v", len(i.extNotes), i.extNotes)
	}
	if !strings.Contains(i.extNotes[0], "[other] ") {
		t.Fatalf("expected the surviving note to belong to other, got %q", i.extNotes[0])
	}
}

func TestClearNotesNoMatchKeepsNotes(t *testing.T) {
	i := newNotesTestInteractive()
	i.Notify("kagi", "info", "pending")

	i.ClearNotes("nope")

	if len(i.extNotes) != 1 {
		t.Fatalf("expected note to survive, got %d", len(i.extNotes))
	}
}

func TestPersistentExtensionWidgetRowsAreBounded(t *testing.T) {
	i := newNotesTestInteractive()
	lines := make([]string, 20)
	for idx := range lines {
		lines[idx] = "line"
	}
	i.SetWidget("extension", "widget", "above_input", "Title", lines)

	i.mu.Lock()
	got := i.extensionChromeLinesLocked(80)
	i.mu.Unlock()
	if len(got) != maxExtensionWidgetRows {
		t.Fatalf("widget rows = %d, want %d: %v", len(got), maxExtensionWidgetRows, got)
	}
	if !strings.Contains(strings.Join(got, "\n"), "extension widgets truncated") {
		t.Fatalf("bounded widget rows omitted truncation marker: %v", got)
	}
}

func TestPersistentExtensionStatusRowsAreBounded(t *testing.T) {
	i := newNotesTestInteractive()
	for n := 0; n < maxExtensionStatusRows+4; n++ {
		i.SetStatus("extension", fmt.Sprintf("status-%d", n), "info", "line")
	}

	i.mu.Lock()
	got := i.extensionChromeLinesLocked(80)
	i.mu.Unlock()
	if len(got) != maxExtensionStatusRows {
		t.Fatalf("status rows = %d, want %d: %v", len(got), maxExtensionStatusRows, got)
	}
	if !strings.Contains(strings.Join(got, "\n"), "extension statuses truncated") {
		t.Fatalf("bounded status rows omitted truncation marker: %v", got)
	}
}

func TestNarrowRightBarFallbackCapsCombinedExtensionChrome(t *testing.T) {
	i := newNotesTestInteractive()
	for n := 0; n < 3; n++ {
		lines := make([]string, 4)
		for row := range lines {
			lines[row] = fmt.Sprintf("extension-%d-line-%d", n, row)
		}
		i.SetWidget(fmt.Sprintf("extension-%d", n), "plan", "right_bar", "Plan", lines)
	}
	i.SetStatus("status-extension", "progress", "info", "still visible")

	i.mu.Lock()
	got := i.extensionChromeLinesForLayoutLocked(72, false, true)
	i.mu.Unlock()
	if len(got) != maxNarrowExtensionChromeRows {
		t.Fatalf("narrow fallback rows = %d, want %d: %v", len(got), maxNarrowExtensionChromeRows, got)
	}
	if !strings.Contains(strings.Join(got, "\n"), "extension chrome truncated") {
		t.Fatalf("narrow fallback omitted truncation marker: %v", got)
	}
}

func TestPersistentExtensionStatusAndWidget(t *testing.T) {
	i := newNotesTestInteractive()
	i.SetStatus("tasked-phases", "progress", "success", "2/4 tasks checked")
	i.SetWidget("tasked-phases", "plan", "above_input", "Tasked phases", []string{"Current phase: parse", "[ ] read files"})

	i.mu.Lock()
	lines := i.extensionChromeLinesLocked(80)
	statusKey := i.extensionStatusesKeyLocked()
	widgetKey := i.extensionWidgetsKeyLocked()
	i.mu.Unlock()
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "2/4 tasks checked") || !strings.Contains(joined, "Tasked phases") || !strings.Contains(joined, "[ ] read files") {
		t.Fatalf("persistent extension chrome = %q", joined)
	}
	if !strings.Contains(statusKey, "tasked-phases/progress/success/2/4 tasks checked") {
		t.Fatalf("status cache key = %q", statusKey)
	}
	if !strings.Contains(widgetKey, "tasked-phases/plan/above_input/Tasked phases") {
		t.Fatalf("widget cache key = %q", widgetKey)
	}

	i.SetStatus("tasked-phases", "progress", "", "")
	i.ClearWidget("tasked-phases", "plan")
	if len(i.extStatuses) != 0 || len(i.extWidgets) != 0 {
		t.Fatalf("persistent extension chrome was not cleared: statuses=%v widgets=%v", i.extStatuses, i.extWidgets)
	}
}

func TestRightBarWidgetsAreDeterministicAndFallbackAboveInput(t *testing.T) {
	i := newNotesTestInteractive()
	i.SetWidget("zeta", "plan", "right_bar", "Zeta", []string{"zeta line"})
	i.SetWidget("alpha", "second", "right_bar", "Second", []string{"second line"})
	i.SetWidget("alpha", "first", "right_bar", "First", []string{"first line"})
	i.SetWidget("legacy", "plan", "above_input", "Legacy", []string{"legacy line"})

	i.mu.Lock()
	widgets := i.rightBarWidgetsLocked()
	wideAbove := i.extensionChromeLinesAtLocked(80, true)
	narrowFallback := i.extensionChromeLinesAtLocked(72, false)
	i.mu.Unlock()

	if len(widgets) != 3 || widgets[0].Extension != "alpha" || widgets[0].ID != "first" || widgets[1].ID != "second" || widgets[2].Extension != "zeta" {
		t.Fatalf("right-bar ordering = %+v", widgets)
	}
	wideText := strings.Join(wideAbove, "\n")
	if strings.Contains(wideText, "Zeta") || strings.Contains(wideText, "Second") || strings.Contains(wideText, "First") {
		t.Fatalf("right-bar widgets leaked into wide above-input chrome: %q", wideText)
	}
	fallbackText := strings.Join(narrowFallback, "\n")
	if !strings.Contains(fallbackText, "Zeta") || !strings.Contains(fallbackText, "First") || !strings.Contains(fallbackText, "Legacy") {
		t.Fatalf("narrow fallback omitted widgets: %q", fallbackText)
	}
}

func TestClearExtensionChromeRemovesOnlyTheExitedExtension(t *testing.T) {
	i := newNotesTestInteractive()
	i.SetStatus("gone", "progress", "success", "done")
	i.SetWidget("gone", "plan", "right_bar", "Gone", []string{"old"})
	i.SetStatus("keep", "progress", "info", "still here")
	i.SetWidget("keep", "plan", "right_bar", "Keep", []string{"new"})
	i.extNotes = []string{"[gone] old note", "[keep] keep note"}

	i.ClearExtensionChrome("gone")

	if _, ok := i.extStatuses["gone"]; ok {
		t.Fatal("exited extension status survived")
	}
	if _, ok := i.extWidgets["gone"]; ok {
		t.Fatal("exited extension widget survived")
	}
	if _, ok := i.extStatuses["keep"]; !ok {
		t.Fatal("unrelated extension status was removed")
	}
	if _, ok := i.extWidgets["keep"]; !ok {
		t.Fatal("unrelated extension widget was removed")
	}
	if got := strings.Join(i.extNotes, "\n"); got != "[keep] keep note" {
		t.Fatalf("extension notes after cleanup = %q", got)
	}
}

func TestInteractiveRedrawPlacesWideRightBarBesideMainPane(t *testing.T) {
	term := &alertTestTerminal{}
	i := NewInteractive(InteractiveConfig{Terminal: term, Theme: tui.Dark})
	i.rend.Resize(80, 24)
	i.SetWidget("tasked-phases", "plan", "right_bar", "Plan", []string{"[ ] read files"})
	i.redraw()

	out := stripANSIBytes(term.String())
	if !strings.Contains(out, "Plan") || !strings.Contains(out, "[ ] read files") {
		t.Fatalf("wide redraw omitted right-bar content: %q", out)
	}
	if !strings.Contains(out, "│") {
		t.Fatal("wide redraw omitted the main-pane divider")
	}
}

func TestCtrlBTogglesRightBarAndFallsBackAboveInput(t *testing.T) {
	term := &alertTestTerminal{}
	i := NewInteractive(InteractiveConfig{Terminal: term, Theme: tui.Dark})
	i.SetWidget("tasked-phases", "plan", "right_bar", "Plan", []string{"[ ] read files"})

	if i.rightBarHidden {
		t.Fatal("right bar starts hidden")
	}
	if done := i.handleKey(context.Background(), tui.Key{Kind: tui.KeyCtrlB}); done {
		t.Fatal("ctrl+b unexpectedly exited interactive mode")
	}
	if !i.rightBarHidden {
		t.Fatal("ctrl+b did not hide the right bar")
	}

	i.mu.Lock()
	fallback := strings.Join(i.extensionChromeLinesForLayoutLocked(80, false, true), "\n")
	i.mu.Unlock()
	if !strings.Contains(fallback, "Plan") {
		t.Fatalf("hidden right-bar widget did not fall back above input: %q", fallback)
	}

	i.handleKey(context.Background(), tui.Key{Kind: tui.KeyCtrlB})
	if i.rightBarHidden {
		t.Fatal("second ctrl+b did not show the right bar")
	}
}
