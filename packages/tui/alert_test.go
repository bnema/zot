package tui

import (
	"bytes"
	"testing"
)

func TestWriteBellWritesStandaloneAlert(t *testing.T) {
	var out bytes.Buffer
	if err := WriteBell(&out); err != nil {
		t.Fatalf("WriteBell() error = %v", err)
	}
	if got := out.String(); got != "\a" {
		t.Fatalf("WriteBell() = %q, want standalone BEL", got)
	}
}
