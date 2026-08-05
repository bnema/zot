package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/patriceckhart/zot/packages/agent/ext"
)

const (
	extensionName    = "tasked-phases"
	extensionVersion = "1.0.0"
	toolName         = "tasked_phases"
	panelID          = "tasked-phases"
)

const toolSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "action": {
      "type": "string",
      "enum": ["get_status", "set_spec", "replace_plan", "add_phase", "update_phase", "remove_phase", "add_task", "update_task", "remove_task", "set_current_phase", "set_task_checked", "set_phase_checked", "clear"],
      "description": "State operation to perform. Closed plans must be restarted with clear, or set_spec immediately followed by replace_plan."
    },
    "spec": {"type": "string", "description": "Spec text used by set_spec"},
    "phaseId": {"type": "string", "description": "Target phase id, raw id without the brackets shown in summaries"},
    "phaseTitle": {"type": "string", "description": "Phase title for add_phase or update_phase"},
    "phaseGoal": {"type": "string", "description": "Phase goal for add_phase or update_phase"},
    "taskId": {"type": "string", "description": "Target task id"},
    "taskText": {"type": "string", "description": "Task text for add_task or update_task"},
    "checked": {"type": "boolean", "description": "Checked state for set_task_checked or set_phase_checked"},
    "phases": {
      "type": "array",
      "description": "Full plan replacement used by replace_plan",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "properties": {
          "id": {"type": "string", "description": "Optional phase id. If omitted, one is generated."},
          "title": {"type": "string", "description": "Phase title"},
          "goal": {"type": "string", "description": "Optional short goal for the phase"},
          "tasks": {
            "type": "array",
            "description": "Checklist tasks for the phase",
            "items": {
              "type": "object",
              "additionalProperties": false,
              "properties": {
                "id": {"type": "string", "description": "Optional task id. If omitted, one is generated."},
                "text": {"type": "string", "description": "Checklist task text"},
                "checked": {"type": "boolean", "description": "Whether the task starts checked"}
              },
              "required": ["text"]
            }
          }
        },
        "required": ["title"]
      }
    }
  },
  "required": ["action"]
}`

type app struct {
	ext   *ext.Extension
	store *stateStore

	panelMu   sync.Mutex
	panelOpen bool
}

func newApp(e *ext.Extension) *app {
	return &app{ext: e, store: newStateStore()}
}

func main() {
	e := ext.New(extensionName, extensionVersion)
	a := newApp(e)

	e.OnHello(func(host ext.HostInfo) {
		dataDir := host.DataDir
		if dataDir == "" {
			dataDir = host.ExtensionDir
		}
		if err := a.store.load(dataDir, host.CWD); err != nil {
			e.Logf("load project state: %v", err)
			a.publishUnavailableChrome()
			return
		}
		if state, err := a.store.snapshot(); err == nil {
			a.publishChrome(state)
		} else {
			e.Logf("snapshot project state: %v", err)
			a.publishUnavailableChrome()
		}
	})
	restoreSession := func(event ext.Event) {
		if err := a.store.activateSession(event.State); err != nil {
			e.Logf("restore session state: %v", err)
			a.publishUnavailableChrome()
			return
		}
		if state, err := a.store.snapshot(); err == nil {
			a.publishChrome(state)
			a.renderPanelIfOpen(state)
		} else {
			e.Logf("restore session snapshot: %v", err)
			a.publishUnavailableChrome()
		}
	}
	e.On("session_opened", restoreSession)
	e.On("session_switched", restoreSession)
	e.On("session_forked", restoreSession)
	e.InterceptTurnStart(func(_ int) ext.TurnStartDecision {
		state, err := a.store.snapshot()
		if err != nil {
			e.Logf("load turn focus: %v", err)
			return ext.TurnStartDecision{}
		}
		return ext.TurnStartDecision{Context: buildTurnContext(state)}
	})

	e.Tool(toolName,
		"Persist and update a structured spec, phased plan, current phase, and checklist tasks. Keep the plan current throughout implementation: update it continuously, call set_task_checked immediately after each completed checklist task, and call set_current_phase when moving to another phase.",
		json.RawMessage(toolSchema), a.handleTool)
	e.Command("phases", "Show the current spec, phases, and checklist state", a.handleCommand)
	e.OnPanelKey(panelID, a.handlePanelKey, a.handlePanelClose)

	if err := e.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (a *app) handleCommand(_ string) ext.Response {
	state, err := a.store.snapshot()
	if err != nil {
		a.publishUnavailableChrome()
		return ext.Errorf("load tasked phases: %v", err)
	}
	a.publishChrome(state)
	a.panelMu.Lock()
	a.panelOpen = true
	a.panelMu.Unlock()
	return ext.OpenPanel(panelID, "Tasked phases", buildViewLines(state), "Escape closes this panel")
}

func (a *app) handlePanelKey(key, _ string) {
	if key == "esc" {
		a.ext.ClosePanel(panelID)
		a.setPanelOpen(false)
	}
}

func (a *app) handlePanelClose() { a.setPanelOpen(false) }

func (a *app) setPanelOpen(open bool) {
	a.panelMu.Lock()
	a.panelOpen = open
	a.panelMu.Unlock()
}

func (a *app) renderPanelIfOpen(state PlanState) {
	a.panelMu.Lock()
	open := a.panelOpen
	a.panelMu.Unlock()
	if open {
		a.ext.RenderPanel(panelID, "Tasked phases", buildViewLines(state), "Escape closes this panel")
	}
}

func (a *app) handleTool(args json.RawMessage) ext.ToolResult {
	var params toolArgs
	if err := json.Unmarshal(args, &params); err != nil {
		return ext.TextErrorResult("invalid args: " + err.Error())
	}
	if !isKnownAction(params.Action) {
		return ext.TextErrorResult("unsupported action: " + params.Action)
	}

	if params.Action == actionGetStatus {
		state, err := a.store.snapshot()
		if err != nil {
			a.publishUnavailableChrome()
			return toolResult(params.Action, PlanState{}, "", err)
		}
		a.publishChrome(state)
		a.renderPanelIfOpen(state)
		return toolResult(params.Action, state, "Current phased plan status", nil)
	}

	state, headline, err := a.store.transact(func(current PlanState) (PlanState, string, error) {
		return applyAction(current, params)
	})
	// A zero state means transact could not obtain a loaded snapshot. Do not
	// replace persistent chrome with an invented empty plan; clear it instead.
	// For action and persistence errors, transact returns the unchanged usable
	// state, which should remain visible to help the user recover.
	if err == nil || state.Version != 0 {
		a.publishChrome(state)
	} else {
		a.publishUnavailableChrome()
	}
	if err == nil {
		a.renderPanelIfOpen(state)
	}
	return toolResult(params.Action, state, headline, err)
}

func (a *app) publishUnavailableChrome() {
	clearChrome(a.ext)
	a.panelMu.Lock()
	open := a.panelOpen
	a.panelMu.Unlock()
	if open {
		a.ext.RenderPanel(panelID, "Tasked phases", []string{"Plan state could not be restored for this session."}, "Escape closes this panel")
	}
}

type chromeContent struct {
	summary string
	lines   []string
}

const (
	chromePhaseMaxChars = 32
	chromeTaskMaxChars  = 64
)

func buildChrome(state PlanState) (chromeContent, bool) {
	// A spec-only state is still a plan in progress. Show its compact summary
	// so the right bar opens as soon as planning starts, without exposing the
	// spec text in persistent chrome.
	if !hasStoredPlan(state) || isPlanComplete(state) || isPlanClosed(state) {
		return chromeContent{}, false
	}
	return chromeContent{
		summary: buildChromeSummary(state),
		lines:   buildChecklistLines(state),
	}, true
}

func buildChromeSummary(state PlanState) string {
	phaseDone := 0
	for _, phase := range state.Phases {
		if isPhaseDone(phase) {
			phaseDone++
		}
	}
	taskDone, taskTotal := getPlanProgress(state)
	return fmt.Sprintf("p %d/%d | t %d/%d", phaseDone, len(state.Phases), taskDone, taskTotal)
}

func chromeLabel(value, fallback string, maxLength int) string {
	value = truncatePlain(displaySingleLine(value), maxLength)
	if value == "" {
		return fallback
	}
	return value
}

func getChromePhase(state PlanState) *Phase {
	if current := getCurrentPhase(state); current != nil && !isPhaseDone(*current) {
		return current
	}
	for i := range state.Phases {
		if !isPhaseDone(state.Phases[i]) {
			return &state.Phases[i]
		}
	}
	if len(state.Phases) == 0 {
		return nil
	}
	return &state.Phases[len(state.Phases)-1]
}

func getChromeTask(phase Phase) *PhaseTask {
	for i := range phase.Tasks {
		if !phase.Tasks[i].Checked {
			return &phase.Tasks[i]
		}
	}
	return nil
}

func buildChecklistLines(state PlanState) []string {
	activePhase := getChromePhase(state)
	activeTaskID := ""
	if activePhase != nil {
		if activeTask := getChromeTask(*activePhase); activeTask != nil {
			activeTaskID = activeTask.ID
		}
	}

	lines := make([]string, 0)
	for phaseIndex, phase := range state.Phases {
		if phaseIndex > 0 {
			lines = append(lines, "")
		}
		phaseDone, phaseTotal := getPhaseProgress(phase)
		phaseMarker := "[ ]"
		if isPhaseDone(phase) {
			phaseMarker = "[x]"
		} else if activePhase != nil && phase.ID == activePhase.ID {
			phaseMarker = "[>]"
		}
		lines = append(lines, fmt.Sprintf("%s %s  %d/%d", phaseMarker, chromeLabel(formatPhaseTitle(phase), "untitled phase", chromePhaseMaxChars), phaseDone, phaseTotal))
		for _, task := range phase.Tasks {
			taskMarker := "[ ]"
			if task.Checked {
				taskMarker = "[x]"
			} else if activePhase != nil && phase.ID == activePhase.ID && task.ID == activeTaskID {
				taskMarker = "[>]"
			}
			lines = append(lines, fmt.Sprintf(" %s %s", taskMarker, chromeLabel(formatTaskText(task), "untitled task", chromeTaskMaxChars)))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "No phases yet")
	}
	return lines
}

type chromeHost interface {
	SetStatus(key, text string)
	ClearStatus(key string)
	SetWidget(id, position, title string, lines []string)
	ClearWidget(id string)
}

func clearChrome(host chromeHost) {
	host.ClearStatus("progress")
	host.ClearWidget("plan")
}

func applyChrome(host chromeHost, state PlanState) {
	chrome, ok := buildChrome(state)
	if !ok {
		clearChrome(host)
		return
	}
	host.SetStatus("progress", chrome.summary)
	host.SetWidget("plan", ext.WidgetPositionRightBar, chrome.summary, chrome.lines)
}

func (a *app) publishChrome(state PlanState) {
	applyChrome(a.ext, state)
}

func toolResult(action string, state PlanState, headline string, err error) ext.ToolResult {
	text := buildToolResultText(action, state, headline, err)
	var result ext.ToolResult
	if err != nil {
		result = ext.TextErrorResult(text)
	} else {
		result = ext.TextResult(text)
	}
	result.Details = ext.JSONDetails(state)
	return result
}

func isKnownAction(action string) bool {
	for _, known := range toolActions {
		if action == known {
			return true
		}
	}
	return false
}
