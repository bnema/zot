package agent

import "testing"

func TestConfigSettingsStorePersistsShowInstructionsAtStartup(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())
	if err := SaveConfig(Config{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}

	if err := (configSettingsStore{}).SetShowInstructionsAtStartup(true); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ShowInstructionsAtStartup == nil || !*cfg.ShowInstructionsAtStartup {
		t.Fatal("show_instructions_at_startup was not persisted as enabled")
	}
	if cfg.Theme != "dark" {
		t.Fatalf("unrelated config changed: theme = %q, want dark", cfg.Theme)
	}
}

func TestConfigSettingsStorePersistsTerminalAlerts(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())
	if err := SaveConfig(Config{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}

	if err := (configSettingsStore{}).SetTerminalAlertsEnabled(false); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TerminalAlertsEnabled == nil || *cfg.TerminalAlertsEnabled {
		t.Fatal("terminal_alerts_enabled was not persisted as disabled")
	}
	if cfg.Theme != "dark" {
		t.Fatalf("unrelated config changed: theme = %q, want dark", cfg.Theme)
	}
}

func TestConfigSettingsStorePersistsTerminalTitle(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())
	if err := SaveConfig(Config{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}

	if err := (configSettingsStore{}).SetTerminalTitleEnabled(false); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TerminalTitleEnabled == nil || *cfg.TerminalTitleEnabled {
		t.Fatal("terminal_title_enabled was not persisted as disabled")
	}
	if cfg.Theme != "dark" {
		t.Fatalf("unrelated config changed: theme = %q, want dark", cfg.Theme)
	}
}

func TestConfigSettingsStorePersistsJailByDefault(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())
	if err := SaveConfig(Config{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}

	if err := (configSettingsStore{}).SetJailByDefault(true); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JailByDefault == nil || !*cfg.JailByDefault {
		t.Fatal("jail_by_default was not persisted as enabled")
	}
	if cfg.Theme != "dark" {
		t.Fatalf("unrelated config changed: theme = %q, want dark", cfg.Theme)
	}
}

func TestConfigSettingsStorePersistsAutoCompactThreshold(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())
	if err := SaveConfig(Config{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}

	if err := (configSettingsStore{}).SetAutoCompactThreshold(70); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AutoCompactThreshold == nil || *cfg.AutoCompactThreshold != 70 {
		t.Fatalf("auto_compact_threshold = %v, want 70", cfg.AutoCompactThreshold)
	}
	if cfg.Theme != "dark" {
		t.Fatalf("unrelated config changed: theme = %q, want dark", cfg.Theme)
	}
}

func TestConfigSettingsStorePersistsLSPDefaults(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())
	if err := SaveConfig(Config{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}

	if err := (configSettingsStore{}).SetLSPEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := (configSettingsStore{}).SetSubagentLSPEnabled(false); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LSPEnabled == nil || *cfg.LSPEnabled {
		t.Fatal("lsp_enabled was not persisted as disabled")
	}
	if cfg.SubagentLSPEnabled == nil || *cfg.SubagentLSPEnabled {
		t.Fatal("subagent_lsp_enabled was not persisted as disabled")
	}
	if cfg.Theme != "dark" {
		t.Fatalf("unrelated config changed: theme = %q, want dark", cfg.Theme)
	}
}

func TestConfigLSPEnabledForDefaultsToTrue(t *testing.T) {
	cfg := Config{}
	if !cfg.LSPEnabledFor(false) {
		t.Fatal("main-session LSP should default to enabled")
	}
	if !cfg.LSPEnabledFor(true) {
		t.Fatal("sub-agent LSP should default to enabled")
	}

	noMain, noSub := false, false
	cfg.LSPEnabled = &noMain
	cfg.SubagentLSPEnabled = &noSub
	if cfg.LSPEnabledFor(false) || cfg.LSPEnabledFor(true) {
		t.Fatal("explicitly disabled LSP settings were ignored")
	}
	yesSub := true
	cfg.SubagentLSPEnabled = &yesSub
	write, edit := true, true
	cfg.LSPDiagnosticsOnWrite = &write
	cfg.LSPDiagnosticsOnEdit = &edit
	if cfg.LSPDiagnosticsOnWriteEnabled(false) || cfg.LSPDiagnosticsOnEditEnabled(false) {
		t.Fatal("diagnostics remained enabled with main-session LSP disabled")
	}
	if !cfg.LSPDiagnosticsOnWriteEnabled(true) || !cfg.LSPDiagnosticsOnEditEnabled(true) {
		t.Fatal("sub-agent diagnostics did not follow sub-agent LSP settings")
	}
}

func TestConfigSettingsStorePersistsInheritedTheme(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())
	if err := SaveConfig(Config{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}

	if err := (configSettingsStore{}).SetTheme("inherited"); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme != "inherited" {
		t.Fatalf("theme = %q, want inherited", cfg.Theme)
	}
}
