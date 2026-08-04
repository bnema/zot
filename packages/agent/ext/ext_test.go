package ext

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/patriceckhart/zot/packages/agent/extproto"
)

// ---------- test harness ----------

// extHarness wires an Extension to io.Pipe pairs so a test can play
// the role of the host: write host→ext frames, read ext→host frames.
// The scanner runs in a permanent background goroutine and delivers
// frames over a buffered channel, avoiding the deadlock that would
// occur if the test goroutine alternated between writing and reading
// a synchronous pipe.
type extHarness struct {
	ext    *Extension
	hostW  *io.PipeWriter // test writes here → ext reads as stdin
	frames chan rawFrame  // ext→host frames delivered here
}

type rawFrame struct {
	hdr extproto.Frame
	raw []byte
}

func newHarness(name string) *extHarness {
	extStdinR, extStdinW := io.Pipe()
	extStdoutR, extStdoutW := io.Pipe()

	e := New(name, "0.0.0-test")
	e.in = extStdinR
	e.out = extStdoutW
	e.stderr = io.Discard

	h := &extHarness{
		ext:    e,
		hostW:  extStdinW,
		frames: make(chan rawFrame, 64),
	}

	// Background reader: scan ext's stdout and push every frame into
	// the channel so the test goroutine never needs to block on the pipe.
	go func() {
		scanner := bufio.NewScanner(extStdoutR)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			b := scanner.Bytes()
			cp := make([]byte, len(b))
			copy(cp, b)
			var f extproto.Frame
			json.Unmarshal(cp, &f)
			h.frames <- rawFrame{f, cp}
		}
		close(h.frames)
	}()

	return h
}

// next returns the next frame, timing out after 2 s.
func (h *extHarness) next(t *testing.T) rawFrame {
	t.Helper()
	select {
	case f, ok := <-h.frames:
		if !ok {
			t.Fatal("frame channel closed (ext stdout EOF)")
		}
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for frame from extension")
		return rawFrame{}
	}
}

// drainUntil reads frames until one with type == want arrives.
func (h *extHarness) drainUntil(t *testing.T, want string) rawFrame {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case f, ok := <-h.frames:
			if !ok {
				t.Fatalf("frame channel closed before seeing %q", want)
			}
			if f.hdr.Type == want {
				return f
			}
		case <-deadline.C:
			t.Fatalf("timeout waiting for frame type %q", want)
			return rawFrame{}
		}
	}
}

