package modes

import (
	"errors"
	"strings"
	"testing"

	"github.com/bnema/zut/packages/core"
)

type recordingPonytailSettingsStore struct {
	SettingsStore
	values   []bool
	calls    int
	err      error
	failures map[int]error
}

func (s *recordingPonytailSettingsStore) SetPonytailEnabled(enabled bool) error {
	s.calls++
	if s.err != nil {
		return s.err
	}
	if err := s.failures[s.calls]; err != nil {
		return err
	}
	s.values = append(s.values, enabled)
	return nil
}

func TestSettingsDialogOffersPonytailEnabledByDefault(t *testing.T) {
	store := &recordingPonytailSettingsStore{}
	interactive := NewInteractive(InteractiveConfig{
		SettingsStore: store,
		RefreshPrompt: func() error { return nil },
	})
	interactive.openSettingsDialog()

	item := findSettingsItem(interactive.settingsDialog.items, "ponytail_enabled")
	if item == nil {
		t.Fatal("settings dialog is missing Ponytail mode")
	}
	if !item.value {
		t.Fatal("Ponytail mode should be enabled when the config value is absent")
	}
	if item.disabled {
		t.Fatal("Ponytail mode should be available with persistence and refresh capabilities")
	}
}

func TestSettingsDialogDisablesPonytailWithoutCapabilities(t *testing.T) {
	cases := []struct {
		name string
		cfg  InteractiveConfig
	}{
		{name: "neither capability", cfg: InteractiveConfig{}},
		{name: "persistence only", cfg: InteractiveConfig{SettingsStore: &recordingPonytailSettingsStore{}}},
		{name: "refresh only", cfg: InteractiveConfig{RefreshPrompt: func() error { return nil }}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			interactive := NewInteractive(tc.cfg)
			interactive.openSettingsDialog()

			item := findSettingsItem(interactive.settingsDialog.items, "ponytail_enabled")
			if item == nil {
				t.Fatal("settings dialog is missing Ponytail mode")
			}
			if !item.disabled {
				t.Fatal("Ponytail mode should be disabled without both capabilities")
			}
			if item.hint == "" {
				t.Fatal("disabled Ponytail mode should explain why it is unavailable")
			}
		})
	}
}

func TestApplyPonytailToggleRefreshesAndPersists(t *testing.T) {
	store := &recordingPonytailSettingsStore{}
	refreshCalls := 0
	interactive := NewInteractive(InteractiveConfig{
		SettingsStore: store,
		RefreshPrompt: func() error {
			refreshCalls++
			return nil
		},
	})
	interactive.rend = nil

	interactive.applySettingToggle("ponytail_enabled", false)
	if interactive.ponytailEnabled() {
		t.Fatal("Ponytail mode remained enabled after disabling it")
	}
	interactive.applySettingToggle("ponytail_enabled", true)
	if !interactive.ponytailEnabled() {
		t.Fatal("Ponytail mode remained disabled after enabling it")
	}
	if refreshCalls != 2 {
		t.Fatalf("refresh calls = %d, want 2", refreshCalls)
	}
	if len(store.values) != 2 || store.values[0] || !store.values[1] {
		t.Fatalf("persisted values = %v, want [false true]", store.values)
	}
	if interactive.statusErr != "" {
		t.Fatalf("unexpected status error: %q", interactive.statusErr)
	}
}

func TestApplyPonytailToggleRollsBackWhenRefreshFails(t *testing.T) {
	store := &recordingPonytailSettingsStore{}
	refreshCalls := 0
	interactive := NewInteractive(InteractiveConfig{
		SettingsStore: store,
		RefreshPrompt: func() error {
			refreshCalls++
			if refreshCalls == 1 {
				return errors.New("refresh failed")
			}
			return nil
		},
	})
	interactive.rend = nil

	interactive.applySettingToggle("ponytail_enabled", false)
	if !interactive.ponytailEnabled() {
		t.Fatal("Ponytail mode stayed disabled after refresh rollback")
	}
	if refreshCalls != 2 {
		t.Fatalf("refresh calls = %d, want initial refresh and rollback refresh", refreshCalls)
	}
	if len(store.values) != 2 || store.values[0] || !store.values[1] {
		t.Fatalf("persisted rollback values = %v, want [false true]", store.values)
	}
	if !strings.Contains(interactive.statusErr, "refresh failed") {
		t.Fatalf("status error = %q, want refresh failure", interactive.statusErr)
	}
}

