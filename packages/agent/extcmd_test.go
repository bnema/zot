package agent

import (
	"bytes"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/patriceckhart/zot/packages/agent/extensions"
)

// TestExtInstallDotSource verifies that `zot ext install .` resolves the
// source directory correctly instead of collapsing it to the extensions/
// parent directory.
func TestExtInstallDotSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZOT_HOME", home)

	// Pre-create extensions/ to mimic a normal first run.
	if err := os.MkdirAll(filepath.Join(home, "extensions"), 0o755); err != nil {
		t.Fatal(err)
	}

	srcParent := t.TempDir()
	src := filepath.Join(srcParent, "kagi")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extension.json"), []byte(`{"name":"kagi","exec":"go"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	if err := os.Chdir(src); err != nil {
		t.Fatal(err)
	}

	if err := extInstall([]string{"."}); err != nil {
		t.Fatalf("install with '.' failed: %v", err)
	}

	out := filepath.Join(home, "extensions", "kagi")
	if _, err := os.Stat(filepath.Join(out, "extension.json")); err != nil {
		t.Fatalf("expected installed extension at %s: %v", out, err)
	}
}

// TestExtInstallNamedDir verifies that a normal named source installs
// successfully when its manifest uses a PATH-resolved executable.
func TestExtInstallNamedDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZOT_HOME", home)

	src := filepath.Join(t.TempDir(), "myext")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extension.json"), []byte(`{"name":"myext","exec":"go"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := extInstall([]string{src}); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "extensions", "myext", "extension.json")); err != nil {
		t.Fatalf("expected installed extension: %v", err)
	}
}

func TestExtInstallThemeOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZOT_HOME", home)

	src := filepath.Join(t.TempDir(), "theme-source")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extension.json"), []byte(`{"name":"theme-only"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "theme.json"), []byte(`{"name":"Theme"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := extInstall([]string{src}); err != nil {
		t.Fatalf("theme-only install failed: %v", err)
	}
	out := filepath.Join(home, "extensions", "theme-only")
	for _, file := range []string{"extension.json", "theme.json"} {
		if _, err := os.Stat(filepath.Join(out, file)); err != nil {
			t.Fatalf("installed theme missing %s: %v", file, err)
		}
	}
}

func TestExtInstallRejectsMissingExecutable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZOT_HOME", home)

	src := filepath.Join(t.TempDir(), "tasked-phases")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extension.json"), []byte(`{"name":"tasked-phases","exec":"./tasked-phases","language":"go"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := extInstall([]string{src})
	if err == nil || !strings.Contains(err.Error(), "--build=go") {
		t.Fatalf("install error = %v, want explicit Go builder guidance", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "extensions", "tasked-phases")); !os.IsNotExist(statErr) {
		t.Fatalf("failed install left a destination: %v", statErr)
	}
}

