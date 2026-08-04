//go:build !darwin && !linux

package tui

import "context"

func ReadClipboardImage(_ context.Context) (ClipboardImage, bool, error) {
	return ClipboardImage{}, false, nil
}

func ReadClipboardImagePNG() (string, []byte, bool, error) {
	return "", nil, false, nil
}
