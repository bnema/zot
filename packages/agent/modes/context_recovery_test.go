package modes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/bnema/zut/packages/core"
	"github.com/bnema/zut/packages/provider"
)

type hostRecoveryClient struct {
	mu            sync.Mutex
	calls         int
	retried       chan provider.Request
	retryOverflow bool
}

func (c *hostRecoveryClient) Name() string { return "host-context-recovery-test" }

type proactiveContextClient struct {
	mu             sync.Mutex
	requests       []provider.Request
	summaryErr     error
	onSummary      func()
	normalOverflow bool
}

func (c *proactiveContextClient) Name() string { return "proactive-context-test" }

func (c *proactiveContextClient) Stream(_ context.Context, req provider.Request) (<-chan provider.Event, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	c.mu.Unlock()

	events := make(chan provider.Event, 2)
	go func() {
		defer close(events)
		if strings.Contains(req.System, "context summarization assistant") {
			if c.onSummary != nil {
				c.onSummary()
			}
			if c.summaryErr != nil {
				events <- provider.EventDone{Stop: provider.StopError, Err: c.summaryErr}
				return
			}
			events <- provider.EventTextDelta{Delta: "condensed history"}
			events <- provider.EventDone{Stop: provider.StopEnd}
			return
		}
		if c.normalOverflow {
			events <- provider.EventDone{Stop: provider.StopError, Err: errors.New("context window exceeded")}
			return
		}
		events <- provider.EventDone{Stop: provider.StopEnd, Message: provider.Message{
			Role:    provider.RoleAssistant,
			Content: []provider.Content{provider.TextBlock{Text: "completed"}},
		}}
	}()
	return events, nil
}

func TestPromptWithContextRecoveryProactivelyCompactsCacheOnlyUsage(t *testing.T) {
	client := &proactiveContextClient{}
	agent := core.NewAgent(client, "model", "system", nil)
	history := []provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "one"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "two"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "three"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "four"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "five"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "six"}}},
	}
	agent.SetMessages(history)
	agent.SeedLastTurnUsage(provider.Usage{CacheReadTokens: 85})

	var persisted [][]provider.Message
	result, err := PromptWithContextRecovery(context.Background(), agent, "continue", nil, nil, ContextRecoveryOptions{
		ContextWindow: 100,
		PersistCompaction: func(messages []provider.Message) error {
			persisted = append(persisted, messages)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("PromptWithContextRecovery: %v", err)
	}
	if !result.Compacted || len(persisted) != 1 {
		t.Fatalf("compacted/persisted = %t/%d, want true/1", result.Compacted, len(persisted))
	}
	if got := recoveredAssistantText(agent.Messages()[result.OutputStart:]); got != "completed" {
		t.Fatalf("output after compacted boundary = %q, want completed", got)
	}

	client.mu.Lock()
	requests := append([]provider.Request(nil), client.requests...)
	client.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d, want compaction then prompt", len(requests))
	}
	if !recoveryRequestContainsText(requests[0], "<conversation>") || !recoveryRequestContainsText(requests[0], "one") {
		t.Fatalf("first request was not compaction over old history: %#v", requests[0])
	}
	if got := requestUserTextCount(requests[1], "continue"); got != 1 {
		t.Fatalf("prompt count after proactive compaction = %d, want 1", got)
	}
}

func TestPromptWithContextRecoveryHonorsDisabledThreshold(t *testing.T) {
	client := &proactiveContextClient{}
	agent := core.NewAgent(client, "model", "system", nil)
	agent.SetMessages([]provider.Message{{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "history"}}}})
	agent.SeedLastTurnUsage(provider.Usage{InputTokens: 100, CacheReadTokens: 100})
	disabled := 0

	result, err := PromptWithContextRecovery(context.Background(), agent, "continue", nil, nil, ContextRecoveryOptions{
		ContextWindow:        100,
		AutoCompactThreshold: &disabled,
	})
	if err != nil {
		t.Fatalf("PromptWithContextRecovery: %v", err)
	}
	if result.Compacted {
		t.Fatal("disabled threshold unexpectedly compacted")
	}
	client.mu.Lock()
	requests := append([]provider.Request(nil), client.requests...)
	client.mu.Unlock()
	if len(requests) != 1 || requestUserTextCount(requests[0], "continue") != 1 {
		t.Fatalf("requests with disabled threshold = %#v, want one direct prompt", requests)
	}
}

