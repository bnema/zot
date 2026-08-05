package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/patriceckhart/zot/packages/agent/modes"
	"github.com/patriceckhart/zot/packages/agent/subagents"
	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
)

// runSubagentWorkerMode is the daemon-mode entry point used by every
// subagent-spawned zot subprocess. It's intentionally close in shape to
// runJSONMode but with two key differences:
//
//   - Lifetime: the process stays alive across many user turns. The
//     initial positional task (if any) is the first turn; subsequent
//     turns arrive through the inbox unix socket at args.SubagentWorker.
//
//   - Output: every emitted JSON line is also mirrored verbatim into
//     events.jsonl (see ZOT_SUBAGENT_EVENT_LOG) so a separate zot
//     process can /subagents open this agent and replay its full history
//     even after the parent that spawned us is long gone.
//
// The runner in packages/agent/subagents/runner.go is the only caller in
// production; tests use the stubchild binary under
// packages/agent/subagents/testdata/cmd/stubchild instead of the real model
// loop.
func runSubagentWorkerMode(ctx context.Context, args Args, version string) error {
	if args.SubagentWorker == "" {
		return fmt.Errorf("--subagent-worker requires a socket path")
	}
	if os.Getenv("ZOT_SUBAGENT_CREDENTIAL_STDIN") == "1" {
		var inherited subagents.Credential
		dec := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20))
		if err := dec.Decode(&inherited); err != nil {
			return fmt.Errorf("read inherited subagent credential: %w", err)
		}
		if inherited.Value == "" || inherited.Method == "" {
			return fmt.Errorf("read inherited subagent credential: missing value or method")
		}
		args.inheritedCredential = inherited.Value
		args.inheritedAuthMethod = inherited.Method
		args.inheritedAccountID = inherited.AccountID
	}

	r, err := Resolve(args, true)
	if err != nil {
		return err
	}
	extMgr, stopExt := setupNonInteractiveExtensions(ctx, args, &r, version)
	defer stopExt()

	ag := r.NewAgent()
	initialAg := ag
	defer func() {
		closeAgentLSP(ag)
		if ag != initialAg {
			closeAgentLSP(initialAg)
		}
	}()
	wireNonInteractiveAgentExtHooks(ctx, ag, extMgr)
	sess, err := openOrCreateSession(args, r, ag, version)
	if err != nil {
		return err
	}
	if sess != nil {
		var providerName, model string
		sess, ag, providerName, model, err = applyInitialSessionResume(ctx, args, r, extMgr, sess, ag)
		if err != nil {
			return err
		}
		r.Provider, r.Model = providerName, model
		ag.OnToolResult = func(_ string, result core.ToolResult) { persistExtensionToolResult(extMgr, sess, result) }
		defer sess.Close()
	}
	announceSession(extMgr, sess)

	// Open the inbox listener BEFORE emitting agent_ready so the
	// supervisor can dial through on the very first send. The
	// subagent supervisor's Inbox retries dialing for a
	// short window, but emitting ready first and then listening
	// would still race in tight loops.
	ln, err := subagents.Listen(args.SubagentWorker)
	if err != nil {
		return fmt.Errorf("subagent-worker listen: %w", err)
	}
	defer ln.Close()

	// Event log is owned by the supervisor's runner via stdout, but
	// the daemon also writes a redundant copy here when the runner's
	// pipe is closed (e.g. parent zot exited but the agent is still
	// running headless). The env var is set by the runner; if it's
	// empty we silently skip the second mirror.
	var logMirror *subagents.EventLog
	if path := os.Getenv("ZOT_SUBAGENT_EVENT_LOG"); path != "" {
		logMirror, _ = subagents.OpenEventLog(path)
	}
	if logMirror != nil {
		defer logMirror.Close()
	}

	em := newSubagentEmitter(os.Stdout, logMirror)
	em.setProtocolIdentity(os.Getenv("ZOT_SUBAGENT_AGENT_ID"))
	em.emit("agent.ready", map[string]any{
		"version": version,
		"cwd":     r.CWD,
		"model":   r.Model,
	})

	// Heartbeats make a live-but-detached child distinguishable from a
	// failed turn after supervisor reconnect. The parent still treats
	// unknown event types as forward-compatible.
	heartbeatDone := make(chan struct{})
	defer close(heartbeatDone)
	go func() {
		interval := 10 * time.Second
		if raw := os.Getenv("ZOT_SUBAGENT_HEARTBEAT_INTERVAL"); raw != "" {
			if parsed, parseErr := time.ParseDuration(raw); parseErr == nil && parsed > 0 {
				interval = parsed
			}
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatDone:
				return
			case <-ticker.C:
				em.emit("agent.heartbeat", map[string]any{"activity": "alive"})
			}
		}
	}()

	// Keep a per-turn cancel so the "cancel" inbox message can interrupt
	// an in-flight turn without tearing down the whole daemon.
	var (
		mu           sync.Mutex
		cancelFn     context.CancelFunc
		busyTurn     bool
		turnNo       = initialTurnNumber(os.Getenv("ZOT_SUBAGENT_EVENT_LOG"))
		turnWG       sync.WaitGroup
		closing      bool
		shutdown     = make(chan struct{})
		shutdownOnce sync.Once
	)
	maxOutputBytes, maxOutputLines := workerOutputLimits()

	runOne := func(prompt string) {
		mu.Lock()
		// launchTurn admits the goroutine to turnWG before it starts so
		// shutdown can join it. A shutdown can win the scheduling race
		// after that admission but before this goroutine acquires mu; in
		// that case do not start a new Prompt after waitForActiveTurn has
		// declared the worker closing.
		if closing {
			mu.Unlock()
			return
		}
		if args.SubagentMaxTurns > 0 && turnNo >= args.SubagentMaxTurns {
			// Consume a unique rejected attempt so repeated prompts after
			// the limit cannot reuse the same turn id in the event log.
			turnNo++
			step := turnNo
			mu.Unlock()
			turnID := fmt.Sprintf("turn-%d", step)
			errPayload := map[string]any{"code": "max_turns", "message": "maximum subagent turns reached"}
			em.emit("turn.result", map[string]any{"status": "failed", "turn_id": turnID, "error": errPayload})
			em.emit("turn.failed", map[string]any{"turn_id": turnID, "error": errPayload})
			em.emit("turn_end", map[string]any{"step": step, "turn_id": turnID, "error": errPayload["message"]})
			em.emit("agent.idle", map[string]any{"turn_id": turnID})
			return
		}
		if busyTurn {
			// Drop concurrent turns rather than queuing. The
			// supervisor protocol assumes one outstanding turn per
			// agent; if a user really wants to interrupt and start
			// another, they should send "cancel" first.
			mu.Unlock()
			em.emit("error", map[string]any{"message": "agent busy; send 'cancel' first"})
			return
		}
		busyTurn = true
		turnNo++
		step := turnNo
		c, cancel := context.WithCancel(ctx)
		cancelFn = cancel
		mu.Unlock()

		turnID := fmt.Sprintf("turn-%d", step)
		em.emit("turn.started", map[string]any{"step": step, "turn_id": turnID})

		sink := func(ev core.AgentEvent) {
			em.emit(ev.Type(), modes.EventToJSON(ev))
		}

		start := len(ag.Messages())
		err := ag.Prompt(c, prompt, nil, sink)
		WriteNewTranscript(ag, sess, start)

		output := finalAssistantText(ag.Messages()[start:])
		resultStatus := "succeeded"
		if err != nil {
			if errors.Is(err, context.Canceled) {
				resultStatus = "canceled"
			} else {
				resultStatus = "failed"
			}
		}
		resultPayload := map[string]any{
			"status":  resultStatus,
			"turn_id": turnID,
			"summary": boundedResultSummary(output),
			"output":  boundedResultOutput(output, maxOutputBytes, maxOutputLines),
			"error":   resultErrorPayload(err),
		}
		em.emit("turn.result", resultPayload)
		if err != nil {
			em.emit("turn.failed", map[string]any{"turn_id": turnID, "error": resultErrorPayload(err)})
		}
		em.emit("turn_end", map[string]any{
			"step":    step,
			"turn_id": turnID,
			"error":   errString(err),
		})
		em.emit("agent.idle", map[string]any{"turn_id": turnID})

		mu.Lock()
		busyTurn = false
		cancelFn = nil
		mu.Unlock()
	}

	launchTurn := func(prompt string) {
		mu.Lock()
		if closing {
			mu.Unlock()
			return
		}
		// Register the goroutine before launching it. Shutdown can then
		// close the gate and wait for every turn that was admitted, even
		// if runOne has not acquired mu and set busyTurn yet.
		turnWG.Add(1)
		mu.Unlock()
		go func() {
			defer turnWG.Done()
			runOne(prompt)
		}()
	}

	waitForActiveTurn := func(cancel bool) {
		mu.Lock()
		closing = true
		activeCancel := cancelFn
		mu.Unlock()
		if cancel && activeCancel != nil {
			activeCancel()
		}
		turnWG.Wait()
	}

	requestShutdown := func() {
		mu.Lock()
		closing = true
		mu.Unlock()
		shutdownOnce.Do(func() { close(shutdown) })
	}

	// Initial task: run before processing the inbox so the agent
	// "starts working" the moment it boots, matching what users
	// expect from `/subagents new <task>`.
	if args.Prompt != "" {
		launchTurn(args.Prompt)
	}

	// Inbox loop: one supervisor message at a time. We don't spawn
	// a goroutine per turn because runOne already serialises them
	// via the busyTurn flag; doing the dispatch on the main
	// goroutine keeps the daemon's lifecycle easy to follow.
	for {
		select {
		case <-ctx.Done():
			waitForActiveTurn(true)
			em.emit("agent.exited", map[string]any{"reason": "cancelled"})
			return ctx.Err()
		case <-shutdown:
			// Shutdown is an explicit supervisor stop. Cancel an active
			// turn before joining it so a detached worker cannot remain
			// alive indefinitely while the host waits for its socket to
			// disappear.
			waitForActiveTurn(true)
			em.emit("agent.exited", map[string]any{"reason": "shutdown"})
			return nil
		case msg, ok := <-ln.Lines():
			if !ok {
				waitForActiveTurn(true)
				em.emit("agent.exited", map[string]any{"reason": "inbox-closed"})
				return nil
			}
			command, parseErr := subagents.ParseCommand(msg)
			if parseErr != nil {
				em.emit("error", map[string]any{"message": "invalid supervisor command"})
				continue
			}
			switch command.Type {
			case subagents.CommandAgentShutdown:
				requestShutdown()
			case subagents.CommandTurnCancel:
				mu.Lock()
				if cancelFn != nil {
					cancelFn()
				}
				mu.Unlock()
			case subagents.CommandAgentPing:
				em.emit("agent.heartbeat", map[string]any{"activity": "alive"})
			case subagents.CommandTurnStart:
				var payload subagents.TurnStartPayload
				if err := command.DecodePayload(&payload); err != nil {
					em.emit("error", map[string]any{"message": "invalid turn.start payload"})
					continue
				}
				launchTurn(payload.Prompt)
			default:
				// Unknown commands are rejected rather than interpreted as
				// child-originated control requests.
				em.emit("error", map[string]any{"message": "unknown supervisor command"})
			}
		}
	}
}

