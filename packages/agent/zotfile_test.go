package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patriceckhart/zot/packages/agent/tools"
)

func TestReadZotfileConsentKeyAcceptsOnlyYesNo(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  byte
	}{
		{name: "lowercase yes", input: []byte{'y'}, want: 'y'},
		{name: "uppercase yes", input: []byte{'Y'}, want: 'y'},
		{name: "lowercase no", input: []byte{'n'}, want: 'n'},
		{name: "uppercase no", input: []byte{'N'}, want: 'n'},
		{name: "ignore unrelated keys", input: append([]byte("x\r\n\x1b[A"), 'y'), want: 'y'},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readZotfileConsentKey(bytes.NewReader(tt.input))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("answer = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadZotfileConsentKeyInterruptsOnCtrlC(t *testing.T) {
	if _, err := readZotfileConsentKey(bytes.NewReader([]byte{'x', 3, 'y'})); !errors.Is(err, errZotfileConsentInterrupted) {
		t.Fatalf("error = %v, want interrupted", err)
	}
}

func TestReadZotfileConsentKeyReportsInputError(t *testing.T) {
	if _, err := readZotfileConsentKey(strings.NewReader("xxx")); !errors.Is(err, io.EOF) {
		t.Fatalf("error = %v, want EOF", err)
	}
}

func writeTestZotfile(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("Be useful."), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadZotfileRejectsUnenforcedPermissions(t *testing.T) {
	for _, field := range []string{
		`"net":{"allow":["example.com"]}`,
		`"env":{"read":["HOME"]}`,
	} {
		dir := writeTestZotfile(t, `{"zotfile":1,"name":"test","permissions":{`+field+`}}`)
		if _, _, err := loadZotfile(dir); err == nil {
			t.Fatalf("manifest with %s was accepted", field)
		}
	}
}

func TestLoadZotfileRejectsUnsafeOrCollidingNames(t *testing.T) {
	for _, name := range []string{"...", "Name", "two words", "a/b"} {
		dir := writeTestZotfile(t, `{"zotfile":1,"name":"`+name+`"}`)
		if _, _, err := loadZotfile(dir); err == nil {
			t.Fatalf("unsafe manifest name %q was accepted", name)
		}
	}
}

func TestResolveZotfileRefUsesLocalFirstThenOfficialCollection(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Mkdir("local-agent", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("packed-agent.zot", []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		ref  string
		want string
	}{
		{"local-agent", "local-agent"},
		{"packed-agent", "packed-agent.zot"},
		{"remote-agent", officialZotfileCollection + "/remote-agent"},
		{"missing.zot", "missing.zot"},
		{"agents/remote-agent", "https://github.com/patriceckhart/agents/remote-agent"},
		{`agents\remote-agent`, "https://github.com/patriceckhart/agents/remote-agent"},
		{"./missing-agent", "./missing-agent"},
		{"Remote-Agent", "Remote-Agent"},
		{"https://github.com/acme/agents/example", "https://github.com/acme/agents/example"},
	}
	for _, tt := range tests {
		got, err := resolveZotfileRef(tt.ref)
		if err != nil {
			t.Fatalf("resolve %q: %v", tt.ref, err)
		}
		if got != tt.want {
			t.Errorf("resolve %q = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestLoadZotfileRejectsBundledExecutableExtension(t *testing.T) {
	dir := writeTestZotfile(t, `{"zotfile":1,"name":"test"}`)
	ext := filepath.Join(dir, "extensions", "bad")
	if err := os.MkdirAll(ext, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ext, "extension.json"), []byte(`{"name":"bad"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadZotfile(dir); err == nil || !strings.Contains(err.Error(), "cannot yet be confined") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckZotfileMinVersion(t *testing.T) {
	var zf zotfileLoaded
	zf.Manifest.Runtime.MinZot = "0.3.0"
	if err := checkZotfileRequirements(zf, "0.2.75"); err == nil {
		t.Fatal("old zot version accepted")
	}
	if err := checkZotfileRequirements(zf, "0.3.0"); err != nil {
		t.Fatalf("minimum version rejected: %v", err)
	}
}

func TestApplyZotfileModelRequirementsRejectsUnsupportedFields(t *testing.T) {
	var m ZotfileManifest
	m.Model.MinTier = "frontier"
	if err := applyZotfileModelRequirements(&Args{}, m); err == nil {
		t.Fatal("unsupported min_tier was ignored")
	}
	m.Model.MinTier = ""
	m.Model.Requires = []string{"audio"}
	if err := applyZotfileModelRequirements(&Args{}, m); err == nil {
		t.Fatal("unsupported capability was ignored")
	}
}

func TestUntarRejectsTraversalAndOversizedEntry(t *testing.T) {
	makeTar := func(name string, size int64) []byte {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: size}); err != nil {
			t.Fatal(err)
		}
		_ = tw.Close()
		return buf.Bytes()
	}
	if err := untar(bytes.NewReader(makeTar("../escape", 0)), t.TempDir()); err == nil {
		t.Fatal("path traversal accepted")
	}
	if err := untar(bytes.NewReader(makeTar("large", maxZotfileEntrySize+1)), t.TempDir()); err == nil {
		t.Fatal("oversized entry accepted")
	}
}

func TestParseGitHubAgentURL(t *testing.T) {
	tests := []struct {
		input                 string
		owner, repo, ref, dir string
	}{
		{"https://github.com/patriceckhart/agents/zot-maintenance", "patriceckhart", "agents", "HEAD", "zot-maintenance"},
		{"https://github.com/patriceckhart/agents/tree/main/zot-maintenance", "patriceckhart", "agents", "main", "zot-maintenance"},
		{"https://github.com/acme/agent.git", "acme", "agent", "HEAD", ""},
	}
	for _, tt := range tests {
		u, err := url.Parse(tt.input)
		if err != nil {
			t.Fatal(err)
		}
		owner, repo, ref, dir, err := parseGitHubAgentURL(u)
		if err != nil {
			t.Fatalf("parse %s: %v", tt.input, err)
		}
		if owner != tt.owner || repo != tt.repo || ref != tt.ref || dir != tt.dir {
			t.Fatalf("parse %s = %q, %q, %q, %q", tt.input, owner, repo, ref, dir)
		}
	}
}

func TestLoadRemoteZotfileDownloadsTemporaryGitHubArchive(t *testing.T) {
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	files := map[string]string{
		"agents-main/zot-maintenance/manifest.json": `{"zotfile":1,"name":"zot-maintenance"}`,
		"agents-main/zot-maintenance/AGENT.md":      "Maintain zot.",
	}
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(archive.Bytes())
	}))
	defer server.Close()
	oldArchiveURL := githubArchiveURL
	githubArchiveURL = func(_, _, _ string) string { return server.URL }
	t.Cleanup(func() { githubArchiveURL = oldArchiveURL })

	u, _ := url.Parse("https://github.com/patriceckhart/agents/zot-maintenance")
	zf, cleanup, err := loadRemoteZotfile(u)
	if err != nil {
		t.Fatal(err)
	}
	if zf.Manifest.Name != "zot-maintenance" || !zf.Temp {
		t.Fatalf("unexpected zotfile: %+v", zf)
	}
	root := filepath.Dir(filepath.Dir(zf.Dir))
	cleanup()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("temporary checkout was not removed: %v", err)
	}
}

func TestPermissionSummaryShowsDeniedScopes(t *testing.T) {
	got := permissionSummary(tools.PermissionSet{})
	if !strings.Contains(got, "fs read: none") || !strings.Contains(got, "fs write: none") {
		t.Fatalf("summary did not show denied scopes:\n%s", got)
	}
}
