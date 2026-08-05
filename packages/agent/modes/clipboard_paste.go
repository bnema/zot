package modes

import (
	"strings"

	"github.com/bnema/zut/packages/tui"
)

type clipboardTextReader func() (string, bool, error)

// resolveClipboardText turns an application-level ctrl+v event into the same
// KeyPaste event terminals produce for bracketed paste. Keeping that conversion
// before dialog routing makes clipboard paste available to every text input.
func resolveClipboardText(k tui.Key, read clipboardTextReader) (tui.Key, bool, error) {
	if k.Kind != tui.KeyPasteClipboard {
		return k, false, nil
	}
	text, ok, err := read()
	if err != nil || !ok || text == "" {
		return k, false, err
	}
	return tui.Key{Kind: tui.KeyPaste, Paste: text}, true, nil
}

func singleLinePaste(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