// subagentEmitter serialises events to stdout and (optionally) to a
// durable log file. Concurrent goroutines call emit so we have to
// hold a mutex around the encoder.
type subagentEmitter struct {
	mu      sync.Mutex
	w       *os.File
	mirror  *subagents.EventLog
	agentID string

	// orphan flips true the first time a stdout write fails (broken
	// pipe — the supervisor died). Until then the mirror stays
	// dormant: the supervisor is the canonical writer to events.jsonl
	// (it parses our stdout and Append()s each event itself). Writing
	// from both sides used to land every event in the log twice,
	// which showed up as a fully-duplicated transcript the next time
	// the agent was reloaded — the exact "why is everything doubled"
	// bug. Once orphaned the mirror takes over so the events still
	// land on disk for the next reload.
	orphan bool
}

func newSubagentEmitter(w *os.File, mirror *subagents.EventLog) *subagentEmitter {
	return &subagentEmitter{w: w, mirror: mirror}
}

func (e *subagentEmitter) setProtocolIdentity(agentID string) {
	e.mu.Lock()
	e.agentID = agentID
	e.mu.Unlock()
}

func (e *subagentEmitter) emit(typ string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	payload := make(map[string]any, len(data))
	for key, value := range data {
		payload[key] = value
	}
	turnID, _ := payload["turn_id"].(string)

	e.mu.Lock()
	defer e.mu.Unlock()

	wireType := canonicalWorkerEvent(typ)
	envelope := subagents.NewEventEnvelope(wireType, e.agentID, turnID, payload)
	line, err := subagents.MarshalJSONL(envelope)

	if !e.orphan && err == nil {
		if _, werr := e.w.Write(line); werr != nil {
			// Supervisor's stdout pipe is gone (parent zot exited but we
			// kept running). Switch to mirror-only mode and preserve this
			// event in the durable log.
			e.orphan = true
		}
	}

	if e.orphan && e.mirror != nil {
		_ = e.mirror.Append(subagents.NewEvent(wireType, payload))
	}
}

