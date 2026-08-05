package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestRightBarColumnsFallsBackWhenMainPaneWouldBeTooNarrow(t *testing.T) {
	if _, _, ok := RightBarColumns(72); ok {
		t.Fatal("right bar should fall back at 72 columns")
	}
	main, rail, ok := RightBarColumns(73)
	if !ok {
		t.Fatal("right bar should fit at 73 columns")
	}
	if main < RightBarMinMainWidth || rail < RightBarMinWidth || rail > RightBarMaxWidth {
		t.Fatalf("right bar columns = main %d, rail %d", main, rail)
	}
	if main+RightBarSeparatorWidth+rail != 73 {
		t.Fatalf("column budget = %d, want 73", main+RightBarSeparatorWidth+rail)
	}
}

func TestRenderRightBarSortsWidgetsAndBoundsRows(t *testing.T) {
	lines := RenderRightBar(Dark, []RightBarWidget{
		{Extension: "zeta", ID: "second", Title: "Z", Lines: []string{"z"}},
		{Extension: "alpha", ID: "first", Title: "A", Lines: []string{"a"}},
	}, 24, 7)
	if len(lines) != 7 {
		t.Fatalf("right bar rows = %d, want 7", len(lines))
	}
	for idx, line := range lines {
		if width := visibleWidth(line); width != 24 {
			t.Fatalf("right bar line %d width = %d, want 24: %q", idx, width, line)
		}
	}
	joined := stripANSI(strings.Join(lines, "\n"))
	if strings.Index(joined, "[alpha] A") > strings.Index(joined, "[zeta] Z") {
		t.Fatalf("widgets are not deterministic by extension: %q", joined)
	}
	if strings.ContainsAny(joined, "┌┐└┘─") {
		t.Fatalf("right bar added an outer border: %q", joined)
	}

	truncated := RenderRightBar(Dark, []RightBarWidget{
		{Extension: "alpha", ID: "large", Lines: []string{"a", "b", "c", "d", "e", "f", "g", "h"}},
	}, 24, 7)
	if !strings.Contains(stripANSI(strings.Join(truncated, "\n")), "right bar") {
		t.Fatalf("truncation marker missing: %q", truncated)
	}
}

func TestRenderRightBarClipsLongWidgetLinesWithEllipsis(t *testing.T) {
	lines := RenderRightBar(Dark, []RightBarWidget{{
		Extension: "plan",
		Lines:     []string{"one two three four five six"},
	}}, 16, 8)

	if len(lines) != 8 {
		t.Fatalf("right bar rows = %d, want 8", len(lines))
	}
	plain := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(plain, " one two thre...") || strings.Contains(plain, "four five six") {
		t.Fatalf("long widget line was not clipped with an ellipsis: %q", plain)
	}
	for idx, line := range lines {
		if width := visibleWidth(line); width != 16 {
			t.Fatalf("clipped right bar line %d width = %d, want 16: %q", idx, width, line)
		}
	}
}

func TestRenderRightBarPreservesPhaseProgressWhenTitleIsClipped(t *testing.T) {
	lines := RenderRightBar(Dark, []RightBarWidget{
		{
			Extension: "tasked-phases",
			Lines:     []string{"[ ] A phase title that is much too long to fit  0/3"},
		},
	}, 24, 5)
	plainLines := strings.Split(stripANSI(strings.Join(lines, "\n")), "\n")
	if len(plainLines) < 3 {
		t.Fatalf("phase row is missing: %q", plainLines)
	}
	phase := strings.TrimRight(plainLines[2], " ")
	if !strings.HasSuffix(phase, "0/3") || !strings.Contains(phase, "...  0/3") {
		t.Fatalf("phase progress was clipped with its title: %q", phase)
	}
}

func TestRenderRightBarUsesChecklistIndentEllipsisAndBoldPhases(t *testing.T) {
	lines := RenderRightBar(Dark, []RightBarWidget{{
		Extension: "tasked-phases",
		Title:     "p 0/1 | t 0/1",
		Lines: []string{
			"[>] Copper Lantern Setup  0/1",
			" [ ] Sort the leftover star stickers",
		},
	}}, 36, 8)
	plain := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(plain, " [>] Copper Lantern Setup  0/1") {
		t.Fatalf("phase row has the wrong indentation: %q", plain)
	}
	plainLines := strings.Split(plain, "\n")
	if len(plainLines) < 4 {
		t.Fatalf("checklist rows are missing: %q", plain)
	}
	expectedTask := truncateRightBarLine("  [ ] Sort the leftover star stickers", 36)
	if got := strings.TrimRight(plainLines[3], " "); got != expectedTask {
		t.Fatalf("task row = %q, want clipped row %q", got, expectedTask)
	}
	if !strings.Contains(strings.Join(lines, "\n"), Bold("Copper Lantern Setup")) {
		t.Fatalf("phase name is not bold: %q", lines)
	}
	if strings.Contains(strings.Join(lines, "\n"), Bold("Sort the leftover")) {
		t.Fatalf("task text should not be bold: %q", lines)
	}
	if len(plainLines) < 2 || !strings.HasPrefix(plainLines[0], "[tasked-phases] p 0/1 | t 0/1") || strings.TrimSpace(plainLines[1]) != "" {
		t.Fatalf("widget title is not followed by an empty row: %q", plain)
	}
}

