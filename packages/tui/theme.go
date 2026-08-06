// Package tui is a small terminal UI: raw-mode input, differential
// line renderer, multi-line editor, and chat view. No external TUI
// framework; just ANSI escape codes.
package tui

import (
	"strings"
	"sync/atomic"
)

// TerminalColor describes a theme colour in one of the colour spaces
// terminals commonly support. Every semantic theme colour uses this type;
// xterm-256, ANSI, and RGB values are rendered according to terminal capability.
type TerminalColor struct {
	Mode  terminalColorMode
	Index int
	R     int
	G     int
	B     int
}

type terminalColorMode int

const (
	terminalColor256 terminalColorMode = iota
	terminalColorANSI
	terminalColorRGB
)

func Color256(index int) TerminalColor { return TerminalColor{Mode: terminalColor256, Index: index} }
func ColorANSI(sgr int) TerminalColor  { return TerminalColor{Mode: terminalColorANSI, Index: sgr} }
func ColorRGB(r, g, b int) TerminalColor {
	return TerminalColor{Mode: terminalColorRGB, R: r, G: g, B: b}
}

// TerminalProfile is the best-effort color snapshot reported by the
// controlling terminal. The ANSI palette is optional; unknown slots are
// resolved through the classic xterm-256 palette.
type TerminalProfile struct {
	Foreground TerminalColor
	Background TerminalColor

	HasForeground bool
	HasBackground bool

	Palette      [16]TerminalColor
	PaletteKnown uint16
	TrueColor    bool
	Light        bool
	SchemeKnown  bool
}

// PaletteColor returns a reported ANSI palette entry when available.
func (p TerminalProfile) PaletteColor(index int) (TerminalColor, bool) {
	if index < 0 || index >= len(p.Palette) || p.PaletteKnown&(uint16(1)<<index) == 0 {
		return TerminalColor{}, false
	}
	return p.Palette[index], true
}

// Semantic palette used by zut. Each role is a TerminalColor so themes can
// use indexed, ANSI, or RGB values without changing render code.
type Theme struct {
	FG           TerminalColor
	Muted        TerminalColor
	Accent       TerminalColor
	Background   *TerminalColor // optional full-row TUI background; nil keeps terminal default
	User         TerminalColor  // label color for the user role
	UserBubbleBG TerminalColor  // background tint behind user message rows
	UserBubbleFG TerminalColor  // foreground colour for user message rows
	Assistant    TerminalColor  // label color for the zut role
	Tool         TerminalColor
	ToolOut      TerminalColor
	Error        TerminalColor
	Warning      TerminalColor
	Spinner      TerminalColor // spinner + funny working line
	ThinkingMax  TerminalColor // status color for the opt-in max reasoning level
	SelectionBG  TerminalColor // background for highlighted rows
	SelectionFG  TerminalColor // foreground for highlighted rows

	// Inherited selects terminal-owned colors. Terminal.TrueColor controls
	// whether fixed indexed values are emitted as xterm-256 or truecolor and
	// whether RGB values are quantized for terminals without direct-color
	// support.
	Inherited bool
	Terminal  TerminalProfile

	SpinnerFrames     []string
	SpinnerMessages   []string
	SpinnerIntervalMS int

	SyntaxBaseStyle string
	Syntax          SyntaxTheme
}

// SyntaxTheme contains the chroma token colors used by code fences,
// file previews, and diffs. Values are chroma style entries, so they
// may include attributes after the color (for example "#81a1c1 bold").
type SyntaxTheme struct {
	Keyword             string
	KeywordConstant     string
	KeywordDeclaration  string
	KeywordNamespace    string
	KeywordReserved     string
	KeywordType         string
	NameBuiltin         string
	NameFunction        string
	NameClass           string
	NameDecorator       string
	LiteralString       string
	LiteralStringEscape string
	LiteralNumber       string
	Comment             string
	CommentPreproc      string
	Operator            string
	Punctuation         string
	Text                string
}

var nordSyntax = SyntaxTheme{
	Keyword:             "#81a1c1 bold",
	KeywordConstant:     "#81a1c1",
	KeywordDeclaration:  "#81a1c1",
	KeywordNamespace:    "#81a1c1",
	KeywordReserved:     "#81a1c1 bold",
	KeywordType:         "#88c0d0",
	NameBuiltin:         "#88c0d0",
	NameFunction:        "#8fbcbb",
	NameClass:           "#a3be8c bold",
	NameDecorator:       "#b48ead",
	LiteralString:       "#a3be8c",
	LiteralStringEscape: "#bf616a",
	LiteralNumber:       "#d08770",
	Comment:             "#616e88 italic",
	CommentPreproc:      "#b48ead",
	Operator:            "#eceff4",
	Punctuation:         "#d8dee9",
	Text:                "#e5e9f0",
}

