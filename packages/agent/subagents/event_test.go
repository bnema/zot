package subagents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEventLogAppendAndRead is the simplest possible invariant:
// what we write goes back through ReadEventLog exactly, including
// the payload fields and the timestamp.
func TestEventLogAppendAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := OpenEventLog(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	want := []Event{
		NewEvent("turn_start", map[string]any{"step": 0}),
		NewEvent("assistant_message", map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "hi"}},
		}),
		NewEvent("turn_end", map[string]any{"stop": "end"}),
	}
	for _, ev := range want {
		if err := log.Append(ev); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	_ = log.Close()

	got, err := ReadEventLog(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Type != want[i].Type {
			t.Errorf("event %d type = %q; want %q", i, got[i].Type, want[i].Type)
		}
	}
}

func TestEventLogAppendUsesUTCForZeroTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := OpenEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Append(Event{Type: "zero_time", Version: ProtocolVersion, Data: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := ReadEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].Time.Location() != time.UTC {
		t.Fatalf("zero timestamp location = %v, want UTC", got[0].Time.Location())
	}
}

// TestReadEventLogIgnoresGarbage documents that one malformed line
// doesn't break the whole replay. The runner's responsibility is to
// only write well-formed events, but a partial crash could leave
// a half-written line; we don't want that to prevent the dashboard
// from showing the rest.
func TestReadVersionedEventMergesPayloadFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	wire := `{"version":1,"type":"turn.progress","message_id":"m1","agent_id":"a1","turn_id":"t1","timestamp":"2026-08-04T12:00:00Z","payload":{"text":"still working","future":{"step":2}}}` + "\n"
	if err := os.WriteFile(path, []byte(wire), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Data["text"] != "still working" {
		t.Fatalf("versioned payload was not replayed: %+v", got)
	}
	if future, ok := got[0].Data["future"].(map[string]any); !ok || future["step"] != float64(2) {
		t.Fatalf("future payload field was lost: %+v", got[0].Data)
	}
}

func TestReadEventLogSkipsOversizedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	oversized := fmt.Sprintf("%s\n", strings.Repeat("x", maxEventLogLineBytes+100))
	valid := NewEvent("valid", map[string]any{"ok": true})
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append([]byte(oversized), append(data, '\n')...), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEventLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Type != "valid" {
		t.Fatalf("oversized line affected replay: %+v", got)
	}
}

func TestEventReadersRetainLatestEventsInOrder(t *testing.T) {
	total := maxEventLogEvents + 2
	var input bytes.Buffer
	for i := 0; i < total; i++ {
		ev := Event{
			Time:    time.Unix(int64(i), 0).UTC(),
			Type:    "indexed",
			Version: ProtocolVersion,
			Data:    map[string]any{"index": i},
		}
		line, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		input.Write(line)
		input.WriteByte('\n')
	}

	check := func(name string, got []Event) {
		t.Helper()
		if len(got) != maxEventLogEvents {
			t.Fatalf("%s retained %d events, want %d", name, len(got), maxEventLogEvents)
		}
		for i, ev := range got {
			index, ok := ev.Data["index"].(float64)
			if !ok || int(index) != i+2 {
				t.Fatalf("%s event %d index = %v, want %d", name, i, ev.Data["index"], i+2)
			}
		}
	}

	got, err := readEvents(bytes.NewReader(input.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	check("readEvents", got)

	got, offset := readFollowerEvents(bytes.NewReader(input.Bytes()), 0)
	check("readFollowerEvents", got)
	if offset != int64(input.Len()) {
		t.Fatalf("follower offset = %d, want %d", offset, input.Len())
	}
}

func TestReadEventLogIgnoresGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := OpenEventLog(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := log.Append(NewEvent("first", nil)); err != nil {
		t.Fatalf("append: %v", err)
	}
	// Sneak a garbage line in by writing directly.
	if _, err := log.f.WriteString("not json at all\n"); err != nil {
		t.Fatalf("raw write: %v", err)
	}
	if err := log.Append(NewEvent("second", nil)); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = log.Close()

	got, err := ReadEventLog(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 || got[0].Type != "first" || got[1].Type != "second" {
		t.Fatalf("garbage tripped parser: %+v", got)
	}
}

// TestReadEventLogDeduplicatesDoubleWrites pins the read-time fix
// for already-polluted events.jsonl files. The historical bug: the
// supervisor parsed child stdout and called log.Append(ev), AND the
// child's mirror appended the same event. Two writes per event
// landed in the file in quick succession with near-identical
// timestamps, and the next zot launch replayed everything twice —
// the user saw every transcript line duplicated.
//
// The write-time fix makes the child's mirror dormant when the
// supervisor is alive, but old files are still polluted. ReadEventLog
// now folds back-to-back same-type identical-payload events whose
// timestamps are within ~250ms of each other.
func TestReadEventLogDeduplicatesDoubleWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := OpenEventLog(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Two writes per logical event, mimicking the supervisor +
	// mirror double-write pattern. Timestamps differ by a few ms
	// like they would in the wild.
	now := time.Now()
	writePair := func(typ string, data map[string]any) {
		e1 := NewEvent(typ, data)
		e1.Time = now
		if err := log.Append(e1); err != nil {
			t.Fatalf("append: %v", err)
		}
		e2 := NewEvent(typ, data)
		e2.Time = now.Add(2 * time.Millisecond)
		if err := log.Append(e2); err != nil {
			t.Fatalf("append: %v", err)
		}
		now = now.Add(500 * time.Millisecond) // next event well outside the dedup window
	}

	writePair("assistant_message", map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "hi"}},
	})
	writePair("turn_end", map[string]any{"step": float64(1)})
	_ = log.Close()

	got, err := ReadEventLog(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected double-writes to collapse to 2 events; got %d", len(got))
	}
	if got[0].Type != "assistant_message" || got[1].Type != "turn_end" {
		t.Errorf("unexpected types after dedup: %q, %q", got[0].Type, got[1].Type)
	}
}

