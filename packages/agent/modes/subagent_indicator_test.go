package modes

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/bnema/zut/packages/agent/extproto"
	"github.com/bnema/zut/packages/agent/subagents"
	"github.com/bnema/zut/packages/tui"
	"github.com/mattn/go-runewidth"
)

type sizedSubagentIndicatorTerminal struct {
	*alertTestTerminal
	cols int
	rows int
}

func (t *sizedSubagentIndicatorTerminal) Size() (int, int) { return t.cols, t.rows }

func TestRenderSubagentActivityLinesShowsOnlyActiveTurns(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	lines := renderSubagentActivityLines(tui.Dark, "/", []subagents.AgentSnapshot{
		{
			ID:           "design-123",
			Subagent:     "senior-engineer",
			Status:       subagents.StatusPending,
			ProcessState: subagents.ProcessPending,
			TurnState:    subagents.TurnQueued,
			Activity:     "queued",
			LastActivity: now.Add(-4 * time.Second),
		},
		{
			ID:           "tests-456",
			Subagent:     "",
			Status:       subagents.StatusRunning,
			ProcessState: subagents.ProcessAlive,
			TurnState:    subagents.TurnRunning,
			Activity:     "tool: read",
			LastActivity: now.Add(-72 * time.Second),
		},
		{
			ID:           "heartbeat-789",
			Subagent:     "implementer",
			Status:       subagents.StatusRunning,
			ProcessState: subagents.ProcessAlive,
			TurnState:    subagents.TurnRunning,
			Activity:     "idle",
			LastActivity: now.Add(-5 * time.Second),
		},
		{
			ID:           "idle-789",
			Subagent:     "reviewer",
			Status:       subagents.StatusRunning,
			ProcessState: subagents.ProcessAlive,
			TurnState:    subagents.TurnIdle,
			Activity:     "idle",
			LastActivity: now.Add(-time.Second),
		},
		{
			ID:           "settled-012",
			Subagent:     "implementer",
			Status:       subagents.StatusPending,
			ProcessState: subagents.ProcessPending,
			TurnState:    subagents.TurnSucceeded,
			Activity:     "done",
			LastActivity: now.Add(-time.Second),
		},
		{
			ID:           "done-345",
			Subagent:     "implementer",
			Status:       subagents.StatusDone,
			ProcessState: subagents.ProcessExited,
			TurnState:    subagents.TurnSucceeded,
			Activity:     "done",
			LastActivity: now.Add(-time.Second),
		},
		{
			ID:           "detached-678",
			Subagent:     "reviewer",
			Status:       subagents.StatusRunning,
			ProcessState: subagents.ProcessDetached,
			TurnState:    subagents.TurnRunning,
			Activity:     "detached",
			LastActivity: now.Add(-time.Second),
		},
	}, 80, now)

	got := plainActivityLines(lines)
	want := []string{
		"  / senior-engineer · queued · 4s",
		"  / tests-456 · tool: read · 1m12s",
		"  / implementer · working · 5s",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("activity lines = %#v, want %#v", got, want)
	}
}

func TestRenderSubagentActivityLinesKeepsNameAndAgeOnNarrowTerminals(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	const width = 27
	lines := renderSubagentActivityLines(tui.Dark, "|", []subagents.AgentSnapshot{{
		ID:           "review-123",
		Subagent:     "reviewer",
		Status:       subagents.StatusRunning,
		ProcessState: subagents.ProcessAlive,
		TurnState:    subagents.TurnRunning,
		Activity:     "tool: read an exceptionally long filename",
		LastActivity: now.Add(-72 * time.Second),
	}}, width, now)

	if len(lines) != 1 {
		t.Fatalf("line count = %d, want 1", len(lines))
	}
	line := plainActivityLines(lines)[0]
	if runewidth.StringWidth(line) > width {
		t.Fatalf("line width = %d, want <= %d: %q", runewidth.StringWidth(line), width, line)
	}
	for _, want := range []string{"reviewer", "1m12s"} {
		if !strings.Contains(line, want) {
			t.Fatalf("narrow line %q is missing %q", line, want)
		}
	}
}

