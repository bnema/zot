package modes

import "testing"

func TestStartupInstructionsAreOptIn(t *testing.T) {
	paths := []string{"/repo/AGENTS.md"}

	disabled := NewInteractive(InteractiveConfig{StartupContextPaths: paths})
	disabled.rend = nil
	if len(disabled.view.StartupContextPaths) != 0 {
		t.Fatalf("default startup rendered %d instruction paths", len(disabled.view.StartupContextPaths))
	}
	disabled.applySettingToggle("show_instructions_at_startup", true)
	if len(disabled.view.StartupContextPaths) != 1 {
		t.Fatalf("live toggle rendered %d instruction paths, want 1", len(disabled.view.StartupContextPaths))
	}
	disabled.applySettingToggle("show_instructions_at_startup", false)
	if len(disabled.view.StartupContextPaths) != 0 {
		t.Fatalf("disabled live toggle left %d instruction paths", len(disabled.view.StartupContextPaths))
	}

	enabledValue := true
	enabled := NewInteractive(InteractiveConfig{
		StartupContextPaths:       paths,
		ShowInstructionsAtStartup: &enabledValue,
	})
	if len(enabled.view.StartupContextPaths) != 1 {
		t.Fatalf("enabled startup rendered %d instruction paths, want 1", len(enabled.view.StartupContextPaths))
	}
}
