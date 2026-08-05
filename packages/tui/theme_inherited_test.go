package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestDetectTrueColor(t *testing.T) {
	tests := []struct {
		name      string
		term      string
		colorTerm string
		want      bool
	}{
		{name: "COLORTERM truecolor", term: "screen", colorTerm: "truecolor", want: true},
		{name: "COLORTERM 24bit", term: "screen", colorTerm: "24bit", want: true},
		{name: "direct TERM", term: "xterm-direct", want: true},
		{name: "direct TERM suffix", term: "screen-direct", want: true},
		{name: "256 color TERM", term: "xterm-256color", colorTerm: "", want: false},
		{name: "unknown TERM", term: "dumb", colorTerm: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectTrueColor(tt.term, tt.colorTerm); got != tt.want {
				t.Fatalf("DetectTrueColor(%q, %q) = %v, want %v", tt.term, tt.colorTerm, got, tt.want)
			}
		})
	}
}

func TestParseOSCColorResponse(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		kind int
		slot int
		want TerminalColor
	}{
		{
			name: "foreground hash",
			raw:  "\x1b]10;#123456\x07",
			kind: 10,
			want: ColorRGB(0x12, 0x34, 0x56),
		},
		{
			name: "background rgb",
			raw:  "\x1b]11;rgb:ffff/8000/0000\x1b\\",
			kind: 11,
			want: ColorRGB(255, 127, 0),
		},
		{
			name: "palette slot",
			raw:  "\x1b]4;12;rgb:12/34/56\x07",
			kind: 4,
			slot: 12,
			want: ColorRGB(0x12, 0x34, 0x56),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseOSCColorResponse([]byte(tt.raw))
			if !ok {
				t.Fatal("parseOSCColorResponse returned !ok")
			}
			if got.kind != tt.kind || got.slot != tt.slot || got.color != tt.want {
				t.Fatalf("response = %#v, want kind=%d slot=%d color=%#v", got, tt.kind, tt.slot, tt.want)
			}
		})
	}
}

func TestANSIThemeColorsAdaptToTargetSGR(t *testing.T) {
	if got := Dark.FGColor(ColorANSI(100), "x"); got != "\x1b[90mx\x1b[0m" {
		t.Fatalf("ANSI foreground = %q, want bright-black foreground", got)
	}
	if got := Dark.BG(ColorANSI(31), "x"); got != "\x1b[41mx\x1b[0m" {
		t.Fatalf("ANSI background = %q, want red background", got)
	}
}

func TestNearestXtermColorCacheIsStable(t *testing.T) {
	colors := [][3]int{
		{0, 0, 0},
		{66, 69, 75},
		{200, 100, 50},
		{-20, 300, 128},
	}
	for _, color := range colors {
		first := nearestXtermColor(color[0], color[1], color[2])
		second := nearestXtermColor(color[0], color[1], color[2])
		if first != second {
			t.Fatalf("nearest color changed for %#v: first=%d second=%d", color, first, second)
		}
		if first < 0 || first > 255 {
			t.Fatalf("nearest color out of range for %#v: %d", color, first)
		}
	}
}

func inheritedTestTheme(trueColor bool) Theme {
	detected := Dark
	detected.Terminal = TerminalProfile{
		Foreground:    ColorRGB(220, 210, 200),
		Background:    ColorRGB(12, 18, 24),
		HasForeground: true,
		HasBackground: true,
		TrueColor:     trueColor,
	}
	detected.Terminal.Palette[10] = ColorRGB(20, 220, 80)
	detected.Terminal.Palette[12] = ColorRGB(40, 100, 240)
	detected.Terminal.Palette[13] = ColorRGB(220, 70, 210)
	detected.Terminal.PaletteKnown = 1<<10 | 1<<12 | 1<<13
	return InheritedTheme(detected)
}

