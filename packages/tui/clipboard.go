package tui

import (
	"errors"
	"time"
)

const (
	clipboardImageReadTimeout = 5 * time.Second
	maxClipboardImageBytes    = 32 << 20
)

var errClipboardImageTooLarge = errors.New("clipboard image exceeds size limit")

// ClipboardImage is an image read from the system clipboard.
type ClipboardImage struct {
	MimeType string
	Data     []byte
}
