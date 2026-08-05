//go:build linux

package tui

import (
	"fmt"
	"os"
)

// ReadClipboardText reads plain text from a Wayland or X11 clipboard. The
// small command-line clients are used to preserve zut's CGO-free build.
func ReadClipboardText() (string, bool, error) {
	var commands []clipboardTextCommand
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		commands = append(commands, clipboardTextCommand{name: "wl-paste", args: []string{"--no-newline", "--type", "text"}})
	}
	if os.Getenv("DISPLAY") != "" {
		commands = append(commands,
			clipboardTextCommand{name: "xclip", args: []string{"-selection", "clipboard", "-out"}},
			clipboardTextCommand{name: "xsel", args: []string{"--clipboard", "--output"}},
		)
	}
	// Environment forwarding through multiplexers is not always reliable.
	// Try every backend if no display variable made a preferred list.
	if len(commands) == 0 {
		commands = []clipboardTextCommand{
			{name: "wl-paste", args: []string{"--no-newline", "--type", "text"}},
			{name: "xclip", args: []string{"-selection", "clipboard", "-out"}},
			{name: "xsel", args: []string{"--clipboard", "--output"}},
		}
	}
	text, ok, err := readClipboardTextCommands(commands...)
	if err == errClipboardCommandUnavailable {
		return "", false, fmt.Errorf("text clipboard unavailable: install wl-clipboard, xclip, or xsel, or use the terminal paste shortcut")
	}
	if err != nil {
		return "", false, fmt.Errorf("read text clipboard: %w", err)
	}
	return text, ok, nil
}
