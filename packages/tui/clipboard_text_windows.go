//go:build windows

package tui

import "fmt"

// ReadClipboardText reads plain text through the PowerShell installation
// included with supported Windows versions.
func ReadClipboardText() (string, bool, error) {
	text, ok, err := readClipboardTextCommands(
		clipboardTextCommand{name: "powershell.exe", args: []string{"-NoProfile", "-NonInteractive", "-Command", "[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; Get-Clipboard -Raw -Format Text"}},
		clipboardTextCommand{name: "pwsh.exe", args: []string{"-NoProfile", "-NonInteractive", "-Command", "Get-Clipboard -Raw"}},
	)
	if err == errClipboardCommandUnavailable {
		return "", false, fmt.Errorf("text clipboard unavailable: PowerShell was not found")
	}
	if err != nil {
		return "", false, fmt.Errorf("read text clipboard: %w", err)
	}
	return text, ok, nil
}
