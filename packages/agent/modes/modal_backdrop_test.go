package modes

import (
	"strings"
	"testing"

	"github.com/bnema/zut/packages/tui"
)

func TestSettingsDialogDimsBackdropAndMainCursor(t *testing.T) {
	term := &alertTestTerminal{}
	i := NewInteractive(InteractiveConfig{Terminal: term, Theme: tui.Dark})
	i.rend.Resize(80, 24)
	i.ed.SetValue("draft")
	if !i.settingsDialog.Open([]settingsItem{{key: "test", label: "test setting"}}) {
		t.Fatal("settings dialog did not open")
	}

	i.redraw()
	got := term.String()

	input := i.cfg.Theme.AccentBar(i.cfg.Theme.Accent) + "draft"
	if !strings.Contains(got, tui.Dim(input)) {
		t.Fatalf("settings dialog left the main input undimmed: %q", got)
	}
	if header := frameHeader(i.cfg.Theme, "settings", 80); strings.Contains(got, tui.Dim(header)) {
		t.Fatalf("settings dialog itself was dimmed: %q", got)
	}
	cursor := tui.CursorColor(i.cfg.Theme.DimColor(tui.Color256(15), modalBackdropDimPercent))
	if !strings.Contains(got, cursor) {
		t.Fatalf("settings dialog left the main cursor bright; missing %q in %q", cursor, got)
	}

	term.mu.Lock()
	term.data = nil
	term.mu.Unlock()
	i.settingsDialog.Close()
	i.redraw()
	if got := term.String(); !strings.Contains(got, tui.CursorColor(tui.Color256(15))) {
		t.Fatalf("closing settings did not restore the main cursor color: %q", got)
	}
}