func TestRenderSubagentActivityLinesSanitizesWorkerText(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	lines := renderSubagentActivityLines(tui.Dark, "/", []subagents.AgentSnapshot{{
		ID:           "review-123",
		Subagent:     "reviewer\x1b]52;c;ignored\a",
		Status:       subagents.StatusRunning,
		ProcessState: subagents.ProcessAlive,
		TurnState:    subagents.TurnRunning,
		Activity:     "tool:\tread\x1b[2J\rfile",
		LastActivity: now.Add(-time.Second),
	}}, 80, now)
	if len(lines) != 1 {
		t.Fatalf("line count = %d, want 1", len(lines))
	}
	plain := plainActivityLines(lines)[0]
	if strings.IndexFunc(plain, unicode.IsControl) >= 0 {
		t.Fatalf("indicator retained a control character: %q", plain)
	}
	for _, want := range []string{"reviewer", "tool: read file"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("sanitized indicator %q is missing %q", plain, want)
		}
	}
}

func TestTruncateSubagentIndicatorTextPreservesGraphemeClusters(t *testing.T) {
	if got, want := truncateSubagentIndicatorText("a👩‍💻abcdef", 6), "a👩‍💻..."; got != want {
		t.Fatalf("truncated grapheme = %q, want %q", got, want)
	}
}

func TestFormatSubagentActivityAgeFallsBackToLifecycleTimes(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	if got, want := formatSubagentActivityAge(subagents.AgentSnapshot{UpdatedAt: now.Add(-7 * time.Second)}, now), "7s"; got != want {
		t.Fatalf("updated-at age = %q, want %q", got, want)
	}
	if got, want := formatSubagentActivityAge(subagents.AgentSnapshot{Started: now.Add(-8 * time.Second)}, now), "8s"; got != want {
		t.Fatalf("started-at age = %q, want %q", got, want)
	}
}

func TestLimitSubagentActivityLinesSummarizesOmittedWorkers(t *testing.T) {
	lines := []string{"one", "two", "three", "four"}
	got := plainActivityLines(limitSubagentActivityLines(tui.Dark, lines, 3, 80))
	want := []string{"one", "two", "  … 2 more active subagents"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("limited lines = %#v, want %#v", got, want)
	}
}