func TestInheritedThemeUsesTrueColorAndTerminalPalette(t *testing.T) {
	th := inheritedTestTheme(true)
	if !th.Inherited || !th.Terminal.TrueColor {
		t.Fatalf("inherited theme mode = (inherited=%v, trueColor=%v), want truecolor", th.Inherited, th.Terminal.TrueColor)
	}
	if got := th.FG256(th.Accent, "x"); !strings.Contains(got, "\x1b[38;2;40;100;240m") {
		t.Fatalf("accent did not use the reported terminal palette: %q", got)
	}
	if got := th.FG256(th.FG, "x"); !strings.Contains(got, "\x1b[38;2;220;210;200m") {
		t.Fatalf("foreground did not inherit the terminal default: %q", got)
	}
	if got := th.UserBubble("x", 3); !strings.Contains(got, "\x1b[48;2;") {
		t.Fatalf("user bubble did not use truecolor background: %q", got)
	}
	if got := strings.Join(th.HighlightCode("func main() {}", "go"), "\n"); !strings.Contains(got, "\x1b[38;2;") {
		t.Fatalf("syntax highlighting did not use truecolor: %q", got)
	}
	if got := th.DimColor(th.FG, 45); got.Mode != terminalColorRGB {
		t.Fatalf("DimColor mode = %v, want RGB", got.Mode)
	}
}

func TestInheritedThemeFallsBackTo256Colors(t *testing.T) {
	th := inheritedTestTheme(false)
	if !th.Inherited || th.Terminal.TrueColor {
		t.Fatalf("inherited theme mode = (inherited=%v, trueColor=%v), want 256", th.Inherited, th.Terminal.TrueColor)
	}
	if got := th.FG256(th.Accent, "x"); !strings.Contains(got, "\x1b[38;5;12m") || strings.Contains(got, "\x1b[38;2;") {
		t.Fatalf("accent did not use 256-color fallback: %q", got)
	}
	if got := th.UserBubble("x", 3); strings.Contains(got, "\x1b[48;2;") || !strings.Contains(got, "\x1b[48;5;") {
		t.Fatalf("user bubble did not quantize its background: %q", got)
	}
	if got := th.DimColor(th.FG, 45); got.Mode != terminalColor256 {
		t.Fatalf("DimColor mode = %v, want 256", got.Mode)
	}
	if got := strings.Join(th.HighlightCode("func main() {}", "go"), "\n"); strings.Contains(got, "\x1b[38;2;") {
		t.Fatalf("syntax highlighting unexpectedly used truecolor: %q", got)
	}
}

func TestExplicitThemesFollowTerminalCapability(t *testing.T) {
	if got, want := Dark.FG256(Dark.Accent, "x"), "\x1b[38;5;111mx\x1b[0m"; got != want {
		t.Fatalf("explicit theme output = %q, want %q", got, want)
	}
	if got := Dark.UserBubble("x", 1); !strings.Contains(got, "\x1b[48;5;") || strings.Contains(got, "\x1b[48;2;") {
		t.Fatalf("non-truecolor RGB bubble did not quantize: %q", got)
	}

	trueColor := withTerminalProfile(Dark, Theme{Terminal: TerminalProfile{TrueColor: true}})
	if got := trueColor.FG256(trueColor.Accent, "x"); !strings.Contains(got, "\x1b[38;2;") {
		t.Fatalf("truecolor explicit theme did not emit RGB: %q", got)
	}
	if got := trueColor.UserBubble("x", 1); !strings.Contains(got, "\x1b[48;2;66;69;75m") {
		t.Fatalf("truecolor explicit RGB bubble did not emit RGB: %q", got)
	}
}

func TestAvailableThemesIncludesInherited(t *testing.T) {
	options := AvailableThemes(t.TempDir())
	for _, option := range options {
		if option.Value == "inherited" {
			if option.Label != "inherited (from terminal)" || !option.Builtin {
				t.Fatalf("unexpected inherited option: %#v", option)
			}
			return
		}
	}
	t.Fatal("available themes did not include inherited")
}

func TestIsLightThemePreservesExplicitDarkTheme(t *testing.T) {
	dark := Dark
	dark.Terminal.SchemeKnown = true
	dark.Terminal.Light = true
	if IsLightTheme(dark) {
		t.Fatal("explicit dark theme was reclassified from the terminal scheme")
	}

	inherited := InheritedTheme(dark)
	if !IsLightTheme(inherited) {
		t.Fatal("inherited light terminal theme was not recognized as light")
	}
}

func TestRendererDetectsInheritedSelectionBackground(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)
	r.Resize(20, 2)
	r.SetTheme(inheritedTestTheme(true))
	selected := r.theme.SelectionStyle() + "selected" + reset
	r.Draw([]string{"", selected}, -1, -1)
	buf.Reset()

	r.Draw([]string{"", "plain"}, -1, -1)
	if !strings.Contains(buf.String(), MoveTo(1, 1)) {
		t.Fatalf("selection repaint did not include unchanged rows: %q", buf.String())
	}
}
