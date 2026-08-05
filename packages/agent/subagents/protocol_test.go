package subagents

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

func TestProtocolEnvelopeRoundTrip(t *testing.T) {
	e := NewEventEnvelope(EventTurnResult, "agent-123", "turn-4", map[string]any{
		"status": "succeeded",
		"output": "done",
	})
	if e.Version != ProtocolVersion {
		t.Fatalf("version = %d, want %d", e.Version, ProtocolVersion)
	}
	if e.MessageID == "" || !IsMessageID(e.MessageID) {
		t.Fatalf("message id = %q, want a UUID", e.MessageID)
	}
	if e.Timestamp.IsZero() {
		t.Fatal("constructor did not set timestamp")
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	line, err := MarshalJSONL(e)
	if err != nil {
		t.Fatalf("MarshalJSONL: %v", err)
	}
	if !bytes.HasSuffix(line, []byte{'\n'}) {
		t.Fatal("JSONL message is not newline terminated")
	}
	var wire map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(line), &wire); err != nil {
		t.Fatalf("wire JSON: %v", err)
	}
	for _, field := range []string{"version", "type", "message_id", "agent_id", "turn_id", "timestamp", "payload"} {
		if _, ok := wire[field]; !ok {
			t.Errorf("wire message lacks %q", field)
		}
	}

	got, err := ParseJSONL(line)
	if err != nil {
		t.Fatalf("ParseJSONL: %v", err)
	}
	if got.Version != e.Version || got.Type != e.Type || got.MessageID != e.MessageID ||
		got.AgentID != e.AgentID || got.TurnID != e.TurnID {
		t.Fatalf("metadata changed on round trip: got %+v, want %+v", got, e)
	}
	var result TurnResultPayload
	if err := got.DecodePayload(&result); err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if result.Status != "succeeded" || result.Output != "done" {
		t.Fatalf("decoded payload = %+v", result)
	}
}

func TestProtocolPreservesUnknownEventAndPayload(t *testing.T) {
	input := `{"version":1,"type":"future.event","message_id":"msg-future","agent_id":"agent-1","turn_id":"turn-1","timestamp":"2026-08-04T12:00:00Z","future_top":{"enabled":true},"payload":{"known":"yes","future":{"nested":[1,2,{"x":true}]}}}`
	e, err := ParseEnvelope([]byte(input))
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	if e.Type != "future.event" {
		t.Fatalf("type = %q", e.Type)
	}
	if e.IsEvent() {
		t.Fatal("unknown event was reported as a known event")
	}
	if _, ok := e.Unknown["future_top"]; !ok {
		t.Fatalf("unknown top-level field was not retained: %#v", e.Unknown)
	}
	fields, err := e.PayloadFields()
	if err != nil {
		t.Fatalf("PayloadFields: %v", err)
	}
	if string(fields["future"]) != `{"nested":[1,2,{"x":true}]}` {
		t.Fatalf("unknown payload field changed: %s", fields["future"])
	}

	encoded, err := MarshalEnvelope(e)
	if err != nil {
		t.Fatalf("MarshalEnvelope: %v", err)
	}
	var roundTripped map[string]any
	if err := json.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatalf("round-trip JSON: %v", err)
	}
	if _, ok := roundTripped["future_top"]; !ok {
		t.Fatal("unknown top-level field was dropped")
	}
	payload, ok := roundTripped["payload"].(map[string]any)
	if !ok || payload["future"] == nil {
		t.Fatalf("unknown payload was dropped: %#v", roundTripped["payload"])
	}
}

func TestProtocolJSONCommandsAndDirectionChecks(t *testing.T) {
	command := NewCommand(CommandTurnStart, "agent-1", "turn-2", TurnStartPayload{Prompt: "hello"})
	line, err := MarshalJSONL(command)
	if err != nil {
		t.Fatalf("MarshalJSONL: %v", err)
	}
	got, err := ParseCommand(string(line))
	if err != nil {
		t.Fatalf("ParseCommand: %v", err)
	}
	if !got.IsCommand() || got.IsEvent() || got.Type != CommandTurnStart {
		t.Fatalf("direction = command:%v event:%v type:%q", got.IsCommand(), got.IsEvent(), got.Type)
	}
	if _, err := ParseEvent(line); !errors.Is(err, ErrNotEvent) {
		t.Fatalf("ParseEvent(command) = %v, want ErrNotEvent", err)
	}

	event := NewEventEnvelope(EventAgentHeartbeat, "agent-1", "", AgentHeartbeatPayload{Activity: "idle"})
	eventLine, err := MarshalJSONL(event)
	if err != nil {
		t.Fatalf("Marshal event: %v", err)
	}
	if _, err := ParseCommand(string(eventLine)); !errors.Is(err, ErrNotCommand) {
		t.Fatalf("ParseCommand(event) = %v, want ErrNotCommand", err)
	}
}

func TestProtocolStreamReadWrite(t *testing.T) {
	first := NewCommand(CommandAgentPing, "agent-1", "", nil)
	second := NewEventEnvelope(EventAgentReady, "agent-1", "", nil)
	var stream bytes.Buffer
	if err := WriteEnvelope(&stream, first); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := WriteEnvelope(&stream, second); err != nil {
		t.Fatalf("write second: %v", err)
	}
	reader := bufio.NewReader(&stream)
	gotFirst, err := ReadEnvelope(reader)
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	gotSecond, err := ReadEnvelope(reader)
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	if gotFirst.Type != CommandAgentPing || gotSecond.Type != EventAgentReady {
		t.Fatalf("stream types = %q, %q", gotFirst.Type, gotSecond.Type)
	}
	if _, err := ReadEnvelope(reader); !errors.Is(err, io.EOF) {
		t.Fatalf("read after stream = %v, want EOF", err)
	}
}

