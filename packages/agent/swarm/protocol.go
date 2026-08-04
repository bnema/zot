package swarm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ProtocolVersion is the version of the supervisor/worker JSONL protocol.
// Version zero is reserved for the legacy flat event and text-inbox formats;
// it is not a versioned wire format.
const ProtocolVersion = 1

// LegacyProtocolVersion identifies a message read from the pre-versioned
// protocol. Legacy messages are accepted by the readers but new messages are
// always emitted at ProtocolVersion.
const LegacyProtocolVersion = 0

// Canonical parent-to-worker command names.
const (
	CommandAgentPing     = "agent.ping"
	CommandTurnStart     = "turn.start"
	CommandTurnCancel    = "turn.cancel"
	CommandAgentShutdown = "agent.shutdown"
)

// Canonical worker-to-parent event names.
const (
	EventAgentReady     = "agent.ready"
	EventAgentHeartbeat = "agent.heartbeat"
	EventTurnStarted    = "turn.started"
	EventTurnProgress   = "turn.progress"
	EventToolStarted    = "tool.started"
	EventToolFinished   = "tool.finished"
	EventMessageDelta   = "message.delta"
	EventTurnResult     = "turn.result"
	EventTurnFailed     = "turn.failed"
	EventAgentIdle      = "agent.idle"
	EventAgentExited    = "agent.exited"
)

// Short aliases are kept alongside the direction-prefixed names because the
// latter read well at call sites while the former are convenient in switches.
const (
	AgentPing     = CommandAgentPing
	TurnStart     = CommandTurnStart
	TurnCancel    = CommandTurnCancel
	AgentShutdown = CommandAgentShutdown

	AgentReady     = EventAgentReady
	AgentHeartbeat = EventAgentHeartbeat
	TurnStarted    = EventTurnStarted
	TurnProgress   = EventTurnProgress
	ToolStarted    = EventToolStarted
	ToolFinished   = EventToolFinished
	MessageDelta   = EventMessageDelta
	AgentIdle      = EventAgentIdle
	AgentExited    = EventAgentExited
)

var (
	// ErrEmptyProtocolLine is returned for a blank JSONL line or command.
	ErrEmptyProtocolLine = errors.New("swarm protocol: empty line")
	// ErrNotCommand is returned when ParseCommand is given a JSON event.
	ErrNotCommand = errors.New("swarm protocol: message is not a command")
	// ErrNotEvent is returned when ParseEvent is given a JSON command.
	ErrNotEvent = errors.New("swarm protocol: message is not an event")
	// ErrUnsupportedProtocolVersion is returned by Validate for a version
	// this implementation cannot interpret.
	ErrUnsupportedProtocolVersion = errors.New("swarm protocol: unsupported version")
)

var commandNames = map[string]struct{}{
	CommandAgentPing:     {},
	CommandTurnStart:     {},
	CommandTurnCancel:    {},
	CommandAgentShutdown: {},
}

var eventNames = map[string]struct{}{
	EventAgentReady:     {},
	EventAgentHeartbeat: {},
	EventTurnStarted:    {},
	EventTurnProgress:   {},
	EventToolStarted:    {},
	EventToolFinished:   {},
	EventMessageDelta:   {},
	EventTurnResult:     {},
	EventTurnFailed:     {},
	EventAgentIdle:      {},
	EventAgentExited:    {},
}

// IsCommandName reports whether name is one of the commands defined by this
// version of the protocol. Readers intentionally do not use this as a parse
// gate: a newer command can still be decoded and forwarded by an older
// supervisor.
func IsCommandName(name string) bool {
	_, ok := commandNames[name]
	return ok
}

// IsEventName reports whether name is one of the events defined by this
// version of the protocol. Unknown event names are valid protocol messages.
func IsEventName(name string) bool {
	_, ok := eventNames[name]
	return ok
}

// CommandName and EventName make APIs which accept only a direction more
// self-documenting without preventing unknown names from being preserved.
type CommandName string
type EventName string

// MessageID returns a fresh opaque id suitable for correlating a command and
// its result. UUIDs are used rather than a counter so ids remain unique when
// several supervisors or workers are running at once.
func NewMessageID() string { return uuid.NewString() }

