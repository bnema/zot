package modes

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/patriceckhart/zot/packages/agent/subagents"
	"github.com/patriceckhart/zot/packages/core"
)

func TestAutoSubagentsSystemPromptTogglesProfileManifestWithDelegationGuidance(t *testing.T) {
	iv := &Interactive{
		agent: &core.Agent{System: "base system"},
		cfg: InteractiveConfig{
			AutoSubagentsSystemAddendum: "auto-subagents guidance",
			SubagentsSystemAddendum:     "[subagents_list]\n- reviewer\n[/subagents_list]",
		},
	}

	iv.applyAutoSubagentsSystemPrompt(true)
	if !strings.Contains(iv.agent.System, "[subagents_list]") || !strings.Contains(iv.agent.System, "auto-subagents guidance") {
		t.Fatalf("enabled system prompt missing subagent blocks: %q", iv.agent.System)
	}
	before := iv.agent.System
	iv.applyAutoSubagentsSystemPrompt(true)
	if iv.agent.System != before {
		t.Fatalf("enabling twice changed system prompt: %q -> %q", before, iv.agent.System)
	}

	iv.applyAutoSubagentsSystemPrompt(false)
	if strings.Contains(iv.agent.System, "[subagents_list]") || strings.Contains(iv.agent.System, "auto-subagents guidance") {
		t.Fatalf("disabled system prompt retained subagent blocks: %q", iv.agent.System)
	}
}

func TestAutoSubagentsTogglePreservesMatchingBasePromptBlocks(t *testing.T) {
	manifest := "[subagents_list]\n- reviewer\n[/subagents_list]"
	guidance := "auto-subagents guidance"
	base := "base system\n\n" + manifest + "\n\n" + guidance
	iv := &Interactive{
		agent: &core.Agent{System: base},
		cfg: InteractiveConfig{
			AutoSubagentsSystemAddendum: guidance,
			SubagentsSystemAddendum:     manifest,
		},
	}

	iv.applyAutoSubagentsSystemPrompt(true)
	if strings.Count(iv.agent.System, manifest) != 2 || strings.Count(iv.agent.System, guidance) != 2 {
		t.Fatalf("enabling should append owned duplicate blocks: %q", iv.agent.System)
	}
	iv.applyAutoSubagentsSystemPrompt(false)
	if got := strings.TrimSpace(iv.agent.System); got != base {
		t.Fatalf("disabling changed matching base prompt: %q; want %q", got, base)
	}
}

func TestAutoSubagentsSummaryIncludesCompleteFinalResponseAfterLongTask(t *testing.T) {
	response := "first response line\n" + strings.Repeat("result ", 150) + "final marker"
	mgr := subagents.New(subagents.Config{
		Root:     t.TempDir(),
		RepoRoot: t.TempDir(),
		NewRunner: func(*subagents.Agent) subagents.Runner {
			return subagents.RunnerFunc(func(_ context.Context, sink subagents.Sink) error {
				sink.Transcript(response)
				return nil
			})
		},
	})
	a, err := mgr.Spawn(context.Background(), strings.Repeat("long task ", 150))
	if err != nil {
		t.Fatal(err)
	}

	iv := newQueuedAutoSubagentsInteractive()
	iv.TrackSubagentWorker(a, a.Task)
	update := waitForQueuedPrompt(t, iv)
	if !strings.Contains(update, "final response:\n"+response) {
		t.Fatalf("update missing complete final response: %q", update)
	}
}

func TestTrackSubagentWorkerReportsStartupFailure(t *testing.T) {
	mgr := subagents.New(subagents.Config{
		Root:     t.TempDir(),
		RepoRoot: t.TempDir(),
		NewRunner: func(*subagents.Agent) subagents.Runner {
			return subagents.RunnerFunc(func(context.Context, subagents.Sink) error {
				return errors.New("listener startup failed")
			})
		},
	})
	a, err := mgr.Spawn(context.Background(), "report the date")
	if err != nil {
		t.Fatal(err)
	}

	iv := newQueuedAutoSubagentsInteractive()
	iv.TrackSubagentWorker(a, a.Task)

	update := waitForQueuedPrompt(t, iv)
	if !strings.Contains(update, "status: failed") {
		t.Fatalf("update missing failed status: %q", update)
	}
	if !strings.Contains(update, "listener startup failed") {
		t.Fatalf("update missing startup error: %q", update)
	}
}

func TestCompleteSupervisorWatchReportsTurnOutcomeOnce(t *testing.T) {
	started := make(chan struct{})
	mgr := subagents.New(subagents.Config{
		Root:     t.TempDir(),
		RepoRoot: t.TempDir(),
		NewRunner: func(*subagents.Agent) subagents.Runner {
			return subagents.RunnerFunc(func(ctx context.Context, _ subagents.Sink) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			})
		},
	})
	a, err := mgr.Spawn(context.Background(), "report the date")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	t.Cleanup(func() {
		_ = mgr.Stop(a.ID)
		a.Wait()
	})

	iv := newQueuedAutoSubagentsInteractive()
	entry := &subagentWatchEntry{agent: a, task: a.Task}
	iv.subagentWatch = []*subagentWatchEntry{entry}
	iv.completeSupervisorWatchEntry(entry, "completed", "")
	iv.completeSupervisorWatchEntry(entry, "failed", "late error")

	update := waitForQueuedPrompt(t, iv)
	if !strings.Contains(update, "status: completed") {
		t.Fatalf("update uses daemon status instead of turn outcome: %q", update)
	}
	iv.mu.Lock()
	queued := len(iv.queued)
	iv.mu.Unlock()
	if queued != 1 {
		t.Fatalf("queued updates = %d; want exactly one", queued)
	}
}

func newQueuedAutoSubagentsInteractive() *Interactive {
	return &Interactive{
		agent:      &core.Agent{},
		busy:       true,
		compacting: true,
		dirty:      make(chan struct{}, 1),
	}
}

func waitForQueuedPrompt(t *testing.T, iv *Interactive) string {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		iv.mu.Lock()
		if len(iv.queued) > 0 {
			prompt := iv.queued[0]
			iv.mu.Unlock()
			return prompt
		}
		iv.mu.Unlock()
		select {
		case <-deadline.C:
			t.Fatal("timed out waiting for auto-subagents update")
		case <-ticker.C:
		}
	}
}
