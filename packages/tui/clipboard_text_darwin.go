//go:build darwin

package tui

import "fmt"

// ReadClipboardText reads plain text from the macOS system clipboard.
func ReadClipboardText() (string, bool, error) {
	text, ok, err := readClipboardTextCommands(clipboardTextCommand{name: "/usr/bin/pbpaste"})
	if err == errClipboardCommandUnavailable {
		return "", false, fmt.Errorf("text clipboard unavailable: pbpaste was not found")
	}
	return text, ok, err
}
