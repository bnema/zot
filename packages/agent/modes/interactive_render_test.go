package modes

import (
	"strings"
	"sync"
	"testing"

	"github.com/bnema/zut/packages/core"
	"github.com/bnema/zut/packages/provider"
	"github.com/bnema/zut/packages/tui"
)

func TestLatestFrameSchedulerKeepsNewestRequest(t *testing.T) {
	s := newLatestFrameScheduler()
	started := make(chan struct{})
	release := make(chan struct{})
	type renderedFrame struct {
		req     renderRequest
		version int
	}
	frames := make(chan renderedFrame, 4)
	done := make(chan struct{})
	var startedOnce sync.Once
	latestVersion := 0
	go func() {
		s.run(func(req renderRequest) {
			frames <- renderedFrame{req: req, version: latestVersion}
			startedOnce.Do(func() { close(started) })
			<-release
		})
		close(done)
	}()

	latestVersion = 1
	if !s.request(false) {
		t.Fatal("initial render request was rejected")
	}
	first := <-frames
	<-started
	if first.req.clear {
		t.Fatal("ordinary request unexpectedly requested a clear")
	}
	for n := 0; n < 1000; n++ {
		latestVersion = n + 2
		if !s.request(false) {
			t.Fatal("request rejected before shutdown")
		}
	}
	close(release)
	second := <-frames
	if second.req.clear {
		t.Fatal("coalesced ordinary request unexpectedly requested a clear")
	}
	if second.version != 1001 {
		t.Fatalf("renderer did not observe newest state: got version %d", second.version)
	}
	s.stop()
	<-done

	select {
	case <-frames:
		t.Fatal("scheduler retained more than one pending frame")
	default:
	}
}

func TestStableChatCacheTracksViewInvalidation(t *testing.T) {
	i := &Interactive{
		view: &tui.View{
			Theme: tui.Dark,
			Messages: []provider.Message{{
				Role:    provider.RoleUser,
				Content: []provider.Content{provider.TextBlock{Text: "cache theme"}},
			}},
			MessagesRevision: 1,
		},
	}

	i.mu.Lock()
	before := strings.Join(i.stableChatRowsLocked(80), "\n")
	i.view.Theme = tui.Light
	i.view.InvalidateRenderCache()
	after := strings.Join(i.stableChatRowsLocked(80), "\n")
	i.mu.Unlock()

	if before == after {
		t.Fatal("stable chat cache reused rows after a theme invalidation")
	}
}

func TestInteractiveToolProgressStormDoesNotInvalidate(t *testing.T) {
	i := &Interactive{
		dirty:     make(chan struct{}, 1),
		toolCalls: make(map[string]*tui.ToolCallView),
		toolGate:  make(map[string]int),
	}
	i.handleEventForPresentation(core.EvToolUseStart{ID: "storm", Name: "bash"})
	select {
	case <-i.dirty:
	default:
		t.Fatal("tool start did not invalidate the presentation")
	}

	const progressEvents = 4096
	for n := 0; n < progressEvents; n++ {
		i.handleEventForPresentation(core.EvToolProgress{
			ID:   "storm",
			Text: strings.Repeat("progress payload ", 256),
		})
	}
	select {
	case <-i.dirty:
		t.Fatal("invisible progress created presentation invalidation")
	default:
	}

	result := strings.Repeat("completed tool output\n", 256)
	i.handleEventForPresentation(core.EvToolResult{
		ID: "storm",
		Result: core.ToolResult{Content: []provider.Content{
			provider.TextBlock{Text: result},
		}},
	})
	select {
	case <-i.dirty:
	default:
		t.Fatal("completed tool result did not invalidate the presentation")
	}
	if got := i.toolCalls["storm"].Result; got != result {
		t.Fatalf("completed tool result was not retained: got %d bytes", len(got))
	}
	if i.toolCalls["storm"].Revision == 0 {
		t.Fatal("completed tool result did not advance its render revision")
	}

	view := &tui.View{Theme: tui.Dark, ExpandAll: false, ToolCalls: []tui.ToolCallView{*i.toolCalls["storm"]}}
	rows := view.BuildLive(80)
	if len(rows) == 0 {
		t.Fatal("completed tool result produced no live frame rows")
	}
}
