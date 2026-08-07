package modes

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bnema/zut/packages/core"
	"github.com/bnema/zut/packages/provider"
	"github.com/bnema/zut/packages/tui"
)

type compactQueueClient struct {
	compactionStarted chan struct{}
	releaseCompaction chan struct{}
	followUpRequest   chan provider.Request

	mu    sync.Mutex
	calls int
}

func (c *compactQueueClient) Name() string { return "compact-queue-test" }

func (c *compactQueueClient) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()

	out := make(chan provider.Event, 2)
	go func() {
		defer close(out)
		if call == 1 {
			close(c.compactionStarted)
			select {
			case <-ctx.Done():
				out <- provider.EventDone{Stop: provider.StopAborted, Err: ctx.Err()}
			case <-c.releaseCompaction:
				out <- provider.EventTextDelta{Delta: "summary"}
				out <- provider.EventDone{Stop: provider.StopEnd}
			}
			return
		}

		c.followUpRequest <- req
		out <- provider.EventDone{
			Stop: provider.StopEnd,
			Message: provider.Message{
				Role:    provider.RoleAssistant,
				Content: []provider.Content{provider.TextBlock{Text: "done"}},
			},
		}
	}()
	return out, nil
}

type autoCompactPacerClient struct {
	compactionStarted chan struct{}
	releaseCompaction chan struct{}

	mu    sync.Mutex
	calls int
}

func (c *autoCompactPacerClient) Name() string { return "auto-compact-pacer-test" }

func (c *autoCompactPacerClient) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()

	out := make(chan provider.Event, 3)
	go func() {
		defer close(out)
		switch call {
		case 1:
			const response = "a final response that is still buffered for typewriter rendering"
			out <- provider.EventTextDelta{Delta: response}
			out <- provider.EventUsage{Usage: provider.Usage{InputTokens: 150000}}
			out <- provider.EventDone{
				Stop: provider.StopEnd,
				Message: provider.Message{
					Role:    provider.RoleAssistant,
					Content: []provider.Content{provider.TextBlock{Text: response}},
				},
			}
		case 2:
			close(c.compactionStarted)
			select {
			case <-ctx.Done():
				out <- provider.EventDone{Stop: provider.StopAborted, Err: ctx.Err()}
			case <-c.releaseCompaction:
				out <- provider.EventTextDelta{Delta: "summary"}
				out <- provider.EventDone{Stop: provider.StopEnd}
			}
		}
	}()
	return out, nil
}

func TestAutoCompactionSettlesFinalStreamingStateBeforeCompacting(t *testing.T) {
	client := &autoCompactPacerClient{
		compactionStarted: make(chan struct{}),
		releaseCompaction: make(chan struct{}),
	}
	agent := core.NewAgent(client, "test-model", "", nil)
	threshold := 70
	interactive := NewInteractive(InteractiveConfig{
		Agent:                agent,
		Provider:             "anthropic",
		Model:                "claude-sonnet-4-5-20250929",
		AutoCompactThreshold: &threshold,
	})
	interactive.runCtx = context.Background()

	// Do not start the pacer: the test controls the unpainted final delta so
	// it can prove compaction never races with a still-live streaming frame.
	interactive.startTurn(context.Background(), "initial request")
	select {
	case <-client.compactionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("auto-compaction did not start")
	}

	interactive.mu.Lock()
	streaming, pending := interactive.streaming.Len(), len(interactive.streamPending)
	streamOn, flushPending := interactive.streamOn, interactive.streamFlushPending
	interactive.mu.Unlock()
	if streaming != 0 || pending != 0 || streamOn || flushPending {
		t.Fatalf("auto-compaction started with live stream state: rendered=%d pending=%d on=%t flush=%t", streaming, pending, streamOn, flushPending)
	}

	close(client.releaseCompaction)
}

