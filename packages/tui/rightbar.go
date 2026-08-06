package tui

import (
	"sort"
	"strings"
)

const (
	// RightBarSeparatorWidth is the one-cell divider between the main pane
	// and the host-owned right bar.
	RightBarSeparatorWidth = 1
	// RightBarMinWidth keeps widget titles and checklist markers readable.
	RightBarMinWidth = 24
	// RightBarMaxWidth prevents a long extension line from taking most of
	// the transcript on wide terminals.
	RightBarMaxWidth = 36
	// RightBarMinMainWidth preserves enough room for the editor and chat.
	RightBarMinMainWidth = 48

	// rightBarRightPadding leaves a small gutter between widget text and the
	// terminal's right edge.
	rightBarRightPadding = 1
)

// RightBarWidget is declarative content for one persistent extension widget.
// The host owns ordering and layout; widgets are display-only in the first
// version and should use OpenPanel for interaction.
type RightBarWidget struct {
	Extension string
	ID        string
	Title     string
	Lines     []string
}

// RightBarColumns returns the main-pane and side-rail widths for a terminal.
// It returns ok=false when keeping both panes usable would leave too little
// room for the rail; callers should then render right-bar widgets using their
// normal fallback placement.
func RightBarColumns(cols int) (mainWidth, rightBarWidth int, ok bool) {
	if cols <= 0 {
		return cols, 0, false
	}
	available := cols - RightBarSeparatorWidth
	if available <= RightBarMinMainWidth {
		return cols, 0, false
	}

	rightBarWidth = cols / 3
	if rightBarWidth < RightBarMinWidth {
		rightBarWidth = RightBarMinWidth
	}
	if rightBarWidth > RightBarMaxWidth {
		rightBarWidth = RightBarMaxWidth
	}
	if available-rightBarWidth < RightBarMinMainWidth {
		rightBarWidth = available - RightBarMinMainWidth
	}
	if rightBarWidth < RightBarMinWidth {
		return cols, 0, false
	}
	return available - rightBarWidth, rightBarWidth, true
}

// RenderRightBar renders a bounded, full-height side rail without adding a
// second frame around the host-owned separator. Widgets are sorted by
// extension name and ID so output stays deterministic even when extension
// frames arrive concurrently. Long lines are clipped with a three-dot ellipsis
// while reserving one cell of right padding.
func RenderRightBar(th Theme, widgets []RightBarWidget, width, height int) []string {
	if width <= 0 || height <= 0 {
		return nil
	}

	contentWidth := width - rightBarRightPadding
	ordered := append([]RightBarWidget(nil), widgets...)
	sort.SliceStable(ordered, func(a, b int) bool {
		if ordered[a].Extension != ordered[b].Extension {
			return ordered[a].Extension < ordered[b].Extension
		}
		return ordered[a].ID < ordered[b].ID
	})

	content := make([]string, 0, height)
	truncated := false
	padLine := func(text string, phase bool) string {
		if contentWidth <= 0 {
			return strings.Repeat(" ", width)
		}
		if phase {
			text = truncateRightBarPhaseLine(text, contentWidth)
		} else {
			text = truncateRightBarLine(text, contentWidth)
		}
		if visible := visibleWidth(text); visible < width {
			text += strings.Repeat(" ", width-visible)
		}
		return text
	}
	paintLine := func(text string, color *TerminalColor, dim, phase bool) string {
		text = padLine(text, phase)
		if color == nil {
			return text
		}
		paint := func(value string, valueColor TerminalColor) string {
			if dim {
				value = Dim(value)
			}
			return th.FGColor(valueColor, value)
		}
		if markerStart, markerEnd, _, ok := rightBarChecklistMarker(text); ok {
			if phase {
				if nameStart, nameEnd, ok := rightBarChecklistPhaseName(text); ok {
					return paint(text[:markerStart], *color) +
						paint(text[markerStart:markerEnd], th.Muted) +
						paint(text[markerEnd:nameStart], *color) +
						paint(Bold(text[nameStart:nameEnd]), *color) +
						paint(text[nameEnd:], *color)
				}
			}
			return paint(text[:markerStart], *color) +
				paint(text[markerStart:markerEnd], th.Muted) +
				paint(text[markerEnd:], *color)
		}
		if phase {
			return paint(Bold(text), *color)
		}
		return paint(text, *color)
	}
	appendContent := func(text string, color *TerminalColor, dim, phase bool) bool {
		text = strings.ReplaceAll(text, "\r", "")
		for _, line := range strings.Split(text, "\n") {
			if len(content) >= height {
				truncated = true
				return false
			}
			content = append(content, paintLine(line, color, dim, phase))
		}
		return true
	}

	for idx, widget := range ordered {
		if idx > 0 && !appendContent("", nil, false, false) {
			break
		}
		label := "[" + widget.Extension + "]"
		if widget.Title != "" {
			label += " " + widget.Title
		}
		if !appendContent(label, &th.Accent, false, false) {
			break
		}
		if !appendContent("", nil, false, false) {
			break
		}
		activePhaseIndent := rightBarActivePhaseIndent(widget.Lines)
		if activePhaseIndent >= 0 {
			activePhaseIndent++ // host-owned line prefix
		}
		phaseIndent := rightBarChecklistIndent(widget.Lines)
		if phaseIndent >= 0 {
			phaseIndent++ // host-owned line prefix
		}
		activePhase := false
		for _, line := range widget.Lines {
			line = " " + line
			dim := rightBarChecklistLineDim(line, activePhaseIndent, &activePhase)
			phase := rightBarChecklistPhaseLine(line, phaseIndent)
			if !appendContent(line, &th.FG, dim, phase) {
				break
			}
		}
		if truncated {
			break
		}
	}
	if truncated {
		marker := paintLine("... right bar ...", &th.Muted, false, false)
		if len(content) == height {
			content[len(content)-1] = marker
		} else {
			content = append(content, marker)
		}
	}
	for len(content) < height {
		content = append(content, strings.Repeat(" ", width))
	}
	return content
}