// IsMessageID reports whether id has the UUID form emitted by NewMessageID.
// Message ids are otherwise opaque on the wire, so readers do not require
// this form when accepting messages from a newer implementation.
func IsMessageID(id string) bool {
	if id == "" {
		return false
	}
	_, err := uuid.Parse(id)
	return err == nil
}

// Envelope is one versioned JSONL command or event. Payload is deliberately a
// RawMessage: an old supervisor can retain an event it does not know about,
// including fields added inside that event's payload, and write it back
// without decoding it into a lossy map.
//
// The JSON representation of a current message is:
//
//	{"version":1,"type":"...","message_id":"...","agent_id":"...",
//	 "turn_id":"...","timestamp":"...","payload":{...}}
//
// TurnID is omitted when it is empty. Unknown top-level fields are retained in
// Unknown. A flat legacy event is represented by Legacy=true and uses `time`
// instead of `timestamp` when marshaled.
type Envelope struct {
	Version   int             `json:"version"`
	Type      string          `json:"type"`
	MessageID string          `json:"message_id,omitempty"`
	AgentID   string          `json:"agent_id,omitempty"`
	TurnID    string          `json:"turn_id,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`

	// Unknown contains unrecognised top-level fields from a versioned
	// envelope. Payload itself retains unrecognised payload fields.
	Unknown map[string]json.RawMessage `json:"-"`

	// Legacy is true only for an old flat event read by ParseEnvelope. It
	// causes MarshalJSON to retain the old flat shape. It is not set for a
	// text inbox command, since text commands are normalised to a new
	// versioned command as soon as they are read.
	Legacy bool `json:"-"`

	// LegacyCommand and LegacyText record that this envelope came from a
	// text inbox command. They are useful to bridges that need to echo the
	// original command, and do not affect versioned JSON marshaling.
	LegacyCommand bool   `json:"-"`
	LegacyText    string `json:"-"`
}

// Command and ProtocolEvent are aliases rather than separate structs so a
// caller can pass either kind to the same JSONL transport without a wrapper.
// Direction checks are available through IsCommand and IsEvent.
type Command = Envelope
type ProtocolEvent = Envelope
type EventEnvelope = Envelope
type CommandEnvelope = Envelope

// NewEnvelope creates a current-protocol envelope and assigns a message id
// and timestamp. payload may be any JSON-marshalable value. A nil payload is
// encoded as an empty object, which keeps command and event payloads uniform.
//
// For a marshalable payload this helper cannot fail. Call
// NewEnvelopeWithPayload when construction errors must be reported instead of
// being represented as a null payload.
func NewEnvelope(typ, agentID, turnID string, payload ...any) Envelope {
	e, err := NewEnvelopeWithPayload(typ, agentID, turnID, firstPayload(payload))
	if err == nil {
		return e
	}
	// Keep the constructor useful in error-free call sites. MarshalJSON will
	// still succeed with a valid JSON null; callers which need the original
	// error should use NewEnvelopeWithPayload. Preserve the metadata even
	// when only the caller's payload was not marshalable.
	return Envelope{
		Version:   ProtocolVersion,
		Type:      typ,
		MessageID: NewMessageID(),
		AgentID:   agentID,
		TurnID:    turnID,
		Timestamp: time.Now().UTC(),
		Payload:   json.RawMessage("null"),
	}
}

// NewEnvelopeWithPayload is the checked form of NewEnvelope.
func NewEnvelopeWithPayload(typ, agentID, turnID string, payload any) (Envelope, error) {
	if strings.TrimSpace(typ) == "" {
		return Envelope{}, errors.New("swarm protocol: empty message type")
	}
	p, err := marshalPayload(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("swarm protocol payload: %w", err)
	}
	return Envelope{
		Version:   ProtocolVersion,
		Type:      typ,
		MessageID: NewMessageID(),
		AgentID:   agentID,
		TurnID:    turnID,
		Timestamp: time.Now().UTC(),
		Payload:   p,
	}, nil
}

// NewCommand creates a current-protocol command. Unknown command names are
// allowed so a bridge can forward commands introduced by a newer peer.
func NewCommand(name, agentID, turnID string, payload ...any) Envelope {
	return NewEnvelope(name, agentID, turnID, payload...)
}