func TestProtocolMessageIDsAreUnique(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		id := NewMessageID()
		if !IsMessageID(id) {
			t.Fatalf("NewMessageID returned non-UUID %q", id)
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate message id %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestProtocolPayloadMutationKeepsMessageID(t *testing.T) {
	e := NewCommand(CommandTurnStart, "agent-1", "turn-1", TurnStartPayload{Prompt: "before"})
	id := e.MessageID
	if err := e.SetPayload(TurnStartPayload{Prompt: "after"}); err != nil {
		t.Fatalf("SetPayload: %v", err)
	}
	if e.MessageID != id {
		t.Fatalf("SetPayload changed message id from %q to %q", id, e.MessageID)
	}
}

func TestProtocolUnsupportedVersion(t *testing.T) {
	_, err := ParseEnvelope([]byte(`{"version":2,"type":"future.event","message_id":"m","agent_id":"a","timestamp":"2026-08-04T12:00:00Z","payload":{}}`))
	if !errors.Is(err, ErrUnsupportedProtocolVersion) {
		t.Fatalf("ParseEnvelope version 2 = %v, want unsupported version", err)
	}
}

func TestProtocolEmptyAndMalformedInputs(t *testing.T) {
	for _, input := range []string{"", "   ", "not a command"} {
		if _, err := ParseCommand(input); err == nil {
			t.Errorf("ParseCommand(%q) succeeded", input)
		}
	}
	if _, err := ParseEnvelope([]byte(`{"payload":{}}`)); err == nil {
		t.Fatal("ParseEnvelope without type succeeded")
	}
	if _, err := ParseEnvelope([]byte(`{"version":1,"type":"x","payload":`)); err == nil {
		t.Fatal("ParseEnvelope malformed JSON succeeded")
	}

	invalid := NewEnvelope("x", "agent-1", "turn-1", func() {})
	if err := invalid.Validate(); err == nil {
		t.Fatal("NewEnvelope accepted an unmarshalable payload")
	}
	if _, err := MarshalEnvelope(invalid); err == nil {
		t.Fatal("MarshalEnvelope accepted an unmarshalable payload")
	}

	zeroVersion := Envelope{
		Type:      "x",
		MessageID: "message-1",
		AgentID:   "agent-1",
		Timestamp: time.Now().UTC(),
		Payload:   json.RawMessage(`{}`),
	}
	wire, err := MarshalEnvelope(zeroVersion)
	if err != nil {
		t.Fatalf("MarshalEnvelope zero version: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatalf("decode zero-version envelope: %v", err)
	}
	var version int
	if err := json.Unmarshal(fields["version"], &version); err != nil {
		t.Fatalf("decode version: %v", err)
	}
	if version != ProtocolVersion {
		t.Fatalf("version = %d, want %d", version, ProtocolVersion)
	}
}

func TestProtocolResultShape(t *testing.T) {
	result := TurnResultPayload{
		Status:       "succeeded",
		Summary:      "implemented parser",
		Output:       "full output",
		Structured:   json.RawMessage(`{"ok":true}`),
		Artifacts:    []ArtifactReference{{Name: "patch", Ref: "subagent://a/patch"}},
		ChangedFiles: []string{"protocol.go"},
		Usage:        map[string]any{"input_tokens": 3},
	}
	e := NewEventEnvelope(EventTurnResult, "a", "t", result)
	var decoded TurnResultPayload
	if err := e.DecodePayload(&decoded); err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if decoded.Status != "succeeded" || len(decoded.Artifacts) != 1 || decoded.Artifacts[0].Ref != "subagent://a/patch" {
		t.Fatalf("decoded result = %+v", decoded)
	}
}

func TestProtocolTimestampIsRFC3339(t *testing.T) {
	e := NewEventEnvelope(EventAgentReady, "a", "", nil)
	wire, err := MarshalEnvelope(e)
	if err != nil {
		t.Fatalf("MarshalEnvelope: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	var timestamp string
	if err := json.Unmarshal(fields["timestamp"], &timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
		t.Fatalf("timestamp %q is not RFC3339Nano: %v", timestamp, err)
	}
}

func TestProtocolKnownNames(t *testing.T) {
	if !IsCommandName(CommandAgentPing) || !IsCommandName(CommandTurnStart) || !IsCommandName(CommandTurnCancel) || !IsCommandName(CommandAgentShutdown) {
		t.Fatal("one or more canonical command names are not registered")
	}
	if !IsEventName(EventAgentReady) || !IsEventName(EventAgentHeartbeat) || !IsEventName(EventTurnStarted) || !IsEventName(EventTurnProgress) || !IsEventName(EventToolStarted) || !IsEventName(EventToolFinished) || !IsEventName(EventMessageDelta) || !IsEventName(EventTurnResult) || !IsEventName(EventTurnFailed) || !IsEventName(EventAgentIdle) || !IsEventName(EventAgentExited) {
		t.Fatal("one or more canonical event names are not registered")
	}
	if IsEventName("future.event") || IsCommandName("future.command") {
		t.Fatal("future names should not be reported as known names")
	}
}
