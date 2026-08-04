//go:build !darwin && !linux

package tui

func ReadClipboardImage() (ClipboardImage, bool, error) {
	return ClipboardImage{}, false, nil
}

func ReadClipboardImagePNG() (string, []byte, bool, error) {
	return "", nil, false, nil
}