var defaultSpinnerFrames = []string{
	"⠋",
	"⠙",
	"⠚",
	"⠞",
	"⠖",
	"⠦",
	"⠴",
	"⠲",
	"⠳",
	"⠓",
}

var defaultSpinnerMessages = []string{
	"thinking",
	"reticulating splines",
	"bribing the tokenizer",
	"asking the rubber duck",
	"summoning daemons",
	"consulting the oracle",
	"herding tokens",
	"compiling excuses",
	"poking the model",
	"negotiating with rate limits",
	"picking a fight with syntax",
	"reading between the bits",
	"tasting the semicolons",
	"pretending to understand the code",
	"petting the cache",
	"drafting clever replies",
	"warming up the GPU choir",
	"arguing with a stack trace",
	"googling the answer (not really)",
	"rewriting history",
	"every draft is a stone in the work",
	"bringing order to the unhewn",
	"finding the load-bearing measure",
	"where clarity grows, work grows lighter",
	"every correction serves the work",
}

var Dark = Theme{
	FG:                Color256(253),
	Muted:             Color256(244),
	Accent:            Color256(111),        // soft blue
	User:              Color256(180),        // warm tan (unused now that the speaker label is gone, kept for skin compat)
	UserBubbleBG:      ColorRGB(66, 69, 75), // #42454B
	UserBubbleFG:      Color256(248),        // slightly lighter grey for readability on #42454B
	Assistant:         Color256(117),        // bright cyan — the zut label color
	Tool:              Color256(114),        // green
	ToolOut:           Color256(245),
	Error:             Color256(203),
	Warning:           Color256(214),
	Spinner:           Color256(183), // soft purple
	ThinkingMax:       Color256(207), // vivid magenta
	SelectionBG:       Color256(24),  // deep blue background
	SelectionFG:       Color256(231), // near-white foreground
	SpinnerFrames:     defaultSpinnerFrames,
	SpinnerMessages:   defaultSpinnerMessages,
	SpinnerIntervalMS: 80,
	SyntaxBaseStyle:   "monokai",
	Syntax:            nordSyntax,
}

var Light = Theme{
	FG:                Color256(236),
	Muted:             Color256(244),
	Accent:            Color256(33),
	User:              Color256(94),
	UserBubbleBG:      Color256(254), // very pale grey panel behind user rows on light theme
	UserBubbleFG:      Color256(240), // dark grey text, legible on the pale panel
	Assistant:         Color256(31),  // deep cyan
	Tool:              Color256(28),
	ToolOut:           Color256(240),
	Error:             Color256(160),
	Warning:           Color256(166),
	Spinner:           Color256(91),  // purple
	ThinkingMax:       Color256(127), // deep magenta
	SelectionBG:       Color256(153), // light blue
	SelectionFG:       Color256(232), // near-black
	SpinnerFrames:     defaultSpinnerFrames,
	SpinnerMessages:   defaultSpinnerMessages,
	SpinnerIntervalMS: 80,
	SyntaxBaseStyle:   "monokai",
	Syntax:            nordSyntax,
}

// FGColor wraps s in a foreground color. The name is intentionally explicit
// because the color may be xterm-256, ANSI, or RGB.
func (t Theme) FGColor(c TerminalColor, s string) string {
	return t.fgPrefix(c) + s + reset
}

// FG256 retains the historical helper name while accepting the full
// TerminalColor model.
//
// Deprecated: use FGColor.
func (t Theme) FG256(c TerminalColor, s string) string {
	return t.FGColor(c, s)
}

// BG256 retains the historical helper name while accepting the full
// TerminalColor model.
//
// Deprecated: use BG.
func (t Theme) BG256(c TerminalColor, s string) string {
	return t.bgPrefix(c) + s + reset
}

// BG wraps s in a terminal background color. RGB values are quantized when
// the active terminal does not advertise truecolor support.
func (t Theme) BG(c TerminalColor, s string) string {
	return t.bgPrefix(c) + s + reset
}

func (t Theme) fgPrefix(color TerminalColor) string {
	return sgrFGColor(t.resolveColor(color))
}

func (t Theme) bgPrefix(color TerminalColor) string {
	return sgrBGColor(t.resolveColor(color))
}

// DimColor fades a theme color toward the terminal background. It is used
// by inherited theme construction and is also available to callers that need
// terminal-aware dimming for overlays.
func (t Theme) DimColor(color TerminalColor, percent int) TerminalColor {
	percent = clampPercent(percent)
	foreground := t.resolveColor(color)
	background := t.Terminal.Background
	if !t.Terminal.HasBackground {
		background = Color256(0)
	}
	fg, _ := rgbForTerminalColor(foreground)
	bg, _ := rgbForTerminalColor(background)
	return t.resolveColor(ColorRGB(
		blendChannel(fg[0], bg[0], percent),
		blendChannel(fg[1], bg[1], percent),
		blendChannel(fg[2], bg[2], percent),
	))
}

