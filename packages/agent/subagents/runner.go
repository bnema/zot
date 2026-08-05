package subagents

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// execRunner spawns `zut --subagent-worker <inbox> --session <path>` in
// Agent.Dir (the shared repository root or an isolated worktree) and consumes
// its JSONL event stream on stdout.
//
// Why a long-lived daemon and not `zut --print`: the supervisor and
// the user expect agents to keep accepting follow-up prompts. A
// one-shot subprocess can't do that; this design gives each subagent
// agent a persistent session file plus an inbox socket the parent
// writes to, mirroring Claude Code's "Agents view" model.
//
// Events flow:
//
//	child stdout  -->  decoder  -->  EventLog (events.jsonl)
//	                              -->  Sink (Activity/Transcript)
//
// The on-disk log is the durable record. The Sink updates are an
// in-memory mirror so the dashboard doesn't have to tail the file
// for the parent's own agents. /subagents open in a separate zut would
// read the log directly.
type execRunner struct {
	agent             *Agent
	resolveCredential func(context.Context, string) (Credential, error)

	// Command overrides the default `zut --subagent-worker ...`
	// invocation. Tests set this to a fake binary (or `go run`
	// against a tiny stub program) so the supervisor logic can be
	// tested without a real child. Production code leaves it nil.
	Command []string

	// SessionPath is the agent's session file. Empty means "defer
	// to r.agent.SessionPath", which Supervisor.Spawn always populates
	// with <subagent-root>/agents/<id>/session.json. Tests that
	// hand-build an Agent without going through Spawn must set
	// one of the two; the runner refuses to invent a fallback
	// because the only plausible one (<Dir>/.zut/session.json)
	// would litter the user's repo — every agent's Dir points
	// at it directly.
	SessionPath string
}

// subagentWorkerArgsOpts captures every dynamic input to subagentWorkerArgs.
// The fields map 1:1 onto child CLI flags; empty values omit the flag
// entirely and let the child resolve a default the same way a normal
// `zut` invocation does.
type subagentWorkerArgsOpts struct {
	Exe         string
	Dir         string
	SessionPath string
	InboxPath   string
	Task        string
	Model       string
	Provider    string
	BaseURL     string
	InsecureTLS bool
	Reasoning   string
	FastMode    bool
	FastModeSet bool
	Subagent    string
	MaxTurns    int
	Tools       []string
}

// defaultChildArgs builds the argv execRunner uses when its Command
// override is empty. Centralised so the spawn-vs-resume decision
// (whether to pass the original Task as a positional) lives in one
// place that tests can hit directly without going through Run's
// side effects.
//
// On Spawn (Resuming==false) we pass the task so the child's first
// turn runs immediately. On Resume (Resuming==true) we omit it: the
// child reopens the existing session file, loads the prior
// conversation, and just waits on the inbox for the next prompt.
// Re-firing the task on every resume produces a duplicate turn that
// collides with whatever the user types next, surfacing the
// "agent busy; send 'cancel' first" error between two assistant
// messages — which is exactly the bug this helper fixes.
func defaultChildArgs(exe string, a *Agent, sessionPath, inboxPath string) []string {
	task := a.Task
	if a.Resuming {
		task = ""
	}
	return subagentWorkerArgs(subagentWorkerArgsOpts{
		Exe:         exe,
		Dir:         a.Dir,
		SessionPath: sessionPath,
		InboxPath:   inboxPath,
		Task:        task,
		Model:       a.Model,
		Provider:    a.Provider,
		BaseURL:     a.BaseURL,
		InsecureTLS: a.InsecureTLS,
		Reasoning:   a.Reasoning,
		FastMode:    a.FastMode,
		FastModeSet: true,
		Subagent:    a.Subagent,
		MaxTurns:    a.MaxTurns,
		Tools:       a.Tools,
	})
}