// TestReadEventLogKeepsLegitimateAdjacentEvents pins the inverse:
// two events of the same type with identical payloads but separated
// by more than the dedup window (250ms) must both survive. The
// agent legitimately can emit identical adjacent events (think: a
// retry running the same tool seconds apart).
func TestReadEventLogKeepsLegitimateAdjacentEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := OpenEventLog(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	now := time.Now()
	for i := 0; i < 3; i++ {
		e := NewEvent("tool_call", map[string]any{"name": "read"})
		e.Time = now.Add(time.Duration(i) * time.Second)
		if err := log.Append(e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	_ = log.Close()

	got, err := ReadEventLog(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("dedup collapsed legitimately separated events; got %d, want 3", len(got))
	}
}

// TestFollowerEmitsNewEvents follows a live file and ensures events
// appended after the follower starts make it to the channel.
func TestFollowerEmitsNewEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	// Pre-seed one event so the file exists. The follower's
	// contract is "emit events appended AFTER it starts", so this
	// first event is not expected on the channel.
	log, err := OpenEventLog(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer log.Close()
	if err := log.Append(NewEvent("preexisting", nil)); err != nil {
		t.Fatalf("append: %v", err)
	}

	fol := FollowEventLog(path, 10*time.Millisecond)
	defer fol.Close()

	// Give the follower one tick to read the pre-existing event so
	// its offset advances past the first line. After that, any new
	// append should show up.
	time.Sleep(40 * time.Millisecond)
	drain(fol.Events())

	for i := 0; i < 3; i++ {
		if err := log.Append(NewEvent("live", map[string]any{"i": i})); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	got := waitFor(t, fol.Events(), 3, time.Second)
	if len(got) != 3 {
		t.Fatalf("got %d live events, want 3", len(got))
	}
	for i, ev := range got {
		if ev.Type != "live" {
			t.Errorf("event %d type = %q; want live", i, ev.Type)
		}
	}
}

func TestFollowerDoesNotSkipPartialFinalEvent(t *testing.T) {
	seed := NewEvent("preexisting", nil)
	seedBytes, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	seedBytes = append(seedBytes, '\n')
	partial := NewEvent("partial", map[string]any{"ok": true})
	partialBytes, err := json.Marshal(partial)
	if err != nil {
		t.Fatal(err)
	}
	mid := len(partialBytes) / 2
	incomplete := append(append([]byte(nil), seedBytes...), partialBytes[:mid]...)
	events, offset := readFollowerEvents(bytes.NewReader(incomplete), 0)
	if len(events) != 1 || events[0].Type != "preexisting" {
		t.Fatalf("complete prefix = %+v", events)
	}
	if offset != int64(len(seedBytes)) {
		t.Fatalf("offset = %d, want complete prefix %d", offset, len(seedBytes))
	}

	complete := append(append([]byte(nil), seedBytes...), append(partialBytes, '\n')...)
	events, finalOffset := readFollowerEvents(bytes.NewReader(complete[offset:]), offset)
	if len(events) != 1 || events[0].Type != "partial" {
		t.Fatalf("completed event = %+v", events)
	}
	if finalOffset != int64(len(complete)) {
		t.Fatalf("final offset = %d, want %d", finalOffset, len(complete))
	}
}

func drain(ch <-chan Event) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func waitFor(t *testing.T, ch <-chan Event, n int, timeout time.Duration) []Event {
	t.Helper()
	var got []Event
	deadline := time.After(timeout)
	for len(got) < n {
		select {
		case ev := <-ch:
			got = append(got, ev)
		case <-deadline:
			return got
		}
	}
	return got
}
