package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSwarmAgentArgs locks in the exact flag set the subprocess
// runner uses to start a swarm agent in daemon mode. Past
// regressions in this area:
//
//   - "--no-sess" instead of "--no-session" (old print-mode runner):
//     every spawned agent died with "unknown flag" before it could
//     talk to the model.
//
//   - Forgetting --cwd: the child resolved tools against the parent
//     zot's working directory, defeating the whole point of the
//     worktree isolation.
//
//   - Forgetting --session: a daemon-mode agent without a session
//     file would lose context between follow-up turns, making
//     "send another message" mostly useless.
//
// The test asserts the load-bearing pieces are present in plausible
// positions. If a flag is renamed, update both the runner and this
// test so we notice immediately.
func TestCredentialStdinHelperProcess(t *testing.T) {
	if os.Getenv("ZOT_SWARM_CREDENTIAL_HELPER") != "1" {
		return
	}
	if os.Getenv("ZOT_SWARM_CREDENTIAL_STDIN") != "1" {
		os.Exit(2)
	}
	var credential Credential
	if err := json.NewDecoder(os.Stdin).Decode(&credential); err != nil {
		os.Exit(3)
	}
	if credential.Value != "inherited-secret" || credential.Method != "apikey" {
		os.Exit(4)
	}
	for _, arg := range os.Args {
		if strings.Contains(arg, credential.Value) {
			os.Exit(5)
		}
	}
	for _, env := range os.Environ() {
		if strings.Contains(env, credential.Value) {
			os.Exit(6)
		}
	}
	fmt.Println(`{"type":"agent_stopped","data":{"reason":"completed"}}`)
	os.Exit(0)
}

func TestExecRunnerTransfersCredentialOnlyOnStdin(t *testing.T) {
	t.Setenv("ZOT_SWARM_CREDENTIAL_HELPER", "1")
	root := t.TempDir()
	a := &Agent{
		ID:           "credential-test",
		Dir:          root,
		Provider:     "custom",
		SessionPath:  filepath.Join(root, "session.json"),
		InboxPath:    filepath.Join(root, "inbox"),
		EventLogPath: filepath.Join(root, "events.jsonl"),
	}
	calls := 0
	r := &execRunner{
		agent:   a,
		Command: []string{os.Args[0], "-test.run=^TestCredentialStdinHelperProcess$"},
		resolveCredential: func(context.Context, string) (Credential, error) {
			calls++
			return Credential{Value: "inherited-secret", Method: "apikey"}, nil
		},
	}
	if err := r.Run(context.Background(), agentSink{a: a}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("credential resolver called %d times, want 1", calls)
	}
}

func TestApplyEventPreservesMultilineRolesAndFinalAssistant(t *testing.T) {
	a := &Agent{}
	sink := agentSink{a: a}
	applyEventToSink(NewEvent("user_message", map[string]any{"content": []any{
		map[string]any{"type": "text", "text": "first line\n\nlast line"},
	}}), sink)
	applyEventToSink(NewEvent("assistant_message", map[string]any{"content": []any{
		map[string]any{"type": "text", "text": "complete\nanswer"},
	}}), sink)

	snap := a.Snapshot()
	wantLines := []string{"user: first line", "user: ", "user: last line", "complete", "answer"}
	if strings.Join(snap.Lines, "\n") != strings.Join(wantLines, "\n") {
		t.Fatalf("transcript = %#v, want %#v", snap.Lines, wantLines)
	}
	if snap.LastAssistant != "complete\nanswer" {
		t.Fatalf("last assistant = %q", snap.LastAssistant)
	}
}

func TestSwarmAgentArgs(t *testing.T) {
	args := swarmAgentArgs(swarmAgentArgsOpts{
		Exe:         "/path/to/zot",
		Dir:         "/tmp/worktree",
		SessionPath: "/tmp/state/session.json",
		InboxPath:   "/tmp/state/in.sock",
		Task:        "do the thing",
	})
	if len(args) < 7 {
		t.Fatalf("argv unexpectedly short: %v", args)
	}
	if args[0] != "/path/to/zot" {
		t.Fatalf("argv[0] = %q; want the binary path", args[0])
	}
	// The task must come last so anything that looks flag-like in
	// the task body doesn't get interpreted as a flag.
	if args[len(args)-1] != "do the thing" {
		t.Fatalf("task should be last positional; got %v", args)
	}

	mustHave := map[string]string{
		"--swarm-agent": "/tmp/state/in.sock",
		"--session":     "/tmp/state/session.json",
		"--cwd":         "/tmp/worktree",
	}
	for flag, value := range mustHave {
		i := indexOf(args, flag)
		if i < 0 {
			t.Errorf("argv missing %q: %v", flag, args)
			continue
		}
		if i+1 >= len(args) || args[i+1] != value {
			t.Errorf("argv %q value = %q; want %q", flag, safeAt(args, i+1), value)
		}
	}

	// Reject prior bad flags explicitly so a future revert is caught.
	joined := strings.Join(args, " ")
	for _, bad := range []string{"--print", "--no-sess ", "--no-session"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("argv contains stale/wrong flag %q: %s", bad, joined)
		}
	}
}

