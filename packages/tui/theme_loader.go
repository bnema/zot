package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ThemeOption is one selectable theme discovered under $ZOT_HOME/themes.
// Value is stored in config.json.
type ThemeOption struct {
	Value       string
	Label       string
	Description string
	Path        string
	Builtin     bool
}

// ThemeFile is the user-editable JSON shape loaded from
// $ZOT_HOME/themes/*.json. It carries metadata plus separate overrides
// for dark and light terminals.
type ThemeFile struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Colors      ThemeFileColorModes `json:"colors"`
	Overrides   ThemeOverrides      `json:"-"`
}

func (tf *ThemeFile) UnmarshalJSON(data []byte) error {
	type alias ThemeFile
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	// Allow a tiny theme file with overrides at the top level, e.g.
	// {"spinner_frames":[".","o"],"spinner_messages":["working"]}.
	// Metadata fields are ignored by ThemeOverrides because they do not
	// have matching json tags.
	_ = json.Unmarshal(data, &a.Overrides)
	*tf = ThemeFile(a)
	return nil
}

type ThemeFileColorModes struct {
	Base     ThemeOverrides `json:"-"`
	Dark     ThemeOverrides `json:"dark"`
	Light    ThemeOverrides `json:"light"`
	HasDark  bool           `json:"-"`
	HasLight bool           `json:"-"`
}

func (m *ThemeFileColorModes) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v, ok := raw["dark"]; ok {
		m.HasDark = true
		_ = json.Unmarshal(v, &m.Dark)
	}
	if v, ok := raw["light"]; ok {
		m.HasLight = true
		_ = json.Unmarshal(v, &m.Light)
	}
	// Also allow colors to directly contain overrides shared by both
	// modes, e.g. {"colors":{"accent":204}}.
	var base ThemeOverrides
	_ = json.Unmarshal(data, &base)
	m.Base = base
	return nil
}

// ThemeOverrides is intentionally pointer-based so a theme file can
// override only the colors it cares about and inherit the built-in
// dark/light defaults for everything else.
type ThemeOverrides struct {
	FG                *TerminalColorValue  `json:"fg,omitempty"`
	Muted             *TerminalColorValue  `json:"muted,omitempty"`
	Accent            *TerminalColorValue  `json:"accent,omitempty"`
	Background        *TerminalColorValue  `json:"background,omitempty"`
	User              *TerminalColorValue  `json:"user,omitempty"`
	UserBubbleBG      *TerminalColorValue  `json:"user_bubble_bg,omitempty"`
	UserBubbleFG      *TerminalColorValue  `json:"user_bubble_fg,omitempty"`
	Assistant         *TerminalColorValue  `json:"assistant,omitempty"`
	Tool              *TerminalColorValue  `json:"tool,omitempty"`
	ToolOut           *TerminalColorValue  `json:"tool_out,omitempty"`
	Error             *TerminalColorValue  `json:"error,omitempty"`
	Warning           *TerminalColorValue  `json:"warning,omitempty"`
	Spinner           *TerminalColorValue  `json:"spinner,omitempty"`
	ThinkingMax       *TerminalColorValue  `json:"thinking_max,omitempty"`
	ThinkingMaxCamel  *TerminalColorValue  `json:"thinkingMax,omitempty"`
	SelectionBG       *TerminalColorValue  `json:"selection_bg,omitempty"`
	SelectionFG       *TerminalColorValue  `json:"selection_fg,omitempty"`
	SpinnerFrames     []string             `json:"spinner_frames,omitempty"`
	SpinnerMessages   []string             `json:"spinner_messages,omitempty"`
	SpinnerIntervalMS *int                 `json:"spinner_interval_ms,omitempty"`
	SyntaxBaseStyle   *string              `json:"syntax_base_style,omitempty"`
	Syntax            SyntaxThemeOverrides `json:"syntax,omitempty"`
}

