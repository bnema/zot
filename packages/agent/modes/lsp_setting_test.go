package modes

import (
	"errors"
	"strings"
	"testing"
)

type failingLSPSettingsStore struct {
	SettingsStore
}

func (failingLSPSettingsStore) SetLSPEnabled(bool) error {
	return errors.New("persist lsp setting")
}

func (failingLSPSettingsStore) SetSubagentLSPEnabled(bool) error {
	return errors.New("persist subagent lsp setting")
}

type rollbackFailingLSPSettingsStore struct {
	SettingsStore
	calls  int
	values []bool
}

func (s *rollbackFailingLSPSettingsStore) SetLSPEnabled(enabled bool) error {
	s.calls++
	if s.calls == 2 {
		return errors.New("rollback disk full")
	}
	s.values = append(s.values, enabled)
	return nil
}

func (s *rollbackFailingLSPSettingsStore) SetSubagentLSPEnabled(bool) error {
	return nil
}

func TestLSPSettingToggleKeepsMemoryOnPersistenceFailure(t *testing.T) {
	mainEnabled := false
	subagentEnabled := false
	interactive := &Interactive{cfg: InteractiveConfig{
		LSPEnabled:         &mainEnabled,
		SubagentLSPEnabled: &subagentEnabled,
		SettingsStore:      failingLSPSettingsStore{},
	}}

	interactive.applySettingToggle("lsp_enabled", true)
	if *interactive.cfg.LSPEnabled {
		t.Fatal("main-session LSP changed after persistence failure")
	}
	interactive.applySettingToggle("subagent_lsp_enabled", true)
	if *interactive.cfg.SubagentLSPEnabled {
		t.Fatal("sub-agent LSP changed after persistence failure")
	}
}

func TestLSPSettingToggleKeepsDurableStateWhenRollbackPersistenceFails(t *testing.T) {
	mainEnabled := true
	store := &rollbackFailingLSPSettingsStore{}
	refreshCalls := 0
	interactive := NewInteractive(InteractiveConfig{
		LSPEnabled:    &mainEnabled,
		SettingsStore: store,
		RefreshTools: func() error {
			refreshCalls++
			if refreshCalls == 1 {
				return errors.New("refresh failed")
			}
			return nil
		},
	})
	interactive.rend = nil

	interactive.applySettingToggle("lsp_enabled", false)
	if *interactive.cfg.LSPEnabled {
		t.Fatal("in-memory LSP setting reverted despite rollback persistence failure")
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