func rightBarChecklistMarker(text string) (start, end int, marker byte, ok bool) {
	start = 0
	for start < len(text) && text[start] == ' ' {
		start++
	}
	if len(text) < start+3 || text[start] != '[' || text[start+2] != ']' {
		return 0, 0, 0, false
	}
	switch text[start+1] {
	case ' ', 'x', 'X', '>':
		return start, start + 3, text[start+1], true
	default:
		return 0, 0, 0, false
	}
}

func truncateRightBarLine(text string, width int) string {
	if width <= 0 || visibleWidth(text) <= width {
		return text
	}
	if width <= 3 {
		return truncateToWidth("...", width)
	}
	return truncateToWidth(text, width-3) + "..."
}

func truncateRightBarPhaseLine(text string, width int) string {
	if width <= 0 || visibleWidth(text) <= width {
		return text
	}
	nameStart, nameEnd, ok := rightBarChecklistPhaseName(text)
	if !ok {
		return truncateRightBarLine(text, width)
	}
	prefix := text[:nameStart]
	suffix := text[nameEnd:]
	nameWidth := width - visibleWidth(prefix) - visibleWidth(suffix)
	if nameWidth == 0 {
		return prefix + suffix
	}
	if nameWidth < 0 {
		return truncateRightBarLine(text, width)
	}
	return prefix + truncateRightBarLine(text[nameStart:nameEnd], nameWidth) + suffix
}

func rightBarChecklistIndent(lines []string) int {
	indent := -1
	for _, line := range lines {
		start, _, _, ok := rightBarChecklistMarker(line)
		if ok && (indent < 0 || start < indent) {
			indent = start
		}
	}
	return indent
}

func rightBarChecklistPhaseLine(text string, phaseIndent int) bool {
	if phaseIndent < 0 {
		return false
	}
	start, _, _, ok := rightBarChecklistMarker(text)
	return ok && start == phaseIndent
}

func rightBarChecklistPhaseName(text string) (start, end int, ok bool) {
	_, markerEnd, _, markerOK := rightBarChecklistMarker(text)
	if !markerOK {
		return 0, 0, false
	}
	start = markerEnd
	if start < len(text) && text[start] == ' ' {
		start++
	}
	trimmedEnd := len(strings.TrimRight(text, " "))
	if trimmedEnd <= start {
		return 0, 0, false
	}
	suffix := text[start:trimmedEnd]
	separator := strings.LastIndex(suffix, "  ")
	if separator < 0 {
		return rightBarFallbackPhaseName(text, start, trimmedEnd)
	}
	nameEnd := start + separator
	progress := strings.TrimSpace(text[nameEnd:trimmedEnd])
	if !rightBarProgressToken(progress) {
		return rightBarFallbackPhaseName(text, start, trimmedEnd)
	}
	for nameEnd > start && text[nameEnd-1] == ' ' {
		nameEnd--
	}
	if nameEnd <= start {
		return 0, 0, false
	}
	return start, nameEnd, true
}

