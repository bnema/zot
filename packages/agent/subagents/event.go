package subagents

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"
)

// Event is one structured datum in an agent's durable event log.
//
// The shape is intentionally a thin map+timestamp wrapper rather
// than a strict struct so the subagent package doesn't have to track
// every field added to core.AgentEvent. The child writes
// `modes.EventToJSON(ev)` plus a few subagent-internal types
// (lifecycle, user_input echo, error). The supervisor doesn't
// interpret most fields; it just keeps them for replay and lifts
// a few well-known ones into AgentSnapshot (Activity, Status,
// Tail) for the dashboard.
//
// The on-disk file (events.jsonl) is append-only, one event per
// line, newline-terminated JSON. Reading is forward-only; readers
// stat the file size and read from their last offset on every
// poll.
type Event struct {
	Time      time.Time      `json:"time"`
	Type      string         `json:"type"`
	Version   int            `json:"version,omitempty"`
	MessageID string         `json:"message_id,omitempty"`
	AgentID   string         `json:"agent_id,omitempty"`
	TurnID    string         `json:"turn_id,omitempty"`
	Data      map[string]any `json:"-"`
	Raw       map[string]any `json:"-"` // original canonical event for replay
}

// MarshalJSON writes the canonical versioned event shape used by the
// worker protocol and durable event logs.
func (e Event) MarshalJSON() ([]byte, error) {
	version := e.Version
	if version == 0 {
		version = ProtocolVersion
	}
	when := e.Time
	if when.IsZero() {
		when = time.Now().UTC()
	}
	payload := e.Data
	if payload == nil {
		payload = map[string]any{}
	}
	out := map[string]any{
		"version":   version,
		"type":      e.Type,
		"timestamp": when,
		"payload":   payload,
	}
	if e.MessageID != "" {
		out["message_id"] = e.MessageID
	}
	if e.AgentID != "" {
		out["agent_id"] = e.AgentID
	}
	if e.TurnID != "" {
		out["turn_id"] = e.TurnID
	}
	return json.Marshal(out)
}

// UnmarshalJSON accepts the canonical versioned event shape and retains the
// decoded payload for dashboard replay.
func (e *Event) UnmarshalJSON(b []byte) error {
	var wire struct {
		Version   int            `json:"version"`
		Type      string         `json:"type"`
		MessageID string         `json:"message_id,omitempty"`
		AgentID   string         `json:"agent_id,omitempty"`
		TurnID    string         `json:"turn_id,omitempty"`
		Timestamp time.Time      `json:"timestamp"`
		Payload   map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return err
	}
	if wire.Version != ProtocolVersion {
		return fmt.Errorf("subagent event: unsupported version %d", wire.Version)
	}
	if wire.Type == "" {
		return errors.New("subagent event: missing type")
	}
	if wire.Timestamp.IsZero() {
		return errors.New("subagent event: missing timestamp")
	}
	if wire.Payload == nil {
		wire.Payload = map[string]any{}
	}
	e.Raw = map[string]any{}
	if err := json.Unmarshal(b, &e.Raw); err != nil {
		return err
	}
	e.Time = wire.Timestamp
	e.Type = wire.Type
	e.Version = wire.Version
	e.MessageID = wire.MessageID
	e.AgentID = wire.AgentID
	e.TurnID = wire.TurnID
	e.Data = wire.Payload
	return nil
}

const (
	// Event log lines are produced by a child process and may contain model
	// payloads. Bound both per-line decoding and replay memory so a corrupt or
	// hostile log cannot force an unbounded allocation during Reload.
	maxEventLogLineBytes = 2 * 1024 * 1024
	maxEventLogEvents    = 100_000
)

// EventLog is an append-only writer for events.jsonl. Safe for
// concurrent writers (the child only has one writer, but tests
// occasionally fan out).
type EventLog struct {
	path string
	mu   sync.Mutex
	f    *os.File
}