// NewEventEnvelope creates a current-protocol event. The name is not checked
// against EventName so unknown future events remain forward-compatible.
func NewEventEnvelope(name, agentID, turnID string, payload ...any) Envelope {
	return NewEnvelope(name, agentID, turnID, payload...)
}

// NewProtocolEvent is a more explicit spelling of NewEventEnvelope. NewEvent
// is already the legacy flat-event constructor in this package.
func NewProtocolEvent(name, agentID, turnID string, payload ...any) Envelope {
	return NewEventEnvelope(name, agentID, turnID, payload...)
}

// NewCommandEnvelope is an explicit alias for NewCommand.
func NewCommandEnvelope(name, agentID, turnID string, payload ...any) Envelope {
	return NewCommand(name, agentID, turnID, payload...)
}

// NewEventMessage is an explicit alias for NewEventEnvelope.
func NewEventMessage(name, agentID, turnID string, payload ...any) Envelope {
	return NewEventEnvelope(name, agentID, turnID, payload...)
}

func firstPayload(payload []any) any {
	if len(payload) == 0 || payload[0] == nil {
		return map[string]any{}
	}
	return payload[0]
}

func marshalPayload(payload any) (json.RawMessage, error) {
	if payload == nil {
		return json.RawMessage("{}"), nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), b...), nil
}

// Validate checks the required fields of a current-protocol envelope. It is
// intentionally separate from ParseEnvelope: parsing an unknown future event
// should succeed, while a sender can opt into strict validation before write.
func (e Envelope) Validate() error {
	if e.Legacy {
		if e.Type == "" {
			return errors.New("swarm protocol: legacy event has no type")
		}
		return nil
	}
	if e.Version != ProtocolVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedProtocolVersion, e.Version)
	}
	if e.Type == "" {
		return errors.New("swarm protocol: missing type")
	}
	if e.MessageID == "" {
		return errors.New("swarm protocol: missing message_id")
	}
	if e.AgentID == "" {
		return errors.New("swarm protocol: missing agent_id")
	}
	if e.Timestamp.IsZero() {
		return errors.New("swarm protocol: missing timestamp")
	}
	if len(e.Payload) == 0 || !json.Valid(e.Payload) {
		return errors.New("swarm protocol: invalid payload")
	}
	return nil
}

// IsCommand reports whether this envelope has a known command name.
func (e Envelope) IsCommand() bool { return IsCommandName(e.Type) }

// IsEvent reports whether this envelope has a known event name.
func (e Envelope) IsEvent() bool { return IsEventName(e.Type) }

// DecodePayload decodes the payload into dst without changing the preserved
// raw payload. This is the preferred way for a consumer to inspect a known
// event while still retaining it for a later forward-compatible write.
func (e Envelope) DecodePayload(dst any) error {
	if dst == nil {
		return errors.New("swarm protocol: nil payload destination")
	}
	if len(e.Payload) == 0 {
		return errors.New("swarm protocol: missing payload")
	}
	return json.Unmarshal(e.Payload, dst)
}

// PayloadFields returns an object payload as raw fields. It returns an error
// for scalar or array payloads, rather than silently discarding their shape.
func (e Envelope) PayloadFields() (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := e.DecodePayload(&fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("swarm protocol: payload is not an object")
	}
	return fields, nil
}

// PayloadValue returns a decoded generic payload. PayloadFields is preferable
// when the caller needs to preserve exact nested JSON values.
func (e Envelope) PayloadValue() (any, error) {
	var value any
	if err := e.DecodePayload(&value); err != nil {
		return nil, err
	}
	return value, nil
}

// SetPayload replaces the payload while keeping the envelope metadata and
// message id intact.
func (e *Envelope) SetPayload(payload any) error {
	if e == nil {
		return errors.New("swarm protocol: nil envelope")
	}
	p, err := marshalPayload(payload)
	if err != nil {
		return fmt.Errorf("swarm protocol payload: %w", err)
	}
	e.Payload = p
	return nil
}

// ParseEnvelope parses either a versioned envelope or a legacy flat event.
// Unknown event types and unknown payload fields are valid and retained.
func ParseEnvelope(data []byte) (Envelope, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return Envelope{}, ErrEmptyProtocolLine
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Envelope{}, fmt.Errorf("swarm protocol: decode envelope: %w", err)
	}
	if raw == nil {
		return Envelope{}, errors.New("swarm protocol: envelope must be an object")
	}
	var e Envelope
	if hasNumericVersion(raw) {
		if err := unmarshalVersionedEnvelope(raw, &e); err != nil {
			return Envelope{}, err
		}
		return e, nil
	}
	if err := unmarshalLegacyEvent(raw, &e); err != nil {
		return Envelope{}, err
	}
	return e, nil
}

