package modes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
