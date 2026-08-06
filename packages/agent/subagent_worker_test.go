package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bnema/zut/packages/agent/subagents"
	"github.com/bnema/zut/packages/core"
	"github.com/bnema/zut/packages/provider"
)

// TestWorkerOutputLimitsUseSupervisorPolicy verifies that the child reads
// the effective output caps propagated by the supervisor and retains safe
// defaults for standalone workers.
func TestWorkerOutputLimitsUseSupervisorPolicy(t *testing.T) {
	t.Setenv("ZUT_SUBAGENT_MAX_OUTPUT_BYTES", "123")
	t.Setenv("ZUT_SUBAGENT_MAX_OUTPUT_LINES", "7")
	bytesLimit, linesLimit := workerOutputLimits()
	if bytesLimit != 123 || linesLimit != 7 {
		t.Fatalf("worker output limits = (%d, %d), want (123, 7)", bytesLimit, linesLimit)
	}
	t.Setenv("ZUT_SUBAGENT_MAX_OUTPUT_BYTES", "invalid")
	t.Setenv("ZUT_SUBAGENT_MAX_OUTPUT_LINES", "0")
	bytesLimit, linesLimit = workerOutputLimits()
	if bytesLimit != 500_000 || linesLimit != 5_000 {
		t.Fatalf("invalid worker output limits = (%d, %d), want defaults", bytesLimit, linesLimit)
	}
}

type subagentContextRecoveryClient struct {
	mu            sync.Mutex
	calls         int
	requests      []provider.Request
	retried       chan provider.Request
	compactionErr error
	retryOverflow bool
}

func (c *subagentContextRecoveryClient) Name() string { return "subagent-context-recovery-test" }

func (c *subagentContextRecoveryClient) Stream(_ context.Context, req provider.Request) (<-chan provider.Event, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.requests = append(c.requests, req)
	c.mu.Unlock()

	events := make(chan provider.Event, 2)
	go func() {
		defer close(events)
		switch call {
		case 1:
			events <- provider.EventDone{Stop: provider.StopError, Err: errors.New("provider error: input exceeds the context window")}
		case 2:
			if c.compactionErr != nil {
				events <- provider.EventDone{Stop: provider.StopError, Err: c.compactionErr}
				return
			}
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

func TestPromptWithContextRecoveryCompactsAndContinues(t *testing.T) {
	client := &subagentContextRecoveryClient{retried: make(chan provider.Request, 1)}
	agent := core.NewAgent(client, "test-model", "", nil)
	agent.SetMessages([]provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "one"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "two"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "three"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "four"}}},
	})

	outputStart, err := promptWithContextRecovery(context.Background(), agent, "finish the task", nil, nil)
	if err != nil {
		t.Fatalf("promptWithContextRecovery returned %v", err)
	}
	if got := finalAssistantText(agent.Messages()[outputStart:]); got != "recovered" {
		t.Fatalf("final assistant text = %q, want recovered", got)
	}

	request := <-client.retried
	if got := workerRequestUserTextCount(request, "finish the task"); got != 1 {
		t.Fatalf("retried request contains prompt %d times, want 1: %#v", got, request.Messages)
	}
	client.mu.Lock()
	calls := client.calls
	requests := append([]provider.Request(nil), client.requests...)
	client.mu.Unlock()
	if calls != 3 || len(requests) != 3 {
		t.Fatalf("provider calls/requests = %d/%d, want 3/3", calls, len(requests))
	}
	if !workerRequestContainsText(requests[1], "<conversation>") || !workerRequestContainsText(requests[1], "one") {
		t.Fatalf("compaction request does not include the older transcript: %#v", requests[1].Messages)
	}
}

func TestPromptWithContextRecoveryStopsAfterOneRetry(t *testing.T) {
	client := &subagentContextRecoveryClient{
		retried:       make(chan provider.Request, 1),
		retryOverflow: true,
	}
	agent := core.NewAgent(client, "test-model", "", nil)
	agent.SetMessages([]provider.Message{{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "prior context"}}}})

	_, err := promptWithContextRecovery(context.Background(), agent, "finish the task", nil, nil)
	if !provider.IsContextOverflowError(err) {
		t.Fatalf("promptWithContextRecovery error = %v, want context overflow", err)
	}
	<-client.retried
	client.mu.Lock()
	calls := client.calls
	client.mu.Unlock()
	if calls != 3 {
		t.Fatalf("provider calls = %d, want one initial prompt, compaction, and one continuation", calls)
	}
}

