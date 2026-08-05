package tui

import (
	"time"
)

const (
	clipboardImageReadTimeout = 5 * time.Second
	maxClipboardImageBytes    = 32 << 20
)

// ClipboardImage is an image read from the system clipboard.
type ClipboardImage struct {
	MimeType string
	Data     []byte
}
