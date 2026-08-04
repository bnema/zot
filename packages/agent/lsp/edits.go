package lsp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxWorkspaceEditFileBytes = 8 * 1024 * 1024

// ApplyWorkspaceEdit applies only text edits inside root. It rejects create,
// rename, delete, non-file URIs, symlink escapes, malformed ranges, and files
// larger than the bounded safety limit.
func ApplyWorkspaceEdit(root string, edit WorkspaceEdit) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("workspace root: %w", err)
	}
	all := make(map[string][]TextEdit, len(edit.Changes)+len(edit.DocumentChanges))
	for uri, changes := range edit.Changes {
		all[uri] = append(all[uri], changes...)
	}
	for _, raw := range edit.DocumentChanges {
		var change struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Edits []TextEdit `json:"edits"`
			Kind  string     `json:"kind"`
		}
		if err := json.Unmarshal(raw, &change); err != nil {
			return fmt.Errorf("workspace edit document change: %w", err)
		}
		if change.Kind != "" || change.TextDocument.URI == "" {
			return fmt.Errorf("workspace edit contains an unsupported resource operation")
		}
		all[change.TextDocument.URI] = append(all[change.TextDocument.URI], change.Edits...)
	}
	for uri, changes := range all {
		path, err := uriToPath(uri)
		if err != nil {
			return err
		}
		if err := safeWorkspacePath(rootReal, path); err != nil {
			return err
		}
		if err := applyTextEdits(path, changes); err != nil {
			return fmt.Errorf("apply edit to %s: %w", path, err)
		}
	}
	return nil
}

func safeWorkspacePath(root, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	candidate := abs
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		candidate = real
	} else {
		parent := filepath.Dir(abs)
		if real, parentErr := filepath.EvalSymlinks(parent); parentErr == nil {
			candidate = filepath.Join(real, filepath.Base(abs))
		}
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("workspace edit path %q is outside workspace", path)
	}
	return nil
}

type byteEdit struct {
	start int
	end   int
	text  string
}

func applyTextEdits(path string, edits []TextEdit) error {
	if len(edits) == 0 {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() > maxWorkspaceEditFileBytes {
		return fmt.Errorf("file is larger than %d bytes", maxWorkspaceEditFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	converted := make([]byteEdit, 0, len(edits))
	for _, edit := range edits {
		start, err := positionOffset(data, edit.Range.Start)
		if err != nil {
			return err
		}
		end, err := positionOffset(data, edit.Range.End)
		if err != nil {
			return err
		}
		if end < start {
			return fmt.Errorf("edit range ends before it starts")
		}
		converted = append(converted, byteEdit{start: start, end: end, text: edit.NewText})
	}
	sort.Slice(converted, func(i, j int) bool {
		if converted[i].start != converted[j].start {
			return converted[i].start > converted[j].start
		}
		return converted[i].end > converted[j].end
	})
	for i := 1; i < len(converted); i++ {
		if converted[i].end > converted[i-1].start {
			return fmt.Errorf("workspace text edits overlap")
		}
	}
	out := append([]byte(nil), data...)
	for _, edit := range converted {
		out = append(append(append([]byte(nil), out[:edit.start]...), []byte(edit.text)...), out[edit.end:]...)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".zot-lsp-edit-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func positionOffset(data []byte, position Position) (int, error) {
	if position.Line < 0 || position.Character < 0 {
		return 0, fmt.Errorf("invalid LSP position %d:%d", position.Line, position.Character)
	}
	lineStart := 0
	line := 0
	for line < position.Line {
		next := strings.IndexByte(string(data[lineStart:]), '\n')
		if next < 0 {
			return 0, fmt.Errorf("LSP line %d is outside file", position.Line)
		}
		lineStart += next + 1
		line++
	}
	lineEnd := len(data)
	if next := strings.IndexByte(string(data[lineStart:]), '\n'); next >= 0 {
		lineEnd = lineStart + next
	}
	lineBytes := data[lineStart:lineEnd]
	if len(lineBytes) > 0 && lineBytes[len(lineBytes)-1] == '\r' {
		lineBytes = lineBytes[:len(lineBytes)-1]
		lineEnd--
	}
	units := 0
	for offset, r := range string(lineBytes) {
		if units == position.Character {
			return lineStart + offset, nil
		}
		if r > 0xffff {
			units += 2
		} else {
			units++
		}
	}
	if units == position.Character {
		return lineEnd, nil
	}
	return 0, fmt.Errorf("LSP character %d is outside line %d", position.Character, position.Line)
}
