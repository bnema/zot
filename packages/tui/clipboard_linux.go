//go:build linux

package tui

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

const clipboardImageCommandTimeout = 5 * time.Second

var supportedClipboardImageMIMEs = []string{
	"image/png",
	"image/jpeg",
	"image/gif",
	"image/webp",
}

type clipboardImageCommand struct {
	name string
	args []string
}

// ReadClipboardImage reads an image from the Wayland or X11 system clipboard.
// Clipboard helper failures are intentionally treated as an empty clipboard so
// that optional desktop integrations do not affect paste handling.
func ReadClipboardImage() (ClipboardImage, bool, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if image, ok := readClipboardImageWayland(); ok {
			return image, true, nil
		}
	}
	if os.Getenv("DISPLAY") != "" {
		if image, ok := readClipboardImageX11(); ok {
			return image, true, nil
		}
	}
	return ClipboardImage{}, false, nil
}

// ReadClipboardImagePNG preserves the legacy Linux behavior. Linux image
// reads retain their clipboard MIME type through ReadClipboardImage instead.
func ReadClipboardImagePNG() (string, []byte, bool, error) {
	return "", nil, false, nil
}

func readClipboardImageWayland() (ClipboardImage, bool) {
	types, ok := runClipboardImageCommand(clipboardImageCommand{
		name: "wl-paste",
		args: []string{"--list-types"},
	})
	if !ok {
		return ClipboardImage{}, false
	}

	available := make(map[string]bool)
	for _, line := range strings.Split(string(types), "\n") {
		mime := strings.ToLower(strings.TrimSpace(line))
		if isSupportedClipboardImageMIME(mime) {
			available[mime] = true
		}
	}
	for _, mime := range supportedClipboardImageMIMEs {
		if !available[mime] {
			continue
		}
		data, ok := runClipboardImageCommand(clipboardImageCommand{
			name: "wl-paste",
			args: []string{"--type", mime},
		})
		if ok {
			return ClipboardImage{MimeType: mime, Data: data}, true
		}
	}
	return ClipboardImage{}, false
}

func readClipboardImageX11() (ClipboardImage, bool) {
	for _, mime := range supportedClipboardImageMIMEs {
		data, ok := runClipboardImageCommand(clipboardImageCommand{
			name: "xclip",
			args: []string{"-selection", "clipboard", "-out", "-target", mime},
		})
		if ok {
			return ClipboardImage{MimeType: mime, Data: data}, true
		}
	}
	return ClipboardImage{}, false
}

func isSupportedClipboardImageMIME(mime string) bool {
	for _, supported := range supportedClipboardImageMIMEs {
		if mime == supported {
			return true
		}
	}
	return false
}

func runClipboardImageCommand(command clipboardImageCommand) ([]byte, bool) {
	path, err := exec.LookPath(command.name)
	if err != nil {
		return nil, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), clipboardImageCommandTimeout)
	defer cancel()
	data, err := exec.CommandContext(ctx, path, command.args...).Output()
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}