func TestExtInstallBuildGo(t *testing.T) {
	if _, err := osexec.LookPath("go"); err != nil {
		t.Skip("go is not installed")
	}

	home := t.TempDir()
	t.Setenv("ZOT_HOME", home)
	src := filepath.Join(t.TempDir(), "go-extension")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("extension.json", `{"name":"built-go","exec":"./built-go","language":"go"}`)
	mustWrite("go.mod", "module example.com/built-go\n\ngo 1.25.0\n")
	mustWrite("main.go", "package main\n\nfunc main() {}\n")

	if err := extInstall([]string{"--build=go", src}); err != nil {
		t.Fatalf("build install failed: %v", err)
	}

	installed := filepath.Join(home, "extensions", "built-go", "built-go")
	info, err := os.Stat(installed)
	if err != nil {
		t.Fatalf("built executable was not installed: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed executable mode = %o, want an execute bit", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(src, "built-go")); !os.IsNotExist(err) {
		t.Fatalf("build wrote into the source directory: %v", err)
	}
}

func TestExtInstallBuildFailureLeavesNoDestination(t *testing.T) {
	if _, err := osexec.LookPath("go"); err != nil {
		t.Skip("go is not installed")
	}

	home := t.TempDir()
	t.Setenv("ZOT_HOME", home)
	src := filepath.Join(t.TempDir(), "broken-go")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extension.json"), []byte(`{"name":"broken-go","exec":"./broken-go"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module example.com/broken-go\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\n\nfunc main( {\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := extInstall([]string{"--build=go", src})
	if err == nil || !strings.Contains(err.Error(), "build extension with go") {
		t.Fatalf("build error = %v, want Go build failure", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "extensions", "broken-go")); !os.IsNotExist(statErr) {
		t.Fatalf("failed build left a destination: %v", statErr)
	}
	entries, err := os.ReadDir(filepath.Join(home, "extensions"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".zot-extension-install-") {
			t.Fatalf("failed build left staging directory %s", entry.Name())
		}
	}
}

func TestExtInstallRejectsUnknownBuilder(t *testing.T) {
	err := extInstall([]string{"--build=make", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "unsupported extension builder") {
		t.Fatalf("error = %v, want unsupported builder error", err)
	}
}

func TestParseExtInstallArgs(t *testing.T) {
	for _, test := range []struct {
		name    string
		args    []string
		builder string
		wantErr string
	}{
		{name: "equals", args: []string{"--build=go", "./ext"}, builder: "go"},
		{name: "separate", args: []string{"./ext", "--build", "go"}, builder: "go"},
		{name: "missing builder", args: []string{"--build=", "./ext"}, wantErr: "requires a builder"},
		{name: "missing source", args: []string{"--build=go"}, wantErr: "usage:"},
		{name: "extra source", args: []string{"./one", "./two"}, wantErr: "usage:"},
		{name: "unknown option", args: []string{"--wat", "./ext"}, wantErr: "unknown ext install option"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseExtInstallArgs(test.args)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if got.source != "./ext" || got.builder != test.builder {
				t.Fatalf("options = %#v, want source ./ext and builder %q", got, test.builder)
			}
		})
	}
}

func TestExtInstallRejectsBuildForThemeOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZOT_HOME", home)
	src := filepath.Join(t.TempDir(), "theme-source")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extension.json"), []byte(`{"name":"theme-only"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "theme.json"), []byte(`{"name":"Theme"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := extInstall([]string{"--build=go", src})
	if err == nil || !strings.Contains(err.Error(), "theme-only") {
		t.Fatalf("error = %v, want theme-only builder error", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "extensions", "theme-only")); !os.IsNotExist(statErr) {
		t.Fatalf("failed build left a destination: %v", statErr)
	}
}

func TestExtInstallRejectsBuildForPATHExecutable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZOT_HOME", home)
	src := filepath.Join(t.TempDir(), "path-extension")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extension.json"), []byte(`{"name":"path-extension","exec":"node"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := extInstall([]string{"--build=go", src})
	if err == nil || !strings.Contains(err.Error(), "extension-relative executable path") {
		t.Fatalf("error = %v, want local executable requirement", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "extensions", "path-extension")); !os.IsNotExist(statErr) {
		t.Fatalf("failed build left a destination: %v", statErr)
	}
}

func TestExtInstallRejectsNonExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable permissions are not enforced on Windows")
	}

	home := t.TempDir()
	t.Setenv("ZOT_HOME", home)
	src := filepath.Join(t.TempDir(), "non-executable")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extension.json"), []byte(`{"name":"non-executable","exec":"./run.sh","language":"shell"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "run.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := extInstall([]string{src})
	if err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("install error = %v, want executable permission guidance", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "extensions", "non-executable")); !os.IsNotExist(statErr) {
		t.Fatalf("failed install left a destination: %v", statErr)
	}
}

