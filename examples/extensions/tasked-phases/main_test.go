package main

import (
	"strings"
	"testing"

	"github.com/patriceckhart/zot/packages/agent/ext"
)

func TestBuildChromeHidesEmptyPlan(t *testing.T) {
	chrome, visible := buildChrome(createEmptyState())
	if visible {
		t.Fatal("empty plan should not publish persistent chrome")
	}
	if chrome.summary != "" || len(chrome.lines) != 0 {
		t.Fatalf("empty chrome = %#v, want zero content", chrome)
	}
}

func TestBuildChromeUsesCompactSummaryWithoutSpecText(t *testing.T) {
	state := PlanState{
		Version: 1,
		Spec:    "private spec text must not be persistent chrome",
		Phases: []Phase{
			{
				ID:    "phase-1",
				Title: "Finished discovery",
				Tasks: []PhaseTask{{ID: "task-1", Text: "Completed discovery", Checked: true}},
			},
			{
				ID:    "phase-2",
				Title: "Implement compact output",
				Tasks: []PhaseTask{
					{ID: "task-2", Text: "Completed implementation", Checked: true},
					{ID: "task-3", Text: "Remaining implementation task"},
				},
			},
		},
		CurrentPhaseID: "phase-2",
	}

	chrome, visible := buildChrome(state)
	if !visible {
		t.Fatal("stored plan should publish persistent chrome")
	}
	if want := "p 1/2 | t 2/3"; chrome.summary != want {
		t.Fatalf("chrome summary = %q, want %q", chrome.summary, want)
	}
	if strings.Contains(chrome.summary, state.Spec) || strings.Contains(chrome.summary, state.Phases[1].Title) {
		t.Fatalf("compact summary leaked plan text: %q", chrome.summary)
	}
	joined := strings.Join(chrome.lines, "\n")
	for _, want := range []string{
		"[x] Finished discovery  1/1",
		" [x] Completed discovery",
		"[>] Implement compact output  1/2",
		" [>] Remaining implementation task",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("checklist missing %q: %s", want, joined)
		}
	}
}

func TestBuildChromeHidesCompletePlan(t *testing.T) {
	closedAt := int64(1)
	state := PlanState{
		Version: 1,
		Phases: []Phase{{
			ID:    "phase-1",
			Title: "Ship",
			Tasks: []PhaseTask{{ID: "task-1", Text: "Done", Checked: true}},
		}},
		ClosedAt: &closedAt,
	}

	if got, want := buildChromeSummary(state), "p 1/1 | t 1/1"; got != want {
		t.Fatalf("closed chrome summary = %q, want %q", got, want)
	}
	if _, visible := buildChrome(state); visible {
		t.Fatal("complete plan should not publish persistent chrome")
	}
}

func TestBuildChromeHidesCompletePlanBeforeCloseMetadata(t *testing.T) {
	state := PlanState{
		Version: 1,
		Spec:    "complete plan",
		Phases: []Phase{{
			ID:    "phase-1",
			Title: "Ship",
			Tasks: []PhaseTask{{ID: "task-1", Text: "Done", Checked: true}},
		}},
	}

	if _, visible := buildChrome(state); visible {
		t.Fatal("complete plan without close metadata should not publish persistent chrome")
	}
}

type chromeRecorder struct {
	statusSet       int
	statusClear     int
	statusTexts     []string
	widgetSet       int
	widgetClear     int
	widgetPositions []string
	widgetTitles    []string
	widgetLines     [][]string
}

func (r *chromeRecorder) SetStatus(_, text string) {
	r.statusSet++
	r.statusTexts = append(r.statusTexts, text)
}
func (r *chromeRecorder) ClearStatus(string) { r.statusClear++ }
func (r *chromeRecorder) SetWidget(_, position, title string, lines []string) {
	r.widgetSet++
	r.widgetPositions = append(r.widgetPositions, position)
	r.widgetTitles = append(r.widgetTitles, title)
	r.widgetLines = append(r.widgetLines, append([]string(nil), lines...))
}
func (r *chromeRecorder) ClearWidget(string) { r.widgetClear++ }

