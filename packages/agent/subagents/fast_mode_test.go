package subagents

import (
	"context"
	"strings"
	"testing"
)

func TestSupervisorFastModePropagatesToChild(t *testing.T) {
	root := t.TempDir()
	f := New(Config{
		Root:     root,
		RepoRoot: root,
		FastMode: true,
		NewRunner: func(a *Agent) Runner {
			return RunnerFunc(func(ctx context.Context, _ Sink) error {
				<-ctx.Done()
				return ctx.Err()
			})
		},
	})

	a, err := f.SpawnReq(context.Background(), SpawnRequest{Task: "x"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	cleanedUp := false
	t.Cleanup(func() {
		if cleanedUp {
			return
		}
		if err := f.Stop(a.ID); err != nil {
			t.Errorf("cleanup stop: %v", err)
		}
		a.Wait()
	})
	if !a.FastMode {
		t.Fatal("spawned agent FastMode = false, want true")
	}
	args := defaultChildArgs("/zut", a, "/session", "/inbox")
	if !containsArg(args, "--fast-mode") {
		t.Fatalf("child args = %v, want --fast-mode", args)
	}

	if err := f.Stop(a.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	a.Wait()
	cleanedUp = true

	g := New(Config{Root: root, RepoRoot: root})
	if loaded, errs := g.Reload(); loaded != 1 || len(errs) != 0 {
		t.Fatalf("reload loaded=%d errs=%v", loaded, errs)
	}
	reloaded := g.Get(a.ID)
	if reloaded == nil || !reloaded.FastMode {
		t.Fatalf("reloaded FastMode = %v, want true", reloaded != nil && reloaded.FastMode)
	}
}

func TestSpawnRequestFastModeIsBoundByHostSetting(t *testing.T) {
	falseValue, trueValue := false, true
	cases := []struct {
		name        string
		hostFast    bool
		profileFast *bool
		want        bool
	}{
		{name: "unset inherits enabled host", hostFast: true, want: true},
		{name: "unset inherits disabled host", hostFast: false, want: false},
		{name: "false disables enabled host", hostFast: true, profileFast: &falseValue, want: false},
		{name: "true cannot enable disabled host", hostFast: false, profileFast: &trueValue, want: false},
		{name: "true preserves enabled host", hostFast: true, profileFast: &trueValue, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			f := New(Config{
				Root:     root,
				RepoRoot: root,
				FastMode: tc.hostFast,
				NewRunner: func(*Agent) Runner {
					return RunnerFunc(func(context.Context, Sink) error { return nil })
				},
			})
			a, err := f.SpawnReq(context.Background(), SpawnRequest{Task: "x", FastMode: tc.profileFast})
			if err != nil {
				t.Fatalf("spawn: %v", err)
			}
			a.Wait()
			if a.FastMode != tc.want {
				t.Fatalf("FastMode = %v, want %v", a.FastMode, tc.want)
			}
			args := defaultChildArgs("/zut", a, "/session", "/inbox")
			wantFlag := "--no-fast-mode"
			if tc.want {
				wantFlag = "--fast-mode"
			}
			if !containsArg(args, wantFlag) {
				t.Fatalf("child args = %v, want %s", args, wantFlag)
			}
		})
	}
}

func TestSupervisorReloadPreservesExplicitFastModeOff(t *testing.T) {
	root := t.TempDir()
	newSupervisor := func(fastMode bool) *Supervisor {
		return New(Config{
			Root:     root,
			RepoRoot: root,
			FastMode: fastMode,
			NewRunner: func(a *Agent) Runner {
				return RunnerFunc(func(ctx context.Context, _ Sink) error {
					<-ctx.Done()
					return ctx.Err()
				})
			},
		})
	}

	f := newSupervisor(false)
	a, err := f.SpawnReq(context.Background(), SpawnRequest{Task: "x"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if a.FastMode {
		t.Fatal("spawned agent FastMode = true, want false")
	}
	if err := f.Stop(a.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	a.Wait()

	g := newSupervisor(true)
	if loaded, errs := g.Reload(); loaded != 1 || len(errs) != 0 {
		t.Fatalf("reload loaded=%d errs=%v", loaded, errs)
	}
	if reloaded := g.Get(a.ID); reloaded == nil || reloaded.FastMode {
		t.Fatalf("reloaded FastMode = %v, want false", reloaded != nil && reloaded.FastMode)
	}
}

func TestSubagentWorkerArgsOmitFastModeWhenUnset(t *testing.T) {
	args := subagentWorkerArgs(subagentWorkerArgsOpts{Exe: "/zut", Dir: "/wt", SessionPath: "/s", InboxPath: "/i"})
	if strings.Contains(strings.Join(args, " "), "--fast-mode") || strings.Contains(strings.Join(args, " "), "--no-fast-mode") {
		t.Fatalf("child args = %v, want fast mode omitted when unset", args)
	}
}

func TestSubagentWorkerArgsExplicitlyDisablesFastMode(t *testing.T) {
	args := subagentWorkerArgs(subagentWorkerArgsOpts{
		Exe:         "/zut",
		Dir:         "/wt",
		SessionPath: "/s",
		InboxPath:   "/i",
		FastModeSet: true,
	})
	if !containsArg(args, "--no-fast-mode") {
		t.Fatalf("child args = %v, want --no-fast-mode", args)
	}
	if containsArg(args, "--fast-mode") {
		t.Fatalf("child args = %v, want no positive fast-mode flag", args)
	}
}

func TestDefaultChildArgsExplicitlyDisablesFastMode(t *testing.T) {
	args := defaultChildArgs("/zut", &Agent{Dir: "/wt"}, "/s", "/i")
	if !containsArg(args, "--no-fast-mode") {
		t.Fatalf("child args = %v, want --no-fast-mode", args)
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
