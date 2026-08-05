package main

import (
	"strings"
	"testing"
)

func TestBuildChromeHidesEmptyPlan(t *testing.T) {
	chrome, visible := buildChrome(createEmptyState())
	if visible {
		t.Fatal("empty plan should not publish persistent chrome")
	}
	if chrome.status != "" || len(chrome.lines) != 0 {
		t.Fatalf("empty chrome = %#v, want zero content", chrome)
	}
}

func TestBuildChromeShowsStoredPlan(t *testing.T) {
	state := PlanState{
		Version: 1,
		Spec:    "Ship the compact view",
		Phases: []Phase{{
			ID:    "phase-1",
			Title: "Implement",
			Tasks: []PhaseTask{{ID: "task-1", Text: "Hide empty state"}},
		}},
	}

	chrome, visible := buildChrome(state)
	if !visible {
		t.Fatal("stored plan should publish persistent chrome")
	}
	if chrome.status != "0/1 tasks checked" {
		t.Fatalf("chrome status = %q, want %q", chrome.status, "0/1 tasks checked")
	}
	joined := strings.Join(chrome.lines, "\n")
	for _, want := range []string{"Spec: Ship the compact view", "Progress: 0/1 tasks checked", "Current phase: Implement"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("chrome lines missing %q: %s", want, joined)
		}
	}
}

type chromeRecorder struct {
	statusSet   int
	statusClear int
	widgetSet   int
	widgetClear int
}

func (r *chromeRecorder) SetStatus(string, string) { r.statusSet++ }
func (r *chromeRecorder) ClearStatus(string)       { r.statusClear++ }
func (r *chromeRecorder) SetWidget(string, string, string, []string) {
	r.widgetSet++
}
func (r *chromeRecorder) ClearWidget(string) { r.widgetClear++ }

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