func TestApplyChromePublishesCompactStatusAndTitle(t *testing.T) {
	recorder := &chromeRecorder{}
	state := PlanState{
		Version: 1,
		Spec:    "must not be shown",
		Phases: []Phase{
			{
				ID:    "phase-1",
				Title: "Finished",
				Tasks: []PhaseTask{{ID: "task-1", Text: "Ship", Checked: true}},
			},
			{
				ID:    "phase-2",
				Title: "Pending",
				Tasks: []PhaseTask{{ID: "task-2", Text: "Review"}},
			},
		},
	}
	applyChrome(recorder, state)

	if recorder.widgetSet != 1 || len(recorder.widgetPositions) != 1 || recorder.widgetPositions[0] != ext.WidgetPositionRightBar {
		t.Fatalf("widget placement = %v, want %q", recorder.widgetPositions, ext.WidgetPositionRightBar)
	}
	want := buildChromeSummary(state)
	if len(recorder.statusTexts) != 1 || recorder.statusTexts[0] != want {
		t.Fatalf("status updates = %v, want %q", recorder.statusTexts, want)
	}
	if len(recorder.widgetTitles) != 1 || recorder.widgetTitles[0] != want {
		t.Fatalf("widget titles = %v, want %q", recorder.widgetTitles, want)
	}
	if len(recorder.widgetLines) != 1 || len(recorder.widgetLines[0]) == 0 {
		t.Fatalf("widget lines = %v, want checklist rows", recorder.widgetLines)
	}
	if !strings.Contains(strings.Join(recorder.widgetLines[0], "\n"), "[>] Review") {
		t.Fatalf("active task is missing from widget lines: %v", recorder.widgetLines[0])
	}
}

func TestApplyChromeClearsEmptyPlan(t *testing.T) {
	recorder := &chromeRecorder{}
	applyChrome(recorder, createEmptyState())

	if recorder.statusClear != 1 || recorder.widgetClear != 1 {
		t.Fatalf("empty plan clear operations = status %d, widget %d; want one each", recorder.statusClear, recorder.widgetClear)
	}
	if recorder.statusSet != 0 || recorder.widgetSet != 0 {
		t.Fatalf("empty plan published updates = status %d, widget %d; want none", recorder.statusSet, recorder.widgetSet)
	}
}

func TestApplyChromeClearsCompletedPlan(t *testing.T) {
	recorder := &chromeRecorder{}
	state := PlanState{
		Version: 1,
		Phases: []Phase{{
			ID:    "phase-1",
			Title: "Ship",
			Tasks: []PhaseTask{{ID: "task-1", Text: "Done", Checked: true}},
		}},
	}
	applyChrome(recorder, state)

	if recorder.statusClear != 1 || recorder.widgetClear != 1 {
		t.Fatalf("complete plan clear operations = status %d, widget %d; want one each", recorder.statusClear, recorder.widgetClear)
	}
	if recorder.statusSet != 0 || recorder.widgetSet != 0 {
		t.Fatalf("complete plan published updates = status %d, widget %d; want none", recorder.statusSet, recorder.widgetSet)
	}
}

func TestApplyChromeShowsEmptySummaryWithoutSpecText(t *testing.T) {
	recorder := &chromeRecorder{}
	state := PlanState{Version: 1, Spec: "spec only"}
	applyChrome(recorder, state)

	if recorder.statusClear != 0 || recorder.widgetClear != 0 || recorder.statusSet != 1 || recorder.widgetSet != 1 {
		t.Fatalf("spec-only chrome lifecycle = %#v, want a compact update", recorder)
	}
	if got, want := recorder.statusTexts[0], "p 0/0 | t 0/0"; got != want {
		t.Fatalf("spec-only status = %q, want %q", got, want)
	}
	if got, want := recorder.widgetTitles[0], "p 0/0 | t 0/0"; got != want {
		t.Fatalf("spec-only widget title = %q, want %q", got, want)
	}
	if len(recorder.widgetLines[0]) != 1 || recorder.widgetLines[0][0] != "No phases yet" {
		t.Fatalf("spec-only widget lines = %v, want empty-plan hint", recorder.widgetLines[0])
	}
}