func TestPromptWithContextRecoveryDoesNotCompactAfterCancellation(t *testing.T) {
	client := &subagentContextRecoveryClient{retried: make(chan provider.Request, 1)}
	agent := core.NewAgent(client, "test-model", "", nil)
	agent.SetMessages([]provider.Message{{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "prior context"}}}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := promptWithContextRecovery(ctx, agent, "finish the task", func(event core.AgentEvent) {
		if turnEnd, ok := event.(core.EvTurnEnd); ok && provider.IsContextOverflowError(turnEnd.Err) {
			cancel()
		}
	}, nil)
	if !provider.IsContextOverflowError(err) {
		t.Fatalf("promptWithContextRecovery error = %v, want context overflow", err)
	}
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("context error = %v, want %v", err, context.Canceled)
	}
	client.mu.Lock()
	calls := client.calls
	client.mu.Unlock()
	if calls != 1 {
		t.Fatalf("provider calls = %d, want only the initial prompt after cancellation", calls)
	}
}

func TestPromptWithContextRecoveryPropagatesCompactionFailure(t *testing.T) {
	client := &subagentContextRecoveryClient{
		retried:       make(chan provider.Request, 1),
		compactionErr: errors.New("compaction provider unavailable"),
	}
	agent := core.NewAgent(client, "test-model", "", nil)
	agent.SetMessages([]provider.Message{{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "prior context"}}}})

	_, err := promptWithContextRecovery(context.Background(), agent, "finish the task", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "compact transcript after initial overflow") {
		t.Fatalf("promptWithContextRecovery error = %v, want compaction failure", err)
	}
	payload := resultErrorPayload(err)
	if payload["code"] != "turn_failed" {
		t.Fatalf("result error payload = %#v, want turn_failed", payload)
	}
}

func TestPromptWithContextRecoveryKeepsSinglePromptIntact(t *testing.T) {
	client := &subagentContextRecoveryClient{retried: make(chan provider.Request, 1)}
	agent := core.NewAgent(client, "test-model", "", nil)

	_, err := promptWithContextRecovery(context.Background(), agent, "finish the task", nil, nil)
	if !provider.IsContextOverflowError(err) {
		t.Fatalf("promptWithContextRecovery error = %v, want context overflow", err)
	}
	client.mu.Lock()
	calls := client.calls
	requests := append([]provider.Request(nil), client.requests...)
	client.mu.Unlock()
	if len(requests) != 1 || workerRequestUserTextCount(requests[0], "finish the task") != 1 {
		t.Fatalf("initial request does not retain the prompt: %#v", requests)
	}
	if len(agent.Messages()) != 1 || workerRequestMessageTextCount(agent.Messages(), "finish the task") != 1 {
		t.Fatalf("single prompt was not retained: %#v", agent.Messages())
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1 without compaction", calls)
	}
}

func TestPromptWithContextRecoveryRetainsPromptWhenPersistenceFails(t *testing.T) {
	client := &subagentContextRecoveryClient{retried: make(chan provider.Request, 1)}
	history := []provider.Message{{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "prior context"}}}}
	session, err := core.NewSession(t.TempDir(), "cwd", "provider", "model", "test")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := session.AppendMessage(history[0]); err != nil {
		t.Fatalf("append history: %v", err)
	}
	agent := core.NewAgent(client, "test-model", "", nil)
	agent.SetMessages(history)

	outputStart, err := promptWithContextRecovery(context.Background(), agent, "finish the task", nil, func([]provider.Message) error {
		return errors.New("persist failed")
	})
	if err == nil || !strings.Contains(err.Error(), "persist compacted transcript") {
		t.Fatalf("promptWithContextRecovery error = %v, want persistence failure", err)
	}
	if got := workerRequestMessageTextCount(agent.Messages(), "prior context"); got != 1 || workerRequestMessageTextCount(agent.Messages(), "finish the task") != 1 {
		t.Fatalf("agent transcript lost the pending task after persistence failure: %#v", agent.Messages())
	}
	WriteNewTranscript(agent, session, outputStart)
	if err := session.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}
	reopened, messages, err := core.OpenSession(session.Path)
	if err != nil {
		t.Fatalf("reopen session: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened session: %v", err)
		}
	})
	if got := workerRequestMessageTextCount(messages, "finish the task"); got != 1 {
		t.Fatalf("persisted prompt count = %d, want 1: %#v", got, messages)
	}
	client.mu.Lock()
	calls := client.calls
	client.mu.Unlock()
	if calls != 2 {
		t.Fatalf("provider calls = %d, want initial prompt and compaction only", calls)
	}
}

