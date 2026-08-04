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

const maxMessageBytes = 32 * 1024 * 1024

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
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("malformed LSP header %q", line)
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
	_ = WriteMessage(&b, payload)
	return b.Bytes()
}
