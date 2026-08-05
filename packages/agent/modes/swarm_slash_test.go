package modes

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/patriceckhart/zot/packages/agent/subagents"
	"github.com/patriceckhart/zot/packages/agent/swarm"
)

// newInteractiveForSwarmTest builds the minimal Interactive scaffolding
// runSwarm needs. It does NOT call NewInteractive (which would pull in
// the whole TUI); the runSwarm method only touches cfg.Swarm, the
// status mutex, and the swarm dialog, so we hand-build those.
func newInteractiveForSwarmTest(t *testing.T) (*Interactive, *swarm.Swarm) {
	t.Helper()
	root := t.TempDir()
	f := swarm.New(swarm.Config{
		Root:     root,
		RepoRoot: root,
		NewRunner: func(a *swarm.Agent) swarm.Runner {
			return swarm.RunnerFunc(func(ctx context.Context, sink swarm.Sink) error {
				<-ctx.Done()
				return ctx.Err()
			})
		},
	})
	iv := &Interactive{
		swarmDialog: newSwarmDialog(),
		dirty:       make(chan struct{}, 1),
	}
	iv.cfg.Swarm = f
	return iv, f
}

// TestRunSwarmBareDoesNotPanic regression-tests the slice-out-of-range
// panic that hit when /swarm was typed with no subcommand: runSwarm
// did args[1:] without checking len(args), which panics as [1:0].
func TestRunSwarmBareDoesNotPanic(t *testing.T) {
	iv, _ := newInteractiveForSwarmTest(t)
	defer iv.cfg.Swarm.StopAll()

	// Bare /swarm: parts[1:] from the dispatcher is an empty slice.
	iv.runSwarm(context.Background(), nil)

	if !iv.swarmDialog.Active() {
		t.Fatal("bare /swarm should open the dashboard")
	}
}

func TestRunSwarmSubcommandsDoNotPanic(t *testing.T) {
	iv, _ := newInteractiveForSwarmTest(t)
	defer iv.cfg.Swarm.StopAll()

	// Each row is the slice that the dispatcher hands to runSwarm —
	// i.e. parts[1:] where parts was strings.Fields of the slash
	// command. Mixing zero-arg and arg'd forms exercises both
	// branches of the reslice guard.
	cases := [][]string{
		{"list"},
		{"new"},
		{"new", "fix", "the", "thing"},
		{"kill"},
		{"kill", "no-such-id"},
		{"remove"},
		{"remove", "no-such-id"},
		{"send"},
		{"send", "no-such-id"},
		{"send", "no-such-id", "hello", "world"},
		{"bogus"},
	}
	for _, args := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("runSwarm(%v) panicked: %v", args, r)
				}
			}()
			iv.runSwarm(context.Background(), args)
		}()
	}
}

func TestRunSwarmRejectsNamedProfileWithoutResolver(t *testing.T) {
	iv, f := newInteractiveForSwarmTest(t)
	defer f.StopAll()

	iv.runSwarm(context.Background(), []string{"new", "--agent", "reviewer", "review", "auth"})
	if !strings.Contains(iv.statusErr, "named subagent profiles are unavailable") {
		t.Fatalf("status error = %q, want unavailable profile resolver", iv.statusErr)
	}
	if got := len(f.List()); got != 0 {
		t.Fatalf("spawned agents = %d, want 0", got)
	}
}

func TestRunSwarmNewSpawnsAgent(t *testing.T) {
	iv, f := newInteractiveForSwarmTest(t)
	defer f.StopAll()

	iv.runSwarm(context.Background(), []string{"new", "do", "stuff"})
	agents := f.List()
	if len(agents) != 1 {
		t.Fatalf("want 1 agent; got %d", len(agents))
	}
	if agents[0].Task != "do stuff" {
		t.Fatalf("task = %q; want %q", agents[0].Task, "do stuff")
	}
}

func TestRunSwarmNamedProfileAppliesFastModeRestriction(t *testing.T) {
	root := t.TempDir()
	profileFastMode := false
	f := swarm.New(swarm.Config{
		Root:     root,
		RepoRoot: root,
		FastMode: true,
		NewRunner: func(*swarm.Agent) swarm.Runner {
			return swarm.RunnerFunc(func(ctx context.Context, _ swarm.Sink) error {
				<-ctx.Done()
				return ctx.Err()
			})
		},
	})
	defer f.StopAll()
	iv := &Interactive{swarmDialog: newSwarmDialog(), dirty: make(chan struct{}, 1)}
	iv.cfg.Swarm = f
	iv.cfg.ResolveSubagent = func(name string) (*subagents.Profile, error) {
		if name != "reviewer" {
			return nil, nil
		}
		return &subagents.Profile{Name: name, FastMode: &profileFastMode}, nil
	}

	iv.runSwarm(context.Background(), []string{"new", "--agent", "reviewer", "review", "auth"})
	agents := f.List()
	if len(agents) != 1 || agents[0].FastMode {
		t.Fatalf("agent fast mode = %#v, want disabled by profile", agents)
	}
}

