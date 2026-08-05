package bot

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bnema/zut/packages/core"
	"github.com/bnema/zut/packages/provider"
)

type testAdapter struct{}

func (testAdapter) Run(context.Context, func(InboundMessage), func(Command, InboundMessage)) error {
	return nil
}
func (testAdapter) Send(context.Context, string, string, SendOptions) error {
	return nil
}
func (testAdapter) IndicateWorking(context.Context, string) func() { return func() {} }
func (testAdapter) StatusText() string                             { return "" }

type blockingClient struct {
	started chan struct{}
	release chan struct{}

	mu        sync.Mutex
	active    int
	maxActive int
}

func (c *blockingClient) Name() string { return "test" }

func (c *blockingClient) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	c.mu.Lock()
	c.active++
	if c.active > c.maxActive {
		c.maxActive = c.active
	}
	c.mu.Unlock()

	select {
	case c.started <- struct{}{}:
	default:
	}

	out := make(chan provider.Event, 1)
	go func() {
		defer close(out)
		defer func() {
			c.mu.Lock()
			c.active--
			c.mu.Unlock()
		}()
		select {
		case <-ctx.Done():
			out <- provider.EventDone{Stop: provider.StopAborted, Err: ctx.Err()}
		case <-c.release:
			out <- provider.EventDone{Stop: provider.StopEnd, Message: provider.Message{
				Role:    provider.RoleAssistant,
				Content: []provider.Content{provider.TextBlock{Text: "ok"}},
			}}
		}
	}()
	return out, nil
}

func (c *blockingClient) max() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxActive
}

func TestHandleMessageClaimsDrainSlotBeforeSpawningDrainer(t *testing.T) {
	client := &blockingClient{started: make(chan struct{}, 2), release: make(chan struct{})}
	r := NewRunner(testAdapter{}, core.NewAgent(client, "test-model", "", nil), Config{})
	r.runCtx = context.Background()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		r.handleMessage(InboundMessage{ChannelID: "c", Text: "one"})
	}()
	go func() {
		defer wg.Done()
		r.handleMessage(InboundMessage{ChannelID: "c", Text: "two"})
	}()
	wg.Wait()

	select {
	case <-client.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first turn did not start")
	}

	// Give any accidentally spawned second drainer a chance to enter Stream.
	time.Sleep(100 * time.Millisecond)
	if got := client.max(); got != 1 {
		t.Fatalf("concurrent provider streams = %d, want 1", got)
	}

	close(client.release)
}