func TestPromptWithContextRecoveryPersistsCompaction(t *testing.T) {
	client := &subagentContextRecoveryClient{retried: make(chan provider.Request, 1)}
	history := []provider.Message{
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "one"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "two"}}},
		{Role: provider.RoleUser, Content: []provider.Content{provider.TextBlock{Text: "three"}}},
		{Role: provider.RoleAssistant, Content: []provider.Content{provider.TextBlock{Text: "four"}}},
	}
	session, err := core.NewSession(t.TempDir(), "cwd", "provider", "model", "test")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	for _, message := range history {
		if err := session.AppendMessage(message); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}

	agent := core.NewAgent(client, "test-model", "", nil)
	agent.SetMessages(history)
	outputStart, err := promptWithContextRecovery(context.Background(), agent, "finish the task", nil, session.AppendCompaction)
	if err != nil {
		t.Fatalf("promptWithContextRecovery returned %v", err)
	}
	WriteNewTranscript(agent, session, outputStart)
	if err := session.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}

	records, err := os.ReadFile(session.Path)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	compactions := 0
	for _, record := range bytes.Split(bytes.TrimSpace(records), []byte("\n")) {
		var entry struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(record, &entry); err != nil {
			t.Fatalf("decode session record: %v", err)
		}
		if entry.Type == "compaction" {
			compactions++
		}
	}
	if compactions != 1 {
		t.Fatalf("compaction records = %d, want 1", compactions)
	}

	reopened, messages, err := core.OpenSession(session.Path)
	if err != nil {
		t.Fatalf("reopen session: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened session: %v", err)
		}
	})
	if got := workerRequestMessageTextCount(messages, "finish the task"); got != 1 {
		t.Fatalf("persisted prompt count = %d, want 1: %#v", got, messages)
	}
	if got := finalAssistantText(messages); got != "two\nfour\nrecovered" {
		t.Fatalf("persisted assistant text = %q, want prior tail plus recovered result", got)
	}
}

func workerRequestMessageTextCount(messages []provider.Message, want string) int {
	count := 0
	for _, message := range messages {
		for _, block := range message.Content {
			if text, ok := block.(provider.TextBlock); ok && text.Text == want {
				count++
			}
		}
	}
	return count
}

func workerRequestContainsText(req provider.Request, want string) bool {
	for _, message := range req.Messages {
		for _, block := range message.Content {
			if text, ok := block.(provider.TextBlock); ok && strings.Contains(text.Text, want) {
				return true
			}
		}
	}
	return false
}

func workerRequestUserTextCount(req provider.Request, want string) int {
	count := 0
	for _, message := range req.Messages {
		if message.Role != provider.RoleUser {
			continue
		}
		for _, block := range message.Content {
			if text, ok := block.(provider.TextBlock); ok && text.Text == want {
				count++
			}
		}
	}
	return count
}

func TestResultErrorPayloadSanitizesContextOverflow(t *testing.T) {
	payload := resultErrorPayload(errors.New("provider error: input exceeds the context window; echoed task text"))
	if payload["code"] != "context_limit" {
		t.Fatalf("error code = %q, want context_limit", payload["code"])
	}
	if payload["message"] != subagentContextLimitMessage {
		t.Fatalf("error message = %q, want %q", payload["message"], subagentContextLimitMessage)
	}
}

func TestSubagentEventDataSanitizesContextOverflow(t *testing.T) {
	payload := subagentEventData(core.EvTurnEnd{Err: errors.New("provider error: input exceeds the context window; echoed task text")})
	if payload["error"] != subagentContextLimitMessage {
		t.Fatalf("event error = %q, want %q", payload["error"], subagentContextLimitMessage)
	}
}

