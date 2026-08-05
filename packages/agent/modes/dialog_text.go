package modes

import (
	"strings"

	"github.com/bnema/zut/packages/tui"
)

const dialogTextIndent = "    "

// renderDialogMarkdownRows renders assistant prose and wraps every styled row
// to the width available after the dialog's left indent.
func renderDialogMarkdownRows(text string, th tui.Theme, width int) []string {
	innerWidth := dialogTextWidth(width)
	return wrapDialogTextRows(tui.RenderMarkdown(text, th, innerWidth), width)
}

// wrapDialogTextRows wraps text that has already been styled, preserving ANSI
// sequences while keeping every returned row within the dialog width.
func wrapDialogTextRows(text string, width int) []string {
	innerWidth := dialogTextWidth(width)
	var rows []string
	for _, line := range strings.Split(text, "\n") {
		if len(line) > 0 && line[0] == tui.FlushLeftSentinel {
			line = line[1:]
		}
		for _, wrapped := range tui.WrapANSILine(line, innerWidth) {
			rows = append(rows, dialogTextIndent+wrapped)
		}
	}
	return rows
}

func dialogTextWidth(width int) int {
	width -= len(dialogTextIndent)
	if width < 1 {
		return 1
	}
	return width
}