func (t Theme) resolveColor(color TerminalColor) TerminalColor {
	switch color.Mode {
	case terminalColor256:
		index := clampXtermIndex(color.Index)
		if t.Terminal.TrueColor {
			if t.Inherited {
				if palette, ok := t.Terminal.PaletteColor(index); ok {
					return palette
				}
			}
			r, g, b := xterm256RGB(index)
			return ColorRGB(r, g, b)
		}
		return Color256(index)
	case terminalColorANSI:
		if t.Inherited && t.Terminal.TrueColor {
			if index, ok := ansiSGRToXtermIndex(color.Index); ok {
				if palette, ok := t.Terminal.PaletteColor(index); ok {
					return palette
				}
				r, g, b := xterm256RGB(index)
				return ColorRGB(r, g, b)
			}
		}
	case terminalColorRGB:
		if !t.Terminal.TrueColor {
			return Color256(nearestXtermColor(color.R, color.G, color.B))
		}
	}
	return color
}

func sgrFGColor(color TerminalColor) string {
	switch color.Mode {
	case terminalColorANSI:
		return "\x1b[" + itoa(ansiForegroundSGR(color.Index)) + "m"
	case terminalColorRGB:
		return "\x1b[38;2;" + itoa(clampByte(color.R)) + ";" + itoa(clampByte(color.G)) + ";" + itoa(clampByte(color.B)) + "m"
	default:
		return sgrFG(clampXtermIndex(color.Index))
	}
}

func (t Theme) sgrBGColor(color TerminalColor) string {
	return sgrBGColor(t.resolveColor(color))
}

// AccentBar returns a 2-cell-wide leader: a coloured half-block
// glyph followed by a plain space gutter. Used as the speaker-label
// prefix in the chat ("▌ you", "▌ zut") and as the editor prompt so
// the bar reads consistently across the UI.
func (t Theme) AccentBar(c TerminalColor) string {
	return t.FGColor(c, "▌ ")
}

// Highlight paints s with the theme's selection colors (foreground +
// background). The caller is responsible for padding s to the desired
// width; styling alone does not extend the background past content.
func (t Theme) Highlight(s string) string {
	return t.SelectionStyle() + s + reset
}

// SelectionStyle returns the SGR prefix for the theme's selected row.
func (t Theme) SelectionStyle() string {
	return t.fgPrefix(t.SelectionFG) + t.bgPrefix(t.SelectionBG)
}

// BackgroundStyle returns the SGR prefix for the optional full-row
// TUI background. Empty means zut should leave the terminal's
// configured background untouched.
func (t Theme) BackgroundStyle() string {
	if t.Background == nil {
		return ""
	}
	return t.bgPrefix(*t.Background)
}

// SelectionStyleFG returns the SGR prefix for selected-row text with
// a custom foreground. Useful for preserving semantic marks inside a
// highlighted row without hardcoding escape sequences outside theme.go.
func (t Theme) SelectionStyleFG(fg TerminalColor) string {
	return t.fgPrefix(fg) + t.bgPrefix(t.SelectionBG)
}

// PadHighlight styles s and extends the selection background to the
// full terminal width so the highlight is a full row, not just a
// rectangle around the text.
func (t Theme) PadHighlight(s string, width int) string {
	visible := visibleWidth(s)
	if visible < width {
		s += strings.Repeat(" ", width-visible)
	}
	return t.SelectionStyle() + s + reset
}

// UserBubble paints a single user message row with the bubble
// background colour, padding to width so the tint extends to the
// full terminal width. Foreground stays in UserBubbleFG so text
// remains legible against the tint.
func (t Theme) UserBubble(s string, width int) string {
	visible := visibleWidth(s)
	if visible < width {
		s += strings.Repeat(" ", width-visible)
	}
	return t.fgPrefix(t.UserBubbleFG) + t.bgPrefix(t.UserBubbleBG) + s + reset
}

// UserBubbleRow renders one user-bubble row prefixed with a coloured
// half-block accent bar ("▌ ") so every line of the bubble has the
// zut-blue gutter at the very left. The bar lives outside the bubble
// tint (chat bg) so the bubble itself sits inside it. Width is the
// outer width including the bar; the bubble content is padded to
// width-2 (the bar + its trailing space).
func (t Theme) UserBubbleRow(content string, width int) string {
	// Bar plus a single space gutter, in the assistant accent colour
	// so it matches the tool-box / app accent and reads as zut's voice
	// marker. Two cells wide.
	bar := t.FGColor(t.Assistant, "▌ ")
	bubbleW := width - 2
	if bubbleW < 1 {
		bubbleW = 1
	}
	return bar + t.UserBubble(content, bubbleW)
}