// DecodeEnvelope is an alias for ParseEnvelope for callers using decoder
// terminology.
func DecodeEnvelope(data []byte) (Envelope, error) { return ParseEnvelope(data) }

// ParseJSONL parses one newline-delimited JSON message. It accepts a final
// line without a newline, as is customary for a stream after a clean EOF.
func ParseJSONL(line []byte) (Envelope, error) { return ParseEnvelope(line) }

// ParseEnvelopeLine is the string form of ParseJSONL.
func ParseEnvelopeLine(line string) (Envelope, error) {
	return ParseEnvelope([]byte(line))
}

// ParseEvent parses a versioned or legacy JSON event. A known command is
// rejected; unknown types are accepted because an older peer cannot know
// whether a future event name is present in its event registry.
func ParseEvent(data []byte) (Envelope, error) {
	e, err := ParseEnvelope(data)
	if err != nil {
		return Envelope{}, err
	}
	if e.IsCommand() {
		return Envelope{}, ErrNotEvent
	}
	return e, nil
}

// ParseProtocolEvent is an explicit alias for ParseEvent.
func ParseProtocolEvent(data []byte) (Envelope, error) { return ParseEvent(data) }

// ParseEventLine parses one JSONL event line.
func ParseEventLine(line string) (Envelope, error) { return ParseEvent([]byte(line)) }

// ParseCommand parses either a versioned JSON command or one of the legacy
// text inbox commands: `user <prompt>`, `cancel`, and `shutdown`. The text
// form is normalised to a versioned envelope with a fresh message id.
func ParseCommand(line string) (Envelope, error) {
	raw := strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(raw) == "" {
		return Envelope{}, ErrEmptyProtocolLine
	}
	if strings.HasPrefix(strings.TrimSpace(raw), "{") {
		e, err := ParseEnvelope([]byte(raw))
		if err != nil {
			return Envelope{}, err
		}
		if IsEventName(e.Type) {
			return Envelope{}, ErrNotCommand
		}
		return e, nil
	}
	return ParseLegacyCommand(raw)
}

// ParseCommandLine is an alias for ParseCommand.
func ParseCommandLine(line string) (Envelope, error) { return ParseCommand(line) }

// ParseLegacyCommand converts the old text inbox command syntax to a current
// envelope. The old syntax had no agent or turn id, so those fields remain
// empty until the owning supervisor supplies them.
func ParseLegacyCommand(line string) (Envelope, error) {
	if strings.TrimSpace(line) == "" {
		return Envelope{}, ErrEmptyProtocolLine
	}
	var e Envelope
	switch {
	case strings.HasPrefix(line, "user "):
		e = NewCommand(CommandTurnStart, "", "", TurnStartPayload{Prompt: strings.TrimPrefix(line, "user ")})
	case strings.TrimSpace(line) == "cancel":
		e = NewCommand(CommandTurnCancel, "", "", TurnCancelPayload{})
	case strings.TrimSpace(line) == "shutdown":
		e = NewCommand(CommandAgentShutdown, "", "", AgentShutdownPayload{})
	case strings.TrimSpace(line) == "ping":
		e = NewCommand(CommandAgentPing, "", "", AgentPingPayload{})
	default:
		return Envelope{}, fmt.Errorf("swarm protocol: unknown legacy command %q", line)
	}
	e.LegacyCommand = true
	e.LegacyText = line
	return e, nil
}

// MarshalEnvelope returns one JSON object without a trailing newline.
func MarshalEnvelope(e Envelope) ([]byte, error) {
	return json.Marshal(e)
}