func TestPromptWithContextRecoveryRestoresProactiveCompactionOnPersistenceFailure(t *testing.T) {
	client := &proactiveContextClient{}
	agent := core.NewAgent(client, "model", "system", nil)
	history := []provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "original"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "answer"}}},
	}
	agent.SetMessages(history)
	agent.SeedLastTurnUsage(provider.Usage{CacheWriteTokens: 90})

	_, err := PromptWithContextRecovery(context.Background(), agent, "must not send", nil, nil, ContextRecoveryOptions{
		ContextWindow: 100,
		PersistCompaction: func([]provider.Message) error {
			return errors.New("disk full")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "persist compacted transcript: disk full") {
		t.Fatalf("error = %v, want persistence failure", err)
	}
	if got := agent.Messages(); !reflect.DeepEqual(got, history) {
		t.Fatalf("transcript after persistence failure = %#v, want %#v", got, history)
	}
	client.mu.Lock()
	requestCount := len(client.requests)
	client.mu.Unlock()
	if requestCount != 1 {
		t.Fatalf("provider requests = %d, want compaction only", requestCount)
	}
}

func TestPromptWithContextRecoveryDoesNotProactivelyCompactSoleTask(t *testing.T) {
	client := &proactiveContextClient{}
	agent := core.NewAgent(client, "model", "system", nil)
	task := provider.Message{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "exact sole task"}}}
	agent.SetMessages([]provider.Message{task})
	agent.SeedLastTurnUsage(provider.Usage{CacheReadTokens: 95})

	result, err := PromptWithContextRecovery(context.Background(), agent, "continue", nil, nil, ContextRecoveryOptions{ContextWindow: 100})
	if err != nil {
		t.Fatalf("PromptWithContextRecovery: %v", err)
	}
	if result.Compacted {
		t.Fatal("sole task was proactively compacted")
	}
	client.mu.Lock()
	requests := append([]provider.Request(nil), client.requests...)
	client.mu.Unlock()
	if len(requests) != 1 || !recoveryRequestContainsText(requests[0], "exact sole task") {
		t.Fatalf("requests = %#v, want one direct request retaining the sole task", requests)
	}
}

func TestPromptWithContextRecoveryFallsBackWhenProactiveSummaryOverflows(t *testing.T) {
	client := &proactiveContextClient{summaryErr: errors.New("context window exceeded while summarizing")}
	agent := core.NewAgent(client, "model", "system", nil)
	history := []provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "history"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "answer"}}},
	}
	agent.SetMessages(history)
	agent.SeedLastTurnUsage(provider.Usage{InputTokens: 90})

	result, err := PromptWithContextRecovery(context.Background(), agent, "same task", nil, nil, ContextRecoveryOptions{ContextWindow: 100})
	if err != nil {
		t.Fatalf("PromptWithContextRecovery: %v", err)
	}
	if result.Compacted {
		t.Fatal("overflowing proactive summary reported compaction")
	}
	client.mu.Lock()
	requests := append([]provider.Request(nil), client.requests...)
	client.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d, want one summary and one normal prompt", len(requests))
	}
	if got := requestUserTextCount(requests[1], "same task"); got != 1 {
		t.Fatalf("normal recovery prompt count = %d, want 1", got)
	}
}

