package tui

import (
	"strings"
	"testing"
)

func TestToolBoxSideTruncationPreservesANSIColor(t *testing.T) {
	const width = 32
	const contentColor = 196
	line := "    " + Dark.FGColor(Color256(contentColor), strings.Repeat("x", 80))

	got := toolBoxSide(Dark, line, width)
	if !strings.Contains(got, sgrFG(contentColor)) {
		t.Fatalf("narrow tool row lost its content color: %q", got)
	}
	if gotWidth := visibleWidth(got); gotWidth != width {
		t.Fatalf("narrow tool row width = %d, want %d: %q", gotWidth, width, got)
	}
}
