package modes

import (
	"strings"
	"testing"

	"github.com/patriceckhart/zot/packages/tui"
)

func newNotesTestInteractive() *Interactive {
	i := &Interactive{dirty: make(chan struct{}, 1)}
	i.cfg.Theme = tui.Theme{Muted: 8, Warning: 3, Error: 1, Tool: 2, Accent: 4}
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
