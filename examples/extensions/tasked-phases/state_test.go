package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func stringPtr(value string) *string { return &value }
func boolPtr(value bool) *bool       { return &value }

func transition(t *testing.T, state PlanState, params toolArgs) PlanState {
	t.Helper()
	next, _, err := applyAction(ensureState(state), params)
	if err != nil {
		t.Fatalf("apply %s: %v", params.Action, err)
	}
	return ensureState(next)
}

func sampleState() PlanState {
	return ensureState(PlanState{
		Version: 1,
		Spec:    "Ship compact tasked phases output without losing source-of-truth state.",
		Phases: []Phase{
			{
				ID:    "phase-1",
				Title: "Finished discovery",
				Goal:  "Understand the old behavior",
				Tasks: []PhaseTask{
					{ID: "task-1", Text: "Completed discovery task", Checked: true},
					{ID: "task-2", Text: "Completed contract task", Checked: true},
				},
			},
			{
				ID:    "phase-2",
				Title: "Implement compact output",
				Goal:  "Reduce repeated context",
				Tasks: []PhaseTask{
					{ID: "task-3", Text: "Completed implementation task", Checked: true},
					{ID: "task-4", Text: "Remaining implementation task", Checked: false},
				},
			},
		},
		CurrentPhaseID:  "phase-2",
		NextPhaseNumber: 3,
		NextTaskNumber:  5,
	})
}

func TestTransitionsGenerateIDsAndSelectCurrentPhase(t *testing.T) {
	state := transition(t, createEmptyState(), toolArgs{
		Action: actionReplacePlan,
		Phases: &[]PhaseInput{
			{Title: "First", Tasks: &[]TaskInput{{Text: "one"}}},
			{Title: "Second", Tasks: &[]TaskInput{{Text: "two"}}},
		},
	})
	if got := state.Phases[0].ID; got != "phase-1" {
		t.Fatalf("first generated phase id = %q", got)
	}
	if got := state.Phases[1].ID; got != "phase-2" {
		t.Fatalf("second generated phase id = %q", got)
	}
	if got := state.Phases[0].Tasks[0].ID; got != "task-1" {
		t.Fatalf("first generated task id = %q", got)
	}
	if state.CurrentPhaseID != "phase-1" || state.NextPhaseNumber != 3 || state.NextTaskNumber != 3 {
		t.Fatalf("generated counters/current phase = %q, %d, %d", state.CurrentPhaseID, state.NextPhaseNumber, state.NextTaskNumber)
	}

	state = transition(t, state, toolArgs{Action: actionSetTaskChecked, TaskID: "task-1", Checked: boolPtr(true)})
	if state.CurrentPhaseID != "phase-2" {
		t.Fatalf("current phase did not advance to the first incomplete phase: %q", state.CurrentPhaseID)
	}
	state = transition(t, state, toolArgs{Action: actionAddPhase, PhaseTitle: stringPtr("Third")})
	if got := state.Phases[2].ID; got != "phase-3" {
		t.Fatalf("added phase id = %q", got)
	}
	state = transition(t, state, toolArgs{Action: actionAddTask, PhaseID: "phase-3", TaskText: stringPtr("three")})
	if got := state.Phases[2].Tasks[0].ID; got != "task-3" {
		t.Fatalf("added task id = %q", got)
	}
}

