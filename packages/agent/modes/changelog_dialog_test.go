package modes

import (
	"strings"
	"testing"

	"github.com/patriceckhart/zot/packages/tui"
)

func TestChangelogDialogWrapsReleaseNotes(t *testing.T) {
	const width = 64
	body := "\x00H:Other improvements for terminals with constrained display widths\n" +
		"- Add MiniMax-M3 to direct MiniMax global and China catalogs with compatibility for narrow terminal layouts"
	d := newChangelogDialog()
	d.Open("0.3.25", "", body)

	rows := d.Render(tui.Theme{}, width)
	assertRowsFitWidth(t, rows, width)

	plain := strings.Join(rows, "\n")
	for _, word := range strings.Fields(strings.ReplaceAll(body, "\x00H:", "")) {
		if !strings.Contains(plain, word) {
			t.Fatalf("wrapped release notes lost word %q:\n%s", word, plain)
		}
	}
}
