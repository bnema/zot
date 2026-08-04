package tui

// ClipboardImage is an image read from the system clipboard.
type ClipboardImage struct {
	MimeType string
	Data     []byte
}