// MarshalJSONL returns one newline-terminated JSONL message.
func MarshalJSONL(e Envelope) ([]byte, error) {
	b, err := MarshalEnvelope(e)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// EncodeEnvelope is the stream-friendly spelling of MarshalJSONL.
func EncodeEnvelope(e Envelope) ([]byte, error) { return MarshalJSONL(e) }

// WriteEnvelope writes one complete newline-terminated JSONL message.
func WriteEnvelope(w io.Writer, e Envelope) error {
	if w == nil {
		return errors.New("swarm protocol: nil writer")
	}
	b, err := MarshalJSONL(e)
	if err != nil {
		return err
	}
	n, err := w.Write(b)
	if err == nil && n != len(b) {
		return io.ErrShortWrite
	}
	return err
}

// ReadEnvelope reads exactly one JSONL message from a buffered reader. A
// caller reading many messages should reuse the same bufio.Reader.
func ReadEnvelope(r *bufio.Reader) (Envelope, error) {
	if r == nil {
		return Envelope{}, errors.New("swarm protocol: nil reader")
	}
	line, err := r.ReadBytes('\n')
	if len(bytes.TrimSpace(line)) == 0 && err == io.EOF {
		return Envelope{}, io.EOF
	}
	if len(line) != 0 {
		parsed, parseErr := ParseJSONL(line)
		if parseErr != nil {
			return Envelope{}, parseErr
		}
		return parsed, nil
	}
	return Envelope{}, err
}

// MarshalLegacyCommand converts a normalised command back to the text inbox
// syntax. It is intended for compatibility bridges, not for new transports.
func MarshalLegacyCommand(e Envelope) (string, error) {
	if !e.IsCommand() {
		return "", ErrNotCommand
	}
	if e.LegacyCommand && e.LegacyText != "" {
		return e.LegacyText, nil
	}
	fields, err := e.PayloadFields()
	if err != nil {
		return "", err
	}
	switch e.Type {
	case CommandTurnStart:
		var p TurnStartPayload
		if err := e.DecodePayload(&p); err != nil {
			return "", err
		}
		return "user " + p.Prompt, nil
	case CommandTurnCancel:
		return "cancel", nil
	case CommandAgentShutdown:
		return "shutdown", nil
	case CommandAgentPing:
		return "ping", nil
	default:
		_ = fields // retain the object check for useful errors above
		return "", fmt.Errorf("swarm protocol: no legacy spelling for command %q", e.Type)
	}
}

// LegacyEvent converts an envelope to the package's existing flat Event
// representation. This is the compatibility bridge for the durable event
// reader while callers migrate to Envelope.
func (e Envelope) LegacyEvent() (Event, error) {
	if e.IsCommand() {
		return Event{}, ErrNotEvent
	}
	fields, err := e.PayloadFields()
	if err != nil {
		return Event{}, err
	}
	data := make(map[string]any, len(fields))
	for key, raw := range fields {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return Event{}, fmt.Errorf("swarm protocol payload field %q: %w", key, err)
		}
		data[key] = value
	}
	return Event{
		Time: e.Timestamp, Type: e.Type, Version: e.Version,
		MessageID: e.MessageID, AgentID: e.AgentID, TurnID: e.TurnID,
		Data: data,
	}, nil
}

// EnvelopeFromEvent upgrades an existing flat Event to the current protocol.
// The event's Data is copied into a raw payload and the generated envelope
// receives fresh metadata.
func EnvelopeFromEvent(ev Event, agentID, turnID string) Envelope {
	return NewEventEnvelope(ev.Type, agentID, turnID, ev.Data)
}

// EventFromEnvelope is an alias for LegacyEvent.
func EventFromEnvelope(e Envelope) (Event, error) { return e.LegacyEvent() }

func (e Envelope) MarshalJSON() ([]byte, error) {
	if e.Legacy {
		return e.marshalLegacyJSON()
	}
	return e.marshalVersionedJSON()
}

func (e Envelope) marshalVersionedJSON() ([]byte, error) {
	version := e.Version
	if version == LegacyProtocolVersion {
		version = ProtocolVersion
	}
	messageID := e.MessageID
	if messageID == "" {
		messageID = NewMessageID()
	}
	timestamp := e.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	payload := e.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	if !json.Valid(payload) {
		return nil, errors.New("swarm protocol: invalid payload JSON")
	}
	out := make(map[string]json.RawMessage, len(e.Unknown)+7)
	for key, value := range e.Unknown {
		if isEnvelopeField(key) {
			continue
		}
		out[key] = cloneRaw(value)
	}
	putRaw(out, "version", version)
	putRaw(out, "type", e.Type)
	putRaw(out, "message_id", messageID)
	putRaw(out, "agent_id", e.AgentID)
	if e.TurnID != "" {
		putRaw(out, "turn_id", e.TurnID)
	}
	putRaw(out, "timestamp", timestamp)
	out["payload"] = cloneRaw(payload)
	return json.Marshal(out)
}