func TestApplyPonytailToggleKeepsDurableStateWhenRollbackPersistenceFails(t *testing.T) {
	store := &recordingPonytailSettingsStore{
		failures: map[int]error{2: errors.New("rollback disk full")},
	}
	refreshCalls := 0
	interactive := NewInteractive(InteractiveConfig{
		SettingsStore: store,
		RefreshPrompt: func() error {
			refreshCalls++
			if refreshCalls == 1 {
				return errors.New("refresh failed")
			}
			return nil
		},
	})
	interactive.rend = nil

	interactive.applySettingToggle("ponytail_enabled", false)
	if interactive.ponytailEnabled() {
		t.Fatal("in-memory setting reverted despite rollback persistence failure")
	}
	if len(store.values) != 1 || store.values[0] {
		t.Fatalf("durable writes = %v, want only the successful disable", store.values)
	}
	if refreshCalls != 2 {
		t.Fatalf("refresh calls = %d, want failed refresh plus durable-state reconciliation", refreshCalls)
	}
	if !strings.Contains(interactive.statusErr, "rollback disk full") {
		t.Fatalf("status error = %q, want rollback failure", interactive.statusErr)
	}
}

func TestApplyPonytailToggleKeepsStateWhenPersistenceFails(t *testing.T) {
	store := &recordingPonytailSettingsStore{err: errors.New("disk full")}
	refreshCalls := 0
	interactive := NewInteractive(InteractiveConfig{
		SettingsStore: store,
		RefreshPrompt: func() error {
			refreshCalls++
			return nil
		},
	})
	interactive.rend = nil

	interactive.applySettingToggle("ponytail_enabled", false)
	if !interactive.ponytailEnabled() {
		t.Fatal("Ponytail mode changed despite persistence failure")
	}
	if refreshCalls != 0 {
		t.Fatalf("refresh calls = %d, want none", refreshCalls)
	}
	if !strings.Contains(interactive.statusErr, "disk full") {
		t.Fatalf("status error = %q, want persistence failure", interactive.statusErr)
	}
}

func TestApplyPonytailToggleUpdatesLiveAgentPrompt(t *testing.T) {
	const ponytailBlock = "[ponytail test block]"
	store := &recordingPonytailSettingsStore{}
	ag := core.NewAgent(nil, "model", "base\n\n"+ponytailBlock, nil)
	var interactive *Interactive
	refreshCalls := 0
	interactive = NewInteractive(InteractiveConfig{
		Agent:         ag,
		SettingsStore: store,
		RefreshPrompt: func() error {
			refreshCalls++
			system := "base"
			if interactive.ponytailEnabled() {
				system += "\n\n" + ponytailBlock
			}
			ag.SetSystemPrompt(system)
			return nil
		},
	})
	interactive.rend = nil

	interactive.applySettingToggle("ponytail_enabled", false)
	system, _ := ag.PromptConfig()
	if strings.Contains(system, ponytailBlock) {
		t.Fatalf("live prompt still contains disabled Ponytail block: %q", system)
	}
	interactive.applySettingToggle("ponytail_enabled", true)
	system, _ = ag.PromptConfig()
	if strings.Count(system, ponytailBlock) != 1 {
		t.Fatalf("live prompt block count = %d, want 1: %q", strings.Count(system, ponytailBlock), system)
	}
	if refreshCalls != 2 {
		t.Fatalf("refresh calls = %d, want 2", refreshCalls)
	}
}
