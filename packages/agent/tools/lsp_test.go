package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/patriceckhart/zot/packages/agent/lsp"
	"github.com/patriceckhart/zot/packages/provider"
)

func TestLSPToolSchemaAndFormatting(t *testing.T) {
	tool := NewLSPTool(t.TempDir(), lsp.NewManager())
	var schema map[string]any
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatal(err)
	}
	if tool.Name() != "lsp" || !strings.Contains(tool.Description(), "diagnostics") {
		t.Fatalf("tool metadata missing")
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"status"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	text, ok := result.Content[0].(provider.TextBlock)
	if !ok || text.Text == "" {
		t.Fatalf("status result = %#v", result.Content)
	}
	if !strings.Contains(text.Text, "[") && !strings.Contains(text.Text, "{") {
		t.Fatalf("status was not formatted JSON: %q", text.Text)
	}
}

func TestLSPToolGlobRespectsJailForSymlinkMatches(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "outside.go")
	if err := os.WriteFile(outsidePath, []byte("package outside\\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(root, "link.go")); err != nil {
		t.Fatal(err)
	}
	sandbox := NewSandbox(root)
	sandbox.Lock()
	tool := NewLSPTool(root, lsp.NewManager())
	tool.Sandbox = sandbox
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"diagnostics","path":"*.go","run_cli":false}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("jailed symlink glob was accepted: %#v", result)
	}
}

func TestBoundLSPText(t *testing.T) {
	value := boundLSPText(strings.Repeat("x", maxLSPToolOutput+10))
	if len(value) <= maxLSPToolOutput || !strings.Contains(value, "truncated") {
		t.Fatalf("bound output = %d bytes", len(value))
	}
	unicodeValue := boundLSPText(strings.Repeat("界", maxLSPToolOutput/3+10))
	if !utf8.ValidString(unicodeValue) {
		t.Fatal("bound output split a UTF-8 sequence")
	}
}
