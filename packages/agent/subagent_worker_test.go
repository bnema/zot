package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patriceckhart/zot/packages/agent/subagents"
)

// TestWorkerOutputLimitsUseSupervisorPolicy verifies that the child reads
// the effective output caps propagated by the supervisor and retains safe
// defaults for standalone workers.
func TestWorkerOutputLimitsUseSupervisorPolicy(t *testing.T) {
	t.Setenv("ZOT_SUBAGENT_MAX_OUTPUT_BYTES", "123")
	t.Setenv("ZOT_SUBAGENT_MAX_OUTPUT_LINES", "7")
	bytesLimit, linesLimit := workerOutputLimits()
	if bytesLimit != 123 || linesLimit != 7 {
		t.Fatalf("worker output limits = (%d, %d), want (123, 7)", bytesLimit, linesLimit)
	}
	t.Setenv("ZOT_SUBAGENT_MAX_OUTPUT_BYTES", "invalid")
	t.Setenv("ZOT_SUBAGENT_MAX_OUTPUT_LINES", "0")
	bytesLimit, linesLimit = workerOutputLimits()
	if bytesLimit != 500_000 || linesLimit != 5_000 {
		t.Fatalf("invalid worker output limits = (%d, %d), want defaults", bytesLimit, linesLimit)
	}
}

// TestSupervisorEmitterMirrorDormantUntilStdoutBreaks regresses the
// "everything is doubled after reopening a subagent agent" bug.
//
// Symptom: events.jsonl held two copies of every event because the
// child mirrored each event to disk AND the supervisor parsed the
// child's stdout and appended each event to disk too. On next zot
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

// TestSupervisorEmitterStdoutShapeMatchesSupervisorParser pins the
// wire-format contract: each emitted event lands on stdout as one
// JSON object per line with type+time at top level alongside the
// data fields. The supervisor's runner parses this exact shape.
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