// subagentWorkerArgs builds the argv used when execRunner.Command is
// empty. Pulled out so tests can lock in the flag set without
// actually spawning a subprocess.
func subagentWorkerArgs(opts subagentWorkerArgsOpts) []string {
	args := []string{
		opts.Exe,
		"--subagent-worker", opts.InboxPath,
		"--session", opts.SessionPath,
		"--cwd", opts.Dir,
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Provider != "" {
		args = append(args, "--provider", opts.Provider)
	}
	if opts.BaseURL != "" {
		args = append(args, "--base-url", opts.BaseURL)
	}
	if opts.InsecureTLS {
		args = append(args, "--insecure")
	}
	if opts.Reasoning != "" {
		args = append(args, "--reasoning", opts.Reasoning)
	}
	if opts.FastModeSet {
		if opts.FastMode {
			args = append(args, "--fast-mode")
		} else {
			args = append(args, "--no-fast-mode")
		}
	}
	if opts.Subagent != "" {
		args = append(args, "--subagent", opts.Subagent)
	}
	if opts.MaxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprint(opts.MaxTurns))
	}
	if len(opts.Tools) > 0 {
		args = append(args, "--tools", strings.Join(opts.Tools, ","))
	}
	if opts.Task != "" {
		// First task is positional so the child treats it as the
		// initial user turn; the terminator keeps flag-like task text
		// from being parsed as a child CLI option.
		args = append(args, "--", opts.Task)
	}
	return args
}

// resolveSubagentExecutable finds a runnable zut binary without assuming a
// particular installation directory. A parent process can outlive the file
// it was started from (for example after a reinstall), so all known
// candidates are checked concurrently and the first successful lookup wins.
// The cancellation signal stops candidates that have not finished yet. PATH
// is one of the candidates because it covers GOBIN, user-local bin
// directories, package-manager locations, and system installs without
// hard-coding any of them.
func resolveSubagentExecutable(ctx context.Context, self, argv0 string, lookPath func(context.Context, string) (string, error)) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if lookPath == nil {
		lookPath = func(ctx context.Context, candidate string) (string, error) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			default:
			}
			return exec.LookPath(candidate)
		}
	}

	candidates := make([]string, 0, 5)
	addCandidate := func(candidate string) {
		if candidate == "" || candidate == "." {
			return
		}
		for _, existing := range candidates {
			if existing == candidate {
				return
			}
		}
		candidates = append(candidates, candidate)
	}
	addCandidate(self)
	addCandidate(argv0)
	if self != "" {
		addCandidate(filepath.Base(self))
	}
	if argv0 != "" {
		addCandidate(filepath.Base(argv0))
	}
	addCandidate("zut")

	if len(candidates) == 0 {
		return "", fmt.Errorf("locate zut executable: no executable candidates")
	}

	type lookupResult struct {
		candidate string
		path      string
		err       error
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan lookupResult, len(candidates))
	for _, candidate := range candidates {
		go func(candidate string) {
			path, err := lookPath(childCtx, candidate)
			if err == nil && path == "" {
				err = fmt.Errorf("empty executable path")
			}
			results <- lookupResult{candidate: candidate, path: path, err: err}
		}(candidate)
	}

	lookupErrors := make([]string, 0, len(candidates))
	for range candidates {
		result := <-results
		if result.err == nil {
			cancel()
			return result.path, nil
		}
		lookupErrors = append(lookupErrors, fmt.Sprintf("%q: %v", result.candidate, result.err))
	}
	return "", fmt.Errorf("locate zut executable: %s", strings.Join(lookupErrors, "; "))
}