// OpenEventLog opens (or creates) the events.jsonl file at path.
// Parent directories are created as needed.
func OpenEventLog(path string) (*EventLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("event log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("event log open: %w", err)
	}
	_ = f.Chmod(0o600)
	return &EventLog{path: path, f: f}, nil
}

// Path returns the absolute log path.
func (l *EventLog) Path() string { return l.path }

// Append writes one event. The encoding is `<json>\n`. Concurrent
// callers are serialised; small enough events never need partial
// writes since unix guarantees atomicity for writes ≤ PIPE_BUF on
// regular files, and we hold a per-process mutex on top.
func (l *EventLog) Append(ev Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return errors.New("event log: closed")
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if len(b) > maxEventLogLineBytes {
		return fmt.Errorf("event log: event exceeds %d bytes", maxEventLogLineBytes)
	}
	b = append(b, '\n')
	n, err := l.f.Write(b)
	if err == nil && n != len(b) {
		return io.ErrShortWrite
	}
	return err
}

// Close flushes and closes the underlying file.
func (l *EventLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// NewEvent is a convenience constructor that fills the timestamp.
func NewEvent(typ string, data map[string]any) Event {
	if data == nil {
		data = map[string]any{}
	}
	return Event{Time: time.Now().UTC(), Type: typ, Version: ProtocolVersion, Data: data}
}

// ReadEventLog parses every event currently in the file. Used by
// the dashboard on first open and by tests; live tailing uses
// FollowEventLog below.
func ReadEventLog(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	return readEvents(f)
}

type eventRing struct {
	events []Event
	next   int
	count  int
}

func newEventRing() eventRing {
	return eventRing{events: make([]Event, 0)}
}

func (r *eventRing) append(ev Event) {
	if r.count < maxEventLogEvents {
		r.events = append(r.events, ev)
		r.count++
		if r.count == maxEventLogEvents {
			r.next = 0
		} else {
			r.next = r.count
		}
		return
	}

	r.events[r.next] = ev
	r.next = (r.next + 1) % len(r.events)
}

func (r *eventRing) last() (Event, bool) {
	if r.count == 0 {
		return Event{}, false
	}
	i := r.next - 1
	if i < 0 {
		i += len(r.events)
	}
	return r.events[i], true
}

func (r *eventRing) isLikelyDoubleWrite(ev Event) bool {
	previous, ok := r.last()
	return ok && isLikelyDoubleWrite(ev, previous)
}

func (r *eventRing) materialize() []Event {
	out := make([]Event, r.count)
	if r.count == 0 {
		return out
	}
	start := r.next - r.count
	if start < 0 {
		start += len(r.events)
	}
	n := copy(out, r.events[start:])
	copy(out[n:], r.events[:r.count-n])
	return out
}

func readEvents(r io.Reader) ([]Event, error) {
	br := bufio.NewReader(r)
	ring := newEventRing()
	for {
		line, err, truncated := readBoundedLine(br, maxEventLogLineBytes)
		if len(line) > 0 && !truncated {
			var ev Event
			if jerr := json.Unmarshal(line, &ev); jerr == nil && !ring.isLikelyDoubleWrite(ev) {
				ring.append(ev)
			}
			// Malformed and oversized lines are skipped silently; the
			// dashboard renders only well-formed events and the child is
			// the only writer.
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return ring.materialize(), nil
			}
			return ring.materialize(), err
		}
	}
}

// readFollowerEvents parses only newline-terminated records from a live log.
// A follower must not advance past an incomplete final line: another process
// may be in the middle of appending that record, and skipping its start would
// make the completed event invisible on the next poll.
func readFollowerEvents(r io.Reader, offset int64) ([]Event, int64) {
	br := bufio.NewReader(r)
	ring := newEventRing()
	committed := offset
	for {
		line, truncated, consumed, complete, err := readBoundedLineStats(br, maxEventLogLineBytes)
		if !complete {
			break
		}
		committed += consumed
		if len(line) > 0 && !truncated {
			var ev Event
			if jerr := json.Unmarshal(line, &ev); jerr == nil && !ring.isLikelyDoubleWrite(ev) {
				ring.append(ev)
			}
		}
		if err != nil {
			break
		}
	}
	return ring.materialize(), committed
}

