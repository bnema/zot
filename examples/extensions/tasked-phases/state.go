package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	actionGetStatus       = "get_status"
	actionSetSpec         = "set_spec"
	actionReplacePlan     = "replace_plan"
	actionAddPhase        = "add_phase"
	actionUpdatePhase     = "update_phase"
	actionRemovePhase     = "remove_phase"
	actionAddTask         = "add_task"
	actionUpdateTask      = "update_task"
	actionRemoveTask      = "remove_task"
	actionSetCurrentPhase = "set_current_phase"
	actionSetTaskChecked  = "set_task_checked"
	actionSetPhaseChecked = "set_phase_checked"
	actionClear           = "clear"
)

var (
	phaseIDPattern     = regexp.MustCompile(`^phase-(\d+)$`)
	taskIDPattern      = regexp.MustCompile(`^task-(\d+)$`)
	bracketedIDPattern = regexp.MustCompile(`\[([^\]]+)\]`)
)

var toolActions = []string{
	actionGetStatus,
	actionSetSpec,
	actionReplacePlan,
	actionAddPhase,
	actionUpdatePhase,
	actionRemovePhase,
	actionAddTask,
	actionUpdateTask,
	actionRemoveTask,
	actionSetCurrentPhase,
	actionSetTaskChecked,
	actionSetPhaseChecked,
	actionClear,
}

var closedPlanBlockedActions = map[string]bool{
	actionReplacePlan:     true,
	actionAddPhase:        true,
	actionUpdatePhase:     true,
	actionRemovePhase:     true,
	actionAddTask:         true,
	actionUpdateTask:      true,
	actionRemoveTask:      true,
	actionSetCurrentPhase: true,
	actionSetTaskChecked:  true,
	actionSetPhaseChecked: true,
}

type PhaseTask struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Checked bool   `json:"checked"`
}

type Phase struct {
	ID    string      `json:"id"`
	Title string      `json:"title"`
	Goal  string      `json:"goal,omitempty"`
	Tasks []PhaseTask `json:"tasks"`
}

type PlanState struct {
	Version         int     `json:"version"`
	Spec            string  `json:"spec,omitempty"`
	Phases          []Phase `json:"phases"`
	CurrentPhaseID  string  `json:"currentPhaseId,omitempty"`
	ClosedAt        *int64  `json:"closedAt,omitempty"`
	ClosedSummary   string  `json:"closedSummary,omitempty"`
	NextPhaseNumber int     `json:"nextPhaseNumber"`
	NextTaskNumber  int     `json:"nextTaskNumber"`
	UpdatedAt       int64   `json:"updatedAt"`
}

type TaskInput struct {
	ID      *string `json:"id"`
	Text    string  `json:"text"`
	Checked *bool   `json:"checked"`
}

type PhaseInput struct {
	ID    *string      `json:"id"`
	Title string       `json:"title"`
	Goal  *string      `json:"goal"`
	Tasks *[]TaskInput `json:"tasks"`
}

type toolArgs struct {
	Action     string        `json:"action"`
	Spec       *string       `json:"spec"`
	PhaseID    string        `json:"phaseId"`
	PhaseTitle *string       `json:"phaseTitle"`
	PhaseGoal  *string       `json:"phaseGoal"`
	TaskID     string        `json:"taskId"`
	TaskText   *string       `json:"taskText"`
	Checked    *bool         `json:"checked"`
	Phases     *[]PhaseInput `json:"phases"`
}

func nowMillis() int64 { return time.Now().UnixMilli() }

func createEmptyState() PlanState {
	return PlanState{
		Version:         1,
		Phases:          []Phase{},
		NextPhaseNumber: 1,
		NextTaskNumber:  1,
		UpdatedAt:       nowMillis(),
	}
}

func cloneState(state PlanState) PlanState {
	cloned := state
	cloned.Phases = make([]Phase, len(state.Phases))
	for i, phase := range state.Phases {
		cloned.Phases[i] = phase
		cloned.Phases[i].Tasks = append([]PhaseTask(nil), phase.Tasks...)
	}
	return cloned
}

func normalizeOptionalText(value string) string {
	return strings.TrimSpace(value)
}

