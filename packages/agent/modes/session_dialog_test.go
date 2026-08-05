package modes

import (
	"context"
	"strings"
	"testing"
	"unicode"

	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
	"github.com/patriceckhart/zot/packages/tui"
)

func TestFormatSessionRowPlainSanitizesControlBytes(t *testing.T) {
	row := formatSessionRowPlain(core.SessionSummary{
		Provider:      "test\x1b]0;bad\a",
		Model:         "model\x1b[31m",
		MessageCount:  1,
		FirstUserText: "hello\x1b[2J\nworld",
	}, 120)
	if strings.IndexFunc(row, unicode.IsControl) >= 0 {
		t.Fatalf("session row contains control characters: %q", row)
	}
	if !strings.Contains(row, "hello world") {
		t.Fatalf("sanitized session text missing: %q", row)
	}
}

func TestSessionDialogLoadsEntriesWithoutBlockingOpen(t *testing.T) {
	root := t.TempDir()
	cwd := t.TempDir()
	session, err := core.NewSession(root, cwd, "test", "test-model", "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendMessage(provider.Message{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "load this session"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	d := newSessionDialog()
	events := d.Open(context.Background(), root, cwd)
	t.Cleanup(d.Close)
	if !d.Active() || !d.Loading() {
		t.Fatalf("dialog state after Open = active %v, loading %v; want active and loading", d.Active(), d.Loading())
	}
	loadingText := strings.Join(d.Render(tui.Dark, 100), "\n")
	if !strings.Contains(loadingText, "loading sessions") {
		t.Fatalf("loading render = %q, want spinner status", loadingText)
	}
	act := d.HandleKey(tui.Key{Kind: tui.KeyEnter})
	if act.Select || !d.Active() {
		t.Fatalf("enter while loading = %+v, active %v; want no selection and active dialog", act, d.Active())
	}

	for event := range events {
		d.ApplyLoad(event)
	}

	if d.Loading() {
		t.Fatal("dialog still loading after final result")
	}
	if len(d.sessions) != 1 || d.sessions[0].Path != session.Path {
		t.Fatalf("loaded sessions = %+v, want %q", d.sessions, session.Path)
	}
	loadedText := strings.Join(d.Render(tui.Dark, 100), "\n")
	if strings.Contains(loadedText, "loading sessions") {
		t.Fatalf("completed render still shows loading spinner: %q", loadedText)
	}
	if !strings.Contains(loadedText, "load this session") {
		t.Fatalf("completed render missing session text: %q", loadedText)
	}
}

func TestSessionDialogCanceledParentDoesNotRemainLoading(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d := newSessionDialog()
	events := d.Open(ctx, t.TempDir(), t.TempDir())
	if d.Loading() {
		t.Fatal("dialog remained loading with an already-canceled parent")
	}
	if _, ok := <-events; ok {
		t.Fatal("canceled dialog emitted a load event")
	}
}

func TestSessionDialogCancellationFinishesLoading(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := newSessionDialog()
	events := d.Open(ctx, t.TempDir(), t.TempDir())
	cancel()
	for range events {
	}
	d.ApplyLoadClosed()
	if d.Loading() {
		t.Fatal("dialog remained loading after parent cancellation")
	}
}

func TestSessionDialogEscapeCancelsLoading(t *testing.T) {
	d := newSessionDialog()
	events := d.Open(context.Background(), t.TempDir(), t.TempDir())
	act := d.HandleKey(tui.Key{Kind: tui.KeyEsc})
	if !act.Close || d.Active() || d.Loading() {
		t.Fatalf("escape action = %+v, active %v, loading %v; want closed dialog", act, d.Active(), d.Loading())
	}
	for range events {
	}
}

func TestSessionDialogAppliesLoadedEntriesInPathOrder(t *testing.T) {
	d := newSessionDialog()
	d.active = true
	d.loading = true
	d.loadGeneration = 7

	d.ApplyLoad(sessionLoadEvent{
		kind:       sessionLoadStarted,
		generation: 7,
		total:      4,
	})
	d.ApplyLoad(sessionLoadEvent{
		kind:       sessionLoadEntry,
		generation: 7,
		index:      2,
		summary:    core.SessionSummary{Path: "third", MessageCount: 1},
	})
	d.ApplyLoad(sessionLoadEvent{
		kind:       sessionLoadEntry,
		generation: 7,
		index:      1,
		summary:    core.SessionSummary{Path: "hidden", MessageCount: 1, HideFromSessions: true},
	})
	d.ApplyLoad(sessionLoadEvent{
		kind:       sessionLoadEntry,
		generation: 7,
		index:      3,
		summary:    core.SessionSummary{Path: "empty"},
	})
	d.ApplyLoad(sessionLoadEvent{
		kind:       sessionLoadEntry,
		generation: 7,
		index:      0,
		summary:    core.SessionSummary{Path: "first", MessageCount: 1},
	})
	d.ApplyLoad(sessionLoadEvent{kind: sessionLoadFinished, generation: 7})

	if d.Loading() {
		t.Fatal("dialog still loading after final result")
	}
	if len(d.sessions) != 2 || d.sessions[0].Path != "first" || d.sessions[1].Path != "third" {
		t.Fatalf("loaded sessions = %+v, want first then third", d.sessions)
	}
}

func TestSessionDialogIgnoresStaleLoadResults(t *testing.T) {
	first := newSessionDialog()
	firstEvents := first.Open(context.Background(), t.TempDir(), t.TempDir())
	firstGeneration := first.loadGeneration

	secondEvents := first.Open(context.Background(), t.TempDir(), t.TempDir())
	if first.loadGeneration == firstGeneration {
		t.Fatal("reopening picker did not advance load generation")
	}

	first.ApplyLoad(sessionLoadEvent{
		kind:       sessionLoadEntry,
		generation: firstGeneration,
		index:      0,
		summary: core.SessionSummary{
			Path:         "stale",
			MessageCount: 1,
		},
	})
	if len(first.sessions) != 0 {
		t.Fatalf("stale results repopulated picker: %+v", first.sessions)
	}

	first.Close()
	for range firstEvents {
	}
	for range secondEvents {
	}
}
