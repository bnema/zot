//go:build darwin

package tui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/image/tiff"
)

var errClipboardImageTooLarge = errors.New("clipboard image exceeds size limit")

const readClipboardImageScript = `
on run argv
	set outPath to item 1 of argv
	try
		set imgData to the clipboard as «class PNGf»
		set imgKind to "png"
	on error
		try
			set imgData to the clipboard as «class TIFF»
			set imgKind to "tiff"
		on error
			return "NO_IMAGE"
		end try
	end try

	set outFile to POSIX file outPath
	set fileRef to open for access outFile with write permission
	try
		set eof of fileRef to 0
		write imgData to fileRef
		close access fileRef
	on error errMsg number errNum
		try
			close access fileRef
		end try
		error errMsg number errNum
	end try
	return imgKind
end run
`

func ReadClipboardImage(ctx context.Context) (ClipboardImage, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, clipboardImageReadTimeout)
	defer cancel()

	path, data, ok, err := readClipboardImagePNG(ctx)
	if err != nil {
		return ClipboardImage{}, false, err
	}
	if !ok {
		return ClipboardImage{}, false, nil
	}
	_ = removeClipboardImageFile(path)
	return ClipboardImage{MimeType: "image/png", Data: data}, true, nil
}

func ReadClipboardImagePNG() (string, []byte, bool, error) {
	return readClipboardImagePNG(context.Background())
}

func readClipboardImagePNG(ctx context.Context) (string, []byte, bool, error) {
	dir := clipboardImageDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, false, err
	}
	path := filepath.Join(dir, "clipboard-"+time.Now().Format("20060102-150405")+"-"+randomHex(4)+".png")
	rawPath := path + ".raw"
	defer os.Remove(rawPath)

	kind, err := writeClipboardImageData(ctx, rawPath)
	if err != nil {
		return "", nil, false, err
	}
	if kind == "" {
		return "", nil, false, nil
	}

	switch kind {
	case "png":
		if err := os.Rename(rawPath, path); err != nil {
			return "", nil, false, err
		}
	case "tiff":
		if err := convertTIFFFileToPNG(rawPath, path); err != nil {
			return "", nil, false, err
		}
	default:
		clipPath, ok := findClipboardImagePath(kind)
		if !ok {
			return "", nil, false, fmt.Errorf("unexpected clipboard image kind %q", kind)
		}
		if err := copyClipboardImageFileToPNG(clipPath, path); err != nil {
			if err == errClipboardImageTooLarge {
				return "", nil, false, nil
			}
			return "", nil, false, err
		}
	}

	data, err := readClipboardImageFile(path)
	if err == errClipboardImageTooLarge {
		if err := removeClipboardImageFile(path); err != nil {
			return "", nil, false, err
		}
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	return path, data, true, nil
}

func writeClipboardImageData(ctx context.Context, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", readClipboardImageScript, path)
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		if strings.Contains(trimmed, "NO_IMAGE") || strings.Contains(trimmed, "Can’t make") || strings.Contains(trimmed, "Can't make") {
			return "", nil
		}
		if kind, ok := clipboardImageKind(trimmed); ok {
			return kind, nil
		}
		if path, ok := findClipboardImagePath(trimmed); ok {
			return path, nil
		}
		if trimmed == "" {
			return "", fmt.Errorf("osascript failed: %w", err)
		}
		return "", fmt.Errorf("osascript failed: %s", trimmed)
	}
	if trimmed == "NO_IMAGE" {
		return "", nil
	}
	if kind, ok := clipboardImageKind(trimmed); ok {
		return kind, nil
	}
	return trimmed, nil
}

func clipboardImageKind(s string) (string, bool) {
	for _, line := range strings.Split(s, "\n") {
		switch strings.TrimSpace(line) {
		case "png":
			return "png", true
		case "tiff":
			return "tiff", true
		}
	}
	return "", false
}

func readClipboardImageFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, int64(maxClipboardImageBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxClipboardImageBytes {
		return nil, errClipboardImageTooLarge
	}
	return data, nil
}

func removeClipboardImageFile(path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove temporary clipboard image: %w", err)
	}
	return nil
}

func copyClipboardImageFileToPNG(srcPath, dstPath string) error {
	switch strings.ToLower(filepath.Ext(srcPath)) {
	case ".png":
		data, err := readClipboardImageFile(srcPath)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, 0o600)
	case ".tif", ".tiff":
		return convertTIFFFileToPNG(srcPath, dstPath)
	default:
		return fmt.Errorf("clipboard file is not a supported image type: %s", srcPath)
	}
}

func findClipboardImagePath(s string) (string, bool) {
	if p, ok := clipboardImagePath(s); ok {
		return p, true
	}
	for _, ext := range []string{".png", ".tiff", ".tif"} {
		lower := strings.ToLower(s)
		end := strings.Index(lower, ext)
		if end < 0 {
			continue
		}
		end += len(ext)
		start := strings.LastIndex(s[:end], "/")
		if start < 0 {
			continue
		}
		for start > 0 {
			c := s[start-1]
			if c == '\'' || c == '"' || c == '\n' || c == '\r' || c == '\t' {
				break
			}
			start--
		}
		if p, ok := clipboardImagePath(s[start:end]); ok {
			return p, true
		}
	}
	return "", false
}

func clipboardImagePath(s string) (string, bool) {
	p := strings.TrimSpace(s)
	p = strings.Trim(p, "'\"")
	if p == "" {
		return "", false
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return "", false
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png", ".tif", ".tiff":
		return p, true
	default:
		return "", false
	}
}

func convertTIFFFileToPNG(srcPath, dstPath string) error {
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()

	img, err := tiff.Decode(in)
	if err != nil {
		return err
	}

	out, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer out.Close()

	return png.Encode(out, img)
}

func clipboardImageDir() string {
	if info, err := os.Stat("/tmp"); err == nil && info.IsDir() {
		return filepath.Join("/tmp", "zut-clipboard-images")
	}
	return filepath.Join(os.TempDir(), "zut-clipboard-images")
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
