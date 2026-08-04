//go:build linux

package tui

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
)

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
func ReadClipboardImage(ctx context.Context) (ClipboardImage, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, clipboardImageReadTimeout)
	defer cancel()

	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if image, ok := readClipboardImageWayland(ctx); ok {
			return image, true, nil
		}
	}
	if os.Getenv("DISPLAY") != "" {
		if image, ok := readClipboardImageX11(ctx); ok {
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

func readClipboardImageWayland(ctx context.Context) (ClipboardImage, bool) {
	types, ok := runClipboardImageCommand(ctx, clipboardImageCommand{
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
		data, ok := runClipboardImageCommand(ctx, clipboardImageCommand{
			name: "wl-paste",
			args: []string{"--type", mime},
		})
		if ok {
			return ClipboardImage{MimeType: mime, Data: data}, true
		}
	}
	return ClipboardImage{}, false
}

func readClipboardImageX11(ctx context.Context) (ClipboardImage, bool) {
	for _, mime := range supportedClipboardImageMIMEs {
		data, ok := runClipboardImageCommand(ctx, clipboardImageCommand{
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

func runClipboardImageCommand(ctx context.Context, command clipboardImageCommand) ([]byte, bool) {
	path, err := exec.LookPath(command.name)
	if err != nil {
		return nil, false
	}

	cmd := exec.CommandContext(ctx, path, command.args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false
	}
	if err := cmd.Start(); err != nil {
		return nil, false
	}

	data, readErr := io.ReadAll(io.LimitReader(stdout, int64(maxClipboardImageBytes)+1))
	if readErr != nil || len(data) > maxClipboardImageBytes {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, false
	}
	if err := cmd.Wait(); err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}
