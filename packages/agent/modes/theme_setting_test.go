package modes

import (
	"testing"

	"github.com/patriceckhart/zot/packages/tui"
)

func TestSettingsDialogOffersInheritedTheme(t *testing.T) {
	interactive := NewInteractive(InteractiveConfig{})
	interactive.openSettingsDialog()

	for _, item := range interactive.settingsDialog.items {
		if item.key != "theme" {
			continue
		}
		for _, option := range item.options {
			if option.value == "inherited" {
				if option.label != "inherited (from terminal)" {
					t.Fatalf("inherited label = %q, want inherited (from terminal)", option.label)
				}
				return
			}
		}
		t.Fatal("theme picker did not include inherited")
	}
	t.Fatal("settings dialog did not include theme picker")
}

func TestApplyingInheritedThemeUsesCapturedTerminalProfile(t *testing.T) {
	detected := tui.Dark
	detected.Terminal = tui.TerminalProfile{
		Foreground:    tui.ColorRGB(220, 220, 220),
		Background:    tui.ColorRGB(10, 10, 10),
		HasForeground: true,
		HasBackground: true,
		TrueColor:     true,
	}
	interactive := NewInteractive(InteractiveConfig{Theme: detected, ThemeName: "auto"})
	interactive.rend = nil
	interactive.applyThemeNow("inherited")

	if !interactive.cfg.Theme.Inherited {
		t.Fatal("applying inherited theme did not switch the live theme")
	}
	if !interactive.cfg.Theme.Terminal.TrueColor {
		t.Fatal("live inherited color mode = 256, want truecolor")
	}
	if !interactive.cfg.Theme.Terminal.HasForeground || !interactive.cfg.Theme.Terminal.HasBackground {
		t.Fatal("live inherited theme lost the captured terminal profile")
	}
}
