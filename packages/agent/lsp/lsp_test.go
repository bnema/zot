package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFakeLSPProcess(t *testing.T) {
	if os.Getenv("ZOT_FAKE_LSP") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		payload, err := ReadMessage(reader)
		if err != nil {
			return
		}
		var message rpcEnvelope
		if json.Unmarshal(payload, &message) != nil {
			continue
		}
		switch message.Method {
		case "initialize":
			response := []byte(`{"jsonrpc":"2.0","id":` + string(message.ID) + `,"result":{"capabilities":{"definitionProvider":true}}}`)
			_ = WriteMessage(os.Stdout, response)
		case "test/echo":
			response := []byte(`{"jsonrpc":"2.0","id":` + string(message.ID) + `,"result":{"echo":true}}`)
			_ = WriteMessage(os.Stdout, response)
		case "shutdown":
			response := []byte(`{"jsonrpc":"2.0","id":` + string(message.ID) + `,"result":null}`)
			_ = WriteMessage(os.Stdout, response)
		case "textDocument/didOpen":
			var params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(message.Params, &params)
			response := map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": map[string]any{"uri": params.TextDocument.URI, "diagnostics": []any{map[string]any{"range": map[string]any{"start": map[string]int{"line": 0, "character": 0}, "end": map[string]int{"line": 0, "character": 1}}, "severity": 1, "code": "FAKE", "message": "fake problem"}}}}
			data, _ := json.Marshal(response)
			_ = WriteMessage(os.Stdout, data)
		}
	}
}

