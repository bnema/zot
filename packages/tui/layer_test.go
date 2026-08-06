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
		"\x1b[2mplain\x1b[22m",
		"\x1b[2m\x1b[38;5;111maccent\x1b[0m\x1b[2m\x1b[38;5;203merror\x1b[0m\x1b[2m\x1b[22m",
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

func TestDimRetainsFaintAfterBold(t *testing.T) {
	const (
		dim = "\x1b[2m"
		off = "\x1b[22m"
	)
	if got, want := Dim("before "+Bold("bold")+" after"), dim+"before \x1b[1mbold"+off+dim+" after"+off; got != want {
		t.Fatalf("Dim() = %q, want %q", got, want)
	}
	if got, want := "\x1b[31m"+Dim("faint")+" tail", "\x1b[31m"+dim+"faint"+off+" tail"; got != want {
		t.Fatalf("Dim() reset its caller's color: %q, want %q", got, want)
	}
}

func TestCursorColorUsesTerminalColor(t *testing.T) {
	if got, want := CursorColor(ColorRGB(1, 2, 3)), "\x1b]12;rgb:01/02/03\x07"; got != want {
		t.Fatalf("CursorColor() = %q, want %q", got, want)
	}
}