func TestPromptSubmittedDuringCompactionStartsFollowUpTurn(t *testing.T) {
	client := &compactQueueClient{
		compactionStarted: make(chan struct{}),
		releaseCompaction: make(chan struct{}),
		followUpRequest:   make(chan provider.Request, 1),
	}
	agent := core.NewAgent(client, "test-model", "", nil)
	agent.SetMessages([]provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "one"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "two"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "three"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "four"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "five"}}},
	})
	interactive := NewInteractive(InteractiveConfig{Agent: agent})
	interactive.runCtx = context.Background()

	interactive.runCompact(context.Background(), compactContinuationRequest{origin: compactOriginManual})
	select {
	case <-client.compactionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("compaction did not start")
	}

	interactive.ed.SetValue("follow up")
	interactive.handleKey(context.Background(), tui.Key{Kind: tui.KeyEnter})
	close(client.releaseCompaction)

	select {
	case req := <-client.followUpRequest:
		if !requestContainsUserText(req, "follow up") {
			t.Fatalf("follow-up request does not contain queued prompt: %#v", req.Messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued prompt did not start a turn after compaction")
	}
}

type thresholdAutoCompactClient struct {
	compactionStarted chan struct{}
	releaseCompaction chan struct{}
	followUpRequest   chan provider.Request
	firstStop         provider.StopReason
	firstContent      []provider.Content
	followUpContents  []string
	blockFollowUpCall int
	releaseFollowUp   <-chan struct{}

	mu        sync.Mutex
	calls     int
	followUps int
}

func (c *thresholdAutoCompactClient) Name() string { return "threshold-auto-compact-test" }

func (c *thresholdAutoCompactClient) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()

	out := make(chan provider.Event, 3)
	go func() {
		defer close(out)
		switch call {
		case 1:
			stop := c.firstStop
			if stop == "" {
				stop = provider.StopEnd
			}
			content := c.firstContent
			if len(content) == 0 {
				content = []provider.Content{provider.ReasoningBlock{Summary: "still working"}}
			}
			out <- provider.EventUsage{Usage: provider.Usage{InputTokens: 150000}}
			out <- provider.EventDone{
				Stop: stop,
				Message: provider.Message{
					Role:    provider.RoleAssistant,
					Content: content,
				},
			}
		case 2:
			close(c.compactionStarted)
			select {
			case <-ctx.Done():
				out <- provider.EventDone{Stop: provider.StopAborted, Err: ctx.Err()}
			case <-c.releaseCompaction:
				out <- provider.EventTextDelta{Delta: "summary"}
				out <- provider.EventDone{Stop: provider.StopEnd}
			}
		default:
			c.mu.Lock()
			c.followUps++
			followUp := call - 2
			text := "continued"
			if followUp > 0 && followUp <= len(c.followUpContents) {
				text = c.followUpContents[followUp-1]
			}
			c.mu.Unlock()
			c.followUpRequest <- req
			if c.blockFollowUpCall == call {
				select {
				case <-ctx.Done():
					out <- provider.EventDone{Stop: provider.StopAborted, Err: ctx.Err()}
					return
				case <-c.releaseFollowUp:
				}
			}
			out <- provider.EventDone{
				Stop: provider.StopEnd,
				Message: provider.Message{
					Role:    provider.RoleAssistant,
					Content: []provider.Content{provider.TextBlock{Text: text}},
				},
			}
		}
	}()
	return out, nil
}