func TestPromptWithContextRecoveryStopsBeforeContinuationWhenPersistenceCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &proactiveContextClient{}
	agent := core.NewAgent(client, "model", "system", nil)
	history := []provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "history"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "answer"}}},
	}
	agent.SetMessages(history)
	agent.SeedLastTurnUsage(provider.Usage{CacheWriteTokens: 90})
	persisted := 0

	result, err := PromptWithContextRecovery(ctx, agent, "must not run", nil, nil, ContextRecoveryOptions{
		ContextWindow: 100,
		PersistCompaction: func([]provider.Message) error {
			persisted++
			cancel()
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if !result.Compacted || persisted != 1 {
		t.Fatalf("compaction result/persistence = %v/%d, want true/1", result.Compacted, persisted)
	}
	client.mu.Lock()
	requestCount := len(client.requests)
	client.mu.Unlock()
	if requestCount != 1 {
		t.Fatalf("provider requests = %d, want summary only", requestCount)
	}
}

func TestPromptWithContextRecoveryRollsBackWhenCanceledAfterSummary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &proactiveContextClient{onSummary: cancel}
	agent := core.NewAgent(client, "model", "system", nil)
	history := []provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "history"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "answer"}}},
	}
	agent.SetMessages(history)
	agent.SeedLastTurnUsage(provider.Usage{CacheWriteTokens: 90})
	persisted := 0

	_, err := PromptWithContextRecovery(ctx, agent, "must not run", nil, nil, ContextRecoveryOptions{
		ContextWindow: 100,
		PersistCompaction: func([]provider.Message) error {
			persisted++
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if persisted != 0 {
		t.Fatalf("persistence calls = %d, want 0", persisted)
	}
	if got := agent.Messages(); !reflect.DeepEqual(got, history) {
		t.Fatalf("transcript after cancellation = %#v, want %#v", got, history)
	}
	client.mu.Lock()
	requestCount := len(client.requests)
	client.mu.Unlock()
	if requestCount != 1 {
		t.Fatalf("provider requests = %d, want summary only", requestCount)
	}
}

func TestPromptWithContextRecoveryRepairsToolPairAtCompactionBoundary(t *testing.T) {
	client := &proactiveContextClient{}
	agent := core.NewAgent(client, "model", "system", nil)
	agent.SetMessages([]provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "old"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.ToolCallBlock{ID: "split-call", Name: "read"}}},
		{Role: provider.RoleTool, Content: []provider.Content{provider.ToolResultBlock{CallID: "split-call", Content: []provider.Content{provider.TextBlock{Text: "result"}}}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "after tool"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "next"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "ready"}}},
	})
	agent.SeedLastTurnUsage(provider.Usage{InputTokens: 90})

	if _, err := PromptWithContextRecovery(context.Background(), agent, "continue", nil, nil, ContextRecoveryOptions{ContextWindow: 100}); err != nil {
		t.Fatalf("PromptWithContextRecovery: %v", err)
	}
	client.mu.Lock()
	requests := append([]provider.Request(nil), client.requests...)
	client.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d, want summary then continuation", len(requests))
	}
	for _, message := range requests[1].Messages {
		for _, content := range message.Content {
			if result, ok := content.(provider.ToolResultBlock); ok && result.CallID == "split-call" {
				t.Fatalf("continuation contains orphaned tool result: %#v", requests[1].Messages)
			}
		}
	}
}

func recoveryRequestContainsText(req provider.Request, want string) bool {
	for _, message := range req.Messages {
		for _, content := range message.Content {
			if text, ok := content.(provider.TextBlock); ok && strings.Contains(text.Text, want) {
				return true
			}
		}
	}
	return false
}

func (c *hostRecoveryClient) Stream(_ context.Context, req provider.Request) (<-chan provider.Event, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()

	events := make(chan provider.Event, 2)
	go func() {
		defer close(events)
		switch call {
		case 1:
			events <- provider.EventDone{Stop: provider.StopError, Err: errors.New("provider error: input exceeds the context window")}
		case 2:
			events <- provider.EventTextDelta{Delta: "summary"}
			events <- provider.EventDone{Stop: provider.StopEnd}
		default:
			c.retried <- req
			if c.retryOverflow {
				events <- provider.EventDone{Stop: provider.StopError, Err: errors.New("context window exceeded after compaction")}
				return
			}
			events <- provider.EventDone{Stop: provider.StopEnd, Message: provider.Message{
				Role:    provider.RoleAssistant,
				Content: []provider.Content{provider.TextBlock{Text: "recovered"}},
			}}
		}
	}()
	return events, nil
}

func TestPromptWithContextRecoveryCompactsOnceAndSuppressesInitialOverflow(t *testing.T) {
	client := &hostRecoveryClient{retried: make(chan provider.Request, 1)}
	agent := core.NewAgent(client, "test-model", "", nil)
	agent.SetMessages([]provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "one"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "two"}}},
	})

	var sawOverflowTurnEnd bool
	compacted := false
	result, err := PromptWithContextRecovery(context.Background(), agent, "finish the task", nil, func(event core.AgentEvent) {
		if turnEnd, ok := event.(core.EvTurnEnd); ok && provider.IsContextOverflowError(turnEnd.Err) {
			sawOverflowTurnEnd = true
		}
	}, ContextRecoveryOptions{
		PersistCompaction: func([]provider.Message) error {
			compacted = true
			return nil
		},
		SuppressInitialOverflowEvent: true,
	})
	if err != nil {
		t.Fatalf("PromptWithContextRecovery returned %v", err)
	}
	if !result.Compacted || !compacted {
		t.Fatalf("compaction result/persistence = %v/%v, want true/true", result.Compacted, compacted)
	}
	if sawOverflowTurnEnd {
		t.Fatal("recoverable initial overflow turn_end reached the sink")
	}
	if got := recoveredAssistantText(agent.Messages()[result.OutputStart:]); got != "recovered" {
		t.Fatalf("recovery output = %q, want recovered", got)
	}
	request := <-client.retried
	if got := requestUserTextCount(request, "finish the task"); got != 1 {
		t.Fatalf("retried request prompt count = %d, want 1: %#v", got, request.Messages)
	}
}