// TestRunSwarmSendDeliversToAgentInbox spins up a real agent with a
// fake Runner whose only job is to forward inbox lines to a channel,
// then asserts the /swarm send <id> <text...> path routes through
// Swarm.SendUserTurn and lands at the agent verbatim.
func TestRunSwarmSendDeliversToAgentInbox(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("swarm inbox transport uses Unix-domain sockets")
	}
	root := t.TempDir()
	recv := make(chan string, 4)
	ready := make(chan error, 1)
	f := swarm.New(swarm.Config{
		Root:     root,
		RepoRoot: root,
		NewRunner: func(a *swarm.Agent) swarm.Runner {
			return swarm.RunnerFunc(func(ctx context.Context, sink swarm.Sink) error {
				// Stand up a real Listener on the agent's inbox path so
				// SendUserTurn (which dials a unix socket) actually has
				// something to talk to. The runner-test stubs do the
				// same; this is the minimum to exercise the wire.
				ln, err := swarm.Listen(a.InboxPath)
				ready <- err
				if err != nil {
					return err
				}
				defer ln.Close()
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case line, ok := <-ln.Lines():
						if !ok {
							return nil
						}
						recv <- line
					}
				}
			})
		},
	})
	defer f.StopAll()
	iv := &Interactive{swarmDialog: newSwarmDialog(), dirty: make(chan struct{}, 1)}
	iv.cfg.Swarm = f

	a, err := f.Spawn(context.Background(), "do thing")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for agent inbox listener")
	}

	// Run /swarm send <id> <text...>. The dispatcher would have
	// already strings.Fields-ed the input; mirror that here.
	iv.runSwarm(context.Background(), []string{"send", a.ID, "please", "continue"})

	select {
	case msg := <-recv:
		want := "user please continue"
		if msg != want {
			t.Fatalf("agent received %q; want %q", msg, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for agent to receive the prompt")
	}

	if iv.statusErr != "" {
		t.Fatalf("status err set: %q", iv.statusErr)
	}
	if iv.statusOK == "" || iv.statusOK[:7] != "sent to" {
		t.Fatalf("status ok = %q; want \"sent to ...\"", iv.statusOK)
	}
}

func TestNormalizeSpawnReasoning(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{in: "OFF", want: "off", ok: true},
		{in: "minimal", want: "minimum", ok: true},
		{in: "maximum", want: "xhigh", ok: true},
		{in: "MAX", want: "max", ok: true},
		{in: "bogus", ok: false},
	}
	for _, tc := range cases {
		got, err := normalizeSpawnReasoning(tc.in)
		if tc.ok {
			if err != nil || got != tc.want {
				t.Errorf("normalizeSpawnReasoning(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("normalizeSpawnReasoning(%q) succeeded with %q; want error", tc.in, got)
		}
	}
}

func TestParseSpawnFlags(t *testing.T) {
	cases := []struct {
		in                          string
		wantModel, wantProv         string
		wantReasoning, wantSubagent string
		wantTask                    string
	}{
		{"do x", "", "", "", "", "do x"},
		{"--model claude do x", "claude", "", "", "", "do x"},
		{"--model=claude do x", "claude", "", "", "", "do x"},
		{"--provider openai --model gpt-5 do x", "gpt-5", "openai", "", "", "do x"},
		{"--provider=openai --model=gpt-5 do x", "gpt-5", "openai", "", "", "do x"},
		{"--agent reviewer --reasoning high review auth", "", "", "high", "reviewer", "review auth"},
		{"--agent --reasoning high review auth", "", "", "high", "", "review auth"},
		{"--reasoning --agent reviewer review auth", "", "", "", "reviewer", "review auth"},
		{"--thinking=max do x", "", "", "max", "", "do x"},
		// Only LEADING flags are consumed.
		{"do --model x", "", "", "", "", "do --model x"},
		// Missing value: --model with no follow-up token leaves model empty
		// and the next field starts the task.
		{"--model", "", "", "", "", ""},
	}
	for _, c := range cases {
		m, p, r, a, task := parseSpawnFlags(c.in)
		if m != c.wantModel || p != c.wantProv || r != c.wantReasoning || a != c.wantSubagent || task != c.wantTask {
			t.Errorf("parseSpawnFlags(%q) = (%q,%q,%q,%q,%q); want (%q,%q,%q,%q,%q)",
				c.in, m, p, r, a, task, c.wantModel, c.wantProv, c.wantReasoning, c.wantSubagent, c.wantTask)
		}
	}
}

func TestSplitIDAndRest(t *testing.T) {
	cases := []struct {
		in       string
		wantID   string
		wantText string
	}{
		{"", "", ""},
		{"  ", "", ""},
		{"alpha", "alpha", ""},
		{"alpha hello world", "alpha", "hello world"},
		{"  alpha   hello   world  ", "alpha", "hello   world  "},
		{"alpha\thi", "alpha", "hi"},
	}
	for _, c := range cases {
		gotID, gotText := splitIDAndRest(c.in)
		if gotID != c.wantID || gotText != c.wantText {
			t.Errorf("splitIDAndRest(%q) = (%q,%q); want (%q,%q)", c.in, gotID, gotText, c.wantID, c.wantText)
		}
	}
}

func TestRunSwarmWithoutSwarmIsNoop(t *testing.T) {
	iv := &Interactive{
		swarmDialog: newSwarmDialog(),
		dirty:       make(chan struct{}, 1),
	}
	// cfg.Swarm stays nil. The command should set a status err and
	// otherwise be inert.
	iv.runSwarm(context.Background(), nil)
	if iv.swarmDialog.Active() {
		t.Fatal("dialog opened despite no swarm")
	}
	if iv.statusErr == "" {
		t.Fatal("expected a status error when swarm is nil")
	}
}
