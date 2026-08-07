package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/bnema/zut/packages/agent/subagents"
)

func TestSubagentStopTerminatesStuckWorker(t *testing.T) {
	root := t.TempDir()
	started := make(chan struct{}, 1)
	manager := subagents.New(subagents.Config{
		Root:     filepath.Join(root, "subagents"),
		RepoRoot: root,
		NewRunner: func(*subagents.Agent) subagents.Runner {
			return subagents.RunnerFunc(func(ctx context.Context, _ subagents.Sink) error {
				started <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			})
		},
	})
	t.Cleanup(manager.StopAll)

	agent, err := manager.Spawn(context.Background(), "investigate a stuck operation")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("subagent did not start")
	}

	var tracked *subagents.Agent
	tool := &SubagentStopTool{
		Supervisor: manager,
		Enabled:    func() bool { return true },
		OnStopRequested: func(got *subagents.Agent) {
			tracked = got
		},
	}
	args, err := json.Marshal(subagentStopArgs{AgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", textResult(res.Content))
	}
	if tracked != agent {
		t.Fatalf("stop callback agent = %p, want %p", tracked, agent)
	}
	agent.Wait()
	if got := agent.Status(); got != subagents.StatusKilled {
		t.Fatalf("agent status = %s, want %s", got, subagents.StatusKilled)
	}

	var got struct {
		Action string `json:"action"`
		Agent  struct {
			ID    string `json:"agent_id"`
			State string `json:"state"`
		} `json:"agent"`
	}
	if err := json.Unmarshal([]byte(textResult(res.Content)), &got); err != nil {
		t.Fatalf("stop response JSON: %v", err)
	}
	if got.Action != "stop_requested" || got.Agent.ID != agent.ID || got.Agent.State != "cancelled" {
		t.Fatalf("stop response = %+v, want stop_requested cancelled agent %s", got, agent.ID)
	}
}

func TestSubagentResumeRestartsSessionWithFollowUp(t *testing.T) {
	root := t.TempDir()
	started := make(chan *subagents.Agent, 2)
	manager := subagents.New(subagents.Config{
		Root:     filepath.Join(root, "subagents"),
		RepoRoot: root,
		NewRunner: func(agent *subagents.Agent) subagents.Runner {
			return subagents.RunnerFunc(func(ctx context.Context, _ subagents.Sink) error {
				started <- agent
				<-ctx.Done()
				return ctx.Err()
			})
		},
	})
	t.Cleanup(manager.StopAll)

	first, err := manager.Spawn(context.Background(), "review the implementation")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-started:
		if got != first {
			t.Fatalf("first runner agent = %p, want %p", got, first)
		}
	case <-time.After(time.Second):
		t.Fatal("initial subagent did not start")
	}
	if err := manager.Stop(first.ID); err != nil {
		t.Fatal(err)
	}
	first.Wait()

	const prompt = "I applied your review. What do you think now?"
	var tracked *subagents.Agent
	var trackedPrompt string
	tool := &SubagentResumeTool{
		Supervisor: manager,
		Enabled:    func() bool { return true },
		OnResumed: func(agent *subagents.Agent, gotPrompt string) {
			tracked = agent
			trackedPrompt = gotPrompt
		},
	}
	args, err := json.Marshal(subagentResumeArgs{AgentID: first.ID, Prompt: prompt})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", textResult(res.Content))
	}
	trackedID := ""
	if tracked != nil {
		trackedID = tracked.ID
	}
	if trackedID != first.ID || trackedPrompt != prompt {
		t.Fatalf("resume callback = agent %q prompt %q, want agent %s prompt %q", trackedID, trackedPrompt, first.ID, prompt)
	}

	select {
	case resumed := <-started:
		if resumed.ID != first.ID {
			t.Fatalf("resumed id = %q, want %q", resumed.ID, first.ID)
		}
		if resumed.SessionPath != first.SessionPath {
			t.Fatalf("resumed session = %q, want %q", resumed.SessionPath, first.SessionPath)
		}
		if !resumed.Resuming || resumed.ResumePrompt != prompt {
			t.Fatalf("resumed lifecycle = resuming %t prompt %q, want true and %q", resumed.Resuming, resumed.ResumePrompt, prompt)
		}
	case <-time.After(time.Second):
		t.Fatal("resumed subagent did not start")
	}

	var got struct {
		Action string `json:"action"`
		Agent  struct {
			ID string `json:"agent_id"`
		} `json:"agent"`
	}
	if err := json.Unmarshal([]byte(textResult(res.Content)), &got); err != nil {
		t.Fatalf("resume response JSON: %v", err)
	}
	if got.Action != "resumed" || got.Agent.ID != first.ID {
		t.Fatalf("resume response = %+v, want resumed agent %s", got, first.ID)
	}
}