// TestExtInstallCopiesIgnoredExecutableAndUsesManifestName verifies that a
// declared runtime file is copied even when .gitignore matches it, and that
// the manifest name controls the installed directory.
func TestExtInstallCopiesIgnoredExecutableAndUsesManifestName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZOT_HOME", home)

	src := filepath.Join(t.TempDir(), "source-dir")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extension.json"), []byte(`{"name":"tasked-phases","exec":"./tasked-phases","language":"go"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".gitignore"), []byte("tasked-phases\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "tasked-phases"), []byte("not really a binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := extInstall([]string{src}); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	out := filepath.Join(home, "extensions", "tasked-phases")
	info, err := os.Stat(filepath.Join(out, "tasked-phases"))
	if err != nil {
		t.Fatalf("declared executable was not copied: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("copied executable mode = %o, want an execute bit", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(home, "extensions", "source-dir")); !os.IsNotExist(err) {
		t.Fatalf("install used source directory name instead of manifest name: %v", err)
	}
}

func TestCopyDirRespectsGitignore(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	mustWrite := func(rel, content string) {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("extension.json", `{"name":"x"}`)
	mustWrite("main.py", "print('hi')")
	mustWrite(".gitignore", ".venv/\nnode_modules/\n*.log\n")
	mustWrite(".venv/bin/python", "binary")
	mustWrite("node_modules/pkg/index.js", "module")
	mustWrite("debug.log", "noise")
	mustWrite("src/app.py", "code")
	mustWrite(".git/config", "gitdir")

	if err := copyDir(src, dst); err != nil {
		t.Fatal(err)
	}

	wantPresent := []string{"extension.json", "main.py", "src/app.py", ".gitignore"}
	for _, rel := range wantPresent {
		if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("expected %s to be copied: %v", rel, err)
		}
	}

	wantAbsent := []string{".venv", "node_modules", "debug.log", ".git"}
	for _, rel := range wantAbsent {
		if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(rel))); err == nil {
			t.Fatalf("expected %s to be skipped, but it was copied", rel)
		}
	}
}

func TestGitignoreNegation(t *testing.T) {
	g := loadGitignoreFromString("build/\n!build/keep.txt\n")
	if !g.Match("build", true) {
		t.Fatal("expected build/ dir to be ignored")
	}
	if g.Match("build/keep.txt", false) {
		t.Fatal("expected build/keep.txt to be re-included by negation")
	}
}

func TestExtDoctorStaticScanAndRender(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZOT_HOME", home)
	cwd := t.TempDir()
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldCWD)
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}

	mustWrite := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite(filepath.Join(cwd, ".zot", "extensions", "disabled", "extension.json"), `{"name":"disabled","exec":"./run.sh","enabled":false}`)
	mustWrite(filepath.Join(cwd, ".zot", "extensions", "theme", "extension.json"), `{"name":"theme"}`)
	mustWrite(filepath.Join(cwd, ".zot", "extensions", "theme", "theme.json"), `{"name":"Theme"}`)
	mustWrite(filepath.Join(cwd, ".zot", "extensions", "bad", "extension.json"), `{bad json`)
	mustWrite(filepath.Join(cwd, ".zot", "extensions", "dup", "extension.json"), `{"name":"dup","exec":"./project.sh"}`)
	mustWrite(filepath.Join(home, "extensions", "dup", "extension.json"), `{"name":"dup","exec":"./global.sh"}`)

	rows := scanExtDoctorStatic()
	if len(rows) != 5 {
		t.Fatalf("rows = %d, want 5: %#v", len(rows), rows)
	}

	var out bytes.Buffer
	for _, row := range rows {
		printExtDoctorRow(&out, row, extensions.ExtensionDiagnostic{})
	}
	got := out.String()
	for _, want := range []string{
		"disabled [project] disabled",
		"theme [project] theme-only",
		"bad [project] error",
		"dup [global] shadowed",
		"parse manifest:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, got)
		}
	}
}
