package lsp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	maxMessageBytes = 32 * 1024 * 1024
	maxHeaderBytes  = 8 * 1024
	maxHeaderLines  = 64
)

// WriteMessage writes one LSP Content-Length framed JSON-RPC message.
func WriteMessage(w io.Writer, payload []byte) error {
	if len(payload) > maxMessageBytes {
		return fmt.Errorf("LSP message is too large: %d bytes", len(payload))
	}
	header := []byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(payload)))
	if n, err := w.Write(header); err != nil {
		return err
	} else if n != len(header) {
		return io.ErrShortWrite
	}
	n, err := w.Write(payload)
	if err == nil && n != len(payload) {
		return io.ErrShortWrite
	}
	return err
}

// ReadMessage reads one Content-Length framed LSP message. It accepts other
// headers (including Content-Type) and either CRLF or LF header endings.
func ReadMessage(r *bufio.Reader) ([]byte, error) {
	length := -1
	headerBytes := 0
	headerLines := 0
	for {
		line, err := r.ReadSlice('\n')
		if err != nil {
			if err == bufio.ErrBufferFull {
				return nil, errors.New("LSP headers are too large")
			}
			return nil, err
		}
		headerBytes += len(line)
		headerLines++
		if headerBytes > maxHeaderBytes || headerLines > maxHeaderLines {
			return nil, errors.New("LSP headers are too large")
		}
		lineText := strings.TrimRight(string(line), "\r\n")
		if lineText == "" {
			break
		}
		key, value, ok := strings.Cut(lineText, ":")
		if !ok {
			return nil, fmt.Errorf("malformed LSP header %q", lineText)
		}
		if strings.EqualFold(strings.TrimSpace(key), "content-length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid Content-Length %q", strings.TrimSpace(value))
			}
			length = n
		}
	}
	if length < 0 {
		return nil, errors.New("LSP message has no Content-Length")
	}
	if length > maxMessageBytes {
		return nil, fmt.Errorf("LSP message is too large: %d bytes", length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// Frame is useful in tests and for callers which already have a complete
// message. It returns the exact wire representation.
func Frame(payload []byte) []byte {
	var b bytes.Buffer
	if err := WriteMessage(&b, payload); err != nil {
		panic(err)
	}
	return b.Bytes()
}
