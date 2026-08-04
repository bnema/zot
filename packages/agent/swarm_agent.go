package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/patriceckhart/zot/packages/agent/modes"
	"github.com/patriceckhart/zot/packages/agent/swarm"
	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
)

// runSwarmAgentMode is the daemon-mode entry point used by every
// swarm-spawned zot subprocess. It's intentionally close in shape to
// runJSONMode but with two key differences:
//
//   - Lifetime: the process stays alive across many user turns. The
//     initial positional task (if any) is the first turn; subsequent
//     turns arrive through the inbox unix socket at args.SwarmAgent.
//
//   - Output: every emitted JSON line is also mirrored verbatim into
//     events.jsonl (see ZOT_SWARM_EVENT_LOG) so a separate zot
//     process can /swarm open this agent and replay its full history
//     even after the parent that spawned us is long gone.
//
// The runner in packages/agent/swarm/runner.go is the only caller in
// production; tests use the stubchild binary under
// packages/agent/swarm/testdata/cmd/stubchild instead of the real model
// loop.
func runSwarmAgentMode(ctx context.Context, args Args, version string) error {
	if args.SwarmAgent == "" {
		return fmt.Errorf("--swarm-agent requires a socket path")
	}
	if os.Getenv("ZOT_SWARM_CREDENTIAL_STDIN") == "1" {
		var inherited swarm.Credential
		dec := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20))
		if err := dec.Decode(&inherited); err != nil {
			return fmt.Errorf("read inherited swarm credential: %w", err)
		}
		if inherited.Value == "" || inherited.Method == "" {
			return fmt.Errorf("read inherited swarm credential: missing value or method")
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
	// swarm supervisor's Inbox.SendInput retries dialing for a
	// short window, but emitting ready first and then listening
	// would still race in tight loops.
	ln, err := swarm.Listen(args.SwarmAgent)
	if err != nil {
		return fmt.Errorf("swarm-agent listen: %w", err)
	}
	defer ln.Close()

	// Event log is owned by the supervisor's runner via stdout, but
	// the daemon also writes a redundant copy here when the runner's
	// pipe is closed (e.g. parent zot exited but the agent is still
	// running headless). The env var is set by the runner; if it's
	// empty we silently skip the second mirror.
	var logMirror *swarm.EventLog
	if path := os.Getenv("ZOT_SWARM_EVENT_LOG"); path != "" {
		logMirror, _ = swarm.OpenEventLog(path)
	}
	if logMirror != nil {
		defer logMirror.Close()
	}

	em := newSwarmEmitter(os.Stdout, logMirror)

	em.emit("agent_ready", map[string]any{
		"version": version,
		"cwd":     r.CWD,
		"model":   r.Model,
	})

	// turnCtx is the per-turn context. We keep a per-process
	// cancel so the "cancel" inbox message can interrupt an
	// in-flight turn without tearing down the whole daemon.
	var (
		mu       sync.Mutex
		turnCtx  context.Context = ctx
		cancelFn context.CancelFunc
		busyTurn bool
		stopping bool
		turnNo   int
		turns    sync.WaitGroup
	)

	runOne := func(prompt string) {
		mu.Lock()
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
		turnCtx, cancelFn = context.WithCancel(ctx)
		c := turnCtx
		mu.Unlock()

		em.emit("turn_start", map[string]any{"step": step})

		sink := func(ev core.AgentEvent) {
			em.emit(ev.Type(), modes.EventToJSON(ev))
		}

		start := len(ag.Messages())
		err := ag.Prompt(c, prompt, nil, sink)
		WriteNewTranscript(ag, sess, start)

		em.emit("turn_end", map[string]any{
			"step":  step,
			"error": errString(err),
		})

		mu.Lock()
		busyTurn = false
		cancelFn = nil
		mu.Unlock()
	}

	startTurn := func(prompt string) {
		mu.Lock()
		if stopping {
			mu.Unlock()
			return
		}
		turns.Add(1)
		mu.Unlock()
		go func() {
			defer turns.Done()
			runOne(prompt)
		}()
	}
	stopAndWait := func(reason string) {
		mu.Lock()
		stopping = true
		if cancelFn != nil {
			cancelFn()
		}
		mu.Unlock()
		turns.Wait()
		em.emit("agent_stopped", map[string]any{"reason": reason})
	}

	// Initial task: run before processing the inbox so the agent
	// "starts working" the moment it boots, matching what users
	// expect from `/swarm new <task>`.
	if args.Prompt != "" {
		startTurn(args.Prompt)
	}

	// Inbox loop: one supervisor message at a time. We don't spawn
	// a goroutine per turn because runOne already serialises them
	// via the busyTurn flag; doing the dispatch on the main
	// goroutine keeps the daemon's lifecycle easy to follow.
	for {
		select {
		case <-ctx.Done():
			stopAndWait("cancelled")
			return ctx.Err()
		case msg, ok := <-ln.Lines():
			if !ok {
				stopAndWait("inbox-closed")
				return nil
			}
			switch {
			case msg == "shutdown":
				stopAndWait("shutdown")
				return nil
			case msg == "cancel":
				mu.Lock()
				if cancelFn != nil {
					cancelFn()
				}
				mu.Unlock()
			case strings.HasPrefix(msg, "user "):
				prompt := strings.TrimPrefix(msg, "user ")
				startTurn(prompt)
			default:
				em.emit("error", map[string]any{
					"message": "unknown supervisor message: " + truncateForLog(msg, 200),
				})
			}
		}
	}
}

// swarmEmitter serialises events to stdout and (optionally) to a
// durable log file. Concurrent goroutines call emit so we have to
// hold a mutex around the encoder.
type swarmEmitter struct {
	mu     sync.Mutex
	w      *os.File
	mirror *swarm.EventLog

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

func newSwarmEmitter(w *os.File, mirror *swarm.EventLog) *swarmEmitter {
	return &swarmEmitter{w: w, mirror: mirror}
}

func (e *swarmEmitter) emit(typ string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["type"] = typ
	data["time"] = time.Now().Format(time.RFC3339Nano)

	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.orphan {
		// Encode to a buffer first so we can detect the stdout write
		// error — json.Encoder swallows partial writes silently, but
		// a direct Write on *os.File returns broken-pipe immediately.
		line, err := json.Marshal(data)
		if err == nil {
			line = append(line, '\n')
			if _, werr := e.w.Write(line); werr != nil {
				// Supervisor's stdout pipe is gone (parent zot exited
				// but we kept running). Switch to mirror-only mode so
				// subsequent events still get persisted; also retro-
				// actively log this very event to the mirror so it
				// doesn't get lost in the handoff.
				e.orphan = true
			}
		}
	}

	if e.orphan && e.mirror != nil {
		// Drop "type" + "time" out of data into Event fields so the
		// mirror file matches what the supervisor would have written.
		flat := map[string]any{}
		for k, v := range data {
			if k == "type" || k == "time" {
				continue
			}
			flat[k] = v
		}
		_ = e.mirror.Append(swarm.NewEvent(typ, flat))
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