type SyntaxThemeOverrides struct {
	Keyword             *string `json:"keyword,omitempty"`
	KeywordConstant     *string `json:"keyword_constant,omitempty"`
	KeywordDeclaration  *string `json:"keyword_declaration,omitempty"`
	KeywordNamespace    *string `json:"keyword_namespace,omitempty"`
	KeywordReserved     *string `json:"keyword_reserved,omitempty"`
	KeywordType         *string `json:"keyword_type,omitempty"`
	NameBuiltin         *string `json:"name_builtin,omitempty"`
	NameFunction        *string `json:"name_function,omitempty"`
	NameClass           *string `json:"name_class,omitempty"`
	NameDecorator       *string `json:"name_decorator,omitempty"`
	LiteralString       *string `json:"literal_string,omitempty"`
	LiteralStringEscape *string `json:"literal_string_escape,omitempty"`
	LiteralNumber       *string `json:"literal_number,omitempty"`
	Comment             *string `json:"comment,omitempty"`
	CommentPreproc      *string `json:"comment_preproc,omitempty"`
	Operator            *string `json:"operator,omitempty"`
	Punctuation         *string `json:"punctuation,omitempty"`
	Text                *string `json:"text,omitempty"`
}

// TerminalColorValue accepts any of these JSON forms:
//
//	24                         // xterm-256 color index
//	"#42454b"                  // RGB hex
//	{"mode":"ansi","index":100}
//	{"mode":"rgb","r":66,"g":69,"b":75}
//	{"mode":"256","index":254}
type TerminalColorValue struct {
	TerminalColor
}