func rightBarFallbackPhaseName(text string, start, end int) (int, int, bool) {
	for end > start && text[end-1] == ' ' {
		end--
	}
	if end-start >= 3 && text[end-3:end] == "..." {
		end -= 3
	}
	for end > start && text[end-1] == ' ' {
		end--
	}
	if end <= start {
		return 0, 0, false
	}
	return start, end, true
}

func rightBarProgressToken(value string) bool {
	separator := strings.IndexByte(value, '/')
	if separator <= 0 || separator == len(value)-1 {
		return false
	}
	for _, part := range []string{value[:separator], value[separator+1:]} {
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func rightBarActivePhaseIndent(lines []string) int {
	for _, line := range lines {
		start, _, marker, ok := rightBarChecklistMarker(line)
		if ok && marker == '>' {
			return start
		}
	}
	return -1
}

func rightBarChecklistLineDim(text string, activePhaseIndent int, activePhase *bool) bool {
	if activePhaseIndent < 0 {
		return false
	}
	start, _, marker, ok := rightBarChecklistMarker(text)
	if !ok {
		return false
	}
	if start == activePhaseIndent {
		*activePhase = marker == '>'
		return !*activePhase
	}
	if start > activePhaseIndent {
		return !*activePhase
	}
	return false
}

// JoinRightBar combines one main-pane row and one right-bar row without
// allowing either side to soft-wrap. The returned row occupies exactly
// mainWidth + RightBarSeparatorWidth + rightBarWidth display cells.
func JoinRightBar(th Theme, main, rightBar string, mainWidth, rightBarWidth int) string {
	if mainWidth < 1 {
		mainWidth = 1
	}
	if rightBarWidth < 1 {
		rightBarWidth = 1
	}
	main = truncateToWidth(main, mainWidth)
	if visible := visibleWidth(main); visible < mainWidth {
		main += strings.Repeat(" ", mainWidth-visible)
	}
	rightBar = truncateToWidth(rightBar, rightBarWidth)
	if visible := visibleWidth(rightBar); visible < rightBarWidth {
		rightBar += strings.Repeat(" ", rightBarWidth-visible)
	}
	return main + th.FGColor(th.Muted, "│") + rightBar
}

// DrawRightBar renders an already-laid-out visible chat slice and sticky
// bottom block in a fixed frame with a persistent right bar. The normal
// DrawLog path deliberately uses terminal scrollback; a persistent side rail
// needs a fixed viewport so updates can repaint its columns without leaving
// stale rows behind. Interactive mode still owns chat scrolling and passes
// the selected visible slice here.
func (r *Renderer) DrawRightBar(chat, bottom, rightBar []string, cursorBottomRow, cursorCol int) {
	if r.cols == 0 || r.rows == 0 {
		return
	}
	mainWidth, rightBarWidth, ok := RightBarColumns(r.cols)
	if !ok {
		return
	}
	if len(bottom) == 0 {
		bottom = []string{""}
	}
	if len(rightBar) < r.rows {
		padded := make([]string, r.rows)
		copy(padded, rightBar)
		rightBar = padded
	} else if len(rightBar) > r.rows {
		rightBar = rightBar[:r.rows]
	}

	main := make([]string, 0, len(chat)+len(bottom)+1)
	main = append(main, chat...)
	main = append(main, bottom...)
	main = append(main, "") // mirror DrawLog's renderer-owned bottom margin
	cursorRow := len(chat) + cursorBottomRow
	if len(main) > r.rows {
		trim := len(main) - r.rows
		main = main[trim:]
		cursorRow -= trim
	}
	if len(main) < r.rows {
		padding := make([]string, r.rows-len(main))
		main = append(padding, main...)
		cursorRow += len(padding)
	}

	frame := make([]string, r.rows)
	for row := range frame {
		frame[row] = JoinRightBar(r.theme, main[row], rightBar[row], mainWidth, rightBarWidth)
	}

	// Draw() owns the fixed-frame cache. Reset the flow-mode cache so a
	// later fallback to DrawLog starts with a full repaint instead of trying
	// to diff against logical rows from before the rail was enabled. Mark the
	// transition separately so that repaint does not clear scrollback.
	r.logChat = nil
	r.logBottom = nil
	r.logLines = nil
	r.logViewportTop = 0
	r.logHardwareRow = 0
	r.logInit = false
	r.logNeedsFullRepaint = true
	r.Draw(frame, cursorRow, cursorCol)
}