func TestSubagentActivityRowsFitFixedRightBarFrame(t *testing.T) {
	release := make(chan struct{})
	supervisor := subagents.New(subagents.Config{
		Root:     t.TempDir(),
		RepoRoot: t.TempDir(),
		NewRunner: func(*subagents.Agent) subagents.Runner {
			return subagents.RunnerFunc(func(ctx context.Context, sink subagents.Sink) error {
				sink.Activity("thinking")
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		},
	})
	defer func() {
		close(release)
		supervisor.StopAll()
	}()
	for n := 0; n < 7; n++ {
		if _, err := supervisor.SpawnReq(context.Background(), subagents.SpawnRequest{
			Task:     "inspect layout",
			Subagent: "reviewer",
		}); err != nil {
			t.Fatal(err)
		}
	}

	term := &sizedSubagentIndicatorTerminal{
		alertTestTerminal: &alertTestTerminal{},
		cols:              80,
		rows:              8,
	}
	interactive := NewInteractive(InteractiveConfig{Terminal: term, Theme: tui.Dark, Supervisor: supervisor})
	interactive.rend.Resize(80, 8)
	interactive.SetWidget("test", "right", extproto.WidgetPositionRightBar, "status", nil)
	interactive.redraw()

	output := stripANSIBytes(term.String())
	if !strings.Contains(output, "▌") {
		t.Fatalf("input was pushed out of the fixed frame: %q", output)
	}
	if !strings.Contains(output, "more active subagents") {
		t.Fatalf("omitted subagents were not summarized: %q", output)
	}
	if strings.Contains(term.String(), "\x1b[-") {
		t.Fatalf("renderer received a negative cursor row: %q", term.String())
	}
}

func TestSubagentActivityLinesFollowInputPosition(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseRunner := func() { releaseOnce.Do(func() { close(release) }) }
	supervisor := subagents.New(subagents.Config{
		Root:     t.TempDir(),
		RepoRoot: t.TempDir(),
		NewRunner: func(*subagents.Agent) subagents.Runner {
			return subagents.RunnerFunc(func(ctx context.Context, sink subagents.Sink) error {
				sink.Activity("thinking")
				close(started)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		},
	})
	defer func() {
		releaseRunner()
		supervisor.StopAll()
	}()
	agent, err := supervisor.SpawnReq(context.Background(), subagents.SpawnRequest{
		Task:     "inspect the input layout",
		Subagent: "senior-engineer",
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("subagent runner did not start")
	}

	assertPosition := func(t *testing.T, position string, wantBelow bool) {
		t.Helper()
		term := &alertTestTerminal{}
		interactive := NewInteractive(InteractiveConfig{
			Terminal:            term,
			Theme:               tui.Dark,
			Supervisor:          supervisor,
			TUISubagentPosition: position,
		})
		interactive.clock = func() time.Time { return time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC) }
		interactive.rend.Resize(80, 24)
		interactive.redraw()

		output := stripANSIBytes(term.String())
		inputAt := strings.LastIndex(output, "▌")
		subagentAt := strings.LastIndex(output, "senior-engineer")
		if inputAt < 0 || subagentAt < 0 {
			t.Fatalf("rendered input/subagent missing from %q", output)
		}
		if gotBelow := subagentAt > inputAt; gotBelow != wantBelow {
			t.Fatalf("subagent below input = %v, want %v: %q", gotBelow, wantBelow, output)
		}
	}

	t.Run("default below", func(t *testing.T) {
		assertPosition(t, "", true)
	})
	t.Run("configured above", func(t *testing.T) {
		assertPosition(t, tui.SubagentPositionAboveInput, false)
	})
	t.Run("dashboard editor suppresses inline animation", func(t *testing.T) {
		term := &alertTestTerminal{}
		interactive := NewInteractive(InteractiveConfig{Terminal: term, Theme: tui.Dark, Supervisor: supervisor})
		interactive.rend.Resize(80, 24)
		interactive.subagentsDialog.Open(staticSnapshots(), nil, nil, func(string, string, string) error { return nil }, nil, nil, "")
		interactive.subagentsDialog.HandleKey(tui.Key{Kind: tui.KeyRune, Rune: 'n'})
		if interactive.subagentsDialog.NeedsTickRefresh() {
			t.Fatal("subagent spawn editor should suppress dashboard ticks")
		}
		interactive.redraw()
		if interactive.subagentActivityActive {
			t.Fatal("hidden inline indicators should not request animation ticks")
		}
	})

	releaseRunner()
	agent.Wait()
	term := &alertTestTerminal{}
	interactive := NewInteractive(InteractiveConfig{Terminal: term, Theme: tui.Dark, Supervisor: supervisor})
	interactive.rend.Resize(80, 24)
	interactive.redraw()
	if output := stripANSIBytes(term.String()); strings.Contains(output, "senior-engineer") {
		t.Fatalf("finished subagent indicator remained visible: %q", output)
	}
}

func TestSetSubagentSessionScopeRefreshesActivityFilter(t *testing.T) {
	release := make(chan struct{})
	supervisor := subagents.New(subagents.Config{
		Root:     t.TempDir(),
		RepoRoot: t.TempDir(),
		NewRunner: func(*subagents.Agent) subagents.Runner {
			return subagents.RunnerFunc(func(ctx context.Context, sink subagents.Sink) error {
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		},
	})
	defer func() {
		close(release)
		supervisor.StopAll()
	}()
	supervisor.SetActiveSession("session-a")
	if _, err := supervisor.SpawnReq(context.Background(), subagents.SpawnRequest{
		Task:     "inspect scope",
		Subagent: "reviewer",
	}); err != nil {
		t.Fatal(err)
	}

	interactive := NewInteractive(InteractiveConfig{Supervisor: supervisor})
	if got := len(interactive.activeSubagentActivitySnapshots()); got != 1 {
		t.Fatalf("active rows in initial scope = %d, want 1", got)
	}
	interactive.SetSubagentSessionScope("session-b")
	if got := supervisor.ActiveSession(); got != "session-b" {
		t.Fatalf("active session = %q, want session-b", got)
	}
	select {
	case <-interactive.dirty:
	case <-time.After(time.Second):
		t.Fatal("session scope change did not invalidate the interactive view")
	}
	if got := len(interactive.activeSubagentActivitySnapshots()); got != 0 {
		t.Fatalf("active rows after scope change = %d, want 0", got)
	}

	interactive.SetSubagentSessionScope("session-a")
	if got := len(interactive.activeSubagentActivitySnapshots()); got != 1 {
		t.Fatalf("active rows after returning to scope = %d, want 1", got)
	}
}

func plainActivityLines(lines []string) []string {
	plain := make([]string, len(lines))
	for idx, line := range lines {
		plain[idx] = stripANSIBytes(line)
	}
	return plain
}