func (r *execRunner) Run(ctx context.Context, sink Sink) error {
	// SessionPath resolution order:
	//   1. explicit r.SessionPath set by the test / caller
	//   2. r.agent.SessionPath baked in by Supervisor.Spawn — the
	//      production path. Always lives under
	//      <subagent-root>/agents/<id>/session.json so the per-
	//      agent state is entirely outside the working tree.
	//      Crucial because Agent.Dir points at the user's repo;
	//      any .zut/ scratch directory under Dir would litter
	//      their source tree.
	//
	// There is no third fallback. If neither path is set we
	// refuse to start instead of inventing a directory; that
	// way a misconfigured caller fails loudly the first time
	// instead of silently dumping session data into someone's
	// repo.
	sessionPath := r.SessionPath
	if sessionPath == "" {
		sessionPath = r.agent.SessionPath
	}
	if sessionPath == "" {
		return fmt.Errorf("subagent: agent missing session path (set SpawnRequest via Supervisor.SpawnReq, or hand-build Agent with SessionPath populated)")
	}
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		return fmt.Errorf("session dir: %w", err)
	}

	inboxPath := r.agent.InboxPath
	logPath := r.agent.EventLogPath
	if logPath == "" {
		return fmt.Errorf("subagent: agent missing event log path")
	}
	log, err := OpenEventLog(logPath)
	if err != nil {
		return err
	}
	defer log.Close()

	args := r.Command
	if len(args) == 0 {
		self, selfErr := os.Executable()
		argv0 := ""
		if len(os.Args) > 0 {
			argv0 = os.Args[0]
		}
		exe, err := resolveSubagentExecutable(ctx, self, argv0, nil)
		if err != nil {
			if selfErr != nil {
				return fmt.Errorf("locate zut executable (os.Executable: %v): %w", selfErr, err)
			}
			return err
		}
		args = defaultChildArgs(exe, r.agent, sessionPath, inboxPath)
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = r.agent.Dir
	cmd.Env = append(workerEnvironment(r.agent.Provider),
		"ZUT_SUBAGENT_AGENT_ID="+r.agent.ID,
		"ZUT_SUBAGENT_EVENT_LOG="+logPath,
	)
	if r.agent.HeartbeatInterval > 0 {
		cmd.Env = append(cmd.Env, "ZUT_SUBAGENT_HEARTBEAT_INTERVAL="+r.agent.HeartbeatInterval.String())
	}
	if r.agent.maxOutputBytes > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("ZUT_SUBAGENT_MAX_OUTPUT_BYTES=%d", r.agent.maxOutputBytes))
	}
	if r.agent.maxOutputLines > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("ZUT_SUBAGENT_MAX_OUTPUT_LINES=%d", r.agent.maxOutputLines))
	}
	if r.resolveCredential != nil {
		credential, resolveErr := r.resolveCredential(ctx, r.agent.Provider)
		if resolveErr != nil {
			return fmt.Errorf("resolve subagent credential for %s: %w", r.agent.Provider, resolveErr)
		}
		if credential.Value != "" {
			encoded, encodeErr := json.Marshal(credential)
			if encodeErr != nil {
				return fmt.Errorf("encode subagent credential: %w", encodeErr)
			}
			cmd.Stdin = bytes.NewReader(encoded)
			cmd.Env = append(cmd.Env, "ZUT_SUBAGENT_CREDENTIAL_STDIN=1")
		}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	configureWorkerProcess(cmd, stdout, stderr)

	// "spawning" is briefly shown until the first event arrives;
	// the child's "spawned" lifecycle event then overwrites it.
	sink.Activity("starting")
	if err := cmd.Start(); err != nil {
		return err
	}
	r.agent.setProcessPID(cmd.Process.Pid)
	if r.agent.persistFn != nil {
		r.agent.persistFn(r.agent)
	}

	var logErrMu sync.Mutex
	var logErr error
	var stopOnLogErr sync.Once
	appendLog := func(ev Event) {
		if err := log.Append(ev); err != nil {
			logErrMu.Lock()
			if logErr == nil {
				logErr = err
			}
			logErrMu.Unlock()
			// A durable event log is part of the runner's recovery contract.
			// Stop the worker rather than letting it continue in a state the
			// supervisor cannot reconstruct after restart.
			stopOnLogErr.Do(func() { _ = killProcessGroup(cmd) })
		}
	}
	firstLogErr := func() error {
		logErrMu.Lock()
		defer logErrMu.Unlock()
		return logErr
	}

	// stdout: parsed as JSONL. Every well-formed event is appended
	// to the durable log AND forwarded to the in-memory sink so the
	// dashboard updates without having to tail the file. Malformed
	// lines are surfaced as plain transcript so they don't vanish.
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		dec := bufio.NewReader(stdout)
		lineLimit := r.agent.maxOutputBytes + 64*1024
		if lineLimit <= 0 {
			lineLimit = 512 * 1024
		}
		if lineLimit > maxEventLogLineBytes {
			lineLimit = maxEventLogLineBytes
		}
		for {
			line, err, truncated := readBoundedLine(dec, lineLimit)
			if len(line) > 0 {
				trimmed := strings.TrimRight(string(line), "\r\n")
				if trimmed != "" && !truncated {
					if ev, ok := parseEventLine(trimmed); ok {
						appendLog(ev)
						updateAgentFromEvent(r.agent, ev)
						applyEventToSink(ev, sink)
						// Fan prompt-level task completions up to any
						// subscriber on the supervised Agent. The child
						// also forwards provider/tool-loop turn_end
						// events (for example stop=tool_use); those do
						// not contain step and must not be treated as
						// subagent task completion.
						notifyPromptTurnEnd(r.agent, ev)
					} else {
						sink.Transcript(trimmed)
						appendLog(NewEvent("stdout", map[string]any{"text": trimmed}))
					}
				} else if trimmed != "" {
					trimmed += "\n...[line truncated]"
					sink.Transcript(trimmed)
					appendLog(NewEvent("stdout", map[string]any{"text": trimmed, "truncated": true}))
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// stderr: lifecycle/error chatter from the child. Every line
	// is mirrored as a stderr event in the durable log AND surfaced
	// in the transcript so users can diagnose a failing agent
	// without leaving the dashboard.
	go func() {
		defer func() { done <- struct{}{} }()
		br := bufio.NewReader(stderr)
		lineLimit := r.agent.maxOutputBytes + 64*1024
		if lineLimit <= 0 {
			lineLimit = 512 * 1024
		}
		if lineLimit > maxEventLogLineBytes {
			lineLimit = maxEventLogLineBytes
		}
		for {
			line, err, truncated := readBoundedLine(br, lineLimit)
			if len(line) > 0 {
				txt := strings.TrimRight(string(line), "\r\n")
				if truncated {
					txt += "\n...[line truncated]"
				}
				sink.Transcript("stderr: " + txt)
				appendLog(NewEvent("stderr", map[string]any{"text": txt, "truncated": truncated}))
			}
			if err != nil {
				return
			}
		}
	}()

	<-done
	<-done

	err = cmd.Wait()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	}
	if err != nil && ctx.Err() != nil {
		appendLog(NewEvent("agent_stopped", map[string]any{"reason": "cancelled"}))
		if logErr := firstLogErr(); logErr != nil {
			return fmt.Errorf("subagent event log: %w", logErr)
		}
		return ctx.Err()
	}
	if err != nil {
		appendLog(NewEvent("agent_stopped", map[string]any{"reason": "exit", "code": exit, "error": err.Error()}))
		if logErr := firstLogErr(); logErr != nil {
			return fmt.Errorf("subagent event log: %v; worker: %w", logErr, err)
		}
		return err
	}
	appendLog(NewEvent("agent_stopped", map[string]any{"reason": "exit", "code": 0}))
	if logErr := firstLogErr(); logErr != nil {
		return fmt.Errorf("subagent event log: %w", logErr)
	}
	sink.Activity("done")
	return nil
}

func workerEnvironment(provider string) []string {
	// Provider credentials are resolved by the supervisor and transferred
	// over the child's stdin. Do not inherit API-key/token variables into the
	// worker environment, where child tools, crash reports, or diagnostics
	// could expose them. Bedrock's current client still resolves bearer/IAM
	// credentials directly from AWS environment variables, so preserve that
	// provider's ambient AWS chain until it has a structured stdin handoff.
	provider = strings.ToLower(strings.TrimSpace(provider))
	var env []string
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if ok && workerSecretEnvName(name) && (provider != "amazon-bedrock" || !isBedrockCredentialEnv(name)) {
			continue
		}
		env = append(env, entry)
	}
	return env
}

func isBedrockCredentialEnv(name string) bool {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "AWS_BEARER_TOKEN_BEDROCK", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN":
		return true
	default:
		return false
	}
}

