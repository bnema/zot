package modes

import (
	"time"

	"github.com/bnema/zut/packages/tui"
)

// spinner drives the busy animation shown while an operation is active. The
// operation label is owned by the caller's activity state; the spinner owns
// only the visual frame and elapsed time.
type spinner struct {
	frames    []string
	interval  time.Duration
	startedAt time.Time
}

// newSpinner constructs a fresh spinner.
func newSpinner(th tui.Theme) *spinner {
	s := &spinner{}
	s.Configure(th)
	return s
}

func (s *spinner) Configure(th tui.Theme) {
	s.frames = append([]string(nil), th.SpinnerFrames...)
	if len(s.frames) == 0 {
		s.frames = []string{"⠋", "⠙", "⠚", "⠞", "⠖", "⠦", "⠴", "⠲", "⠳", "⠓"}
	}
	interval := th.SpinnerIntervalMS
	if interval <= 0 {
		interval = 80
	}
	s.interval = time.Duration(interval) * time.Millisecond
}

// Start resets the animation clock for a new operation.
func (s *spinner) Start() {
	s.startedAt = time.Now()
}

// Frame returns the current spinner glyph for the running animation.
func (s *spinner) Frame() string {
	if len(s.frames) == 0 {
		return ""
	}
	if s.startedAt.IsZero() {
		return s.frames[0]
	}
	interval := s.interval
	if interval <= 0 {
		interval = 80 * time.Millisecond
	}
	elapsed := time.Since(s.startedAt)
	idx := int(elapsed/interval) % len(s.frames)
	return s.frames[idx]
}

// FrameAt returns a frame based on an absolute clock. Independent background
// activity uses it so its animation does not need to reset or interfere with
// the main turn's spinner lifecycle.
func (s *spinner) FrameAt(now time.Time) string {
	if len(s.frames) == 0 {
		return ""
	}
	if now.IsZero() {
		now = time.Now()
	}
	interval := s.interval
	if interval <= 0 {
		interval = 80 * time.Millisecond
	}
	ticks := now.UnixNano() / int64(interval)
	idx := int(ticks % int64(len(s.frames)))
	if idx < 0 {
		idx += len(s.frames)
	}
	return s.frames[idx]
}

// Elapsed returns the wall-clock duration the spinner has been running.
func (s *spinner) Elapsed() time.Duration {
	if s.startedAt.IsZero() {
		return 0
	}
	return time.Since(s.startedAt).Round(time.Second)
}
