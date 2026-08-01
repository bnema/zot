package agent

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestRunZotfileStartupPreShellFailure(t *testing.T) {
	pre := "!false"
	if runtime.GOOS == "windows" {
		pre = "!exit 1"
	}
	err := runZotfileStartupPre(context.Background(), pre, t.TempDir(), nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected entry.pre shell failure")
	}
	if !strings.Contains(err.Error(), "entry.pre") {
		t.Fatalf("error = %v, want entry.pre context", err)
	}
}

func TestRunZotfileStartupPreEmpty(t *testing.T) {
	if err := runZotfileStartupPre(context.Background(), "  ", t.TempDir(), nil, nil, nil, nil); err != nil {
		t.Fatalf("empty pre: %v", err)
	}
}

func TestRunZotfileStartupPreShellStreams(t *testing.T) {
	cmd := "!printf streamed-pre"
	if runtime.GOOS == "windows" {
		cmd = "!echo streamed-pre"
	}
	var out strings.Builder
	if err := runZotfileStartupPre(context.Background(), cmd, t.TempDir(), nil, nil, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "streamed-pre") {
		t.Fatalf("shell output not streamed: %q", out.String())
	}
}