// Bold wraps s in bold SGR.
func Bold(s string) string { return "\x1b[1m" + s + "\x1b[22m" }

// Dim wraps s in dim SGR. Re-apply dim after each full SGR reset so
// independently styled segments remain dimmed too.
func Dim(s string) string {
	const dim = "\x1b[2m"
	return dim + strings.ReplaceAll(s, reset, reset+dim) + reset
}

// DimLines returns a dimmed copy of lines. It is suitable for content behind
// a modal layer: the foreground layer can retain its normal styling while
// every styled segment in the background stays dimmed.
func DimLines(lines []string) []string {
	out := make([]string, len(lines))
	for idx, line := range lines {
		out[idx] = Dim(line)
	}
	return out
}

// Italic wraps s in italic SGR.
func Italic(s string) string { return "\x1b[3m" + s + "\x1b[23m" }

const reset = "\x1b[0m"

func sgrFG(c int) string { return "\x1b[38;5;" + itoa(clampXtermIndex(c)) + "m" }
func sgrBG(c int) string { return "\x1b[48;5;" + itoa(clampXtermIndex(c)) + "m" }
func sgrBGColor(c TerminalColor) string {
	switch c.Mode {
	case terminalColorANSI:
		return "\x1b[" + itoa(ansiBackgroundSGR(c.Index)) + "m"
	case terminalColorRGB:
		return "\x1b[48;2;" + itoa(clampByte(c.R)) + ";" + itoa(clampByte(c.G)) + ";" + itoa(clampByte(c.B)) + "m"
	default:
		return sgrBG(c.Index)
	}
}

func clampXtermIndex(index int) int {
	if index < 0 {
		return 0
	}
	if index > 255 {
		return 255
	}
	return index
}

func clampByte(value int) int {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return value
}

func clampPercent(percent int) int {
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func rgbForTerminalColor(color TerminalColor) ([3]int, bool) {
	switch color.Mode {
	case terminalColorRGB:
		return [3]int{clampByte(color.R), clampByte(color.G), clampByte(color.B)}, true
	case terminalColor256:
		r, g, b := xterm256RGB(clampXtermIndex(color.Index))
		return [3]int{r, g, b}, true
	case terminalColorANSI:
		if index, ok := ansiSGRToXtermIndex(color.Index); ok {
			r, g, b := xterm256RGB(index)
			return [3]int{r, g, b}, true
		}
	}
	return [3]int{}, false
}

func ansiSGRToXtermIndex(sgr int) (int, bool) {
	switch {
	case sgr >= 30 && sgr <= 37:
		return sgr - 30, true
	case sgr >= 90 && sgr <= 97:
		return sgr - 90 + 8, true
	case sgr >= 40 && sgr <= 47:
		return sgr - 40, true
	case sgr >= 100 && sgr <= 107:
		return sgr - 100 + 8, true
	default:
		return 0, false
	}
}

func ansiForegroundSGR(sgr int) int {
	switch {
	case sgr >= 40 && sgr <= 47:
		return sgr - 10
	case sgr >= 100 && sgr <= 107:
		return sgr - 10
	default:
		return sgr
	}
}

func ansiBackgroundSGR(sgr int) int {
	switch {
	case sgr >= 30 && sgr <= 37:
		return sgr + 10
	case sgr >= 90 && sgr <= 97:
		return sgr + 10
	default:
		return sgr
	}
}

func blendChannel(from, to, percent int) int {
	return clampByte((from*(100-percent) + to*percent + 50) / 100)
}

const xtermColorCacheSize = 256

type xtermColorCacheEntry struct {
	value atomic.Uint64
}

var xtermColorCache [xtermColorCacheSize]xtermColorCacheEntry

func nearestXtermColor(r, g, b int) int {
	r = clampByte(r)
	g = clampByte(g)
	b = clampByte(b)
	key := uint64(r)<<16 | uint64(g)<<8 | uint64(b)
	entry := &xtermColorCache[(key*2654435761)&(xtermColorCacheSize-1)]
	cached := entry.value.Load()
	keyPrefix := (key + 1) << 8
	if cached&^uint64(0xff) == keyPrefix {
		return int(cached & 0xff)
	}

	best := 0
	bestDistance := int(^uint(0) >> 1)
	for index := 0; index < 256; index++ {
		cr, cg, cb := xterm256RGB(index)
		dr := r - cr
		dg := g - cg
		db := b - cb
		distance := dr*dr + dg*dg + db*db
		if distance < bestDistance {
			best = index
			bestDistance = distance
		}
	}
	entry.value.Store(keyPrefix | uint64(best))
	return best
}

// small itoa to avoid pulling strconv into this hot path twice.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [4]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