func (e Envelope) marshalLegacyJSON() ([]byte, error) {
	fields, err := e.PayloadFields()
	if err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage, len(fields)+5)
	for key, value := range fields {
		out[key] = cloneRaw(value)
	}
	for key, value := range e.Unknown {
		if isLegacyEnvelopeField(key) {
			continue
		}
		if _, exists := out[key]; !exists {
			out[key] = cloneRaw(value)
		}
	}
	putRaw(out, "type", e.Type)
	if !e.Timestamp.IsZero() {
		putRaw(out, "time", e.Timestamp)
	}
	if e.MessageID != "" {
		putRaw(out, "message_id", e.MessageID)
	}
	if e.AgentID != "" {
		putRaw(out, "agent_id", e.AgentID)
	}
	if e.TurnID != "" {
		putRaw(out, "turn_id", e.TurnID)
	}
	return json.Marshal(out)
}

func (e *Envelope) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == nil {
		return errors.New("swarm protocol: envelope must be an object")
	}
	var parsed Envelope
	if hasNumericVersion(raw) {
		if err := unmarshalVersionedEnvelope(raw, &parsed); err != nil {
			return err
		}
	} else if err := unmarshalLegacyEvent(raw, &parsed); err != nil {
		return err
	}
	*e = parsed
	return nil
}

func hasNumericVersion(raw map[string]json.RawMessage) bool {
	value, ok := raw["version"]
	if !ok {
		return false
	}
	var version int
	return json.Unmarshal(value, &version) == nil
}

func unmarshalVersionedEnvelope(raw map[string]json.RawMessage, e *Envelope) error {
	if err := decodeRequired(raw, "version", &e.Version); err != nil {
		return err
	}
	if e.Version != ProtocolVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedProtocolVersion, e.Version)
	}
	if err := decodeRequired(raw, "type", &e.Type); err != nil {
		return err
	}
	if err := decodeOptionalString(raw, "message_id", &e.MessageID); err != nil {
		return err
	}
	if err := decodeOptionalString(raw, "agent_id", &e.AgentID); err != nil {
		return err
	}
	if err := decodeOptionalString(raw, "turn_id", &e.TurnID); err != nil {
		return err
	}
	if timestamp, ok := raw["timestamp"]; ok {
		if err := json.Unmarshal(timestamp, &e.Timestamp); err != nil {
			return fmt.Errorf("swarm protocol: timestamp: %w", err)
		}
	}
	if payload, ok := raw["payload"]; ok {
		e.Payload = cloneRaw(payload)
	} else {
		e.Payload = json.RawMessage("{}")
	}
	e.Unknown = unknownFields(raw, map[string]struct{}{
		"version": {}, "type": {}, "message_id": {}, "agent_id": {},
		"turn_id": {}, "timestamp": {}, "payload": {},
	})
	return nil
}

func unmarshalLegacyEvent(raw map[string]json.RawMessage, e *Envelope) error {
	if err := decodeRequired(raw, "type", &e.Type); err != nil {
		return err
	}
	e.Version = LegacyProtocolVersion
	e.Legacy = true
	if err := decodeOptionalString(raw, "message_id", &e.MessageID); err != nil {
		return err
	}
	if err := decodeOptionalString(raw, "agent_id", &e.AgentID); err != nil {
		return err
	}
	if err := decodeOptionalString(raw, "turn_id", &e.TurnID); err != nil {
		return err
	}
	if value, ok := raw["time"]; ok {
		// Legacy Event historically ignored an invalid timestamp. Keep that
		// permissive behaviour while still retaining all other fields.
		_ = json.Unmarshal(value, &e.Timestamp)
	}
	payload := make(map[string]json.RawMessage)
	for key, value := range raw {
		switch key {
		case "type", "time", "message_id", "agent_id", "turn_id":
			continue
		}
		payload[key] = cloneRaw(value)
	}
	if len(payload) == 0 {
		e.Payload = json.RawMessage("{}")
	} else {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		e.Payload = encoded
	}
	return nil
}