// TestSupervisorEmitterMirrorDormantUntilStdoutBreaks regresses the
// "everything is doubled after reopening a subagent agent" bug.
//
// Symptom: events.jsonl held two copies of every event because the
// child mirrored each event to disk AND the supervisor parsed the
// child's stdout and appended each event to disk too. On next zut
// launch the replay produced two transcript lines per real one.
//
// Fix invariant: while stdout writes succeed (i.e. the supervisor is
// alive on the other end of the pipe), the child's mirror writes
// NOTHING. Only when a stdout write fails (broken pipe → orphan)
// does the mirror take over so events still get persisted.
func TestSupervisorEmitterMirrorDormantUntilStdoutBreaks(t *testing.T) {
	// Real *os.File for the emitter's stdout-equivalent so the
	// emitter's write() path (which expects *os.File) actually runs.
	stdoutPath := filepath.Join(t.TempDir(), "stdout.fifo")
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatalf("create stdout file: %v", err)
	}
	defer stdoutFile.Close()

	// Mirror writes go to a separate events.jsonl that we can read
	// at the end to assert how many events the mirror emitted.
	mirrorPath := filepath.Join(t.TempDir(), "events.jsonl")
	mirror, err := subagents.OpenEventLog(mirrorPath)
	if err != nil {
		t.Fatalf("open mirror: %v", err)
	}
	defer mirror.Close()

	em := newSubagentEmitter(stdoutFile, mirror)

	// Healthy stdout: emit three events. Mirror must stay empty.
	em.emit("turn_start", map[string]any{"step": 1})
	em.emit("assistant_message", map[string]any{"text": "hi"})
	em.emit("turn_end", map[string]any{"step": 1})

	got, err := subagents.ReadEventLog(mirrorPath)
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("mirror wrote %d events while supervisor was alive; want 0 (every event would otherwise double on the next reload)\n%+v",
			len(got), got)
	}

	// Simulate supervisor death: close stdout so the next Write
	// returns EBADF / broken pipe. The emitter must flip into
	// orphan mode and start writing through the mirror.
	if err := stdoutFile.Close(); err != nil {
		t.Fatalf("close stdout: %v", err)
	}

	em.emit("assistant_message", map[string]any{"text": "after orphan"})
	em.emit("turn_end", map[string]any{"step": 2})

	got, err = subagents.ReadEventLog(mirrorPath)
	if err != nil {
		t.Fatalf("read mirror post-orphan: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("mirror failed to take over after stdout died: got %d events", len(got))
	}
	if got[len(got)-1].Type != "turn_end" {
		t.Errorf("last mirrored event type = %q; want turn_end", got[len(got)-1].Type)
	}
}

// TestSupervisorEmitterLargeResultIsNotDuplicatedOnWire regresses the
// large-result wire duplication bug.
func TestSupervisorEmitterLargeResultIsNotDuplicatedOnWire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	em := newSubagentEmitter(file, nil)
	em.setProtocolIdentity("agent-1")
	em.emit("turn.result", map[string]any{
		"status":  "succeeded",
		"turn_id": "turn-1",
		"output":  strings.Repeat("x", 500_000),
	})
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	wire, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) >= 900_000 {
		t.Fatalf("large result appears duplicated on wire: %d bytes", len(wire))
	}
	var object map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(wire), &object); err != nil {
		t.Fatal(err)
	}
	if _, exists := object["output"]; exists {
		t.Fatal("large output was flattened a second time")
	}
	payload, ok := object["payload"].(map[string]any)
	if !ok || len(payload["output"].(string)) != 500_000 {
		t.Fatal("canonical payload lost the complete bounded output")
	}
	events, err := subagents.ReadEventLog(path)
	if err != nil || len(events) != 1 {
		t.Fatalf("supervisor event parser could not recover the large result: events=%d err=%v", len(events), err)
	}
	if output, ok := events[0].Data["output"].(string); !ok || len(output) != 500_000 {
		t.Fatal("supervisor parser lost the large result payload")
	}
}

// TestSupervisorEmitterStdoutShapeMatchesSupervisorParser pins the
// wire-format contract: each emitted event lands on stdout as one
// JSON object per line with type+time at top level alongside the
// data fields. The supervisor's runner parses this exact shape.
func TestSupervisorEmitterStdoutShapeMatchesSupervisorParser(t *testing.T) {
	// Pipe so we can read what the emitter wrote.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()

	em := newSubagentEmitter(w, nil)
	em.emit("turn_start", map[string]any{"step": 1})
	_ = w.Close()

	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	// One trailing newline => one event line.
	lines := bytes.Split(bytes.TrimRight(body, "\n"), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 event line, got %d: %q", len(lines), body)
	}
	var object map[string]any
	if err := json.Unmarshal(lines[0], &object); err != nil {
		t.Fatalf("not valid json: %v\n%s", err, lines[0])
	}
	if object["type"] != "turn.started" {
		t.Errorf("type field missing or wrong: %v", object["type"])
	}
	if _, ok := object["timestamp"].(string); !ok {
		t.Errorf("timestamp field missing: %v", object["timestamp"])
	}
	payload, ok := object["payload"].(map[string]any)
	if !ok || payload["step"] != float64(1) {
		t.Errorf("payload step field missing or wrong: %v", object["payload"])
	}
}
