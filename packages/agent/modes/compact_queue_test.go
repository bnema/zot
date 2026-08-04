package modes

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
	"github.com/patriceckhart/zot/packages/tui"
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

	interactive.runCompact(context.Background(), false)
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

	mu    sync.Mutex
	calls int
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
			out <- provider.EventUsage{Usage: provider.Usage{InputTokens: 150000}}
			out <- provider.EventDone{
				Stop: provider.StopEnd,
				Message: provider.Message{
					Role:    provider.RoleAssistant,
					Content: []provider.Content{provider.ReasoningBlock{Summary: "still working"}},
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
			c.followUpRequest <- req
			out <- provider.EventDone{
				Stop: provider.StopEnd,
				Message: provider.Message{
					Role:    provider.RoleAssistant,
					Content: []provider.Content{provider.TextBlock{Text: "continued"}},
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

func TestContextOverflowErrorRecognizesProviderMessages(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "http 413", err: errors.New("provider http 413: request rejected"), want: true},
		{name: "context error code", err: errors.New("context_length_exceeded"), want: true},
		{name: "maximum context length", err: errors.New("This model's maximum context length is 128000 tokens"), want: true},
		{name: "context window exceeded", err: errors.New("context window exceeded"), want: true},
		{name: "maximum input token count", err: errors.New("input token count exceeds the maximum number of tokens allowed"), want: true},
		{name: "maximum output token count", err: errors.New("max_tokens exceeds the maximum number of tokens allowed"), want: false},
		{name: "unrelated error", err: errors.New("rate limit exceeded"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isContextOverflowError(tt.err); got != tt.want {
				t.Fatalf("isContextOverflowError(%q) = %t, want %t", tt.err, got, tt.want)
			}
		})
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