func decodeRequired(raw map[string]json.RawMessage, name string, dst any) error {
	value, ok := raw[name]
	if !ok {
		return fmt.Errorf("swarm protocol: missing %s", name)
	}
	if err := json.Unmarshal(value, dst); err != nil {
		return fmt.Errorf("swarm protocol: %s: %w", name, err)
	}
	if name == "type" {
		if text, ok := dst.(*string); ok && strings.TrimSpace(*text) == "" {
			return errors.New("swarm protocol: empty message type")
		}
	}
	return nil
}

func decodeOptionalString(raw map[string]json.RawMessage, name string, dst *string) error {
	value, ok := raw[name]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(value, dst); err != nil {
		return fmt.Errorf("swarm protocol: %s: %w", name, err)
	}
	return nil
}

func unknownFields(raw map[string]json.RawMessage, known map[string]struct{}) map[string]json.RawMessage {
	var out map[string]json.RawMessage
	for key, value := range raw {
		if _, ok := known[key]; ok {
			continue
		}
		if out == nil {
			out = make(map[string]json.RawMessage)
		}
		out[key] = cloneRaw(value)
	}
	return out
}

func isEnvelopeField(key string) bool {
	switch key {
	case "version", "type", "message_id", "agent_id", "turn_id", "timestamp", "payload":
		return true
	default:
		return false
	}
}

func isLegacyEnvelopeField(key string) bool {
	switch key {
	case "type", "time", "message_id", "agent_id", "turn_id", "version", "timestamp", "payload":
		return true
	default:
		return false
	}
}

func putRaw(out map[string]json.RawMessage, key string, value any) {
	b, err := json.Marshal(value)
	if err == nil {
		out[key] = b
	}
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

// The following small payload types document the stable fields of the
// protocol without forcing a receiver to use them. Receivers can always use
// Envelope.Payload to retain fields added in later versions.
type AgentPingPayload struct{}
type AgentShutdownPayload struct{}
type AgentReadyPayload struct {
	// Version is the worker/application version used by the legacy ready
	// event. WorkerVersion is the unambiguous spelling for new producers.
	Version       string `json:"version,omitempty"`
	WorkerVersion string `json:"worker_version,omitempty"`
	CWD           string `json:"cwd,omitempty"`
	Model         string `json:"model,omitempty"`
	Provider      string `json:"provider,omitempty"`
}
type AgentHeartbeatPayload struct {
	Activity string `json:"activity,omitempty"`
}
type TurnStartPayload struct {
	Prompt string `json:"prompt"`
}
type TurnCancelPayload struct {
	Reason string `json:"reason,omitempty"`
}
type TurnProgressPayload struct {
	Text        string  `json:"text,omitempty"`
	Percent     float64 `json:"percent,omitempty"`
	CurrentTool string  `json:"current_tool,omitempty"`
}
type ToolStartedPayload struct {
	ToolID string `json:"tool_id,omitempty"`
	Name   string `json:"name"`
}
type ToolFinishedPayload struct {
	ToolID string `json:"tool_id,omitempty"`
	Name   string `json:"name,omitempty"`
	Error  string `json:"error,omitempty"`
}
type MessageDeltaPayload struct {
	Text string `json:"text,omitempty"`
}
type ArtifactReference struct {
	Name string `json:"name,omitempty"`
	Ref  string `json:"ref"`
	Mime string `json:"mime,omitempty"`
	Size int64  `json:"size,omitempty"`
}
type ProtocolError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}
type TurnResultPayload struct {
	Status       string              `json:"status"`
	Summary      string              `json:"summary,omitempty"`
	Output       string              `json:"output,omitempty"`
	Structured   json.RawMessage     `json:"structured,omitempty"`
	Artifacts    []ArtifactReference `json:"artifacts,omitempty"`
	ChangedFiles []string            `json:"changed_files,omitempty"`
	Usage        map[string]any      `json:"usage,omitempty"`
	Error        *ProtocolError      `json:"error"`
}
type TurnFailedPayload struct {
	Error *ProtocolError `json:"error,omitempty"`
}
type AgentExitedPayload struct {
	Code   int    `json:"code,omitempty"`
	Reason string `json:"reason,omitempty"`
}