func workerSecretEnvName(name string) bool {
	name = strings.ToUpper(strings.TrimSpace(name))
	for _, suffix := range []string{"_KEY", "_API_KEY", "_OAUTH_TOKEN", "_TOKEN", "_SECRET", "_PASSWORD", "_CREDENTIAL", "_CREDENTIALS"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	switch name {
	case "AWS_ACCESS_KEY_ID", "HF_TOKEN", "AWS_BEARER_TOKEN_BEDROCK", "COPILOT_GITHUB_TOKEN", "GITHUB_COPILOT_TOKEN", "GOOGLE_APPLICATION_CREDENTIALS":
		return true
	default:
		return false
	}
}

func readBoundedLine(r *bufio.Reader, limit int) ([]byte, error, bool) {
	line, truncated, _, _, err := readBoundedLineStats(r, limit)
	return line, err, truncated
}

// readBoundedLineStats reads through one newline or EOF while retaining the
// number of source bytes consumed and whether a newline terminated the record.
// The follower uses those extra values to avoid advancing past a partial final
// JSONL record that may be completed by a concurrent append.
func readBoundedLineStats(r *bufio.Reader, limit int) ([]byte, bool, int64, bool, error) {
	if limit <= 0 {
		limit = 512 * 1024
	}
	var line []byte
	var consumed int64
	truncated := false
	for {
		chunk, err := r.ReadSlice('\n')
		consumed += int64(len(chunk))
		if len(chunk) > 0 {
			if len(line) < limit {
				keep := len(chunk)
				if remaining := limit - len(line); keep > remaining {
					keep = remaining
					truncated = true
				}
				line = append(line, chunk[:keep]...)
			} else {
				truncated = true
			}
		}
		if err != bufio.ErrBufferFull {
			return line, truncated, consumed, len(chunk) > 0 && chunk[len(chunk)-1] == '\n', err
		}
	}
}

// parseEventLine attempts to decode one current-protocol JSONL line as an Event.
func parseEventLine(line string) (Event, bool) {
	if len(line) == 0 || line[0] != '{' {
		return Event{}, false
	}
	env, err := ParseEvent([]byte(line))
	if err != nil {
		return Event{}, false
	}
	fields, err := env.PayloadFields()
	if err != nil {
		return Event{}, false
	}
	data := make(map[string]any, len(fields))
	for key, raw := range fields {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return Event{}, false
		}
		data[key] = value
	}
	when := env.Timestamp
	if when.IsZero() {
		when = time.Now()
	}
	return Event{
		Time: when, Type: env.Type, Version: env.Version,
		MessageID: env.MessageID, AgentID: env.AgentID, TurnID: env.TurnID,
		Data: data,
	}, true
}