func TestRenderRightBarDimsChecklistMarkers(t *testing.T) {
	lines := RenderRightBar(Dark, []RightBarWidget{{
		Extension: "plan",
		Lines:     []string{"[x] done", "[>] active", "[ ] pending"},
	}}, 24, 8)
	rendered := strings.Join(lines, "\n")
	for _, marker := range []string{"[x]", "[>]", "[ ]"} {
		if !strings.Contains(rendered, Dark.FG256(Dark.Muted, marker)) &&
			!strings.Contains(rendered, Dark.FG256(Dark.Muted, Dim(marker))) {
			t.Fatalf("marker %q was not muted: %q", marker, rendered)
		}
	}
	if !strings.Contains(stripANSI(rendered), " done") {
		t.Fatalf("checklist text was not kept readable: %q", rendered)
	}
}

func TestRenderRightBarDimsInactiveChecklistSections(t *testing.T) {
	lines := RenderRightBar(Dark, []RightBarWidget{{
		Extension: "plan",
		Lines: []string{
			"[x] finished phase",
			"  [x] finished task",
			"[>] active phase",
			"  [>] active task",
			"[ ] pending phase",
			"  [ ] pending task",
		},
	}}, 32, 12)
	for _, line := range lines {
		if strings.Contains(stripANSI(line), "active phase") || strings.Contains(stripANSI(line), "active task") {
			if strings.Contains(line, "\x1b[2m") {
				t.Fatalf("active checklist row was dimmed: %q", line)
			}
		}
	}
	plain := stripANSI(strings.Join(lines, "\n"))
	for _, text := range []string{"finished phase", "finished task", "pending phase", "pending task"} {
		if !strings.Contains(plain, text) {
			t.Fatalf("inactive checklist text %q missing: %q", text, plain)
		}
	}
	if !strings.Contains(strings.Join(lines, "\n"), "\x1b[2m") {
		t.Fatalf("inactive checklist rows were not dimmed: %q", lines)
	}
}

func TestJoinRightBarUsesOnlyMutedSeparator(t *testing.T) {
	line := JoinRightBar(Dark, "main", "right", 8, 8)
	if !strings.Contains(line, Dark.FG256(Dark.Muted, "│")) {
		t.Fatalf("separator is not muted: %q", line)
	}
	if strings.ContainsAny(stripANSI(line), "┌┐└┘─") {
		t.Fatalf("joined right bar added an outer rule: %q", line)
	}
}

func TestJoinRightBarPreservesCellBudgetForUnicode(t *testing.T) {
	line := JoinRightBar(Dark, "界", "✓ done", 8, 6)
	if got, want := visibleWidth(line), 8+RightBarSeparatorWidth+6; got != want {
		t.Fatalf("joined width = %d, want %d: %q", got, want, line)
	}
}

func TestRendererDrawRightBarUpdatesClearsAndResizes(t *testing.T) {
	var out bytes.Buffer
	r := NewRenderer(&out)
	r.SetTheme(Dark)
	r.Resize(80, 5)
	r.DrawRightBar(
		[]string{"chat"},
		[]string{"input"},
		RenderRightBar(Dark, []RightBarWidget{{Extension: "plan", Title: "Old", Lines: []string{"first"}}}, 26, 5),
		0,
		0,
	)
	if !strings.Contains(stripANSI(out.String()), "Old") {
		t.Fatalf("initial right-bar frame omitted widget: %q", out.String())
	}

	out.Reset()
	r.DrawRightBar(
		[]string{"chat"},
		[]string{"input"},
		RenderRightBar(Dark, []RightBarWidget{{Extension: "plan", Title: "Updated", Lines: []string{"second"}}}, 26, 5),
		0,
		0,
	)
	if !strings.Contains(stripANSI(out.String()), "Updated") {
		t.Fatalf("right-bar update did not repaint changed content: %q", out.String())
	}
	if r.logInit {
		t.Fatal("right-bar mode left flow renderer initialized")
	}

	out.Reset()
	r.DrawLog([]string{"chat"}, []string{"input"}, 0, 0)
	if !r.logInit {
		t.Fatal("flow renderer was not reinitialized after right-bar clearing")
	}

	out.Reset()
	r.Resize(100, 6)
	if out.Len() == 0 {
		t.Fatal("resize did not invalidate the right-bar frame")
	}
	main, rail, ok := RightBarColumns(100)
	if !ok {
		t.Fatal("right bar should fit after resize")
	}
	if main+RightBarSeparatorWidth+rail != 100 {
		t.Fatalf("resized column budget = %d", main+RightBarSeparatorWidth+rail)
	}
}