// isLikelyDoubleWrite reports whether ev is a back-to-back duplicate
// of previous — same type and identical Data payload, with timestamps
// within a small window (the supervisor and the child's mirror used to
// BOTH write each event to disk, landing the same content twice in quick
// succession in events.jsonl). The historical behaviour was fixed at write
// time (the child's mirror is now dormant unless the supervisor is gone),
// but on-disk files from before the fix are still polluted, so we dedupe
// defensively at read time too.
//
// We deliberately bound by time (250ms) so two genuinely identical adjacent
// events that happen seconds apart — e.g. an agent that runs the same tool
// twice in a row — still both render.
func isLikelyDoubleWrite(ev, previous Event) bool {
	if previous.Type != ev.Type {
		return false
	}
	if !ev.Time.IsZero() && !previous.Time.IsZero() {
		dt := ev.Time.Sub(previous.Time)
		if dt < 0 {
			dt = -dt
		}
		if dt > 250*time.Millisecond {
			return false
		}
	}
	return sameEventData(previous.Data, ev.Data)
}

// sameEventData deep-compares two event payloads without re-encoding them.
// Reload invokes this for adjacent same-type events across every persisted
// subagent log, so serializing large streamed payloads here delays startup.
func sameEventData(a, b map[string]any) bool {
	return len(a) == len(b) && reflect.DeepEqual(a, b)
}

// EventFollower polls an events.jsonl file and emits new events as
// they're appended. The supervisor uses one per agent so dashboard
// snapshots stay current without reparsing the whole file on every
// frame. Close stops the goroutine; the channel returned by Events
// is closed when Close is called or the file is removed.
type EventFollower struct {
	path     string
	interval time.Duration
	out      chan Event
	done     chan struct{}
	closed   chan struct{}
	once     sync.Once
}

// FollowEventLog starts polling path every interval. interval ≤ 0
// defaults to 50ms which is invisible to the user and cheap on
// disk (one stat + one short read per agent per tick).
func FollowEventLog(path string, interval time.Duration) *EventFollower {
	if interval <= 0 {
		interval = 50 * time.Millisecond
	}
	f := &EventFollower{
		path:     path,
		interval: interval,
		out:      make(chan Event, 256),
		done:     make(chan struct{}),
		closed:   make(chan struct{}),
	}
	go f.loop()
	return f
}

// Events returns the receive end of the event stream.
func (f *EventFollower) Events() <-chan Event { return f.out }

// Close stops polling and closes the events channel. It waits for
// the polling goroutine to exit so tests on Windows can remove the
// temp directory without racing an open file handle.
func (f *EventFollower) Close() {
	f.once.Do(func() {
		close(f.done)
		<-f.closed
	})
}

func (f *EventFollower) loop() {
	defer close(f.closed)
	defer close(f.out)
	var offset int64
	tick := time.NewTicker(f.interval)
	defer tick.Stop()
	for {
		select {
		case <-f.done:
			return
		case <-tick.C:
		}
		fi, err := os.Stat(f.path)
		if err != nil {
			continue
		}
		if fi.Size() <= offset {
			continue
		}
		fh, err := os.Open(f.path)
		if err != nil {
			continue
		}
		if _, err := fh.Seek(offset, io.SeekStart); err != nil {
			_ = fh.Close()
			continue
		}
		evs, newOffset := readFollowerEvents(fh, offset)
		_ = fh.Close()
		offset = newOffset
		for _, ev := range evs {
			select {
			case f.out <- ev:
			case <-f.done:
				return
			}
		}
	}
}