func updateAgentFromEvent(a *Agent, ev Event) {
	if a == nil {
		return
	}
	now := ev.Time
	if now.IsZero() {
		now = time.Now()
	}
	a.markActivity(now)
	persist := false
	switch ev.Type {
	case EventAgentReady, "agent_ready":
		a.setProcessState(ProcessAlive)
		a.setTurnState(TurnIdle, ev.TurnID)
		persist = true
	case EventAgentHeartbeat, "agent_heartbeat":
		a.setProcessState(ProcessAlive)
		persist = true
	case EventTurnStarted, "turn_start":
		turnID := ev.TurnID
		if turnID == "" {
			if step, ok := ev.Data["step"].(float64); ok {
				turnID = fmt.Sprintf("turn-%d", int(step))
			}
		}
		a.setProcessState(ProcessAlive)
		a.setTurnState(TurnRunning, turnID)
		persist = true
	case EventTurnResult, "turn_result":
		if result, err := decodeTurnResultEvent(ev, a.ID, a.maxOutputBytes, a.maxOutputLines); err == nil {
			a.setResult(result)
			if a.turnResults != nil {
				select {
				case a.turnResults <- result:
				default:
				}
			}
			if a.stateDir != "" {
				if err := writeTurnResult(a.stateDir, result); err == nil {
					a.lifecycleMu.Lock()
					a.resultRef = ResultRef(a.ID)
					a.lifecycleMu.Unlock()
				}
			}
			switch result.Status {
			case ResultCanceled:
				a.setTurnState(TurnCanceled, result.TurnID)
			case ResultFailed:
				a.setTurnState(TurnFailed, result.TurnID)
			default:
				a.setTurnState(TurnSucceeded, result.TurnID)
			}
		}
		persist = true
	case EventTurnFailed, "turn_failed":
		a.setTurnState(TurnFailed, ev.TurnID)
		persist = true
	case EventAgentIdle, "agent_idle":
		a.setTurnState(TurnIdle, ev.TurnID)
		persist = true
	case EventAgentExited, "agent_exited", "agent_stopped":
		a.setProcessState(ProcessExited)
		persist = true
	case "turn_end":
		// Provider/tool-loop turn_end events (for example stop=tool_use)
		// do not carry the daemon's prompt step and are not terminal for
		// the delegated task. They remain in the event log, but must not
		// overwrite the delegated turn state or trigger persistence.
		if _, ok := ev.Data["step"].(float64); !ok {
			break
		}
		if message, _ := ev.Data["error"].(string); message != "" {
			a.setTurnState(TurnFailed, ev.TurnID)
		} else {
			a.setTurnState(TurnSucceeded, ev.TurnID)
		}
		persist = true
	}
	if persist && a.persistFn != nil {
		a.persistFn(a)
	}
}

