package modes

import (
	"context"
	"errors"
	"testing"
)

func TestRunSlashCommandNameCaseInsensitive(t *testing.T) {
	i := &Interactive{}
	if !i.runSlash(context.Background(), "/Exit") {
		t.Fatal("/Exit did not dispatch to /exit")
	}
}

func TestRunSlashPreservesArgumentCase(t *testing.T) {
	var got string
	i := &Interactive{cfg: InteractiveConfig{
		ChangeCWD: func(path string) error {
			got = path
			return errors.New("stop after capture")
		},
	}}
	i.runSlash(context.Background(), "/CD /Users/Example/Mixed Case")
	if got != "/Users/Example/Mixed Case" {
		t.Fatalf("ChangeCWD path = %q, want original argument case", got)
	}
}
