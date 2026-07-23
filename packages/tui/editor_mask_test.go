package tui

import (
	"strings"
	"testing"
)

func TestEditorMaskConcealsRenderedValue(t *testing.T) {
	ed := NewEditor("")
	ed.SetValue("secret-key")
	ed.Mask = true
	lines, _, _ := ed.Render(80)
	rendered := strings.Join(lines, "\n")
	if strings.Contains(rendered, "secret-key") || rendered != "**********" {
		t.Fatalf("rendered = %q", rendered)
	}
	if got := ed.SubmitValue(); got != "secret-key" {
		t.Fatalf("submitted = %q", got)
	}
}