func (c *TerminalColorValue) UnmarshalJSON(data []byte) error {
	var index int
	if err := json.Unmarshal(data, &index); err == nil {
		c.TerminalColor = Color256(index)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if rgb, ok := parseHexColor(s); ok {
			c.TerminalColor = rgb
			return nil
		}
		return fmt.Errorf("invalid terminal color %q", s)
	}
	var obj struct {
		Mode  string `json:"mode"`
		Index int    `json:"index"`
		R     int    `json:"r"`
		G     int    `json:"g"`
		B     int    `json:"b"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	switch strings.ToLower(obj.Mode) {
	case "", "256", "color256", "xterm256":
		c.TerminalColor = Color256(obj.Index)
	case "ansi":
		c.TerminalColor = ColorANSI(obj.Index)
	case "rgb", "truecolor":
		c.TerminalColor = ColorRGB(obj.R, obj.G, obj.B)
	default:
		return fmt.Errorf("unknown terminal color mode %q", obj.Mode)
	}
	return nil
}

// InheritedTheme returns a theme derived from the terminal snapshot carried
// by detected. It prefers the terminal's reported ANSI palette for semantic
// accents, derives muted and selected surfaces by fading toward the terminal
// background, and uses truecolor output only when the terminal advertises it.
func InheritedTheme(detected Theme) Theme {
	base := Dark
	if detected.Terminal.SchemeKnown {
		if detected.Terminal.Light {
			base = Light
		}
	} else if IsLightTheme(detected) {
		base = Light
	}
	t := withTerminalProfile(base, detected)
	t.Inherited = true

	if t.Terminal.HasForeground {
		t.FG = t.Terminal.Foreground
	}

	accentFallback := t.Accent
	if IsLightTheme(t) {
		accentFallback = Light.Accent
	}
	t.Accent = terminalPaletteColor(t.Terminal, accentFallback,
		ansiBrightBlue, ansiBlue, ansiBrightCyan, ansiCyan, ansiBrightMagenta, ansiMagenta)
	t.Assistant = t.Accent
	t.Tool = terminalPaletteColor(t.Terminal, t.Tool, ansiBrightGreen, ansiGreen)
	t.Error = terminalPaletteColor(t.Terminal, t.Error, ansiBrightRed, ansiRed)
	t.Warning = terminalPaletteColor(t.Terminal, t.Warning, ansiBrightYellow, ansiYellow)
	t.Spinner = terminalPaletteColor(t.Terminal, t.Spinner, ansiBrightMagenta, ansiMagenta)
	t.ThinkingMax = terminalPaletteColor(t.Terminal, t.ThinkingMax, ansiBrightMagenta, ansiMagenta)
	t.User = terminalPaletteColor(t.Terminal, t.User, ansiBrightYellow, ansiYellow)

	if t.Terminal.HasForeground && t.Terminal.HasBackground {
		t.Muted = t.DimColor(t.FG, 45)
		t.ToolOut = t.Muted
		background := t.Terminal.Background
		foreground := t.Terminal.Foreground
		t.UserBubbleBG = blendTerminalColors(background, foreground, 12)
		t.SelectionBG = blendTerminalColors(background, foreground, 25)
	}

	t.UserBubbleFG = t.FG
	t.SelectionFG = t.FG

	// Keep the terminal's configured background untouched. Painting an
	// inherited RGB background across every row would make terminal theme
	// changes and scrollback behavior less predictable.
	t.Background = nil
	return t
}

func withTerminalProfile(base, detected Theme) Theme {
	base.Terminal = detected.Terminal
	return base
}

// Standard ANSI palette slots used as inherited role preferences.
const (
	ansiRed           = 1
	ansiGreen         = 2
	ansiYellow        = 3
	ansiBlue          = 4
	ansiMagenta       = 5
	ansiCyan          = 6
	ansiBrightRed     = 9
	ansiBrightGreen   = 10
	ansiBrightYellow  = 11
	ansiBrightBlue    = 12
	ansiBrightMagenta = 13
	ansiBrightCyan    = 14
)

func terminalPaletteColor(profile TerminalProfile, fallback TerminalColor, preferred ...int) TerminalColor {
	for _, index := range preferred {
		if _, ok := profile.PaletteColor(index); ok {
			// Keep the palette slot as the semantic value. Truecolor
			// resolution expands it back to the terminal-reported RGB;
			// 256-color output preserves the slot rather than re-quantizing
			// the reported RGB to a nearby cube entry.
			return Color256(index)
		}
	}
	return fallback
}

func blendTerminalColors(from, to TerminalColor, percent int) TerminalColor {
	fromRGB, _ := rgbForTerminalColor(from)
	toRGB, _ := rgbForTerminalColor(to)
	percent = clampPercent(percent)
	return ColorRGB(
		blendChannel(fromRGB[0], toRGB[0], percent),
		blendChannel(fromRGB[1], toRGB[1], percent),
		blendChannel(fromRGB[2], toRGB[2], percent),
	)
}

// LoadThemeFromHome applies a custom theme from $ZOT_HOME/themes/*.json
// to detected. Empty/auto/default keeps the built-in detected theme.
// If preferred is set, it may be a theme name, a basename without
// .json, or an absolute/relative path.
func DetectThemeWithCustom(zotHome, preferred string, timeout time.Duration) (Theme, string, error) {
	detected := DetectThemeFromBackground(timeout)
	return LoadThemeFromHome(zotHome, preferred, detected)
}

func LoadThemeFromHome(zotHome, preferred string, detected Theme) (Theme, string, error) {
	path, err := resolveThemePath(zotHome, preferred)
	if err != nil || path == "" {
		return detected, "", err
	}
	switch path {
	case "builtin:inherited":
		return InheritedTheme(detected), "inherited", nil
	case "builtin:dark":
		return withTerminalProfile(Dark, detected), "dark", nil
	case "builtin:light":
		return withTerminalProfile(Light, detected), "light", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return detected, "", err
	}
	var tf ThemeFile
	if err := json.Unmarshal(b, &tf); err != nil {
		return detected, "", fmt.Errorf("parse theme %s: %w", path, err)
	}
	if tf.Name == "" {
		tf.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	base := applyThemeOverrides(detected, tf.Overrides)
	base = applyThemeOverrides(base, tf.Colors.Base)
	if isLightTheme(detected) {
		if tf.Colors.HasLight {
			base = applyThemeOverrides(base, tf.Colors.Light)
		} else if tf.Colors.HasDark {
			base = applyThemeOverrides(base, tf.Colors.Dark)
		}
	} else {
		if tf.Colors.HasDark {
			base = applyThemeOverrides(base, tf.Colors.Dark)
		} else if tf.Colors.HasLight {
			base = applyThemeOverrides(base, tf.Colors.Light)
		}
	}
	return base, tf.Name, nil
}

// AvailableThemes returns built-in and user-installed themes suitable
// for a settings picker. Invalid JSON files are skipped.
func AvailableThemes(zotHome string) []ThemeOption {
	out := []ThemeOption{
		{Value: "auto", Label: "auto", Description: "detect terminal background and use zot defaults", Builtin: true},
		{Value: "inherited", Label: "inherited (from terminal)", Description: "use terminal colors with truecolor or best-effort 256-color output", Builtin: true},
		{Value: "dark", Label: "dark", Description: "built-in dark theme", Builtin: true},
		{Value: "light", Label: "light", Description: "built-in light theme", Builtin: true},
	}
	seen := map[string]bool{"auto": true, "inherited": true, "dark": true, "light": true}
	paths, _ := themeFilesIn(filepath.Join(zotHome, "themes"))
	sort.Strings(paths)
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var tf ThemeFile
		if err := json.Unmarshal(b, &tf); err != nil {
			continue
		}
		value := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if value == "" || seen[value] {
			continue
		}
		desc := tf.Description
		if desc == "" {
			desc = path
		}
		out = append(out, ThemeOption{Value: value, Label: value, Description: desc, Path: path})
		seen[value] = true
	}
	return out
}

// ThemeOptionFromFile parses one theme JSON file for picker display.
// value is what will be stored in config; pass an absolute path for
// extension-owned themes so they can be loaded without copying into
// $ZOT_HOME/themes.
func ThemeOptionFromFile(path, value, source string) (ThemeOption, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ThemeOption{}, false
	}
	var tf ThemeFile
	if err := json.Unmarshal(b, &tf); err != nil {
		return ThemeOption{}, false
	}
	if value == "" {
		value = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	label := value
	if tf.Name != "" {
		label = tf.Name
	}
	desc := tf.Description
	if source != "" {
		if desc != "" {
			desc = "from " + source + " — " + desc
		} else {
			desc = "from " + source
		}
	}
	if desc == "" {
		desc = path
	}
	return ThemeOption{Value: value, Label: label, Description: desc, Path: path}, true
}

func ThemeExists(zotHome, preferred string) bool {
	path, err := resolveThemePath(zotHome, preferred)
	return err == nil && path != ""
}

func resolveThemePath(zotHome, preferred string) (string, error) {
	preferred = strings.TrimSpace(preferred)
	switch strings.ToLower(preferred) {
	case "", "auto", "default", "system":
		return "", nil
	case "inherited", "terminal", "terminal-inherited":
		return "builtin:inherited", nil
	case "dark":
		return "builtin:dark", nil
	case "light":
		return "builtin:light", nil
	}
	if preferred != "" && !strings.HasPrefix(preferred, "builtin:") {
		candidates := []string{preferred}
		if filepath.Ext(preferred) == "" {
			candidates = append(candidates,
				filepath.Join(zotHome, "themes", preferred+".json"),
			)
		} else if !filepath.IsAbs(preferred) {
			candidates = append(candidates,
				filepath.Join(zotHome, "themes", preferred),
			)
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}
		return "", fmt.Errorf("theme %q not found", preferred)
	}

	return "", nil
}

func themeFilesIn(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || strings.ToLower(filepath.Ext(e.Name())) != ".json" {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out, nil
}

func applyThemeOverrides(th Theme, o ThemeOverrides) Theme {
	if o.FG != nil {
		th.FG = o.FG.TerminalColor
	}
	if o.Muted != nil {
		th.Muted = o.Muted.TerminalColor
	}
	if o.Accent != nil {
		th.Accent = o.Accent.TerminalColor
	}
	if o.Background != nil {
		bg := o.Background.TerminalColor
		th.Background = &bg
	}
	if o.User != nil {
		th.User = o.User.TerminalColor
	}
	if o.UserBubbleBG != nil {
		th.UserBubbleBG = o.UserBubbleBG.TerminalColor
	}
	if o.UserBubbleFG != nil {
		th.UserBubbleFG = o.UserBubbleFG.TerminalColor
	}
	if o.Assistant != nil {
		th.Assistant = o.Assistant.TerminalColor
	}
	if o.Tool != nil {
		th.Tool = o.Tool.TerminalColor
	}
	if o.ToolOut != nil {
		th.ToolOut = o.ToolOut.TerminalColor
	}
	if o.Error != nil {
		th.Error = o.Error.TerminalColor
	}
	if o.Warning != nil {
		th.Warning = o.Warning.TerminalColor
	}
	if o.Spinner != nil {
		th.Spinner = o.Spinner.TerminalColor
	}
	if o.ThinkingMax != nil {
		th.ThinkingMax = o.ThinkingMax.TerminalColor
	} else if o.ThinkingMaxCamel != nil {
		th.ThinkingMax = o.ThinkingMaxCamel.TerminalColor
	}
	if o.SelectionBG != nil {
		th.SelectionBG = o.SelectionBG.TerminalColor
	}
	if o.SelectionFG != nil {
		th.SelectionFG = o.SelectionFG.TerminalColor
	}
	if len(o.SpinnerFrames) > 0 {
		th.SpinnerFrames = append([]string(nil), o.SpinnerFrames...)
	}
	if len(o.SpinnerMessages) > 0 {
		th.SpinnerMessages = append([]string(nil), o.SpinnerMessages...)
	}
	if o.SpinnerIntervalMS != nil && *o.SpinnerIntervalMS > 0 {
		th.SpinnerIntervalMS = *o.SpinnerIntervalMS
	}
	if o.SyntaxBaseStyle != nil {
		th.SyntaxBaseStyle = *o.SyntaxBaseStyle
	}
	th.Syntax = applySyntaxOverrides(th.Syntax, o.Syntax)
	return th
}

func applySyntaxOverrides(s SyntaxTheme, o SyntaxThemeOverrides) SyntaxTheme {
	if o.Keyword != nil {
		s.Keyword = *o.Keyword
	}
	if o.KeywordConstant != nil {
		s.KeywordConstant = *o.KeywordConstant
	}
	if o.KeywordDeclaration != nil {
		s.KeywordDeclaration = *o.KeywordDeclaration
	}
	if o.KeywordNamespace != nil {
		s.KeywordNamespace = *o.KeywordNamespace
	}
	if o.KeywordReserved != nil {
		s.KeywordReserved = *o.KeywordReserved
	}
	if o.KeywordType != nil {
		s.KeywordType = *o.KeywordType
	}
	if o.NameBuiltin != nil {
		s.NameBuiltin = *o.NameBuiltin
	}
	if o.NameFunction != nil {
		s.NameFunction = *o.NameFunction
	}
	if o.NameClass != nil {
		s.NameClass = *o.NameClass
	}
	if o.NameDecorator != nil {
		s.NameDecorator = *o.NameDecorator
	}
	if o.LiteralString != nil {
		s.LiteralString = *o.LiteralString
	}
	if o.LiteralStringEscape != nil {
		s.LiteralStringEscape = *o.LiteralStringEscape
	}
	if o.LiteralNumber != nil {
		s.LiteralNumber = *o.LiteralNumber
	}
	if o.Comment != nil {
		s.Comment = *o.Comment
	}
	if o.CommentPreproc != nil {
		s.CommentPreproc = *o.CommentPreproc
	}
	if o.Operator != nil {
		s.Operator = *o.Operator
	}
	if o.Punctuation != nil {
		s.Punctuation = *o.Punctuation
	}
	if o.Text != nil {
		s.Text = *o.Text
	}
	return s
}

func IsLightTheme(th Theme) bool {
	if th.FG == Light.FG && th.SelectionBG == Light.SelectionBG && th.SelectionFG == Light.SelectionFG {
		return true
	}
	return th.Inherited && th.Terminal.SchemeKnown && th.Terminal.Light
}

func isLightTheme(th Theme) bool { return IsLightTheme(th) }

func parseHexColor(s string) (TerminalColor, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return TerminalColor{}, false
	}
	var rgb [3]int
	for i := 0; i < 3; i++ {
		v, ok := parseHexByte(s[i*2 : i*2+2])
		if !ok {
			return TerminalColor{}, false
		}
		rgb[i] = v
	}
	return ColorRGB(rgb[0], rgb[1], rgb[2]), true
}

func parseHexByte(s string) (int, bool) {
	var n int
	for _, r := range s {
		n *= 16
		switch {
		case r >= '0' && r <= '9':
			n += int(r - '0')
		case r >= 'a' && r <= 'f':
			n += int(r-'a') + 10
		case r >= 'A' && r <= 'F':
			n += int(r-'A') + 10
		default:
			return 0, false
		}
	}
	return n, true
}