func canonicalWorkerEvent(typ string) string {
	switch typ {
	case "agent_ready":
		return subagents.EventAgentReady
	case "agent_stopped":
		return subagents.EventAgentExited
	case "turn_start":
		return subagents.EventTurnStarted
	case "turn_progress":
		return subagents.EventTurnProgress
	case "tool_call":
		return subagents.EventToolStarted
	case "tool_result":
		return subagents.EventToolFinished
	case "text_delta":
		return subagents.EventMessageDelta
	default:
		return typ
	}
}

func finalAssistantText(messages []provider.Message) string {
	var out strings.Builder
	for _, message := range messages {
		if message.Role != provider.RoleAssistant {
			continue
		}
		for _, block := range message.Content {
			if text, ok := block.(provider.TextBlock); ok {
				if out.Len() > 0 {
					out.WriteByte('\n')
				}
				out.WriteString(text.Text)
			}
		}
	}
	return out.String()
}

func firstLine(text string) string {
	return strings.Split(strings.TrimSpace(text), "\n")[0]
}

func boundedResultSummary(text string) string {
	return boundedResultOutput(firstLine(text), 4*1024, 1)
}

func workerOutputLimits() (maxBytes, maxLines int) {
	maxBytes, maxLines = 500_000, 5_000
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv("ZOT_SUBAGENT_MAX_OUTPUT_BYTES"))); err == nil && value > 0 {
		maxBytes = value
	}
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv("ZOT_SUBAGENT_MAX_OUTPUT_LINES"))); err == nil && value > 0 {
		maxLines = value
	}
	return maxBytes, maxLines
}