func recoveredAssistantText(messages []provider.Message) string {
	for _, message := range messages {
		if message.Role != provider.RoleAssistant {
			continue
		}
		for _, content := range message.Content {
			if text, ok := content.(provider.TextBlock); ok {
				return text.Text
			}
		}
	}
	return ""
}

func TestPromptWithContextRecoveryReportsRetryOverflowToSink(t *testing.T) {
	client := &hostRecoveryClient{retried: make(chan provider.Request, 1), retryOverflow: true}
	agent := core.NewAgent(client, "test-model", "", nil)
	agent.SetMessages([]provider.Message{{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "prior context"}}}})

	overflowTurnEnds := 0
	_, err := PromptWithContextRecovery(context.Background(), agent, "finish the task", nil, func(event core.AgentEvent) {
		if turnEnd, ok := event.(core.EvTurnEnd); ok && provider.IsContextOverflowError(turnEnd.Err) {
			overflowTurnEnds++
		}
	}, ContextRecoveryOptions{SuppressInitialOverflowEvent: true})
	if !provider.IsContextOverflowError(err) {
		t.Fatalf("PromptWithContextRecovery error = %v, want retry context overflow", err)
	}
	if overflowTurnEnds != 1 {
		t.Fatalf("overflow turn_end count = %d, want retry overflow only", overflowTurnEnds)
	}
	client.mu.Lock()
	calls := client.calls
	client.mu.Unlock()
	if calls != 3 {
		t.Fatalf("provider calls = %d, want prompt, compaction, and one continuation", calls)
	}
}

func newRecoveredModeAgent() *core.Agent {
	return core.NewAgent(&hostRecoveryClient{retried: make(chan provider.Request, 1)}, "test-model", "", nil)
}

func TestRunPrintRecoversContextOverflowWithoutLeakingIntermediateOutput(t *testing.T) {
	agent := newRecoveredModeAgent()
	agent.SetMessages([]provider.Message{{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "prior context"}}}})
	var output bytes.Buffer

	if _, err := RunPrint(context.Background(), agent, "finish the task", nil, &output); err != nil {
		t.Fatalf("RunPrint returned %v", err)
	}
	if got := output.String(); got != "recovered\n" {
		t.Fatalf("stdout = %q, want recovered only", got)
	}
}

func TestRunStreamRecoversContextOverflowWithoutDiagnosticsLeak(t *testing.T) {
	agent := newRecoveredModeAgent()
	agent.SetMessages([]provider.Message{{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "prior context"}}}})
	var output, diagnostics bytes.Buffer

	if err := RunStreamWithDiag(context.Background(), agent, "finish the task", nil, &output, &diagnostics); err != nil {
		t.Fatalf("RunStreamWithDiag returned %v", err)
	}
	if got := output.String(); got != "recovered\n" {
		t.Fatalf("stdout = %q, want recovered assistant text once", got)
	}
	if got := diagnostics.String(); got != "" {
		t.Fatalf("stderr diagnostics = %q, want no context-overflow diagnostic", got)
	}
}

func TestRunJSONRecoversContextOverflowWithJSONLOnly(t *testing.T) {
	agent := newRecoveredModeAgent()
	agent.SetMessages([]provider.Message{{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "prior context"}}}})
	var output bytes.Buffer

	if err := RunJSON(context.Background(), agent, "finish the task", nil, &output); err != nil {
		t.Fatalf("RunJSON returned %v", err)
	}

	var (
		turnEnds int
		done     int
	)
	scanner := bufio.NewScanner(&output)
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("stdout line is not JSONL: %q: %v", scanner.Text(), err)
		}
		switch event["type"] {
		case "error":
			t.Fatalf("recoverable overflow wrote error frame: %#v", event)
		case "turn_end":
			turnEnds++
			if _, hasError := event["error"]; hasError {
				t.Fatalf("recoverable overflow wrote terminal turn_end: %#v", event)
			}
		case "done":
			done++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan JSONL: %v", err)
	}
	if turnEnds != 1 || done != 1 {
		t.Fatalf("turn_end/done count = %d/%d, want one recovered terminal sequence", turnEnds, done)
	}
}
