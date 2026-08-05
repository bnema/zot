package modes

import (
	"strings"
	"testing"

	"github.com/bnema/zut/packages/tui"
	"github.com/mattn/go-runewidth"
)

func TestLogoutDialogSelectionBackgroundSpansFullWidth(t *testing.T) {
	d := newLogoutDialog()
	d.Open([]logoutItem{{label: "Google", target: "google", method: "api key"}})
	const width = 60
	lines := d.Render(tui.Theme{SelectionFG: 15, SelectionBG: 4, Muted: 8}, width)
	selected := lines[2]
	if got := runewidth.StringWidth(stripANSIBytes(selected)); got != width {
		t.Fatalf("selected row width = %d, want %d", got, width)
	}
	if got := strings.Count(selected, "\x1b[0m"); got != 1 {
		t.Fatalf("selected row contains %d resets, want one final reset: %q", got, selected)
	}
}
