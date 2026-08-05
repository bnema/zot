package modes

import (
	"errors"
	"testing"

	"github.com/bnema/zut/packages/tui"
)

func TestResolveClipboardTextConvertsClipboardKeyToPaste(t *testing.T) {
	key, resolved, err := resolveClipboardText(tui.Key{Kind: tui.KeyPasteClipboard, Ctrl: true}, func() (string, bool, error) {
		return "pasted\ntext", true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resolved || key.Kind != tui.KeyPaste || key.Paste != "pasted\ntext" {
		t.Fatalf("key = %#v, resolved = %v", key, resolved)
	}
}

func TestResolveClipboardTextPreservesClipboardKeyWithoutText(t *testing.T) {
	original := tui.Key{Kind: tui.KeyPasteClipboard, Ctrl: true}
	key, resolved, err := resolveClipboardText(original, func() (string, bool, error) {
		return "", false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved || key != original {
		t.Fatalf("key = %#v, resolved = %v, want original unresolved key", key, resolved)
	}
}

func TestResolveClipboardTextReturnsReadError(t *testing.T) {
	want := errors.New("clipboard unavailable")
	_, resolved, err := resolveClipboardText(tui.Key{Kind: tui.KeyPasteClipboard}, func() (string, bool, error) {
		return "", false, want
	})
	if resolved || !errors.Is(err, want) {
		t.Fatalf("resolved = %v, err = %v, want %v", resolved, err, want)
	}
}

func TestSingleLinePaste(t *testing.T) {
	if got := singleLinePaste("  claude\n opus\t5  "); got != "claude opus 5" {
		t.Fatalf("singleLinePaste = %q", got)
	}
}
