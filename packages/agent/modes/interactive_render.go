package modes

import (
	"sync"

	"github.com/bnema/zut/packages/tui"
)

// renderRequest is deliberately a signal, not a frame. The render owner
// reads the newest synchronized interactive state when it wakes, so a burst
// replaces the one pending request instead of queueing stale snapshots.
type renderRequest struct {
	clear      bool
	invalidate bool
	theme      *tui.Theme
}

type latestFrameScheduler struct {
	wake chan struct{}
	done chan struct{}

	mu         sync.Mutex
	clear      bool
	invalidate bool
	theme      *tui.Theme
	stopped    bool
}

func newLatestFrameScheduler() *latestFrameScheduler {
	return &latestFrameScheduler{
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}
}

func (s *latestFrameScheduler) request(clear, invalidate bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return false
	}
	s.clear = s.clear || clear
	s.invalidate = s.invalidate || invalidate
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return true
}

func (s *latestFrameScheduler) requestTheme(theme tui.Theme) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return false
	}
	s.clear = true
	s.theme = &theme
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return true
}

func (s *latestFrameScheduler) next() (renderRequest, bool) {
	<-s.wake
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return renderRequest{}, false
	}
	req := renderRequest{clear: s.clear, invalidate: s.invalidate, theme: s.theme}
	s.clear = false
	s.invalidate = false
	s.theme = nil
	return req, true
}

func (s *latestFrameScheduler) run(render func(renderRequest)) {
	defer close(s.done)
	for {
		req, ok := s.next()
		if !ok {
			return
		}
		render(req)
	}
}

// stop waits for the in-flight render callback to finish. Callers must not
// invoke it while holding i.mu: the callback may need to acquire i.mu.
func (s *latestFrameScheduler) stop() {
	s.mu.Lock()
	if !s.stopped {
		s.stopped = true
		close(s.wake)
	}
	s.mu.Unlock()
	<-s.done
}
