package tui

import "strings"

const (
	InputStylePlain = "plain"
	InputStyleLines = "lines"
	InputStyleBlock = "block"

	StatusPositionAboveInput = "above_input"
	StatusPositionBelowInput = "below_input"

	WorkingPositionAboveInput = "above_input"
	WorkingPositionBelowInput = "below_input"
)

func NormalizeInputStyle(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case InputStyleLines, "line":
		return InputStyleLines
	case InputStyleBlock, "bubble", "boxed", "box":
		return InputStyleBlock
	default:
		return InputStylePlain
	}
}

func NormalizeStatusPosition(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case StatusPositionBelowInput, "below", "bottom":
		return StatusPositionBelowInput
	default:
		return StatusPositionAboveInput
	}
}

func NormalizeWorkingPosition(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case WorkingPositionBelowInput, "below", "bottom":
		return WorkingPositionBelowInput
	default:
		return WorkingPositionAboveInput
	}
}

func InputLines(th Theme, lines []string, width int) []string {
	if width < 1 {
		width = 1
	}
	rule := th.FG256(th.Muted, strings.Repeat("─", width))
	out := make([]string, 0, len(lines)+2)
	out = append(out, rule)
	out = append(out, lines...)
	out = append(out, rule)
	return out
}

func InputBlock(th Theme, lines []string, width int) []string {
	if width < 1 {
		width = 1
	}
	bubbleW := width - 2
	if bubbleW < 1 {
		bubbleW = 1
	}
	row := func(content string) string {
		visible := visibleWidth(content)
		if visible < bubbleW {
			content += strings.Repeat(" ", bubbleW-visible)
		}
		bg := th.sgrBGColor(th.UserBubbleBG)
		bar := bg + th.FG256(th.Accent, "▌ ")
		body := bg + strings.ReplaceAll(content, reset, reset+bg) + reset
		return bar + body
	}
	out := make([]string, 0, len(lines)+2)
	out = append(out, row(""))
	for _, line := range lines {
		out = append(out, row(line))
	}
	out = append(out, row(""))
	return out
}

func CursorColor256(index int) string {
	r, g, b := xterm256RGB(index)
	return "\x1b]12;rgb:" + hexByte(r) + "/" + hexByte(g) + "/" + hexByte(b) + "\x07"
}

func ResetCursorColor() string { return "\x1b]112\x07" }

func CursorShapeBlock() string { return "\x1b[1 q" }

func ResetCursorShape() string { return "\x1b[0 q" }

func xterm256RGB(index int) (int, int, int) {
	if index < 0 {
		index = 0
	}
	if index > 255 {
		index = 255
	}
	ansi := [16][3]int{
		{0, 0, 0}, {128, 0, 0}, {0, 128, 0}, {128, 128, 0},
		{0, 0, 128}, {128, 0, 128}, {0, 128, 128}, {192, 192, 192},
		{128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
		{0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
	}
	if index < 16 {
		c := ansi[index]
		return c[0], c[1], c[2]
	}
	if index < 232 {
		idx := index - 16
		levels := [6]int{0, 95, 135, 175, 215, 255}
		return levels[idx/36], levels[(idx/6)%6], levels[idx%6]
	}
	gray := 8 + (index-232)*10
	return gray, gray, gray
}

func hexByte(v int) string {
	const digits = "0123456789abcdef"
	if v < 0 {
		v = 0
	}
	if v > 255 {
		v = 255
	}
	return string([]byte{digits[v>>4], digits[v&0xf]})
}
