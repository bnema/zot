package modes

import (
	"errors"
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
