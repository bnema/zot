package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeThemeFile(t *testing.T, name, body string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "themes"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "themes", name+".json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestLoadThemeAllowsPartialColorOverrides(t *testing.T) {
	home := writeThemeFile(t, "partial", `{"colors":{"dark":{"accent":204}}}`)

	th, name, err := LoadThemeFromHome(home, "partial", Dark)
	if err != nil {
		t.Fatal(err)
	}
	if name != "partial" {
		t.Fatalf("name = %q, want partial", name)
	}
	if th.Accent != Color256(204) {
		t.Fatalf("accent = %#v, want 204", th.Accent)
	}
	if th.FG != Dark.FG {
		t.Fatalf("fg = %#v, want inherited %#v", th.FG, Dark.FG)
	}
	if len(th.SpinnerFrames) == 0 {
		t.Fatal("spinner frames should be inherited")
	}
}

func TestLoadThemeAllowsThinkingMaxOverride(t *testing.T) {
	home := writeThemeFile(t, "thinking", `{"colors":{"dark":{"thinkingMax":201}}}`)

	th, _, err := LoadThemeFromHome(home, "thinking", Dark)
	if err != nil {
		t.Fatal(err)
	}
	if th.ThinkingMax != Color256(201) {
		t.Fatalf("thinking max = %#v, want 201", th.ThinkingMax)
	}
}

func TestLoadThemeAllowsSpinnerAppearanceOverrides(t *testing.T) {
	home := writeThemeFile(t, "spinner", `{"spinner_frames":[".","o"],"spinner_messages":["working"],"spinner_interval_ms":200}`)

	th, _, err := LoadThemeFromHome(home, "spinner", Dark)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(th.SpinnerFrames); got != 2 {
		t.Fatalf("spinner frame count = %d, want 2", got)
	}
	if th.SpinnerFrames[1] != "o" || th.SpinnerIntervalMS != 200 {
		t.Fatalf("spinner appearance overrides not applied: %#v %d", th.SpinnerFrames, th.SpinnerIntervalMS)
	}
	if th.Accent != Dark.Accent {
		t.Fatalf("accent = %#v, want inherited %#v", th.Accent, Dark.Accent)
	}
}

func TestLoadThemeFallsBackToDarkWhenLightModeMissing(t *testing.T) {
	home := writeThemeFile(t, "darkonly", `{"colors":{"dark":{"spinner_frames":["◢","◣","◤","◥"],"spinner_messages":["working"],"spinner_interval_ms":120}}}`)

	th, _, err := LoadThemeFromHome(home, "darkonly", Light)
	if err != nil {
		t.Fatal(err)
	}
	if len(th.SpinnerFrames) != 4 || th.SpinnerFrames[0] != "◢" {
		t.Fatalf("spinner frames = %#v, want dark fallback frames", th.SpinnerFrames)
	}
	if th.SpinnerIntervalMS != 120 {
		t.Fatalf("spinner interval = %d, want 120", th.SpinnerIntervalMS)
	}
	if th.FG != Light.FG {
		t.Fatalf("fg = %#v, want inherited light fg %#v", th.FG, Light.FG)
	}
}

func TestLoadThemeIgnoresLegacySpinnerMessages(t *testing.T) {
	home := writeThemeFile(t, "shared", `{"colors":{"accent":204,"spinner_messages":["ship"]}}`)

	th, _, err := LoadThemeFromHome(home, "shared", Light)
	if err != nil {
		t.Fatal(err)
	}
	if th.Accent != Color256(204) {
		t.Fatalf("accent = %#v, want 204", th.Accent)
	}
	if len(th.SpinnerFrames) != len(Light.SpinnerFrames) || th.SpinnerIntervalMS != Light.SpinnerIntervalMS {
		t.Fatalf("legacy spinner_messages changed spinner appearance: %#v %d", th.SpinnerFrames, th.SpinnerIntervalMS)
	}
	if th.FG != Light.FG {
		t.Fatalf("fg = %#v, want inherited %#v", th.FG, Light.FG)
	}
}

func TestLoadThemeAcceptsRGBSemanticColors(t *testing.T) {
	home := writeThemeFile(t, "rgb", `{"colors":{"dark":{"fg":"#123456","muted":{"mode":"rgb","r":18,"g":52,"b":86},"accent":{"mode":"rgb","r":200,"g":100,"b":50},"user": {"mode":"ansi","index":31},"selection_bg":{"mode":"256","index":237},"selection_fg":250}}}`)

	detected := Dark
	detected.Terminal.TrueColor = true
	th, _, err := LoadThemeFromHome(home, "rgb", detected)
	if err != nil {
		t.Fatal(err)
	}
	if !th.Terminal.TrueColor {
		t.Fatal("terminal color mode = 256, want truecolor")
	}
	if th.FG != ColorRGB(0x12, 0x34, 0x56) {
		t.Fatalf("foreground = %#v, want RGB", th.FG)
	}
	if th.Accent != ColorRGB(200, 100, 50) {
		t.Fatalf("accent = %#v, want RGB", th.Accent)
	}
	if th.SelectionBG != Color256(237) || th.SelectionFG != Color256(250) {
		t.Fatalf("indexed semantic colors were not preserved: bg=%#v fg=%#v", th.SelectionBG, th.SelectionFG)
	}
	if got := th.FGColor(th.Accent, "x"); !strings.Contains(got, "\x1b[38;2;200;100;50m") {
		t.Fatalf("RGB accent was not rendered as truecolor: %q", got)
	}
}

func TestLoadThemeQuantizesRGBSemanticColorsWithoutTrueColor(t *testing.T) {
	home := writeThemeFile(t, "rgb-fallback", `{"colors":{"dark":{"accent":"#c86432"}}}`)

	th, _, err := LoadThemeFromHome(home, "rgb-fallback", Dark)
	if err != nil {
		t.Fatal(err)
	}
	want := nearestXtermColor(200, 100, 50)
	if got := th.FGColor(th.Accent, "x"); got != sgrFG(want)+"x\x1b[0m" {
		t.Fatalf("RGB accent fallback = %q, want xterm-256 index %d", got, want)
	}
}