// notifyPromptTurnEnd calls Agent.OnTurnEnd only for the subagent
// daemon's prompt-level completion event. Provider/tool-loop
// turn_end events (such as stop=tool_use) do not include step and
// are not terminal for the delegated task.
func notifyPromptTurnEnd(a *Agent, ev Event) {
	if a == nil || ev.Type != "turn_end" {
		return
	}
	step, ok := ev.Data["step"].(float64)
	if !ok {
		return
	}

	errMsg, _ := ev.Data["error"].(string)
	a.mu.Lock()
	fn := a.OnTurnEnd
	if fn == nil {
		a.pendingTurnEnds = append(a.pendingTurnEnds, turnEndNotice{step: int(step), errMsg: errMsg})
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	go fn(int(step), errMsg)
}

// applyEventToSink translates an Event into Sink updates. Only a
// few event types are interpreted; the rest still land in the
// durable log via the caller.
func applyEventToSink(ev Event, sink Sink) {
	type roleSink interface {
		userMessage(string)
		assistantMessage(string)
	}
	appendMessage := func(text string, assistant bool) {
		if text == "" {
			return
		}
		if roles, ok := sink.(roleSink); ok {
			if assistant {
				roles.assistantMessage(text)
			} else {
				roles.userMessage(text)
			}
			return
		}
		if assistant {
			sink.Transcript(text)
		} else {
			sink.Transcript("user: " + text)
		}
	}

	switch ev.Type {
	case "assistant_message", "user_message":
		var text []string
		if c, ok := ev.Data["content"].([]any); ok {
			for _, blk := range c {
				m, _ := blk.(map[string]any)
				if t, _ := m["type"].(string); t == "text" {
					if txt, _ := m["text"].(string); txt != "" {
						text = append(text, txt)
					}
				}
			}
		}
		appendMessage(strings.Join(text, "\n"), ev.Type == "assistant_message")
		if ev.Type == "assistant_message" {
			sink.Activity("idle")
		}
	case "turn_start", EventTurnStarted:
		sink.Activity("thinking")
	case "tool_call", EventToolStarted:
		if name, _ := ev.Data["name"].(string); name != "" {
			sink.Activity("tool: " + truncate(name, 60))
		}
	case "tool_result", EventToolFinished:
		sink.Activity("idle")
	case "turn_end", EventTurnResult, EventAgentIdle:
		sink.Activity("idle")
	case "agent_ready", EventAgentReady, EventAgentHeartbeat:
		sink.Activity("idle")
	case "agent_stopped":
		// terminal status is decided by Supervisor.run from the runner's
		// return value, not from this event. Don't overwrite the
		// activity here.
	case "error":
		if msg, _ := ev.Data["message"].(string); msg != "" {
			sink.Transcript("error: " + msg)
		}
	}
}

// RunnerFunc adapts a plain function into a Runner. Useful for tests
// and for callers who don't need their own type.
type RunnerFunc func(ctx context.Context, sink Sink) error

func (f RunnerFunc) Run(ctx context.Context, sink Sink) error { return f(ctx, sink) }
