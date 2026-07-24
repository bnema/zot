package modes

import "testing"

func TestStartupResourcesAreOptIn(t *testing.T) {
	cfg := InteractiveConfig{
		StartupContextPaths:   []string{"/repo/AGENTS.md"},
		StartupExtensionNames: []string{"workspaces"},
		StartupSkillNames:     []string{"review"},
	}

	disabled := NewInteractive(cfg)
	disabled.rend = nil
	assertStartupResourceCounts(t, disabled, 0)

	disabled.applySettingToggle("show_instructions_at_startup", true)
	assertStartupResourceCounts(t, disabled, 1)

	disabled.applySettingToggle("show_instructions_at_startup", false)
	assertStartupResourceCounts(t, disabled, 0)

	enabledValue := true
	cfg.ShowInstructionsAtStartup = &enabledValue
	enabled := NewInteractive(cfg)
	assertStartupResourceCounts(t, enabled, 1)
}

func assertStartupResourceCounts(t *testing.T, i *Interactive, want int) {
	t.Helper()
	if got := len(i.view.StartupContextPaths); got != want {
		t.Fatalf("startup context count = %d, want %d", got, want)
	}
	if got := len(i.view.StartupExtensionNames); got != want {
		t.Fatalf("startup extension count = %d, want %d", got, want)
	}
	if got := len(i.view.StartupSkillNames); got != want {
		t.Fatalf("startup skill count = %d, want %d", got, want)
	}
}
