package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReadClipboardTextCommandsUsesAvailableCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper is a shell script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "clipboard-test")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'clipboard text'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	text, ok, err := readClipboardTextCommands(clipboardTextCommand{name: "clipboard-test"})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || text != "clipboard text" {
		t.Fatalf("text = %q, ok = %v", text, ok)
	}
}

func TestReadClipboardTextCommandsReportsUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, ok, err := readClipboardTextCommands(clipboardTextCommand{name: "missing-clipboard-command"})
	if ok || err != errClipboardCommandUnavailable {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
}