func TestThresholdAutoCompactionContinuesMostRecentIntent(t *testing.T) {
	client := &thresholdAutoCompactClient{
		compactionStarted: make(chan struct{}),
		releaseCompaction: make(chan struct{}),
		followUpRequest:   make(chan provider.Request, 1),
	}
	agent := core.NewAgent(client, "test-model", "", nil)
	threshold := 70
	interactive := NewInteractive(InteractiveConfig{
		Agent:                agent,
		Provider:             "anthropic",
		Model:                "claude-sonnet-4-5-20250929",
		AutoCompactThreshold: &threshold,
	})
	interactive.runCtx = context.Background()

	interactive.startTurn(context.Background(), "initial request")
	select {
	case <-client.compactionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("threshold auto-compaction did not start")
	}
	close(client.releaseCompaction)

	select {
	case req := <-client.followUpRequest:
		if got := requestUserTextCount(req, autoCompactContinuationPrompt); got != 1 {
			t.Fatalf("continuation request contains auto-continue prompt %d times, want 1: %#v", got, req.Messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("threshold auto-compaction did not continue the most recent intent")
	}
}

func TestThresholdAutoCompactionRescuesStatusAfterCompaction(t *testing.T) {
	client := &thresholdAutoCompactClient{
		compactionStarted: make(chan struct{}),
		releaseCompaction: make(chan struct{}),
		followUpRequest:   make(chan provider.Request, 1),
		firstContent: []provider.Content{
			provider.TextBlock{Text: "I have completed this pass. Next I will inspect the remaining call sites."},
		},
	}
	agent := core.NewAgent(client, "test-model", "", nil)
	threshold := 70
	interactive := NewInteractive(InteractiveConfig{
		Agent:                agent,
		Provider:             "anthropic",
		Model:                "claude-sonnet-4-5-20250929",
		AutoCompactThreshold: &threshold,
	})
	interactive.runCtx = context.Background()

	interactive.startTurn(context.Background(), "initial request")
	select {
	case <-client.compactionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("threshold auto-compaction did not start")
	}
	close(client.releaseCompaction)

	select {
	case req := <-client.followUpRequest:
		if got := requestUserTextCount(req, autoCompactContinuationPrompt); got != 1 {
			t.Fatalf("status rescue request contains auto-continue prompt %d times, want 1: %#v", got, req.Messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("status rescue continuation did not start")
	}
	waitInteractiveIdle(t, interactive)

	client.mu.Lock()
	calls := client.calls
	client.mu.Unlock()
	if calls != 3 {
		t.Fatalf("provider called %d times, want initial request, compaction, and one status rescue", calls)
	}
}

func TestThresholdAutoCompactionBoundsSecondStatusRescue(t *testing.T) {
	client := &thresholdAutoCompactClient{
		compactionStarted: make(chan struct{}),
		releaseCompaction: make(chan struct{}),
		followUpRequest:   make(chan provider.Request, 2),
		firstContent: []provider.Content{
			provider.TextBlock{Text: "Next I will inspect the remaining call sites."},
		},
		followUpContents: []string{
			"Then I will run the targeted tests.",
			"done",
		},
	}
	agent := core.NewAgent(client, "test-model", "", nil)
	threshold := 70
	interactive := NewInteractive(InteractiveConfig{
		Agent:                agent,
		Provider:             "anthropic",
		Model:                "claude-sonnet-4-5-20250929",
		AutoCompactThreshold: &threshold,
	})
	interactive.runCtx = context.Background()

	interactive.startTurn(context.Background(), "initial request")
	select {
	case <-client.compactionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("threshold auto-compaction did not start")
	}
	close(client.releaseCompaction)

	for attempt := 1; attempt <= 2; attempt++ {
		select {
		case req := <-client.followUpRequest:
			if got := requestUserTextCount(req, autoCompactContinuationPrompt); got != attempt {
				t.Fatalf("status rescue %d contains auto-continue prompt %d times, want %d: %#v", attempt, got, attempt, req.Messages)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("bounded status rescue %d did not start", attempt)
		}
	}
	waitInteractiveIdle(t, interactive)

	client.mu.Lock()
	calls, followUps := client.calls, client.followUps
	client.mu.Unlock()
	if calls != 4 || followUps != 2 {
		t.Fatalf("provider calls/follow-ups = %d/%d, want 4/2", calls, followUps)
	}
}

func TestThresholdAutoCompactionStopsAfterStatusRescueCompletion(t *testing.T) {
	client := &thresholdAutoCompactClient{
		compactionStarted: make(chan struct{}),
		releaseCompaction: make(chan struct{}),
		followUpRequest:   make(chan provider.Request, 2),
		firstContent: []provider.Content{
			provider.TextBlock{Text: "Next I will inspect the remaining call sites."},
		},
		followUpContents: []string{"Then I will run the targeted tests.", "All required work is complete."},
	}
	agent := core.NewAgent(client, "test-model", "", nil)
	threshold := 70
	interactive := NewInteractive(InteractiveConfig{
		Agent:                agent,
		Provider:             "anthropic",
		Model:                "claude-sonnet-4-5-20250929",
		AutoCompactThreshold: &threshold,
	})
	interactive.runCtx = context.Background()

	interactive.startTurn(context.Background(), "initial request")
	select {
	case <-client.compactionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("threshold auto-compaction did not start")
	}
	close(client.releaseCompaction)
	for attempt := 1; attempt <= 2; attempt++ {
		select {
		case <-client.followUpRequest:
		case <-time.After(2 * time.Second):
			t.Fatalf("status rescue %d did not start", attempt)
		}
	}
	waitInteractiveIdle(t, interactive)

	client.mu.Lock()
	calls := client.calls
	client.mu.Unlock()
	if calls != 4 {
		t.Fatalf("provider called %d times after completion, want 4", calls)
	}
	select {
	case <-client.followUpRequest:
		t.Fatal("completion after bounded rescue started an extra request")
	default:
	}
}

func TestThresholdStatusRescueDefersToNewerUserPrompt(t *testing.T) {
	client := &thresholdAutoCompactClient{
		compactionStarted: make(chan struct{}),
		releaseCompaction: make(chan struct{}),
		followUpRequest:   make(chan provider.Request, 1),
		firstContent: []provider.Content{
			provider.TextBlock{Text: "Next I will inspect the remaining call sites."},
		},
	}
	agent := core.NewAgent(client, "test-model", "", nil)
	threshold := 70
	interactive := NewInteractive(InteractiveConfig{
		Agent:                agent,
		Provider:             "anthropic",
		Model:                "claude-sonnet-4-5-20250929",
		AutoCompactThreshold: &threshold,
	})
	interactive.runCtx = context.Background()

	interactive.startTurn(context.Background(), "initial request")
	select {
	case <-client.compactionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("threshold auto-compaction did not start")
	}
	interactive.SubmitOrQueue("newer user request", nil)
	close(client.releaseCompaction)

	select {
	case req := <-client.followUpRequest:
		if got := requestUserTextCount(req, "newer user request"); got != 1 {
			t.Fatalf("newer request count = %d, want 1: %#v", got, req.Messages)
		}
		if got := requestUserTextCount(req, autoCompactContinuationPrompt); got != 0 {
			t.Fatalf("superseded status rescue count = %d, want 0: %#v", got, req.Messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("newer user request did not run")
	}
	waitInteractiveIdle(t, interactive)
}

func TestThresholdStatusRescueQueuedUserCancelsFollowUpHandoff(t *testing.T) {
	releaseFollowUp := make(chan struct{})
	client := &thresholdAutoCompactClient{
		compactionStarted: make(chan struct{}),
		releaseCompaction: make(chan struct{}),
		followUpRequest:   make(chan provider.Request, 3),
		firstContent: []provider.Content{
			provider.TextBlock{Text: "Next I will inspect the remaining call sites."},
		},
		followUpContents: []string{
			"Then I will run the targeted tests.",
			"Next I will inspect the user's newly requested work.",
		},
		blockFollowUpCall: 3,
		releaseFollowUp:   releaseFollowUp,
	}
	agent := core.NewAgent(client, "test-model", "", nil)
	threshold := 70
	interactive := NewInteractive(InteractiveConfig{
		Agent:                agent,
		Provider:             "anthropic",
		Model:                "claude-sonnet-4-5-20250929",
		AutoCompactThreshold: &threshold,
	})
	interactive.runCtx = context.Background()

	interactive.startTurn(context.Background(), "initial request")
	select {
	case <-client.compactionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("threshold auto-compaction did not start")
	}
	close(client.releaseCompaction)

	select {
	case req := <-client.followUpRequest:
		if got := requestUserTextCount(req, autoCompactContinuationPrompt); got != 1 {
			t.Fatalf("first status rescue contains auto-continue prompt %d times, want 1: %#v", got, req.Messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first status rescue did not start")
	}

	interactive.SubmitOrQueue("newer user request", nil)
	close(releaseFollowUp)

	select {
	case req := <-client.followUpRequest:
		if got := requestUserTextCount(req, "newer user request"); got != 1 {
			t.Fatalf("newer request count = %d, want 1: %#v", got, req.Messages)
		}
		if got := requestUserTextCount(req, autoCompactContinuationPrompt); got != 1 {
			t.Fatalf("superseded follow-up rescue count = %d, want 1 existing prompt: %#v", got, req.Messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("newer user request did not run after the status rescue")
	}
	waitInteractiveIdle(t, interactive)

	client.mu.Lock()
	calls := client.calls
	client.mu.Unlock()
	if calls != 4 {
		t.Fatalf("provider called %d times, want initial request, compaction, status rescue, and newer user request", calls)
	}
	select {
	case req := <-client.followUpRequest:
		t.Fatalf("superseded handoff started an extra continuation: %#v", req.Messages)
	default:
	}
}

func TestThresholdAutoCompactionDoesNotRescueClearText(t *testing.T) {
	client := &thresholdAutoCompactClient{
		compactionStarted: make(chan struct{}),
		releaseCompaction: make(chan struct{}),
		followUpRequest:   make(chan provider.Request, 1),
		firstContent: []provider.Content{
			provider.TextBlock{Text: "All required work is complete."},
		},
	}
	agent := core.NewAgent(client, "test-model", "", nil)
	threshold := 70
	interactive := NewInteractive(InteractiveConfig{
		Agent:                agent,
		Provider:             "anthropic",
		Model:                "claude-sonnet-4-5-20250929",
		AutoCompactThreshold: &threshold,
	})
	interactive.runCtx = context.Background()

	interactive.startTurn(context.Background(), "initial request")
	select {
	case <-client.compactionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("threshold auto-compaction did not start")
	}
	close(client.releaseCompaction)
	waitInteractiveIdle(t, interactive)

	client.mu.Lock()
	calls := client.calls
	client.mu.Unlock()
	if calls != 2 {
		t.Fatalf("provider called %d times for clear text, want initial request and compaction", calls)
	}
	select {
	case req := <-client.followUpRequest:
		t.Fatalf("clear text unexpectedly started follow-up: %#v", req.Messages)
	default:
	}
}

func TestThresholdAutoCompactionContinuesTruncatedText(t *testing.T) {
	client := &thresholdAutoCompactClient{
		compactionStarted: make(chan struct{}),
		releaseCompaction: make(chan struct{}),
		followUpRequest:   make(chan provider.Request, 1),
		firstStop:         provider.StopLength,
		firstContent:      []provider.Content{provider.TextBlock{Text: "partial answer"}},
	}
	agent := core.NewAgent(client, "test-model", "", nil)
	threshold := 70
	interactive := NewInteractive(InteractiveConfig{
		Agent:                agent,
		Provider:             "anthropic",
		Model:                "claude-sonnet-4-5-20250929",
		AutoCompactThreshold: &threshold,
	})
	interactive.runCtx = context.Background()

	interactive.startTurn(context.Background(), "initial request")
	select {
	case <-client.compactionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("threshold auto-compaction did not start after truncated text")
	}
	close(client.releaseCompaction)

	select {
	case req := <-client.followUpRequest:
		if got := requestUserTextCount(req, autoCompactContinuationPrompt); got != 1 {
			t.Fatalf("truncated continuation request contains auto-continue prompt %d times, want 1: %#v", got, req.Messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("threshold auto-compaction did not continue truncated text")
	}
}

func TestThresholdAutoCompactionForcedContinuationPrecedesQueuedPrompt(t *testing.T) {
	client := &thresholdAutoCompactClient{
		compactionStarted: make(chan struct{}),
		releaseCompaction: make(chan struct{}),
		followUpRequest:   make(chan provider.Request, 2),
		firstStop:         provider.StopLength,
		firstContent:      []provider.Content{provider.TextBlock{Text: "partial answer"}},
	}
	agent := core.NewAgent(client, "test-model", "", nil)
	threshold := 70
	interactive := NewInteractive(InteractiveConfig{
		Agent:                agent,
		Provider:             "anthropic",
		Model:                "claude-sonnet-4-5-20250929",
		AutoCompactThreshold: &threshold,
	})
	interactive.runCtx = context.Background()

	interactive.startTurn(context.Background(), "initial request")
	select {
	case <-client.compactionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("threshold auto-compaction did not start after truncated text")
	}
	interactive.SubmitOrQueue("queued follow-up", nil)
	close(client.releaseCompaction)

	select {
	case req := <-client.followUpRequest:
		if got := requestUserTextCount(req, autoCompactContinuationPrompt); got != 1 {
			t.Fatalf("first request contains auto-continue prompt %d times, want 1: %#v", got, req.Messages)
		}
		if got := requestUserTextCount(req, "queued follow-up"); got != 0 {
			t.Fatalf("forced continuation request included queued prompt %d times, want 0: %#v", got, req.Messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("threshold auto-compaction did not continue truncated text")
	}

	select {
	case req := <-client.followUpRequest:
		if got := requestUserTextCount(req, "queued follow-up"); got != 1 {
			t.Fatalf("second request contains queued prompt %d times, want 1: %#v", got, req.Messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued prompt did not run after forced continuation")
	}

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		interactive.mu.Lock()
		idle := !interactive.busy
		interactive.mu.Unlock()
		if idle {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("interactive did not settle after forced continuation and queued prompt")
		case <-poll.C:
		}
	}

	client.mu.Lock()
	calls := client.calls
	followUps := client.followUps
	client.mu.Unlock()
	if calls != 4 {
		t.Fatalf("provider called %d times, want initial request, compaction, forced continuation, and queued prompt", calls)
	}
	if followUps != 2 {
		t.Fatalf("follow-up requests = %d, want forced continuation and queued prompt", followUps)
	}
}

func TestPreTurnCompactionPreservesPromptImages(t *testing.T) {
	client := &compactQueueClient{
		compactionStarted: make(chan struct{}),
		releaseCompaction: make(chan struct{}),
		followUpRequest:   make(chan provider.Request, 1),
	}
	agent := core.NewAgent(client, "test-model", "", nil)
	agent.SetMessages([]provider.Message{{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "one"}},
	}})
	threshold := 70
	interactive := NewInteractive(InteractiveConfig{
		Agent:                agent,
		Provider:             "anthropic",
		Model:                "claude-sonnet-4-5",
		AutoCompactThreshold: &threshold,
	})
	interactive.runCtx = context.Background()
	interactive.lastCtxInput = 150000
	image := provider.ImageBlock{MimeType: "image/png", Data: []byte("image-data")}

	interactive.startTurnWithImages(context.Background(), "follow up", []provider.ImageBlock{image})
	select {
	case <-client.compactionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("pre-turn compaction did not start")
	}
	close(client.releaseCompaction)

	select {
	case req := <-client.followUpRequest:
		if got := requestUserTextCount(req, "follow up"); got != 1 {
			t.Fatalf("follow-up request contains prompt %d times, want 1: %#v", got, req.Messages)
		}
		if got := requestImageCount(req, image); got != 1 {
			t.Fatalf("follow-up request contains image %d times, want 1: %#v", got, req.Messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pre-turn compaction did not start the pending image request")
	}
}

type contextRecoveryClient struct {
	mu            sync.Mutex
	calls         int
	retried       chan provider.Request
	overflowRetry bool
}

func (c *contextRecoveryClient) Name() string { return "context-recovery-test" }

func (c *contextRecoveryClient) Stream(_ context.Context, req provider.Request) (<-chan provider.Event, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()

	out := make(chan provider.Event, 2)
	go func() {
		defer close(out)
		switch call {
		case 1:
			out <- provider.EventDone{
				Stop: provider.StopError,
				Err:  errors.New("provider error: Your input exceeds the context window of this model. Please adjust your input and try again."),
			}
		case 2:
			out <- provider.EventTextDelta{Delta: "summary"}
			out <- provider.EventDone{Stop: provider.StopEnd}
		default:
			c.retried <- req
			if c.overflowRetry {
				out <- provider.EventDone{
					Stop: provider.StopError,
					Err:  errors.New("context window exceeded after compaction"),
				}
				return
			}
			out <- provider.EventDone{
				Stop: provider.StopEnd,
				Message: provider.Message{
					Role:    provider.RoleAssistant,
					Content: []provider.Content{provider.TextBlock{Text: "done"}},
				},
			}
		}
	}()
	return out, nil
}

func TestContextWindowErrorCompactsAndRetriesPromptOnce(t *testing.T) {
	client := &contextRecoveryClient{retried: make(chan provider.Request, 1)}
	agent := core.NewAgent(client, "test-model", "", nil)
	agent.SetMessages([]provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "one"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "two"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "three"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "four"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "five"}}},
	})
	interactive := NewInteractive(InteractiveConfig{Agent: agent})
	interactive.runCtx = context.Background()

	image := provider.ImageBlock{MimeType: "image/png", Data: []byte("image-data")}
	interactive.startTurnWithImages(context.Background(), "retry me", []provider.ImageBlock{image})

	select {
	case req := <-client.retried:
		if got := requestUserTextCount(req, "retry me"); got != 1 {
			t.Fatalf("retried request contains original prompt %d times, want 1: %#v", got, req.Messages)
		}
		if got := requestImageCount(req, image); got != 1 {
			t.Fatalf("retried request contains original image %d times, want 1: %#v", got, req.Messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context-window error did not compact and retry the prompt")
	}
}

func TestContextWindowRecoveryStopsAfterOneRetry(t *testing.T) {
	client := &contextRecoveryClient{
		retried:       make(chan provider.Request, 2),
		overflowRetry: true,
	}
	agent := core.NewAgent(client, "test-model", "", nil)
	agent.SetMessages([]provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "one"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "two"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "three"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "four"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "five"}}},
	})
	interactive := NewInteractive(InteractiveConfig{Agent: agent})
	interactive.runCtx = context.Background()

	interactive.startTurn(context.Background(), "still too large")
	select {
	case <-client.retried:
	case <-time.After(2 * time.Second):
		t.Fatal("context-window recovery did not retry")
	}

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		interactive.mu.Lock()
		idleWithError := !interactive.busy && interactive.statusErr != ""
		interactive.mu.Unlock()
		if idleWithError {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("second context-window error did not settle")
		case <-poll.C:
		}
	}

	client.mu.Lock()
	calls := client.calls
	client.mu.Unlock()
	if calls != 3 {
		t.Fatalf("provider called %d times, want initial request, compaction, and one retry", calls)
	}
}

func requestContainsUserText(req provider.Request, want string) bool {
	return requestUserTextCount(req, want) > 0
}

func requestUserTextCount(req provider.Request, want string) int {
	count := 0
	for _, message := range req.Messages {
		if message.Role != provider.RoleUser {
			continue
		}
		for _, content := range message.Content {
			if text, ok := content.(provider.TextBlock); ok && text.Text == want {
				count++
			}
		}
	}
	return count
}

func requestImageCount(req provider.Request, want provider.ImageBlock) int {
	count := 0
	for _, message := range req.Messages {
		if message.Role != provider.RoleUser {
			continue
		}
		for _, content := range message.Content {
			if image, ok := content.(provider.ImageBlock); ok && image.MimeType == want.MimeType && string(image.Data) == string(want.Data) {
				count++
			}
		}
	}
	return count
}
