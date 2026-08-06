package modes

import (
	"testing"
	"time"
)

func TestSpinnerFrameAtPreEpoch(t *testing.T) {
	spinner := &spinner{
		frames:   []string{"one", "two", "three"},
		interval: 80 * time.Millisecond,
	}

	if got, want := spinner.FrameAt(time.Unix(0, -80*int64(time.Millisecond))), "three"; got != want {
		t.Fatalf("FrameAt before Unix epoch = %q, want %q", got, want)
	}
}
