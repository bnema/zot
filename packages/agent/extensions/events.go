package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/patriceckhart/zot/packages/agent/extproto"
)

// EmitEvent fires a one-way lifecycle event to every extension that
// subscribed to it via SubscribeFromExt.events. Non-blocking: each
// extension's pipe write happens on a per-call goroutine so a slow
// extension can't stall the agent loop.
//
// Event names are documented on extproto.EventFromHost. Unknown event
// names are still routed (subscribers can use any string they want).
func (m *Manager) EmitEvent(ev extproto.EventFromHost) {
	ev.Type = "event"
	frame, err := extproto.Encode(ev)
	if err != nil {
		return
	}
	m.mu.RLock()
	subs := make([]*Extension, 0, len(m.ext))
	for _, ext := range m.ext {
		ext.mu.Lock()
		_, subscribed := ext.eventSubs[ev.Event]
		ext.mu.Unlock()
		if subscribed {
			subs = append(subs, ext)
		}
	}
	m.mu.RUnlock()

	for _, ext := range subs {
		go func(ext *Extension) {
			defer func() { _ = recover() }()
			_, _ = ext.writeFrame(frame)
		}(ext)
	}
}

// UpdateSessionState updates the cached state used when an extension is
// reloaded during the active session. The session file remains the durable
// source of truth; this only keeps the in-memory lifecycle replay current.
func (m *Manager) UpdateSessionState(extension string, state json.RawMessage) {
	if m == nil || strings.TrimSpace(extension) == "" || len(state) > maxSessionStateBytes {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeSession == nil {
		return
	}
	if m.activeSession.states == nil {
		m.activeSession.states = map[string]json.RawMessage{}
	}
	if len(state) == 0 || strings.TrimSpace(string(state)) == "null" {
		delete(m.activeSession.states, extension)
		return
	}
	m.activeSession.states[extension] = append(json.RawMessage(nil), state...)
}

// EmitSessionEvent broadcasts a session lifecycle event with the active
// session identity. Each extension receives only its own opaque state
// snapshot, preventing one extension from reading another extension's data.
// The latest event is retained and replayed to extensions loaded later.
func (m *Manager) EmitSessionEvent(event string, session *extproto.SessionContext, states map[string]json.RawMessage) {
	if event == "" {
		return
	}
	m.sessionEmitMu.Lock()
	defer m.sessionEmitMu.Unlock()
	base := extproto.EventFromHost{Type: "event", Event: event, Session: cloneSessionContext(session)}
	storedStates := cloneExtensionStates(states)
	m.mu.Lock()
	m.activeSession = &sessionEventState{event: base, states: storedStates}
	subs := make([]*Extension, 0, len(m.ext))
	for _, ext := range m.ext {
		ext.mu.Lock()
		_, subscribed := ext.eventSubs[event]
		ext.mu.Unlock()
		if subscribed {
			subs = append(subs, ext)
		}
	}
	m.mu.Unlock()
	for _, ext := range subs {
		m.sendSessionEvent(ext, base, storedStates[ext.Manifest.Name])
	}
}

func (m *Manager) sendSessionEvent(ext *Extension, base extproto.EventFromHost, state json.RawMessage) {
	if ext == nil {
		return
	}
	frame := base
	frame.State = append(json.RawMessage(nil), state...)
	encoded, err := extproto.Encode(frame)
	if err != nil {
		return
	}
	if !ext.enqueueLifecycleFrame(encoded) {
		ext.recordDiagnostic("session lifecycle frame dropped because the extension queue is full or stopped")
	}
}

func (m *Manager) replaySessionEvent(ext *Extension) {
	m.sessionEmitMu.Lock()
	defer m.sessionEmitMu.Unlock()
	m.mu.RLock()
	if m.activeSession == nil {
		m.mu.RUnlock()
		return
	}
	base := m.activeSession.event
	state := append(json.RawMessage(nil), m.activeSession.states[ext.Manifest.Name]...)
	m.mu.RUnlock()
	m.sendSessionEvent(ext, base, state)
}

func cloneSessionContext(in *extproto.SessionContext) *extproto.SessionContext {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneExtensionStates(in map[string]json.RawMessage) map[string]json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for name, state := range in {
		out[name] = append(json.RawMessage(nil), state...)
	}
	return out
}

// InterceptResult aggregates the outcome of walking every subscribed
// interceptor for one event. The zero value means "allow, no
// modification". Callers check Block first; if allowed, use the
// optional rewrite fields (ModifiedArgs for tool_call, ReplaceText
// for assistant_message) to carry the rewrite into the action.
type InterceptResult struct {
	Block        bool
	Reason       string
	ModifiedArgs json.RawMessage
	ReplaceText  string
	Context      string
}

const (
	interceptTimeout     = 5 * time.Second
	maxInterceptContext  = 16 * 1024
	maxSessionStateBytes = 256 * 1024
)

const interceptContextTruncatedMarker = "\n[extension context truncated]"

func boundInterceptContext(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= maxInterceptContext {
		return text
	}
	limit := maxInterceptContext - len(interceptContextTruncatedMarker)
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return strings.TrimSpace(text[:cut]) + interceptContextTruncatedMarker
}

// InterceptToolCall is the typed entry point for tool_call
// interception. Subscribers are invoked serially; the first to return
// Block=true wins. Rewrites (ModifiedArgs) from earlier subscribers
// flow into later ones, so a chain of guards can successively redact
// / patch the args.
func (m *Manager) InterceptToolCall(ctx context.Context, toolID, toolName string, args json.RawMessage) InterceptResult {
	subs := m.interceptSubsFor("tool_call")
	if len(subs) == 0 {
		return InterceptResult{}
	}
	current := args
	for _, ext := range subs {
		r := m.askIntercept(ctx, ext, extproto.EventInterceptFromHost{
			Event:    "tool_call",
			ToolID:   toolID,
			ToolName: toolName,
			ToolArgs: current,
		})
		if r.Block {
			return r
		}
		if len(r.ModifiedArgs) > 0 && json.Valid(r.ModifiedArgs) {
			current = r.ModifiedArgs
		}
	}
	out := InterceptResult{}
	if !jsonEqual(current, args) {
		out.ModifiedArgs = current
	}
	return out
}

// InterceptTurnStart asks every subscriber whether the upcoming turn
// may run. Block=true aborts the turn with Reason shown to the user.
// Rewrites are not supported for this event.
func (m *Manager) InterceptTurnStart(ctx context.Context, step int) InterceptResult {
	subs := m.interceptSubsFor("turn_start")
	if len(subs) == 0 {
		return InterceptResult{}
	}
	var contexts []string
	for _, ext := range subs {
		r := m.askIntercept(ctx, ext, extproto.EventInterceptFromHost{
			Event: "turn_start",
			Step:  step,
		})
		if r.Block {
			return r
		}
		if text := strings.TrimSpace(r.Context); text != "" {
			contexts = append(contexts, "["+ext.Manifest.Name+"]\n"+text)
		}
	}
	return InterceptResult{Context: boundInterceptContext(strings.Join(contexts, "\n\n"))}
}

// InterceptAssistantMessage asks every subscriber to approve, block,
// or rewrite the assistant's final visible text. Block=true hides the
// message from the user entirely; a non-empty ReplaceText rewrites
// what the user sees while keeping the model's original text in the
// transcript. Successive rewrites chain: each subscriber sees the
// previous subscriber's output.
func (m *Manager) InterceptAssistantMessage(ctx context.Context, text string) InterceptResult {
	subs := m.interceptSubsFor("assistant_message")
	if len(subs) == 0 {
		return InterceptResult{}
	}
	current := text
	for _, ext := range subs {
		r := m.askIntercept(ctx, ext, extproto.EventInterceptFromHost{
			Event: "assistant_message",
			Text:  current,
		})
		if r.Block {
			return r
		}
		if r.ReplaceText != "" {
			current = r.ReplaceText
		}
	}
	out := InterceptResult{}
	if current != text {
		out.ReplaceText = current
	}
	return out
}

// interceptSubsFor returns the snapshot of extensions that subscribed
// to intercepting the named event.
func (m *Manager) interceptSubsFor(event string) []*Extension {
	m.mu.RLock()
	defer m.mu.RUnlock()
	subs := make([]*Extension, 0, len(m.ext))
	for _, ext := range m.ext {
		ext.mu.Lock()
		_, subscribed := ext.interceptSubs[event]
		ext.mu.Unlock()
		if subscribed {
			subs = append(subs, ext)
		}
	}
	return subs
}

// askIntercept sends one EventInterceptFromHost to ext and waits for
// the reply, a timeout, or context cancellation. Returns a typed
// result. Never blocks for longer than interceptTimeout.
func (m *Manager) askIntercept(ctx context.Context, ext *Extension, payload extproto.EventInterceptFromHost) InterceptResult {
	id := newCorrelationID()
	ch := make(chan extproto.EventInterceptResponseFromExt, 1)
	ext.mu.Lock()
	ext.pendingIntercept[id] = ch
	ext.mu.Unlock()

	payload.Type = "event_intercept"
	payload.ID = id
	frame, err := extproto.Encode(payload)
	if err != nil {
		ext.mu.Lock()
		delete(ext.pendingIntercept, id)
		ext.mu.Unlock()
		return InterceptResult{}
	}
	if _, err := ext.writeFrame(frame); err != nil {
		ext.mu.Lock()
		delete(ext.pendingIntercept, id)
		ext.mu.Unlock()
		fmt.Fprintf(ext.logFile, "[zot] intercept write failed: %v\n", err)
		return InterceptResult{}
	}

	select {
	case resp := <-ch:
		return InterceptResult{
			Block:        resp.Block,
			Reason:       resp.Reason,
			ModifiedArgs: resp.ModifiedArgs,
			ReplaceText:  resp.ReplaceText,
			Context:      resp.Context,
		}
	case <-time.After(interceptTimeout):
		ext.mu.Lock()
		delete(ext.pendingIntercept, id)
		ext.mu.Unlock()
		fmt.Fprintf(ext.logFile, "[zot] intercept %s timed out; allowing\n", payload.Event)
		return InterceptResult{}
	case <-ctx.Done():
		ext.mu.Lock()
		delete(ext.pendingIntercept, id)
		ext.mu.Unlock()
		return InterceptResult{}
	}
}

// jsonEqual reports whether a and b encode the same JSON value. Used
// to detect whether the interceptor chain actually mutated the args.
func jsonEqual(a, b json.RawMessage) bool {
	if len(a) == len(b) {
		same := true
		for i := range a {
			if a[i] != b[i] {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	// Fallback to structural compare so whitespace differences don't
	// register as a mutation. (We don't actually rely on this to be
	// cheap, callers only use it to pick a code path.)
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	ae, _ := json.Marshal(av)
	be, _ := json.Marshal(bv)
	return string(ae) == string(be)
}