func TestClientFakeStdioRPC(t *testing.T) {
	root := t.TempDir()
	script := os.Args[0]
	spec := ServerConfig{ID: "fake", Kind: "lsp", Command: script, Args: []string{"-test.run=TestFakeLSPProcess"}, Env: map[string]string{"ZOT_FAKE_LSP": "1"}}
	diagnostics := make(chan []Diagnostic, 1)
	client, err := NewClientWithContext(context.Background(), spec, root, ClientOptions{OnDiagnostics: func(value []Diagnostic) { diagnostics <- value }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	result, err := client.Request(context.Background(), "test/echo", map[string]any{})
	if err != nil || !bytes.Contains(result, []byte(`"echo":true`)) {
		t.Fatalf("echo result = %s, err = %v", result, err)
	}
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := client.DidOpen(path, "go", "package main\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case values := <-diagnostics:
		if len(values) != 1 || values[0].Code != "FAKE" || values[0].Path != path {
			t.Fatalf("diagnostics = %#v", values)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fake diagnostics")
	}
}

func TestFileURIPathRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "space name", "main.go")
	uri, err := pathToURI(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, "file:///") {
		t.Fatalf("URI = %q, want an absolute file URI", uri)
	}
	got, err := uriToPath(uri)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(got) != filepath.Clean(abs) {
		t.Fatalf("round-trip path = %q, want %q", got, abs)
	}
	if runtime.GOOS == "windows" {
		unc := `\\server\share\main.go`
		uncURI, err := pathToURI(unc)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(uncURI, "file://server/") {
			t.Fatalf("UNC URI = %q", uncURI)
		}
		uncPath, err := uriToPath(uncURI)
		if err != nil || filepath.Clean(uncPath) != filepath.Clean(unc) {
			t.Fatalf("UNC round trip = %q, err = %v", uncPath, err)
		}
	}
}

func TestFramingRoundTripWithAdditionalHeader(t *testing.T) {
	payload := []byte(`{"jsonrpc":"2.0","id":7,"result":{"ok":true}}`)
	wire := append([]byte("Content-Type: application/vscode-jsonrpc; charset=utf-8\r\n"), Frame(payload)...)
	got, err := ReadMessage(bufio.NewReader(bytes.NewReader(wire)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
}

func TestZotHomeEnvironmentPrecedence(t *testing.T) {
	configuredHome := filepath.Join(t.TempDir(), "zot")
	xdgHome := filepath.Join(t.TempDir(), "state")
	t.Setenv("ZOT_HOME", configuredHome)
	t.Setenv("XDG_STATE_HOME", xdgHome)
	if got := zotHome(); got != configuredHome {
		t.Fatalf("ZOT_HOME path = %q, want %q", got, configuredHome)
	}
	t.Setenv("ZOT_HOME", "")
	if got := zotHome(); got != filepath.Join(xdgHome, "zot") {
		t.Fatalf("XDG_STATE_HOME path = %q, want %q", got, filepath.Join(xdgHome, "zot"))
	}
}

func TestLoadConfigMergesGlobalProjectAndBuiltins(t *testing.T) {
	root := t.TempDir()
	zotHome := t.TempDir()
	t.Setenv("ZOT_HOME", zotHome)
	if err := os.WriteFile(filepath.Join(zotHome, "lsp.json"), []byte(`{"servers":{"gopls":{"settings":{"global":true},"args":["--global"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lsp.json"), []byte(`{"gopls":{"command":"local-gopls","args":["--local"],"settings":{"local":true}},"sarif-lint":{"command":"reviewdog","kind":"cli","parser":"sarif","fileTypes":[".go"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	gopls := config.Servers["gopls"]
	if gopls.Command != "local-gopls" || len(gopls.Args) != 1 || gopls.Args[0] != "--local" {
		t.Fatalf("gopls override = %#v", gopls)
	}
	if len(gopls.FileTypes) == 0 || len(gopls.RootMarkers) == 0 {
		t.Fatalf("builtin selectors were not retained: %#v", gopls)
	}
	if gopls.Settings["global"] != true || gopls.Settings["local"] != true {
		t.Fatalf("settings did not merge: %#v", gopls.Settings)
	}
	if config.Servers["sarif-lint"].Kind != "cli" || config.Servers["sarif-lint"].Parser != "sarif" {
		t.Fatalf("custom linter = %#v", config.Servers["sarif-lint"])
	}
	selected := config.ApplicableServers(root, filepath.Join(root, "main.go"))
	var foundGopls, foundSarif bool
	for _, server := range selected {
		foundGopls = foundGopls || server.ID == "gopls"
		foundSarif = foundSarif || server.ID == "sarif-lint"
	}
	if !foundGopls || !foundSarif {
		t.Fatalf("selected servers = %#v", selected)
	}
}

func TestLoadConfigAcceptsPiLSPBridgeProviders(t *testing.T) {
	root := t.TempDir()
	zotHome := t.TempDir()
	t.Setenv("ZOT_HOME", zotHome)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configFile := `{
		"$schema": "./node_modules/pi-lsp-bridge/schema.json",
		"autoDetect": true,
		"providers": [
			"gopls",
			{"id":"gopls","args":["serve"]},
			{"id":"bridge-lint","kind":"cli","command":"bridge-lint","fileTypes":[".go"],"rootMarkers":["go.mod"],"cli":{"parser":"generic","mode":"files"}}
		]
	}`
	if err := os.WriteFile(filepath.Join(root, "pi-lsp-bridge.json"), []byte(configFile), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := config.Servers["gopls"].Args; len(got) != 1 || got[0] != "serve" {
		t.Fatalf("gopls provider override = %#v", got)
	}
	lint := config.Servers["bridge-lint"]
	if lint.Kind != "cli" || lint.Parser != "generic" || lint.Mode != "files" {
		t.Fatalf("bridge provider = %#v", lint)
	}
}

func TestLoadConfigAutoDetectFalseKeepsExplicitProviders(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ZOT_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lsp.json"), []byte(`{"autoDetect":false,"providers":["gopls"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	selected := config.ApplicableServers(root, filepath.Join(root, "main.go"))
	if len(selected) != 1 || selected[0].ID != "gopls" {
		t.Fatalf("explicit providers with autoDetect=false = %#v", selected)
	}
}

func TestResolveCommandPrefersWorkspaceTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable bits are not represented on Windows")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "node_modules", ".bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(bin, "fake-lsp")
	if err := os.WriteFile(command, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCommand(root, "fake-lsp")
	if err != nil {
		t.Fatal(err)
	}
	if got != command {
		t.Fatalf("resolved %q, want %q", got, command)
	}
}

func TestParsers(t *testing.T) {
	root := t.TempDir()
	eslint := `[ {"filePath":"src/a.js","messages":[{"ruleId":"no-x","severity":2,"message":"bad","line":2,"column":3}] } ]`
	got := ParseDiagnostics("eslint-json", "eslint", root, eslint)
	if len(got) != 1 || got[0].Severity != "error" || got[0].Range.Start.Line != 1 {
		t.Fatalf("eslint = %#v", got)
	}
	golangci := `{"Issues":[{"FromLinter":"govet","Text":"bad","Severity":"warning","Pos":{"Filename":"a.go","Line":3,"Column":4}}]}`
	if got = ParseDiagnostics("golangci-lint-json", "golangci", root, golangci); len(got) != 1 || got[0].Severity != "warning" {
		t.Fatalf("golangci = %#v", got)
	}
	ruff := `[{"code":"F401","message":"unused","filename":"a.py","location":{"row":1,"column":2}}]`
	if got = ParseDiagnostics("ruff-json", "ruff", root, ruff); len(got) != 1 || got[0].Code != "F401" {
		t.Fatalf("ruff = %#v", got)
	}
	sarif := `{"runs":[{"results":[{"ruleId":"R1","level":"error","message":{"text":"bad"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"a.c"},"region":{"startLine":4,"startColumn":2}}}]}]}]}`
	if got = ParseDiagnostics("sarif", "scan", root, sarif); len(got) != 1 || got[0].Range.Start.Line != 3 {
		t.Fatalf("sarif = %#v", got)
	}
	if got = ParseDiagnostics("generic", "lint", root, "a.go:5:6: warning: bad"); len(got) != 1 || got[0].Severity != "warning" {
		t.Fatalf("generic = %#v", got)
	}
	if got = ParseDiagnostics("generic", "lint", root, "a.go:5:6: cannot find name"); len(got) != 1 || got[0].Message != "cannot find name" {
		t.Fatalf("generic message = %#v", got)
	}
}

func TestDiagnosticDedupUsesCodeAndStartPosition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.go")
	base := Diagnostic{Path: path, Severity: "error", Code: "E1", Message: "same", Range: Range{Start: Position{Line: 1, Character: 2}, End: Position{Line: 1, Character: 3}}}
	endChanged := base
	endChanged.Range.End.Character = 20
	otherCode := base
	otherCode.Code = "E2"
	otherStart := base
	otherStart.Range.Start.Line++
	if got := DeduplicateDiagnostics([]Diagnostic{base, endChanged, otherCode, otherStart}); len(got) != 3 {
		t.Fatalf("deduplicated diagnostics = %#v, want three reports", got)
	}
}

func TestManagerClearsPublishedDiagnostics(t *testing.T) {
	manager := NewManager()
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := manager.workspace(root)
	if err != nil {
		t.Fatal(err)
	}
	manager.storeDiagnostics(ws, "fake", []Diagnostic{{Path: path, Message: "problem"}})
	if got := manager.snapshot(ws, path); len(got) != 1 {
		t.Fatalf("stored diagnostics = %#v", got)
	}
	manager.storeDiagnostics(ws, "fake", []Diagnostic{{Path: path, Clear: true}})
	if got := manager.snapshot(ws, path); len(got) != 0 {
		t.Fatalf("cleared diagnostics = %#v", got)
	}
}

func TestDiagnosticsDedupAndGrouping(t *testing.T) {
	root := t.TempDir()
	makeDiag := func(path, message string, line int, server string) Diagnostic {
		return Diagnostic{Path: filepath.Join(root, path), Severity: "error", Code: "E", Message: message, Server: server, Range: Range{Start: Position{Line: line}}}
	}
	input := []Diagnostic{makeDiag("a.go", "same", 0, "gopls"), makeDiag("a.go", "same", 0, "eslint"), makeDiag("a.go", `Cannot find module "missing"`, 1, "gopls"), makeDiag("a.go", "cascade one", 2, "gopls"), makeDiag("a.go", "cascade two", 3, "gopls"), makeDiag("b.go", "repeat", 1, "a"), makeDiag("c.go", "repeat", 2, "b"), makeDiag("d.go", "repeat", 3, "c")}
	if got := DeduplicateDiagnostics(input); len(got) != len(input)-1 {
		t.Fatalf("dedup count = %d", len(got))
	}
	text := SummarizeDiagnostics(input, root, 50)
	if !strings.Contains(text, "repeated") || !strings.Contains(text, "SECONDARY") {
		t.Fatalf("summary lacks grouping:\n%s", text)
	}
}

func TestApplyWorkspaceEditUsesUTF16AndRejectsOutside(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("a😀b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := URIForPath(path)
	ed := WorkspaceEdit{Changes: map[string][]TextEdit{uri: {{Range: Range{Start: Position{Line: 0, Character: 1}, End: Position{Line: 0, Character: 3}}, NewText: "X"}}}}
	if err := ApplyWorkspaceEdit(root, ed); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "aXb\n" {
		t.Fatalf("edited data = %q", data)
	}
	outside := WorkspaceEdit{Changes: map[string][]TextEdit{URIForPath(filepath.Join(filepath.Dir(root), "outside.txt")): {{Range: Range{}, NewText: "x"}}}}
	if err := ApplyWorkspaceEdit(root, outside); err == nil {
		t.Fatal("outside edit was accepted")
	}
}

func TestWorkspaceEditPreflightsAllTargets(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside.txt")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(inside, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = prepareEditTargets(rootReal, map[string][]TextEdit{
		URIForPath(inside):  {{Range: Range{}, NewText: "inside"}},
		URIForPath(outside): {{Range: Range{}, NewText: "outside"}},
	})
	if err == nil {
		t.Fatal("workspace edit accepted an unsafe target after a safe target")
	}
}

func TestApplyWorkspaceEditFollowsInWorkspaceSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	link := filepath.Join(root, "link.txt")
	if err := os.WriteFile(target, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	edit := WorkspaceEdit{Changes: map[string][]TextEdit{URIForPath(link): {{Range: Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 3}}, NewText: "new"}}}}
	if err := ApplyWorkspaceEdit(root, edit); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new\n" {
		t.Fatalf("target data = %q", data)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("workspace edit replaced the symlink")
	}
}

func TestLimitedBufferCapsReaderCopy(t *testing.T) {
	var buffer limitedBuffer
	buffer.limit = 4
	copied, err := io.Copy(&buffer, strings.NewReader("123456"))
	if err != nil {
		t.Fatal(err)
	}
	if copied != 6 || buffer.String() != "1234" {
		t.Fatalf("copied %d bytes into %q", copied, buffer.String())
	}
}

func TestDiagnosticJSONCodeNumber(t *testing.T) {
	var diagnostic Diagnostic
	if err := json.Unmarshal([]byte(`{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"severity":2,"code":123,"message":"warning"}`), &diagnostic); err != nil {
		t.Fatal(err)
	}
	if diagnostic.Code != "123" || diagnostic.Severity != "warning" {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
}

func TestManagerClosePreventsNewWorkspaces(t *testing.T) {
	manager := NewManager()
	root := t.TempDir()
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Config(root); err == nil {
		t.Fatal("closed manager accepted a new workspace")
	}
	if err := manager.Reload(root); err == nil {
		t.Fatal("closed manager accepted a reload")
	}
}

func TestReduceDiagnosticsSuppressesLocationOnlyChanges(t *testing.T) {
	manager := NewManager()
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	first := Diagnostic{Path: path, Severity: "error", Message: "type mismatch", Range: Range{Start: Position{Line: 1, Character: 2}}}
	if got := manager.ReduceDiagnostics(root, path, []Diagnostic{first}); len(got) != 1 {
		t.Fatalf("first reduction returned %d diagnostics, want 1", len(got))
	}
	shifted := first
	shifted.Range.Start.Line = 20
	if got := manager.ReduceDiagnostics(root, path, []Diagnostic{shifted}); len(got) != 0 {
		t.Fatalf("shifted diagnostic was replayed: %#v", got)
	}
	if got := manager.ReduceDiagnostics(root, path, nil); len(got) != 0 {
		t.Fatalf("clear returned %#v", got)
	}
	if got := manager.ReduceDiagnostics(root, path, []Diagnostic{shifted}); len(got) != 1 {
		t.Fatalf("diagnostic did not reappear after clear: %#v", got)
	}
}