func boundedResultOutput(text string, maxBytes, maxLines int) string {
	const marker = "...[output truncated]"
	lines := strings.Split(text, "\n")
	if maxLines > 0 && len(lines) > maxLines {
		if maxLines == 1 {
			text = marker
		} else {
			text = strings.Join(append(lines[:maxLines-1], marker), "\n")
		}
	}
	if maxBytes > 0 && len([]byte(text)) > maxBytes {
		if maxBytes <= len(marker) {
			return truncateOutputUTF8(text, maxBytes)
		}
		return truncateOutputUTF8(text, maxBytes-len(marker)) + marker
	}
	return text
}

func truncateOutputUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	data := []byte(value)
	if len(data) <= maxBytes {
		return value
	}
	data = data[:maxBytes]
	for len(data) > 0 && !utf8.Valid(data) {
		data = data[:len(data)-1]
	}
	return string(data)
}

func resultErrorPayload(err error) map[string]any {
	if err == nil {
		return nil
	}
	return map[string]any{"code": "turn_failed", "message": truncateForLog(err.Error(), 500)}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func initialTurnNumber(path string) int {
	if strings.TrimSpace(path) == "" {
		return 0
	}
	events, err := subagents.ReadEventLog(path)
	if err != nil {
		return 0
	}
	maxStep := 0
	for _, ev := range events {
		if step, ok := ev.Data["step"].(float64); ok && int(step) > maxStep {
			maxStep = int(step)
		}
		if strings.HasPrefix(ev.TurnID, "turn-") {
			if n, parseErr := strconv.Atoi(strings.TrimPrefix(ev.TurnID, "turn-")); parseErr == nil && n > maxStep {
				maxStep = n
			}
		}
	}
	return maxStep
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return strings.Repeat(".", n)
	}
	return s[:n-3] + "..."
}

// _ keeps the provider import used; provider types may surface
// through ag.OnEvent / modes.EventToJSON in future iterations.
var _ provider.Content = provider.TextBlock{}