// TestSwarmAgentArgsEmptyTaskOmitsPositional makes sure that when the
// agent is being adopted (no fresh task) we don't pass an empty
// positional which the arg parser would treat as a real prompt.
func TestSwarmAgentArgsEmptyTaskOmitsPositional(t *testing.T) {
	args := swarmAgentArgs(swarmAgentArgsOpts{
		Exe: "/zot", Dir: "/wt", SessionPath: "/s.json", InboxPath: "/in.sock",
	})
	for _, a := range args {
		if a == "" {
			t.Fatalf("argv contains an empty positional: %v", args)
		}
	}
	// last arg should be a real flag value, not a stray positional
	if a := args[len(args)-1]; strings.HasPrefix(a, "--") {
		t.Fatalf("argv ends on a flag with no value: %v", args)
	}
}

// TestDefaultChildArgsSpawnIncludesTask pins the spawn shape: a
// fresh (non-resuming) Agent produces argv that ends with the
// original task as a positional, so the child runs it as the
// initial user turn.
func TestDefaultChildArgsSpawnIncludesTask(t *testing.T) {
	a := &Agent{Dir: "/wt", Task: "do thing"}
	args := defaultChildArgs("/zot", a, "/s.json", "/in.sock")
	if got := args[len(args)-1]; got != "do thing" {
		t.Fatalf("spawn argv last = %q; want %q\n%v", got, "do thing", args)
	}
}

// TestDefaultChildArgsResumeOmitsTask is the regression for the
// "agent busy; send 'cancel' first" error: when an Agent is being
// resumed (Resuming==true), the child argv MUST NOT include the
// original Task as a positional. Otherwise the child fires the task
// as a fresh user turn on every resume, racing with whatever the
// user types next via the inbox.
func TestDefaultChildArgsResumeOmitsTask(t *testing.T) {
	a := &Agent{Dir: "/wt", Task: "do thing", Resuming: true}
	args := defaultChildArgs("/zot", a, "/s.json", "/in.sock")
	for _, v := range args {
		if v == "do thing" {
			t.Fatalf("resume argv contains the task; it would re-fire as a duplicate turn\n%v", args)
		}
	}
	// And no trailing positional at all: the last arg should be a
	// flag value, not a stray empty string.
	if got := args[len(args)-1]; got == "" {
		t.Fatalf("resume argv ends with an empty positional: %v", args)
	}
	if strings.HasPrefix(args[len(args)-1], "--") {
		t.Fatalf("resume argv ends on a bare flag (no value): %v", args)
	}
}

func TestNotifyPromptTurnEndIgnoresProviderToolLoopTurnEnd(t *testing.T) {
	called := make(chan struct{}, 1)
	a := &Agent{}
	a.SetOnTurnEnd(func(step int, errMsg string) {
		called <- struct{}{}
	})

	notifyPromptTurnEnd(a, NewEvent("turn_end", map[string]any{"stop": "tool_use"}))

	select {
	case <-called:
		t.Fatal("OnTurnEnd fired for provider/tool-loop turn_end without step")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNotifyPromptTurnEndFiresForDaemonPromptCompletion(t *testing.T) {
	type got struct {
		step int
		err  string
	}
	called := make(chan got, 1)
	a := &Agent{}
	a.SetOnTurnEnd(func(step int, errMsg string) {
		called <- got{step: step, err: errMsg}
	})

	notifyPromptTurnEnd(a, NewEvent("turn_end", map[string]any{"step": float64(2), "error": "boom"}))

	select {
	case g := <-called:
		if g.step != 2 || g.err != "boom" {
			t.Fatalf("callback = (%d, %q); want (2, boom)", g.step, g.err)
		}
	case <-time.After(time.Second):
		t.Fatal("OnTurnEnd did not fire for prompt-level turn_end with step")
	}
}

func indexOf(xs []string, want string) int {
	for i, x := range xs {
		if x == want {
			return i
		}
	}
	return -1
}

func safeAt(xs []string, i int) string {
	if i < 0 || i >= len(xs) {
		return ""
	}
	return xs[i]
}
