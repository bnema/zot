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
		if width := visibleWidth(line); width > 24 {
			t.Fatalf("right bar line %d width = %d, want <= 24: %q", idx, width, line)
		}
	}
	joined := stripANSI(strings.Join(lines, "\n"))
	if strings.Index(joined, "[alpha] A") > strings.Index(joined, "[zeta] Z") {
		t.Fatalf("widgets are not deterministic by extension: %q", joined)
	}

	truncated := RenderRightBar(Dark, []RightBarWidget{
		{Extension: "alpha", ID: "large", Lines: []string{"a", "b", "c", "d", "e", "f"}},
	}, 24, 7)
	if !strings.Contains(stripANSI(strings.Join(truncated, "\n")), "right bar") {
		t.Fatalf("truncation marker missing: %q", truncated)
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
