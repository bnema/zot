package modes

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bnema/zut/packages/agent/subagents"
	"github.com/bnema/zut/packages/core"
	"github.com/bnema/zut/packages/provider"
)

func TestAutoSubagentsSettingsDisableUnavailablePolicy(t *testing.T) {
	trueValue := true
	falseValue := false

	tests := []struct {
		name       string
		supervisor *subagents.Supervisor
		allowed    *bool
		wantHint   string
	}{
		{
			name:     "supervisor unavailable",
			allowed:  &trueValue,
			wantHint: "subagent supervisor not available in this mode",
		},
		{
			name:       "launch policy excludes tool",
			supervisor: subagents.New(subagents.Config{Root: t.TempDir(), RepoRoot: t.TempDir()}),
			allowed:    &falseValue,
			wantHint:   "launch-time tool policy excludes subagent_spawn",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.supervisor != nil {
				t.Cleanup(tt.supervisor.StopAll)
			}
			iv := &Interactive{
				cfg: InteractiveConfig{
					AutoSubagentsEnabled:     &trueValue,
					AutoSubagentsToolAllowed: tt.allowed,
					Supervisor:               tt.supervisor,
				},
				settingsDialog: newSettingsDialog(),
			}
			iv.openSettingsDialog()

			var item settingsItem
			for _, candidate := range iv.settingsDialog.items {
				if candidate.key == "auto_subagents_enabled" {
					item = candidate
					break
				}
			}
			if !item.disabled {
				t.Fatal("auto-subagents setting is not disabled")
			}
			if item.value {
				t.Fatal("disabled auto-subagents setting is shown as enabled")
			}
			if !strings.Contains(item.hint, tt.wantHint) {
				t.Fatalf("hint = %q; want %q", item.hint, tt.wantHint)
			}
		})
	}
}

func TestAutoSubagentsToolRegistrationHonorsLaunchPolicy(t *testing.T) {
	allowed := false
	enabled := false
	supervisor := subagents.New(subagents.Config{Root: t.TempDir(), RepoRoot: t.TempDir()})
	t.Cleanup(supervisor.StopAll)
	iv := &Interactive{
		agent: &core.Agent{Tools: core.Registry{}},
		cfg: InteractiveConfig{
			AutoSubagentsEnabled:     &enabled,
			AutoSubagentsToolAllowed: &allowed,
			Supervisor:               supervisor,
		},
		dirty: make(chan struct{}, 1),
	}

	iv.applyAutoSubagentsTool(true)
	if _, ok := iv.agent.Tools["subagent_spawn"]; ok {
		t.Fatal("subagent_spawn registered despite launch-time policy")
	}
	if _, ok := iv.agent.Tools["subagent_status"]; ok {
		t.Fatal("subagent_status registered despite launch-time policy")
	}
	iv.applySettingToggle("auto_subagents_enabled", true)
	iv.mu.Lock()
	statusErr := iv.statusErr
	value := iv.cfg.AutoSubagentsEnabled != nil && *iv.cfg.AutoSubagentsEnabled
	iv.mu.Unlock()
	if value {
		t.Fatal("auto-subagents toggle enabled despite launch-time policy")
	}
	if !strings.Contains(statusErr, "launch-time tool policy") {
		t.Fatalf("toggle error = %q; want launch-time policy hint", statusErr)
	}
}

func TestAutoSubagentsToolRegistrationHonorsSeparateLaunchPolicies(t *testing.T) {
	for _, tc := range []struct {
		name          string
		spawnAllowed  bool
		statusAllowed bool
		wantSpawn     bool
		wantStatus    bool
	}{
		{name: "status only", statusAllowed: true, wantStatus: true},
		{name: "spawn only", spawnAllowed: true, wantSpawn: true},
		{name: "both", spawnAllowed: true, statusAllowed: true, wantSpawn: true, wantStatus: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enabled := true
			spawnAllowed := tc.spawnAllowed
			statusAllowed := tc.statusAllowed
			supervisor := subagents.New(subagents.Config{Root: t.TempDir(), RepoRoot: t.TempDir()})
			t.Cleanup(supervisor.StopAll)
			iv := &Interactive{
				agent: &core.Agent{Tools: core.Registry{}},
				cfg: InteractiveConfig{
					AutoSubagentsEnabled:           &enabled,
					AutoSubagentsToolAllowed:       &spawnAllowed,
					AutoSubagentsStatusToolAllowed: &statusAllowed,
					Supervisor:                     supervisor,
				},
				dirty: make(chan struct{}, 1),
			}

			iv.applyAutoSubagentsTool(true)
			_, gotSpawn := iv.agent.Tools["subagent_spawn"]
			_, gotStatus := iv.agent.Tools["subagent_status"]
			if gotSpawn != tc.wantSpawn || gotStatus != tc.wantStatus {
				t.Fatalf("registered tools spawn=%v status=%v, want spawn=%v status=%v", gotSpawn, gotStatus, tc.wantSpawn, tc.wantStatus)
			}
		})
	}
}

type serializedAutoSubagentsClient struct {
	started  chan struct{}
	request  chan provider.Request
	release  chan struct{}
	finished chan struct{}
}

func (c *serializedAutoSubagentsClient) Name() string { return "serialized-auto-subagents-test" }

func (c *serializedAutoSubagentsClient) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	c.request <- req
	c.started <- struct{}{}
	out := make(chan provider.Event, 1)
	go func() {
		defer close(out)
		select {
		case <-c.release:
			out <- provider.EventDone{Stop: provider.StopEnd}
		case <-ctx.Done():
			out <- provider.EventDone{Stop: provider.StopAborted, Err: ctx.Err()}
		}
		close(c.finished)
	}()
	return out, nil
}

func TestAutoSubagentsPromptConfigurationIsSynchronized(t *testing.T) {
	client := &serializedAutoSubagentsClient{
		started:  make(chan struct{}, 1),
		request:  make(chan provider.Request, 1),
		release:  make(chan struct{}),
		finished: make(chan struct{}),
	}
	agent := core.NewAgent(client, "test-model", "base system", nil)
	supervisor := subagents.New(subagents.Config{Root: t.TempDir(), RepoRoot: t.TempDir()})
	t.Cleanup(supervisor.StopAll)
	allowed := true
	iv := NewInteractive(InteractiveConfig{
		Agent:                       agent,
		Supervisor:                  supervisor,
		AutoSubagentsToolAllowed:    &allowed,
		AutoSubagentsSystemAddendum: "delegation guidance",
	})

	iv.startTurn(context.Background(), "hello")
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("prompt did not reach provider")
	}

	request := <-client.request
	if request.System != "base system" {
		t.Fatalf("provider request system = %q; want base system snapshot", request.System)
	}
	applied := make(chan struct{})
	go func() {
		iv.applyAutoSubagentsSystemPrompt(true)
		close(applied)
	}()
	select {
	case <-applied:
	case <-time.After(time.Second):
		t.Fatal("system-prompt update did not apply while provider was running")
	}
	if system, _ := agent.PromptConfig(); !strings.Contains(system, "delegation guidance") {
		t.Fatalf("system prompt missing applied guidance: %q", system)
	}

	close(client.release)
	select {
	case <-client.finished:
	case <-time.After(time.Second):
		t.Fatal("prompt did not finish after provider release")
	}
}
