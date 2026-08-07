package subagents

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestResumeWithPromptDeliversFollowUpToIdleWorker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("subagent inbox transport uses Unix-domain sockets")
	}

	root := shortSocketDir(t)
	ready := make(chan struct{}, 1)
	commands := make(chan Envelope, 2)
	f := New(Config{
		Root: root, RepoRoot: root,
		NewRunner: func(a *Agent) Runner {
			return RunnerFunc(func(ctx context.Context, _ Sink) error {
				listener, err := Listen(a.InboxPath)
				if err != nil {
					return err
				}
				defer listener.Close()
				a.setProcessState(ProcessAlive)
				a.setTurnState(TurnIdle, "")
				ready <- struct{}{}
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case line, ok := <-listener.Lines():
						if !ok {
							return nil
						}
						command, err := ParseCommand(line)
						if err != nil {
							return err
						}
						if command.Type == CommandAgentShutdown {
							return nil
						}
						commands <- command
					}
				}
			})
		},
	})
	defer f.StopAll()

	a, err := f.Spawn(context.Background(), "review the implementation")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("idle worker did not open its inbox")
	}

	const followUp = "I applied your review. What do you think now?"
	continued, err := f.ResumeWithPrompt(context.Background(), a.ID, followUp)
	if err != nil {
		t.Fatalf("continue idle worker: %v", err)
	}
	if continued != a {
		t.Fatalf("continued agent = %p, want existing worker %p", continued, a)
	}
	if got := a.TurnState(); got != TurnQueued {
		t.Fatalf("turn state after sending follow-up = %s, want %s", got, TurnQueued)
	}
	if _, err := f.ResumeWithPrompt(context.Background(), a.ID, "duplicate follow-up"); err == nil {
		t.Fatal("second follow-up succeeded while the first was queued")
	}
	select {
	case command := <-commands:
		if command.Type != CommandTurnStart {
			t.Fatalf("command type = %q, want %q", command.Type, CommandTurnStart)
		}
		var payload TurnStartPayload
		if err := command.DecodePayload(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Prompt != followUp {
			t.Fatalf("follow-up prompt = %q, want %q", payload.Prompt, followUp)
		}
	case <-time.After(time.Second):
		t.Fatal("idle worker did not receive the follow-up")
	}

	persisted, err := readAgentMeta(filepath.Join(root, "agents", a.ID))
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ResumePrompt != followUp || persisted.ResumePromptAt.IsZero() {
		t.Fatalf("queued follow-up was not persisted: prompt %q accepted %s", persisted.ResumePrompt, persisted.ResumePromptAt)
	}
	if err := f.Stop(a.ID); err != nil {
		t.Fatal(err)
	}
	a.Wait()

	restarted := make(chan *Agent, 1)
	f2 := New(Config{
		Root: root, RepoRoot: root,
		NewRunner: func(agent *Agent) Runner {
			return RunnerFunc(func(ctx context.Context, _ Sink) error {
				restarted <- agent
				<-ctx.Done()
				return ctx.Err()
			})
		},
	})
	defer f2.StopAll()
	if loaded, errs := f2.Reload(); loaded != 1 || len(errs) != 0 {
		t.Fatalf("reload = (%d, %v), want (1, no errors)", loaded, errs)
	}
	resumed, err := f2.ResumeSession(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-restarted:
		if got != resumed || got.resumePrompt() != followUp {
			t.Fatalf("unacknowledged follow-up was not replayed: agent %p prompt %q", got, got.resumePrompt())
		}
	case <-time.After(time.Second):
		t.Fatal("restarted worker did not receive the follow-up")
	}
}
