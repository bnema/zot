//go:build darwin

package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestReadClipboardImageFileSizeLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clipboard.png")
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, maxClipboardImageBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := readClipboardImageFile(path)
	if err != nil || len(data) != maxClipboardImageBytes {
		t.Fatalf("exact-limit file result = (%d bytes, %v), want (%d bytes, nil)", len(data), err, maxClipboardImageBytes)
	}

	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, maxClipboardImageBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err = readClipboardImageFile(path)
	if err != errClipboardImageTooLarge || data != nil {
		t.Fatalf("over-limit file result = (%d bytes, %v), want (0 bytes, too-large)", len(data), err)
	}
}

func TestRemoveClipboardImageFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clipboard.png")
	if err := os.WriteFile(path, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeClipboardImageFile(path); err != nil {
		t.Fatalf("removeClipboardImageFile() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("clipboard image stat error = %v, want not-exist", err)
	}
}
