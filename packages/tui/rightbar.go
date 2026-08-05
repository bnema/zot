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

// RenderRightBar renders a bounded, full-height side rail. Widgets are sorted
// by extension name and ID so output stays deterministic even when extension
// frames arrive concurrently. Content that does not fit is replaced by a
// compact marker rather than overflowing the terminal or growing the frame.
func RenderRightBar(th Theme, widgets []RightBarWidget, width, height int) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	if width == 1 {
		return make([]string, height)
	}
	if width == 2 {
		out := make([]string, height)
		for row := range out {
			out[row] = strings.Repeat(" ", width)
		}
		out[0] = th.FG256(th.Muted, "──")
		if height > 1 {
			out[height-1] = th.FG256(th.Muted, "──")
		}
		return out
	}
	if height == 1 {
		return []string{th.FG256(th.Muted, strings.Repeat("─", width))}
	}

	ordered := append([]RightBarWidget(nil), widgets...)
	sort.SliceStable(ordered, func(a, b int) bool {
		if ordered[a].Extension != ordered[b].Extension {
			return ordered[a].Extension < ordered[b].Extension
		}
		return ordered[a].ID < ordered[b].ID
	})

	innerWidth := width - 2
	contentRows := height - 2
	content := make([]string, 0, contentRows)
	truncated := false
	appendContent := func(text string, color int) bool {
		if len(content) >= contentRows {
			truncated = true
			return false
		}
		text = strings.ReplaceAll(strings.ReplaceAll(text, "\r", ""), "\n", " ↩ ")
		text = truncateToWidth(text, innerWidth)
		if visible := visibleWidth(text); visible < innerWidth {
			text += strings.Repeat(" ", innerWidth-visible)
		}
		if color >= 0 {
			text = th.FG256(color, text)
		}
		content = append(content, text)
		return true
	}

	for idx, widget := range ordered {
		if idx > 0 && !appendContent("", -1) {
			break
		}
		label := "[" + widget.Extension + "]"
		if widget.Title != "" {
			label += " " + widget.Title
		}
		if !appendContent(label, th.Accent) {
			break
		}
		for _, line := range widget.Lines {
			if !appendContent("  "+line, th.FG) {
				break
			}
		}
		if truncated {
			break
		}
	}
	if truncated {
		marker := "... right bar ..."
		marker = truncateToWidth(marker, innerWidth)
		if len(content) == contentRows {
			content[len(content)-1] = ""
		}
		if !appendContent(marker, th.Muted) && len(content) > 0 {
			// appendContent cannot append when the row budget is full, so
			// replace the final row with the already-padded marker instead.
			marker = marker + strings.Repeat(" ", max(0, innerWidth-visibleWidth(marker)))
			content[len(content)-1] = th.FG256(th.Muted, marker)
		}
	}

	border := func(glyph string) string { return th.FG256(th.Muted, glyph) }
	out := make([]string, 0, height)
	out = append(out, border("┌")+strings.Repeat("─", innerWidth)+border("┐"))
	for _, line := range content {
		out = append(out, border("│")+line+border("│"))
	}
	for len(out) < height-1 {
		out = append(out, border("│")+strings.Repeat(" ", innerWidth)+border("│"))
	}
	out = append(out, border("└")+strings.Repeat("─", innerWidth)+border("┘"))
	return out
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
	return main + th.FG256(th.Muted, "│") + rightBar
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
	// to diff against logical rows from before the rail was enabled.
	r.logChat = nil
	r.logBottom = nil
	r.logLines = nil
	r.logViewportTop = 0
	r.logHardwareRow = 0
	r.logInit = false
	r.Draw(frame, cursorRow, cursorCol)
}