// sendToExt writes a host→ext frame.
func (h *extHarness) sendToExt(t *testing.T, v any) {
	t.Helper()
	b, err := extproto.Encode(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := h.hostW.Write(b); err != nil {
		t.Fatalf("write to ext: %v", err)
	}
}

// handshake performs the hello / hello_ack exchange and drains frames
// until "ready".
func (h *extHarness) handshake(t *testing.T) {
	t.Helper()
	f := h.next(t)
	if f.hdr.Type != "hello" {
		t.Fatalf("expected hello, got %q", f.hdr.Type)
	}
	h.sendToExt(t, extproto.HelloAckFromHost{
		Type:            "hello_ack",
		ProtocolVersion: extproto.ProtocolVersion,
		ZotVersion:      "0.0.0-test",
		Provider:        "anthropic",
		Model:           "claude-test",
	})
	for {
		f := h.next(t)
		if f.hdr.Type == "ready" {
			return
		}
	}
}

// ---------- tests ----------

// TestOnHelloCanRegisterUsingHostInfo checks that extensions can use
// host metadata before announcing their initial registrations.
func TestOnHelloCanRegisterUsingHostInfo(t *testing.T) {
	h := newHarness("cwd-ext")

	h.ext.OnHello(func(host HostInfo) {
		h.ext.Command("cwd", "show cwd", func(args string) Response {
			return Display(host.CWD)
		})
	})

	go h.ext.Run()

	f := h.next(t)
	if f.hdr.Type != "hello" {
		t.Fatalf("expected hello, got %q", f.hdr.Type)
	}
	h.sendToExt(t, extproto.HelloAckFromHost{
		Type:            "hello_ack",
		ProtocolVersion: extproto.ProtocolVersion,
		ZotVersion:      "0.0.0-test",
		Provider:        "anthropic",
		Model:           "claude-test",
		CWD:             "/tmp/project",
	})

	var sawCommand bool
	for {
		f := h.next(t)
		if f.hdr.Type == "register_command" {
			var rc extproto.RegisterCommandFromExt
			if err := json.Unmarshal(f.raw, &rc); err != nil {
				t.Fatalf("unmarshal register_command: %v", err)
			}
			if rc.Name == "cwd" {
				sawCommand = true
			}
		}
		if f.hdr.Type == "ready" {
			break
		}
	}
	if !sawCommand {
		t.Fatal("OnHello-registered command was not announced before ready")
	}
	if got := h.ext.Host().CWD; got != "/tmp/project" {
		t.Fatalf("Host().CWD = %q, want /tmp/project", got)
	}

	h.hostW.Close()
}

// TestOpenPanelEmitsCorrectFrame checks that e.OpenPanel sends a
// well-formed open_panel frame with the correct PanelSpec fields.
func TestOpenPanelEmitsCorrectFrame(t *testing.T) {
	h := newHarness("test-ext")
	go h.ext.Run()
	h.handshake(t)

	go h.ext.OpenPanel("my-panel", "My Title", []string{"line a", "line b"}, "esc close")

	f := h.drainUntil(t, "open_panel")

	var op extproto.OpenPanelFromExt
	if err := json.Unmarshal(f.raw, &op); err != nil {
		t.Fatalf("unmarshal open_panel: %v", err)
	}
	if op.Panel.ID != "my-panel" {
		t.Errorf("panel id: want %q, got %q", "my-panel", op.Panel.ID)
	}
	if op.Panel.Title != "My Title" {
		t.Errorf("panel title: want %q, got %q", "My Title", op.Panel.Title)
	}
	if len(op.Panel.Lines) != 2 || op.Panel.Lines[0] != "line a" || op.Panel.Lines[1] != "line b" {
		t.Errorf("panel lines: got %v", op.Panel.Lines)
	}
	if op.Panel.Footer != "esc close" {
		t.Errorf("panel footer: want %q, got %q", "esc close", op.Panel.Footer)
	}

	h.hostW.Close()
}

func TestAlertEmitsStructuredFrame(t *testing.T) {
	h := newHarness("alert-ext")
	go h.ext.Run()
	h.handshake(t)

	go h.ext.Alert(AlertRequest{Kind: AlertKindBell, Reason: "question_ready"})
	f := h.drainUntil(t, "alert")

	var alert extproto.AlertFromExt
	if err := json.Unmarshal(f.raw, &alert); err != nil {
		t.Fatalf("unmarshal alert: %v", err)
	}
	if alert.Kind != AlertKindBell || alert.Reason != "question_ready" {
		t.Fatalf("alert = %+v, want bell/question_ready", alert)
	}
	h.hostW.Close()
}

func TestToolConfirmationRequestedEvent(t *testing.T) {
	h := newHarness("confirmation-events")
	received := make(chan Event, 1)
	h.ext.On("tool_confirmation_requested", func(event Event) {
		received <- event
	})

	go h.ext.Run()
	h.handshake(t)
	h.sendToExt(t, extproto.EventFromHost{
		Type:        "event",
		Event:       "tool_confirmation_requested",
		ToolID:      "call-1",
		ToolName:    "bash",
		ToolPreview: "go test ./...",
	})

	select {
	case event := <-received:
		if event.Name != "tool_confirmation_requested" || event.ToolID != "call-1" || event.ToolName != "bash" || event.ToolPreview != "go test ./..." {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for confirmation event")
	}
	h.hostW.Close()
}

func TestDeferredToolRegistrationAndActivation(t *testing.T) {
	h := newHarness("deferred-ext")
	h.ext.DeferredTool("weather", "weather lookup", json.RawMessage(`{"type":"object"}`), func(json.RawMessage) ToolResult {
		return TextResult("sunny")
	})
	h.ext.Tool("search_tools", "find tools", json.RawMessage(`{"type":"object"}`), func(json.RawMessage) ToolResult {
		result := TextResult("enabled")
		result.ActivateTools = []string{"weather"}
		return result
	})

	go h.ext.Run()
	if frame := h.next(t); frame.hdr.Type != "hello" {
		t.Fatalf("expected hello, got %q", frame.hdr.Type)
	}
	h.sendToExt(t, extproto.HelloAckFromHost{Type: "hello_ack", ProtocolVersion: extproto.ProtocolVersion})

	deferred := false
	for {
		frame := h.next(t)
		if frame.hdr.Type == "ready" {
			break
		}
		if frame.hdr.Type == "register_tool" {
			var registration extproto.RegisterToolFromExt
			if err := json.Unmarshal(frame.raw, &registration); err != nil {
				t.Fatal(err)
			}
			if registration.Name == "weather" {
				deferred = registration.Deferred
			}
		}
	}
	if !deferred {
		t.Fatal("weather registration was not deferred")
	}

	h.sendToExt(t, extproto.ToolCallFromHost{Type: "tool_call", ID: "call-1", Name: "search_tools", Args: json.RawMessage(`{}`)})
	frame := h.drainUntil(t, "tool_result")
	var result extproto.ToolResultFromExt
	if err := json.Unmarshal(frame.raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.ActivateTools) != 1 || result.ActivateTools[0] != "weather" {
		t.Fatalf("activate_tools = %v", result.ActivateTools)
	}
	h.hostW.Close()
}

// TestBlockingToolWaitsForPanelKey is the core integration test for
// the human-in-the-loop pattern: the tool handler opens a panel,
// blocks on a channel, and only returns a tool_result after a key
// event arrives.
func TestBlockingToolWaitsForPanelKey(t *testing.T) {
	h := newHarness("gate-ext")

	const pid = "gate-panel"
	const toolCallID = "tc-001"

	approved := make(chan bool, 1)

	h.ext.OnPanelKey(pid, func(key, text string) {
		switch {
		case key == "rune" && text == "y":
			h.ext.ClosePanel(pid)
			approved <- true
		case key == "rune" && text == "n", key == "esc":
			h.ext.ClosePanel(pid)
			approved <- false
		}
	}, func() { approved <- false })

	h.ext.Tool("gate", "needs approval",
		json.RawMessage(`{"type":"object","properties":{}}`),
		func(args json.RawMessage) ToolResult {
			h.ext.OpenPanel(pid, "Approve?",
				[]string{"  y  approve", "  n  deny"}, "y/n")
			if <-approved {
				return TextResult("approved")
			}
			return TextErrorResult("denied")
		})

	go h.ext.Run()
	h.handshake(t)

	h.sendToExt(t, extproto.ToolCallFromHost{
		Type: "tool_call", ID: toolCallID, Name: "gate",
		Args: json.RawMessage(`{}`),
	})

	// Tool goroutine must open the panel before it can reply.
	h.drainUntil(t, "open_panel")

	// Send approval — tool should now unblock and emit tool_result.
	h.sendToExt(t, extproto.PanelKeyFromHost{
		Type: "panel_key", PanelID: pid, Key: "rune", Text: "y",
	})

	f := h.drainUntil(t, "tool_result")
	var tr extproto.ToolResultFromExt
	if err := json.Unmarshal(f.raw, &tr); err != nil {
		t.Fatalf("unmarshal tool_result: %v", err)
	}
	if tr.ID != toolCallID {
		t.Errorf("tool_result id: want %q, got %q", toolCallID, tr.ID)
	}
	if tr.IsError {
		t.Errorf("expected success, got is_error=true")
	}
	if len(tr.Content) == 0 || !strings.Contains(tr.Content[0].Text, "approved") {
		t.Errorf("expected 'approved' in content, got %+v", tr.Content)
	}

	h.hostW.Close()
}

// TestBlockingToolDenied mirrors TestBlockingToolWaitsForPanelKey but
// sends "n" so the tool returns an error result.
func TestBlockingToolDenied(t *testing.T) {
	h := newHarness("gate-ext-deny")

	const pid = "deny-panel"
	const toolCallID = "tc-002"

	approved := make(chan bool, 1)

	h.ext.OnPanelKey(pid, func(key, text string) {
		switch {
		case key == "rune" && text == "y":
			h.ext.ClosePanel(pid)
			approved <- true
		case key == "rune" && text == "n", key == "esc":
			h.ext.ClosePanel(pid)
			approved <- false
		}
	}, func() { approved <- false })

	h.ext.Tool("gate2", "needs approval",
		json.RawMessage(`{"type":"object","properties":{}}`),
		func(args json.RawMessage) ToolResult {
			h.ext.OpenPanel(pid, "Approve?", []string{"y/n"}, "")
			if <-approved {
				return TextResult("approved")
			}
			return TextErrorResult("denied")
		})

	go h.ext.Run()
	h.handshake(t)

	h.sendToExt(t, extproto.ToolCallFromHost{
		Type: "tool_call", ID: toolCallID, Name: "gate2",
		Args: json.RawMessage(`{}`),
	})

	h.drainUntil(t, "open_panel")

	h.sendToExt(t, extproto.PanelKeyFromHost{
		Type: "panel_key", PanelID: pid, Key: "rune", Text: "n",
	})

	f := h.drainUntil(t, "tool_result")
	var tr extproto.ToolResultFromExt
	if err := json.Unmarshal(f.raw, &tr); err != nil {
		t.Fatalf("unmarshal tool_result: %v", err)
	}
	if !tr.IsError {
		t.Errorf("expected is_error=true on denial")
	}
	if len(tr.Content) == 0 || !strings.Contains(tr.Content[0].Text, "denied") {
		t.Errorf("expected 'denied' in content, got %+v", tr.Content)
	}

	h.hostW.Close()
}
