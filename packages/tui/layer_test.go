package tui

import (
	"reflect"
	"testing"
)

func TestDimLinesPreservesDimmingAcrossStyledSegments(t *testing.T) {
	lines := []string{
		"plain",
		Dark.FGColor(Dark.Accent, "accent") + Dark.FGColor(Dark.Error, "error"),
	}

	got := DimLines(lines)
	want := []string{
		"\x1b[2mplain\x1b[0m",
		"\x1b[2m\x1b[38;5;111maccent\x1b[0m\x1b[2m\x1b[38;5;203merror\x1b[0m\x1b[2m\x1b[0m",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DimLines() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(lines, []string{
		"plain",
		Dark.FGColor(Dark.Accent, "accent") + Dark.FGColor(Dark.Error, "error"),
	}) {
		t.Fatalf("DimLines mutated input: %#v", lines)
	}
}

func TestCursorColorUsesTerminalColor(t *testing.T) {
	if got, want := CursorColor(ColorRGB(1, 2, 3)), "\x1b]12;rgb:01/02/03\x07"; got != want {
		t.Fatalf("CursorColor() = %q, want %q", got, want)
	}
}
