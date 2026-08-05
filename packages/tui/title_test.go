package tui

import (
	"strings"
	"testing"
)

func TestSetTitleUsesOSC0(t *testing.T) {
	if got, want := SetTitle("zut: fix login"), "\x1b]0;zut: fix login\x07"; got != want {
		t.Fatalf("SetTitle() = %q, want %q", got, want)
	}
}

func TestSetTitleRemovesControlCharacters(t *testing.T) {
	got := SetTitle("safe\x1b]2;injected\x07\nnext")
	payload := got[len("\x1b]0;") : len(got)-1]
	if strings.ContainsRune(payload, '\x1b') || strings.ContainsRune(payload, '\x07') || strings.ContainsRune(payload, '\n') {
		t.Fatalf("title payload contains a control character: %q", got)
	}
	if got != "\x1b]0;safe]2;injectednext\x07" {
		t.Fatalf("sanitized title = %q", got)
	}
}