// sanitizeDisplayText removes terminal escape sequences and non-printing
// control characters at the rendering boundary. Persisted state is never
// modified, so the original extension-supplied text remains recoverable.
func sanitizeDisplayText(text string) string {
	var out strings.Builder
	for index := 0; index < len(text); {
		if text[index] == 0x1b {
			index++
			if index >= len(text) {
				break
			}
			switch text[index] {
			case '[': // CSI sequence.
				index++
				for index < len(text) {
					final := text[index]
					index++
					if final >= 0x40 && final <= 0x7e {
						break
					}
				}
			case ']': // OSC sequence, terminated by BEL or ST.
				index++
				for index < len(text) {
					if text[index] == 0x07 {
						index++
						break
					}
					if text[index] == 0x1b && index+1 < len(text) && text[index+1] == '\\' {
						index += 2
						break
					}
					index++
				}
			default:
				index++
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(text[index:])
		index += size
		if r != '\n' && (unicode.IsControl(r) || r == 0x7f) {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func singleLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func displaySingleLine(text string) string {
	return singleLine(sanitizeDisplayText(text))
}

func truncatePlain(text string, maxLength int) string {
	if maxLength < 1 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxLength {
		return text
	}
	return strings.TrimRight(string(runes[:maxLength-1]), " \t\r\n") + "…"
}

func getPhaseProgress(phase Phase) (done, total int) {
	total = len(phase.Tasks)
	for _, task := range phase.Tasks {
		if task.Checked {
			done++
		}
	}
	return done, total
}

func getPlanProgress(state PlanState) (done, total int) {
	for _, phase := range state.Phases {
		phaseDone, phaseTotal := getPhaseProgress(phase)
		done += phaseDone
		total += phaseTotal
	}
	return done, total
}

func isPhaseDone(phase Phase) bool {
	done, total := getPhaseProgress(phase)
	return total > 0 && done == total
}

func hasStoredPlan(state PlanState) bool {
	return state.Spec != "" || len(state.Phases) > 0
}

func isPlanComplete(state PlanState) bool {
	if len(state.Phases) == 0 {
		return false
	}
	for _, phase := range state.Phases {
		if !isPhaseDone(phase) {
			return false
		}
	}
	return true
}

func isPlanClosed(state PlanState) bool { return state.ClosedAt != nil }

func closePlanIfComplete(state *PlanState) {
	if !isPlanComplete(*state) {
		return
	}
	done, total := getPlanProgress(*state)
	if state.ClosedAt == nil {
		closedAt := nowMillis()
		state.ClosedAt = &closedAt
	}
	state.ClosedSummary = fmt.Sprintf("Completed %d/%d tasks across %d phase(s).", done, total, len(state.Phases))
	state.CurrentPhaseID = ""
}

func getCurrentPhase(state PlanState) *Phase {
	if state.CurrentPhaseID == "" {
		return nil
	}
	for i := range state.Phases {
		if state.Phases[i].ID == state.CurrentPhaseID {
			return &state.Phases[i]
		}
	}
	return nil
}

func getActivePhase(state PlanState) *Phase {
	if current := getCurrentPhase(state); current != nil {
		return current
	}
	for i := range state.Phases {
		if !isPhaseDone(state.Phases[i]) {
			return &state.Phases[i]
		}
	}
	return nil
}

func getSuggestedCurrentPhaseID(state PlanState) string {
	if state.CurrentPhaseID != "" {
		for _, phase := range state.Phases {
			if phase.ID == state.CurrentPhaseID {
				return state.CurrentPhaseID
			}
		}
	}
	for _, phase := range state.Phases {
		if !isPhaseDone(phase) {
			return phase.ID
		}
	}
	if len(state.Phases) > 0 {
		return state.Phases[len(state.Phases)-1].ID
	}
	return ""
}

func extractHighestIDNumber(prefix string, ids []string) int {
	highest := 0
	pattern := taskIDPattern
	if prefix == "phase" {
		pattern = phaseIDPattern
	}
	for _, id := range ids {
		match := pattern.FindStringSubmatch(id)
		if len(match) != 2 {
			continue
		}
		number, err := strconv.Atoi(match[1])
		if err == nil && number > highest {
			highest = number
		}
	}
	return highest
}

func ensureState(state PlanState) PlanState {
	normalized := cloneState(state)
	normalized.Version = 1
	normalized.Spec = normalizeOptionalText(normalized.Spec)
	if normalized.Phases == nil {
		normalized.Phases = []Phase{}
	}
	for i := range normalized.Phases {
		normalized.Phases[i].Goal = optionalNormalized(normalized.Phases[i].Goal)
		if normalized.Phases[i].Tasks == nil {
			normalized.Phases[i].Tasks = []PhaseTask{}
		}
	}
	normalized.ClosedSummary = optionalNormalized(normalized.ClosedSummary)
	if isPlanComplete(normalized) {
		closePlanIfComplete(&normalized)
	} else {
		normalized.ClosedAt = nil
		normalized.ClosedSummary = ""
	}

	highestPhase := make([]string, 0, len(normalized.Phases))
	highestTask := make([]string, 0)
	for _, phase := range normalized.Phases {
		highestPhase = append(highestPhase, phase.ID)
		for _, task := range phase.Tasks {
			highestTask = append(highestTask, task.ID)
		}
	}
	if normalized.NextPhaseNumber < 1 {
		normalized.NextPhaseNumber = 1
	}
	if normalized.NextTaskNumber < 1 {
		normalized.NextTaskNumber = 1
	}
	normalized.NextPhaseNumber = maxInt(normalized.NextPhaseNumber, extractHighestIDNumber("phase", highestPhase)+1)
	normalized.NextTaskNumber = maxInt(normalized.NextTaskNumber, extractHighestIDNumber("task", highestTask)+1)
	if isPlanClosed(normalized) {
		normalized.CurrentPhaseID = ""
	} else {
		normalized.CurrentPhaseID = getSuggestedCurrentPhaseID(normalized)
	}
	normalized.UpdatedAt = nowMillis()
	return normalized
}

func optionalNormalized(value string) string {
	return normalizeOptionalText(value)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func nextPhaseID(state *PlanState) string {
	id := fmt.Sprintf("phase-%d", state.NextPhaseNumber)
	state.NextPhaseNumber++
	return id
}

func nextTaskID(state *PlanState) string {
	id := fmt.Sprintf("task-%d", state.NextTaskNumber)
	state.NextTaskNumber++
	return id
}

func extractBracketedPhaseID(value string) string {
	matches := bracketedIDPattern.FindAllStringSubmatch(value, -1)
	candidate := ""
	for _, match := range matches {
		if len(match) == 2 {
			if normalized := normalizeOptionalText(match[1]); normalized != "" {
				candidate = normalized
			}
		}
	}
	return candidate
}

func findUniquePhaseByTitle(state PlanState, phaseTitle string) *Phase {
	normalizedTitle := singleLine(phaseTitle)
	var match *Phase
	matches := 0
	for i := range state.Phases {
		if singleLine(state.Phases[i].Title) == normalizedTitle {
			match = &state.Phases[i]
			matches++
		}
	}
	if matches == 1 {
		return match
	}
	return nil
}

// findPhase returns a pointer into state.Phases, aliasing the caller's slice
// backing array so mutations are applied to the caller's state copy.
func findPhase(state PlanState, phaseID string) *Phase {
	candidate := normalizeOptionalText(phaseID)
	if candidate == "" {
		return nil
	}
	for i := range state.Phases {
		if state.Phases[i].ID == candidate {
			return &state.Phases[i]
		}
	}
	if bracketedID := extractBracketedPhaseID(candidate); bracketedID != "" {
		for i := range state.Phases {
			if state.Phases[i].ID == bracketedID {
				return &state.Phases[i]
			}
		}
	}
	return findUniquePhaseByTitle(state, candidate)
}

func formatValidPhaseIDs(state PlanState) string {
	if len(state.Phases) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(state.Phases))
	for _, phase := range state.Phases {
		parts = append(parts, fmt.Sprintf("%s (%s)", phase.ID, singleLine(phase.Title)))
	}
	return strings.Join(parts, ", ")
}

func phaseIDError(state PlanState, action, phaseID string) string {
	candidate := normalizeOptionalText(phaseID)
	validIDs := formatValidPhaseIDs(state)
	if candidate == "" {
		return fmt.Sprintf("phaseId is required for %s. Use a valid phase id. Valid phase ids: %s.", action, validIDs)
	}
	return fmt.Sprintf("phaseId %q was not found for %s. Use the raw id from brackets without brackets. Valid phase ids: %s.", singleLine(candidate), action, validIDs)
}

func formatValidTaskIDs(state PlanState) string {
	parts := make([]string, 0)
	for _, phase := range state.Phases {
		for _, task := range phase.Tasks {
			if !task.Checked {
				parts = append(parts, fmt.Sprintf("%s (%s)", task.ID, displaySingleLine(phase.Title)))
			}
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func taskIDError(state PlanState, action, taskID string) string {
	candidate := normalizeOptionalText(taskID)
	validIDs := formatValidTaskIDs(state)
	if candidate == "" {
		return fmt.Sprintf("taskId is required for %s. Use a valid task id. Valid incomplete task ids: %s.", action, validIDs)
	}
	return fmt.Sprintf("taskId %q was not found for %s. Use the task id from brackets. Valid incomplete task ids: %s.", displaySingleLine(candidate), action, validIDs)
}

// findTask returns pointers into state.Phases and their Tasks, aliasing the
// caller's slice backing arrays so mutations are applied to the caller's state.
func findTask(state PlanState, taskID, phaseID string) (phase *Phase, task *PhaseTask) {
	if taskID == "" {
		return nil, nil
	}
	if phaseID != "" {
		phase = findPhase(state, phaseID)
		if phase == nil {
			return nil, nil
		}
		for i := range phase.Tasks {
			if phase.Tasks[i].ID == taskID {
				return phase, &phase.Tasks[i]
			}
		}
		return nil, nil
	}
	for i := range state.Phases {
		for j := range state.Phases[i].Tasks {
			if state.Phases[i].Tasks[j].ID == taskID {
				return &state.Phases[i], &state.Phases[i].Tasks[j]
			}
		}
	}
	return nil, nil
}

func buildPhaseFromInput(state *PlanState, input PhaseInput, phaseIDs, taskIDs map[string]struct{}) (Phase, error) {
	id := ""
	if input.ID != nil {
		id = normalizeOptionalText(*input.ID)
	}
	if id == "" {
		id = nextPhaseID(state)
	}
	if _, exists := phaseIDs[id]; exists {
		return Phase{}, fmt.Errorf("duplicate phase id %q", id)
	}
	phaseIDs[id] = struct{}{}
	phase := Phase{
		ID:    id,
		Title: strings.TrimSpace(input.Title),
		Goal:  optionalInputText(input.Goal),
		Tasks: []PhaseTask{},
	}
	if input.Tasks != nil {
		phase.Tasks = make([]PhaseTask, 0, len(*input.Tasks))
		for _, inputTask := range *input.Tasks {
			taskID := ""
			if inputTask.ID != nil {
				taskID = normalizeOptionalText(*inputTask.ID)
			}
			if taskID == "" {
				taskID = nextTaskID(state)
			}
			if _, exists := taskIDs[taskID]; exists {
				return Phase{}, fmt.Errorf("duplicate task id %q", taskID)
			}
			taskIDs[taskID] = struct{}{}
			checked := false
			if inputTask.Checked != nil {
				checked = *inputTask.Checked
			}
			phase.Tasks = append(phase.Tasks, PhaseTask{ID: taskID, Text: strings.TrimSpace(inputTask.Text), Checked: checked})
		}
	}
	return phase, nil
}

func optionalInputText(value *string) string {
	if value == nil {
		return ""
	}
	return normalizeOptionalText(*value)
}

// applyAction is the state transition used by the protocol handler. It does
// not persist the result; stateStore performs that step while holding its
// mutex, so a failed write never publishes an unpersisted state.
func applyAction(state PlanState, params toolArgs) (PlanState, string, error) {
	state = cloneState(state)
	nextState := cloneState(state)
	if isPlanClosed(state) && closedPlanBlockedActions[params.Action] {
		reopen := (params.Action == actionSetTaskChecked || params.Action == actionSetPhaseChecked) && params.Checked != nil && !*params.Checked
		if !reopen {
			return state, "Plan is closed", errors.New("this plan is already complete and closed; call clear before starting new work, or call set_spec immediately followed by replace_plan")
		}
	}

	switch params.Action {
	case actionGetStatus:
		return state, "Current phased plan status", nil
	case actionSetSpec:
		spec := ""
		if params.Spec != nil {
			spec = normalizeOptionalText(*params.Spec)
		}
		if spec == "" {
			return state, "Spec not updated", errors.New("spec is required for set_spec")
		}
		nextState.Spec = spec
		if isPlanClosed(state) {
			nextState.Phases = []Phase{}
			nextState.CurrentPhaseID = ""
			nextState.NextPhaseNumber = 1
			nextState.NextTaskNumber = 1
		}
		nextState.ClosedAt = nil
		nextState.ClosedSummary = ""
		return nextState, "Saved spec", nil
	case actionReplacePlan:
		if params.Phases == nil {
			return state, "Plan not replaced", errors.New("phases is required for replace_plan")
		}
		nextState.Phases = []Phase{}
		nextState.CurrentPhaseID = ""
		nextState.NextPhaseNumber = 1
		nextState.NextTaskNumber = 1
		phaseIDs := map[string]struct{}{}
		taskIDs := map[string]struct{}{}
		for _, phaseInput := range *params.Phases {
			phase, err := buildPhaseFromInput(&nextState, phaseInput, phaseIDs, taskIDs)
			if err != nil {
				return state, "Plan not replaced", err
			}
			nextState.Phases = append(nextState.Phases, phase)
		}
		nextState.CurrentPhaseID = getSuggestedCurrentPhaseID(nextState)
		return nextState, fmt.Sprintf("Replaced plan with %d phase(s)", len(nextState.Phases)), nil
	case actionAddPhase:
		phaseTitle := ""
		if params.PhaseTitle != nil {
			phaseTitle = normalizeOptionalText(*params.PhaseTitle)
		}
		if phaseTitle == "" {
			return state, "Phase not added", errors.New("phaseTitle is required for add_phase")
		}
		nextState.Phases = append(nextState.Phases, Phase{ID: nextPhaseID(&nextState), Title: phaseTitle, Tasks: []PhaseTask{}})
		if params.PhaseGoal != nil {
			nextState.Phases[len(nextState.Phases)-1].Goal = normalizeOptionalText(*params.PhaseGoal)
		}
		if nextState.CurrentPhaseID == "" {
			nextState.CurrentPhaseID = getSuggestedCurrentPhaseID(nextState)
		}
		return nextState, "Added phase " + phaseTitle, nil
	case actionUpdatePhase:
		phase := findPhase(nextState, params.PhaseID)
		if phase == nil {
			return state, "Phase not updated", errors.New(phaseIDError(nextState, actionUpdatePhase, params.PhaseID))
		}
		if params.PhaseTitle != nil {
			if phaseTitle := normalizeOptionalText(*params.PhaseTitle); phaseTitle != "" {
				phase.Title = phaseTitle
			}
		}
		if params.PhaseGoal != nil {
			phase.Goal = normalizeOptionalText(*params.PhaseGoal)
		}
		return nextState, "Updated phase " + phase.Title, nil
	case actionRemovePhase:
		phase := findPhase(nextState, params.PhaseID)
		if phase == nil {
			return state, "Phase not removed", errors.New(phaseIDError(nextState, actionRemovePhase, params.PhaseID))
		}
		removedID := phase.ID
		filtered := make([]Phase, 0, len(nextState.Phases)-1)
		for _, entry := range nextState.Phases {
			if entry.ID != removedID {
				filtered = append(filtered, entry)
			}
		}
		nextState.Phases = filtered
		nextState.CurrentPhaseID = getSuggestedCurrentPhaseID(nextState)
		return nextState, "Removed phase " + removedID, nil
	case actionAddTask:
		phase := findPhase(nextState, params.PhaseID)
		taskText := ""
		if params.TaskText != nil {
			taskText = normalizeOptionalText(*params.TaskText)
		}
		if phase == nil {
			return state, "Task not added", errors.New(phaseIDError(nextState, actionAddTask, params.PhaseID))
		}
		if taskText == "" {
			return state, "Task not added", errors.New("taskText is required for add_task")
		}
		phase.Tasks = append(phase.Tasks, PhaseTask{ID: nextTaskID(&nextState), Text: taskText})
		return nextState, "Added task to " + phase.Title, nil
	case actionUpdateTask:
		if params.TaskID == "" {
			return state, "Task not updated", errors.New("taskId is required for update_task")
		}
		_, task := findTask(nextState, params.TaskID, params.PhaseID)
		taskText := ""
		if params.TaskText != nil {
			taskText = normalizeOptionalText(*params.TaskText)
		}
		if task == nil {
			return state, "Task not updated", errors.New(taskIDError(nextState, actionUpdateTask, params.TaskID))
		}
		if taskText == "" {
			return state, "Task not updated", errors.New("taskText is required for update_task")
		}
		task.Text = taskText
		return nextState, "Updated task " + task.ID, nil
	case actionRemoveTask:
		if params.TaskID == "" {
			return state, "Task not removed", errors.New("taskId is required")
		}
		phase, task := findTask(nextState, params.TaskID, params.PhaseID)
		if task == nil {
			return state, "Task not removed", errors.New("taskId was not found")
		}
		filtered := make([]PhaseTask, 0, len(phase.Tasks)-1)
		for _, entry := range phase.Tasks {
			if entry.ID != params.TaskID {
				filtered = append(filtered, entry)
			}
		}
		phase.Tasks = filtered
		return nextState, "Removed task " + params.TaskID, nil
	case actionSetCurrentPhase:
		phase := findPhase(nextState, params.PhaseID)
		if phase == nil {
			return state, "Current phase not updated", errors.New(phaseIDError(nextState, actionSetCurrentPhase, params.PhaseID))
		}
		nextState.CurrentPhaseID = phase.ID
		return nextState, "Current phase set to " + phase.Title, nil
	case actionSetTaskChecked:
		if params.TaskID == "" {
			return state, "Task not updated", errors.New("taskId is required for set_task_checked")
		}
		phase, task := findTask(nextState, params.TaskID, params.PhaseID)
		if task == nil {
			return state, "Task not updated", errors.New(taskIDError(nextState, actionSetTaskChecked, params.TaskID))
		}
		if params.Checked == nil {
			return state, "Task not updated", errors.New("checked must be provided for set_task_checked")
		}
		task.Checked = *params.Checked
		if *params.Checked && nextState.CurrentPhaseID == phase.ID && isPhaseDone(*phase) {
			nextState.CurrentPhaseID = ""
		}
		return nextState, fmt.Sprintf("%s task %s", checkedWord(*params.Checked), task.ID), nil
	case actionSetPhaseChecked:
		phase := findPhase(nextState, params.PhaseID)
		if phase == nil {
			return state, "Phase not updated", errors.New(phaseIDError(nextState, actionSetPhaseChecked, params.PhaseID))
		}
		if params.Checked == nil {
			return state, "Phase not updated", errors.New("checked must be provided for set_phase_checked")
		}
		if len(phase.Tasks) == 0 {
			return state, "Phase not updated", errors.New("set_phase_checked requires the phase to have at least one task")
		}
		for i := range phase.Tasks {
			phase.Tasks[i].Checked = *params.Checked
		}
		if *params.Checked && nextState.CurrentPhaseID == phase.ID {
			nextState.CurrentPhaseID = ""
		}
		if !*params.Checked {
			nextState.CurrentPhaseID = phase.ID
		}
		return nextState, fmt.Sprintf("%s phase %s", checkedWord(*params.Checked), phase.Title), nil
	case actionClear:
		return createEmptyState(), "Cleared stored spec and phased checklist", nil
	default:
		return state, "Unsupported action", errors.New("unknown action")
	}
}

func checkedWord(checked bool) string {
	if checked {
		return "Checked"
	}
	return "Unchecked"
}

const (
	compactVisibleIncompleteTasks     = 4
	turnContextVisibleIncompleteTasks = 3
	maxTurnContextChars               = 2048
)

func getIncompleteTasks(phase Phase) []PhaseTask {
	incomplete := make([]PhaseTask, 0)
	for _, task := range phase.Tasks {
		if !task.Checked {
			incomplete = append(incomplete, task)
		}
	}
	return incomplete
}

func formatRemainingTaskCount(count int) string {
	if count == 1 {
		return "1 remaining"
	}
	return fmt.Sprintf("%d remaining", count)
}

func formatPhaseTitle(phase Phase) string  { return truncatePlain(displaySingleLine(phase.Title), 120) }
func formatTaskText(task PhaseTask) string { return truncatePlain(displaySingleLine(task.Text), 180) }

func formatTurnContextID(id string) string { return truncatePlain(singleLine(id), 96) }

func appendIncompleteTaskLines(lines *[]string, tasks []PhaseTask, visibleCount int, indent string) {
	visible := tasks
	if len(visible) > visibleCount {
		visible = visible[:visibleCount]
	}
	for _, task := range visible {
		*lines = append(*lines, fmt.Sprintf("%s[ ] %s [%s]", indent, formatTaskText(task), formatTurnContextID(task.ID)))
	}
	if omitted := len(tasks) - len(visible); omitted > 0 {
		*lines = append(*lines, fmt.Sprintf("%s... %d more incomplete task(s) omitted; call tasked_phases get_status for the full checklist.", indent, omitted))
	}
}

func buildSummary(state PlanState) string {
	if !hasStoredPlan(state) {
		return "No spec or phased checklist has been stored yet."
	}
	lines := make([]string, 0)
	if state.Spec != "" {
		lines = append(lines, "Spec:", sanitizeDisplayText(state.Spec), "")
	}
	if len(state.Phases) == 0 {
		return strings.Join(append(lines, "Phases: (none)"), "\n")
	}
	done, total := getPlanProgress(state)
	lines = append(lines, fmt.Sprintf("Plan progress: %d/%d tasks checked", done, total))
	if isPlanClosed(state) {
		closedSummary := state.ClosedSummary
		if closedSummary == "" {
			closedSummary = "all phases complete"
		}
		lines = append(lines, "Plan closed: "+displaySingleLine(closedSummary))
	}
	lines = append(lines, "Phases:")
	for _, phase := range state.Phases {
		phaseDone, phaseTotal := getPhaseProgress(phase)
		currentMarker := " "
		if phase.ID == state.CurrentPhaseID {
			currentMarker = ">"
		}
		phaseMarker := "[ ]"
		if isPhaseDone(phase) {
			phaseMarker = "[x]"
		}
		goalSuffix := ""
		if phase.Goal != "" {
			goalSuffix = " - " + displaySingleLine(phase.Goal)
		}
		lines = append(lines, fmt.Sprintf("%s %s %s [%s] (%d/%d)%s", currentMarker, phaseMarker, formatPhaseTitle(phase), formatTurnContextID(phase.ID), phaseDone, phaseTotal, goalSuffix))
		if len(phase.Tasks) == 0 {
			lines = append(lines, "    - No tasks yet")
			continue
		}
		for _, task := range phase.Tasks {
			marker := "[ ]"
			if task.Checked {
				marker = "[x]"
			}
			lines = append(lines, fmt.Sprintf("    %s %s [%s]", marker, formatTaskText(task), formatTurnContextID(task.ID)))
		}
	}
	return strings.Join(lines, "\n")
}

func buildCompactSummary(state PlanState) string {
	if !hasStoredPlan(state) {
		return "No spec or phased checklist has been stored yet."
	}
	lines := make([]string, 0)
	if state.Spec != "" {
		lines = append(lines, "Spec: "+truncatePlain(displaySingleLine(state.Spec), 220))
	}
	done, total := getPlanProgress(state)
	lines = append(lines, fmt.Sprintf("Progress: %d/%d tasks checked", done, total))
	if isPlanClosed(state) {
		closedSummary := state.ClosedSummary
		if closedSummary == "" {
			closedSummary = "all phases complete"
		}
		lines = append(lines, "Plan closed: "+displaySingleLine(closedSummary))
		return strings.Join(lines, "\n")
	}
	if currentPhase := getActivePhase(state); currentPhase != nil {
		incompleteTasks := getIncompleteTasks(*currentPhase)
		goalSuffix := ""
		if currentPhase.Goal != "" {
			goalSuffix = " - " + truncatePlain(displaySingleLine(currentPhase.Goal), 120)
		}
		lines = append(lines, fmt.Sprintf("Current phase: %s [%s] (%s)%s", formatPhaseTitle(*currentPhase), formatTurnContextID(currentPhase.ID), formatRemainingTaskCount(len(incompleteTasks)), goalSuffix))
		if len(incompleteTasks) > 0 {
			lines = append(lines, "Incomplete tasks:")
			appendIncompleteTaskLines(&lines, incompleteTasks, compactVisibleIncompleteTasks, "  ")
		}
	} else if len(state.Phases) > 0 {
		lines = append(lines, "Current phase: none")
	}
	return strings.Join(lines, "\n")
}

// buildTurnContext returns the small amount of plan state the model needs at
// the start of a turn. It deliberately excludes the spec, completed phases,
// and future phases; get_status and /phases remain the full-state views.
func buildTurnContext(state PlanState) string {
	if !hasStoredPlan(state) {
		return ""
	}
	if isPlanClosed(state) {
		return truncatePlain("Tasked phases focus: the current plan is complete and closed. Use clear before creating a plan for unrelated work.", maxTurnContextChars)
	}
	phase := getActivePhase(state)
	if phase == nil {
		return ""
	}
	lines := []string{
		"Tasked phases focus:",
		fmt.Sprintf("Current phase: %s [%s]", formatPhaseTitle(*phase), formatTurnContextID(phase.ID)),
	}
	if phase.Goal != "" {
		lines = append(lines, "Goal: "+truncatePlain(displaySingleLine(phase.Goal), 160))
	}
	incompleteTasks := getIncompleteTasks(*phase)
	if len(incompleteTasks) == 0 {
		lines = append(lines, "No incomplete tasks remain in this phase; update the plan or advance the phase as appropriate.")
		return truncatePlain(strings.Join(lines, "\n"), maxTurnContextChars)
	}
	lines = append(lines, "Incomplete tasks:")
	appendIncompleteTaskLines(&lines, incompleteTasks, turnContextVisibleIncompleteTasks, "  ")
	return truncatePlain(strings.Join(lines, "\n"), maxTurnContextChars)
}

func shouldReturnFullSummary(action string, err error) bool {
	return err != nil || action == actionGetStatus || action == actionReplacePlan || action == actionClear
}

func buildToolResultText(action string, state PlanState, headline string, err error) string {
	summary := buildCompactSummary(state)
	if shouldReturnFullSummary(action, err) {
		summary = buildSummary(state)
	}
	if err != nil {
		return "Error: " + err.Error() + "\n\n" + summary
	}
	return headline + "\n\n" + summary
}

func buildViewLines(state PlanState) []string {
	lines := []string{"", " Tasked phases ", ""}
	if !hasStoredPlan(state) {
		return append(lines,
			"  No spec or phases stored yet.",
			"",
			"  Ask the agent to create a spec and phased checklist.",
			"",
			"  Press Escape to close",
		)
	}
	if state.Spec != "" {
		lines = append(lines, "  Spec")
		for _, specLine := range strings.Split(sanitizeDisplayText(state.Spec), "\n") {
			lines = append(lines, "  "+specLine)
		}
		lines = append(lines, "")
	}
	done, total := getPlanProgress(state)
	lines = append(lines, fmt.Sprintf("  Progress: %d/%d tasks checked", done, total))
	if isPlanClosed(state) {
		closedSummary := state.ClosedSummary
		if closedSummary == "" {
			closedSummary = "all phases complete"
		}
		lines = append(lines,
			"  Closed: "+displaySingleLine(closedSummary),
			"  New work should create a fresh plan instead of extending this one.",
		)
	}
	lines = append(lines, "")
	for _, phase := range state.Phases {
		phaseDone, phaseTotal := getPhaseProgress(phase)
		marker := "[ ]"
		if isPhaseDone(phase) {
			marker = "[x]"
		}
		currentTitle := phase.Title
		if phase.ID == state.CurrentPhaseID {
			currentTitle = "> " + currentTitle
		}
		lines = append(lines, fmt.Sprintf("  %s %s [%s] (%d/%d)", marker, displaySingleLine(currentTitle), formatTurnContextID(phase.ID), phaseDone, phaseTotal))
		if phase.Goal != "" {
			lines = append(lines, "    "+displaySingleLine(phase.Goal))
		}
		if len(phase.Tasks) == 0 {
			lines = append(lines, "    - No tasks yet")
			continue
		}
		for _, task := range phase.Tasks {
			taskMarker := "[ ]"
			if task.Checked {
				taskMarker = "[x]"
			}
			lines = append(lines, fmt.Sprintf("    %s %s [%s]", taskMarker, formatTaskText(task), formatTurnContextID(task.ID)))
		}
		lines = append(lines, "")
	}
	return append(lines, "  Press Escape to close")
}

type stateStore struct {
	mu      sync.Mutex
	state   PlanState
	path    string
	loaded  bool
	loadErr error
	// pathErr records a persistence fault independently of the active
	// session state. Activating a session must never clear this fault.
	pathErr error
}

func newStateStore() *stateStore {
	return &stateStore{state: createEmptyState()}
}

func projectStatePath(dataDir, cwd string) string {
	key := sha256.Sum256([]byte(cwd))
	return filepath.Join(dataDir, "projects", hex.EncodeToString(key[:])+".json")
}

func (s *stateStore) load(dataDir, cwd string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return s.loadErr
	}
	s.loaded = true
	s.state = createEmptyState()
	if strings.TrimSpace(dataDir) == "" {
		s.loadErr = errors.New("host did not provide extension data_dir")
		s.pathErr = s.loadErr
		return s.loadErr
	}
	s.path = projectStatePath(dataDir, cwd)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		s.loadErr = fmt.Errorf("create state directory: %w", err)
		s.pathErr = s.loadErr
		return s.loadErr
	}
	// #nosec G302 -- this is a directory; 0700 is the minimum mode that permits traversal.
	if err := os.Chmod(filepath.Dir(s.path), 0o700); err != nil {
		s.loadErr = fmt.Errorf("restrict state directory: %w", err)
		s.pathErr = s.loadErr
		return s.loadErr
	}
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		s.loadErr = fmt.Errorf("read state: %w", err)
		s.pathErr = s.loadErr
		return s.loadErr
	}
	if err := os.Chmod(s.path, 0o600); err != nil {
		s.loadErr = fmt.Errorf("restrict state file: %w", err)
		s.pathErr = s.loadErr
		return s.loadErr
	}
	if len(data) == 0 {
		return nil
	}
	var state PlanState
	if err := json.Unmarshal(data, &state); err != nil {
		s.loadErr = fmt.Errorf("parse state: %w", err)
		s.pathErr = s.loadErr
		return s.loadErr
	}
	s.state = ensureState(state)
	return nil
}

// activateSession replaces the project fallback with the opaque snapshot
// supplied by zot for the active session branch. An empty snapshot means the
// branch has no extension state yet and therefore starts clean.
func (s *stateStore) activateSession(raw json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		return errors.New("state has not been loaded from hello")
	}
	if s.pathErr != nil {
		return s.pathErr
	}
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		s.state = createEmptyState()
		s.loadErr = nil
		return nil
	}
	var state PlanState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("parse session state: %w", err)
	}
	s.state = ensureState(state)
	s.loadErr = nil
	return nil
}

func (s *stateStore) snapshot() (PlanState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		return PlanState{}, errors.New("state has not been loaded from hello")
	}
	if s.loadErr != nil {
		return cloneState(s.state), s.loadErr
	}
	return cloneState(s.state), nil
}

func (s *stateStore) transact(fn func(PlanState) (PlanState, string, error)) (PlanState, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		return PlanState{}, "", errors.New("state has not been loaded from hello")
	}
	if s.pathErr != nil {
		return cloneState(s.state), "", s.pathErr
	}
	if s.loadErr != nil {
		return cloneState(s.state), "", s.loadErr
	}
	current := cloneState(s.state)
	next, headline, err := fn(current)
	if err != nil {
		return current, headline, err
	}
	next = ensureState(next)
	if err := writeJSONAtomic(s.path, next); err != nil {
		return current, headline, fmt.Errorf("persist state: %w", err)
	}
	s.state = next
	return cloneState(next), headline, nil
}

func writeJSONAtomic(path string, value any) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("state path is empty")
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tasked-phases-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
}
