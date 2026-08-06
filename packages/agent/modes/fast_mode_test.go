package modes

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bnema/zut/packages/agent/subagents"
	"github.com/bnema/zut/packages/core"
	"github.com/bnema/zut/packages/provider"
)

type recordingFastModeSettingsStore struct {
	SettingsStore
	setCalls int
	last     bool
}

func (s *recordingFastModeSettingsStore) SetFastMode(enabled bool) error {
	s.setCalls++
	s.last = enabled
	return nil
}

type failingFastModeSettingsStore struct {
	SettingsStore
	err error
}

func (s *failingFastModeSettingsStore) SetFastMode(bool) error { return s.err }

type fastModeProviderClient struct{}

func (fastModeProviderClient) Name() string { return "openai" }
func (fastModeProviderClient) Stream(_ context.Context, _ provider.Request) (<-chan provider.Event, error) {
	out := make(chan provider.Event)
	close(out)
	return out, nil
}

func TestSettingsDialogOffersFastModeOffByDefault(t *testing.T) {
	interactive := NewInteractive(InteractiveConfig{Provider: "openai", Model: "gpt-5"})
	interactive.openSettingsDialog()

	item := findSettingsItem(interactive.settingsDialog.items, "fast_mode")
	if item == nil {
		t.Fatal("settings dialog is missing fast mode")
	}
	if item.value {
		t.Fatal("fast mode is enabled by default")
	}
}

func TestApplyFastModeUpdatesAgentAndStore(t *testing.T) {
	store := &recordingFastModeSettingsStore{}
	agent := core.NewAgent(fastModeProviderClient{}, "gpt-5", "", nil)
	interactive := NewInteractive(InteractiveConfig{
		Provider:      "openai",
		Model:         "gpt-5",
		SettingsStore: store,
		Agent:         agent,
	})
	interactive.openSettingsDialog()
	interactive.rend = nil

	interactive.applySettingToggle("fast_mode", true)

	if interactive.cfg.FastMode == nil || !*interactive.cfg.FastMode {
		t.Fatal("interactive FastMode was not enabled")
	}
	if !agent.FastMode {
		t.Fatal("agent FastMode was not enabled")
	}
	if store.setCalls != 1 || !store.last {
		t.Fatalf("store calls = %d/%v, want one enabled write", store.setCalls, store.last)
	}
}

func TestFastSlashTogglesFastMode(t *testing.T) {
	store := &recordingFastModeSettingsStore{}
	agent := core.NewAgent(fastModeProviderClient{}, "gpt-5", "", nil)
	supervisor := subagents.New(subagents.Config{Root: t.TempDir(), RepoRoot: t.TempDir()})
	interactive := NewInteractive(InteractiveConfig{
		Provider:      "openai",
		Model:         "gpt-5",
		SettingsStore: store,
		Agent:         agent,
		Supervisor:    supervisor,
	})
	interactive.rend = nil

	if interactive.runSlash(context.Background(), "/FAST") {
		t.Fatal("/fast requested exit")
	}
	if interactive.cfg.FastMode == nil || !*interactive.cfg.FastMode || !agent.FastMode || !supervisor.FastMode() {
		t.Fatalf("/fast did not enable every live fast-mode target: cfg=%v agent=%v supervisor=%v", interactive.cfg.FastMode, agent.FastMode, supervisor.FastMode())
	}
	if store.setCalls != 1 || !store.last || interactive.statusOK != "fast mode enabled" {
		t.Fatalf("enable persistence/status = calls=%d last=%v status=%q", store.setCalls, store.last, interactive.statusOK)
	}

	interactive.runSlash(context.Background(), "/fast")
	if interactive.cfg.FastMode == nil || *interactive.cfg.FastMode || agent.FastMode || supervisor.FastMode() {
		t.Fatalf("/fast did not disable every live fast-mode target: cfg=%v agent=%v supervisor=%v", interactive.cfg.FastMode, agent.FastMode, supervisor.FastMode())
	}
	if store.setCalls != 2 || store.last || interactive.statusOK != "fast mode disabled" {
		t.Fatalf("disable persistence/status = calls=%d last=%v status=%q", store.setCalls, store.last, interactive.statusOK)
	}
}

func TestApplyFastModeRejectsUnsupportedProvider(t *testing.T) {
	store := &recordingFastModeSettingsStore{}
	interactive := NewInteractive(InteractiveConfig{
		Provider:      "anthropic",
		Model:         "claude-sonnet-4-5",
		SettingsStore: store,
	})
	interactive.openSettingsDialog()
	interactive.rend = nil

	interactive.applySettingToggle("fast_mode", true)

	if interactive.cfg.FastMode != nil && *interactive.cfg.FastMode {
		t.Fatal("unsupported fast mode was enabled")
	}
	if store.setCalls != 0 {
		t.Fatalf("store calls = %d, want none", store.setCalls)
	}
	if !strings.Contains(interactive.statusErr, "only supported for OpenAI providers") {
		t.Fatalf("status error = %q, want unsupported-provider error", interactive.statusErr)
	}
}

func TestApplyFastModeCanDisableUnsupportedProvider(t *testing.T) {
	enabled := true
	store := &recordingFastModeSettingsStore{}
	agent := core.NewAgent(fastModeProviderClient{}, "claude-sonnet-4-5", "", nil)
	agent.FastMode = true
	interactive := NewInteractive(InteractiveConfig{
		Provider:      "anthropic",
		Model:         "claude-sonnet-4-5",
		FastMode:      &enabled,
		SettingsStore: store,
		Agent:         agent,
	})
	interactive.openSettingsDialog()
	interactive.rend = nil

	interactive.applySettingToggle("fast_mode", false)

	if interactive.cfg.FastMode == nil || *interactive.cfg.FastMode {
		t.Fatal("fast mode was not disabled")
	}
	if agent.FastMode {
		t.Fatal("agent fast mode was not disabled")
	}
	if store.setCalls != 1 || store.last {
		t.Fatalf("store calls = %d/%v, want one disabled write", store.setCalls, store.last)
	}
}

func TestApplyFastModeLeavesLiveStateWhenPersistenceFails(t *testing.T) {
	store := &failingFastModeSettingsStore{err: errors.New("disk full")}
	agent := core.NewAgent(fastModeProviderClient{}, "gpt-5", "", nil)
	interactive := NewInteractive(InteractiveConfig{
		Provider:      "openai",
		Model:         "gpt-5",
		SettingsStore: store,
		Agent:         agent,
	})
	interactive.openSettingsDialog()
	interactive.rend = nil

	interactive.applySettingToggle("fast_mode", true)

	if interactive.cfg.FastMode != nil && *interactive.cfg.FastMode {
		t.Fatal("fast mode changed despite persistence failure")
	}
	if agent.FastMode {
		t.Fatal("agent fast mode changed despite persistence failure")
	}
	if !strings.Contains(interactive.statusErr, "disk full") {
		t.Fatalf("status error = %q, want persistence error", interactive.statusErr)
	}
}

func findSettingsItem(items []settingsItem, key string) *settingsItem {
	for idx := range items {
		if items[idx].key == key {
			return &items[idx]
		}
	}
	return nil
}