func TestReplacePlanRejectsDuplicateIDs(t *testing.T) {
	state := createEmptyState()
	_, _, err := applyAction(state, toolArgs{
		Action: actionReplacePlan,
		Phases: &[]PhaseInput{
			{ID: stringPtr("phase-1"), Title: "First"},
			{ID: stringPtr("phase-1"), Title: "Duplicate"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate phase id") {
		t.Fatalf("duplicate phase id error = %v", err)
	}

	_, _, err = applyAction(state, toolArgs{
		Action: actionReplacePlan,
		Phases: &[]PhaseInput{
			{Title: "First", Tasks: &[]TaskInput{{ID: stringPtr("task-1"), Text: "one"}}},
			{Title: "Second", Tasks: &[]TaskInput{{ID: stringPtr("task-1"), Text: "duplicate"}}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate task id") {
		t.Fatalf("duplicate task id error = %v", err)
	}
}

func TestClosedPlanRulesAndReopen(t *testing.T) {
	state := transition(t, createEmptyState(), toolArgs{
		Action: actionReplacePlan,
		Phases: &[]PhaseInput{{Title: "Done", Tasks: &[]TaskInput{{Text: "finished", Checked: boolPtr(true)}}}},
	})
	if !isPlanClosed(state) || state.CurrentPhaseID != "" {
		t.Fatalf("complete plan was not closed: %+v", state)
	}
	if !strings.Contains(buildSummary(state), "Plan closed: Completed 1/1 tasks across 1 phase(s).") {
		t.Fatalf("closed summary missing: %s", buildSummary(state))
	}

	if _, _, err := applyAction(state, toolArgs{Action: actionAddPhase, PhaseTitle: stringPtr("new")}); err == nil || !strings.Contains(err.Error(), "already complete and closed") {
		t.Fatalf("add to closed plan error = %v", err)
	}
	if _, _, err := applyAction(state, toolArgs{Action: actionReplacePlan, Phases: &[]PhaseInput{}}); err == nil {
		t.Fatal("replace_plan unexpectedly bypassed closed-plan guard")
	}

	state = transition(t, state, toolArgs{Action: actionSetTaskChecked, TaskID: "task-1", Checked: boolPtr(false)})
	if isPlanClosed(state) || state.CurrentPhaseID != "phase-1" || state.Phases[0].Tasks[0].Checked {
		t.Fatalf("unchecked reopen produced %+v", state)
	}

	state = transition(t, state, toolArgs{Action: actionSetSpec, Spec: stringPtr("new work")})
	if len(state.Phases) != 1 {
		t.Fatalf("set_spec on reopened plan unexpectedly cleared phases")
	}
	closed := transition(t, state, toolArgs{Action: actionSetTaskChecked, TaskID: "task-1", Checked: boolPtr(true)})
	if !isPlanClosed(closed) {
		t.Fatal("plan did not close again")
	}
	state = transition(t, closed, toolArgs{Action: actionSetSpec, Spec: stringPtr("fresh work")})
	if len(state.Phases) != 0 || state.Spec != "fresh work" || state.NextPhaseNumber != 1 || state.NextTaskNumber != 1 {
		t.Fatalf("set_spec did not restart a closed plan: %+v", state)
	}
	state = transition(t, state, toolArgs{Action: actionReplacePlan, Phases: &[]PhaseInput{{Title: "Fresh"}}})
	if len(state.Phases) != 1 || isPlanClosed(state) {
		t.Fatalf("replace after set_spec failed: %+v", state)
	}
}

func TestPhaseLookupAcceptsRawBracketedSummaryAndUniqueTitle(t *testing.T) {
	state := sampleState()
	for _, input := range []string{
		"phase-2",
		"[phase-2]",
		"Implement compact output [phase-2]",
		"Implement compact output [phase-2] (1 remaining) - Reduce repeated context",
		"Implement compact output",
	} {
		if phase := findPhase(state, input); phase == nil || phase.ID != "phase-2" {
			t.Fatalf("findPhase(%q) = %#v", input, phase)
		}
	}
	if findPhase(state, "missing") != nil || findPhase(state, "") != nil {
		t.Fatal("missing phase lookup unexpectedly matched")
	}
	duplicate := cloneState(state)
	duplicate.Phases[0].Title = "Duplicate"
	duplicate.Phases[1].Title = "Duplicate"
	if findPhase(duplicate, "Duplicate") != nil {
		t.Fatal("ambiguous title unexpectedly matched")
	}
	if phase := findPhase(duplicate, "phase-2"); phase == nil || phase.ID != "phase-2" {
		t.Fatal("raw id did not win over duplicate title")
	}
}

func TestSummariesAreFullOrCompactAsAppropriate(t *testing.T) {
	state := sampleState()
	compact := buildToolResultText(actionSetTaskChecked, state, "Checked task task-3", nil)
	if !strings.Contains(compact, "Progress: 3/4 tasks checked") || !strings.Contains(compact, "[ ] Remaining implementation task [task-4]") {
		t.Fatalf("compact summary omitted useful active state: %s", compact)
	}
	if strings.Contains(compact, "Phases:") || strings.Contains(compact, "Completed discovery task") {
		t.Fatalf("compact summary included full checklist: %s", compact)
	}
	full := buildToolResultText(actionGetStatus, state, "Current phased plan status", nil)
	if !strings.Contains(full, "Phases:") || !strings.Contains(full, "Completed discovery task [task-1]") {
		t.Fatalf("full summary omitted checklist: %s", full)
	}
	if !strings.Contains(buildSummary(state), "[phase-2] (1/2) - Reduce repeated context") {
		t.Fatal("phase goal/progress missing from full summary")
	}
}

func TestTurnContextContainsOnlyCurrentFocus(t *testing.T) {
	state := sampleState()
	state.Phases[1].Tasks = append(state.Phases[1].Tasks,
		PhaseTask{ID: "task-5", Text: "Next focused task"},
		PhaseTask{ID: "task-6", Text: "Another focused task"},
		PhaseTask{ID: "task-7", Text: "Deferred focused task"},
	)
	state.Phases = append(state.Phases, Phase{
		ID:    "phase-3",
		Title: "Future phase",
		Tasks: []PhaseTask{{ID: "task-8", Text: "Future task"}},
	})
	context := buildTurnContext(state)
	for _, want := range []string{
		"Tasked phases focus:",
		"Current phase: Implement compact output [phase-2]",
		"Goal: Reduce repeated context",
		"[ ] Remaining implementation task [task-4]",
		"[ ] Next focused task [task-5]",
		"[ ] Another focused task [task-6]",
	} {
		if !strings.Contains(context, want) {
			t.Errorf("turn context missing %q: %s", want, context)
		}
	}
	for _, unwanted := range []string{
		"Ship compact tasked phases output",
		"Finished discovery",
		"Completed discovery task",
		"Future phase",
		"Future task",
		"Deferred focused task [task-7]",
	} {
		if strings.Contains(context, unwanted) {
			t.Errorf("turn context included %q: %s", unwanted, context)
		}
	}
}

func TestTurnContextIsBoundedForUntrustedIdentifiers(t *testing.T) {
	state := sampleState()
	state.Phases[1].ID = strings.Repeat("phase-id-", 1000)
	state.CurrentPhaseID = state.Phases[1].ID
	state.Phases[1].Tasks[0].ID = strings.Repeat("task-id-", 1000)

	context := buildTurnContext(state)
	if got := len([]rune(context)); got > maxTurnContextChars {
		t.Fatalf("turn context length = %d; want at most %d", got, maxTurnContextChars)
	}
	if !strings.Contains(context, "Tasked phases focus:") || !strings.Contains(context, "Current phase:") || !strings.Contains(context, "Goal: Reduce repeated context") {
		t.Fatalf("bounded turn context lost its useful focus: %q", context)
	}
}

func TestPanelRendersSpecGoalsTasksAndEmptyState(t *testing.T) {
	empty := strings.Join(buildViewLines(createEmptyState()), "\n")
	if !strings.Contains(empty, "No spec or phases stored yet.") || !strings.Contains(empty, "Press Escape to close") {
		t.Fatalf("empty panel = %s", empty)
	}
	panel := strings.Join(buildViewLines(sampleState()), "\n")
	for _, want := range []string{
		"Tasked phases",
		"Spec",
		"Progress: 3/4 tasks checked",
		"[x] Completed discovery task [task-1]",
		"[ ] Remaining implementation task [task-4]",
		"Understand the old behavior",
		"Press Escape to close",
	} {
		if !strings.Contains(panel, want) {
			t.Errorf("panel missing %q: %s", want, panel)
		}
	}
}

func TestStatePersistenceIsProjectScopedAtomicAndRestrictive(t *testing.T) {
	dataDir := t.TempDir()
	first := newStateStore()
	if err := first.load(dataDir, "/projects/one"); err != nil {
		t.Fatal(err)
	}
	state, _, err := first.transact(func(current PlanState) (PlanState, string, error) {
		return applyAction(current, toolArgs{
			Action: actionReplacePlan,
			Phases: &[]PhaseInput{{Title: "Persisted", Tasks: &[]TaskInput{{Text: "remember me"}}}},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Phases[0].ID != "phase-1" {
		t.Fatalf("persisted state = %+v", state)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(first.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("state file mode = %o, want 600", got)
		}
		if got := (mustStat(t, filepath.Dir(first.path))).Mode().Perm(); got != 0o700 {
			t.Fatalf("state directory mode = %o, want 700", got)
		}
	}

	second := newStateStore()
	if err := second.load(dataDir, "/projects/one"); err != nil {
		t.Fatal(err)
	}
	loaded, err := second.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Phases[0].Tasks[0].Text != "remember me" {
		t.Fatalf("loaded state = %+v", loaded)
	}
	otherProject := newStateStore()
	if err := otherProject.load(dataDir, "/projects/two"); err != nil {
		t.Fatal(err)
	}
	other, err := otherProject.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if hasStoredPlan(other) {
		t.Fatal("state leaked between project CWDs")
	}
	files, err := os.ReadDir(filepath.Dir(first.path))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".tmp") {
			t.Fatalf("temporary file left behind: %s", file.Name())
		}
	}

	var decoded PlanState
	data, err := os.ReadFile(first.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Phases[0].Tasks[0].ID != "task-1" {
		t.Fatalf("on-disk state = %+v", decoded)
	}
}

func TestStateStoreSerializesConcurrentTransitions(t *testing.T) {
	store := newStateStore()
	if err := store.load(t.TempDir(), "/projects/concurrent"); err != nil {
		t.Fatal(err)
	}
	const count = 12
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			title := "phase " + string(rune('a'+i))
			_, _, err := store.transact(func(current PlanState) (PlanState, string, error) {
				return applyAction(current, toolArgs{Action: actionAddPhase, PhaseTitle: stringPtr(title)})
			})
			if err != nil {
				t.Errorf("concurrent transition: %v", err)
			}
		}(i)
	}
	wg.Wait()
	state, err := store.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Phases) != count {
		t.Fatalf("serialized phase count = %d, want %d", len(state.Phases), count)
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
