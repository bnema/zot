package modes

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
	"github.com/patriceckhart/zot/packages/tui"
)

func TestLogoutDialogSelectionBackgroundSpansFullWidth(t *testing.T) {
	d := newLogoutDialog()
	d.Open([]logoutItem{{label: "Google", target: "google", method: "api key"}})
	const width = 60
	lines := d.Render(tui.Theme{
		SelectionFG: tui.Color256(15), SelectionBG: tui.Color256(4), Muted: tui.Color256(8),
	}, width)
	selected := lines[2]
	if got := runewidth.StringWidth(stripANSIBytes(selected)); got != width {
		t.Fatalf("selected row width = %d, want %d", got, width)
	}
	if got := strings.Count(selected, "\x1b[0m"); got != 1 {
		t.Fatalf("selected row contains %d resets, want one final reset: %q", got, selected)
	}
}
