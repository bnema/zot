package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
			return
		}
		if state, err := a.store.snapshot(); err == nil {
			a.publishChrome(state)
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
		"Persist and update a structured spec, phased plan, current phase, and checklist tasks. Use it for spec-driven planning and progress tracking.",
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
	// replace persistent chrome with an invented empty plan. For action and
	// persistence errors, transact returns the unchanged usable state, which
	// should remain visible to help the user recover.
	if err == nil || state.Version != 0 {
		a.publishChrome(state)
	}
	if err == nil {
		a.renderPanelIfOpen(state)
	}
	return toolResult(params.Action, state, headline, err)
}

func (a *app) publishUnavailableChrome() {
	a.ext.SetStatus("progress", "plan unavailable")
	a.ext.SetWidget("plan", "above_input", "Tasked phases", []string{"Plan state could not be restored for this session."})
	a.panelMu.Lock()
	open := a.panelOpen
	a.panelMu.Unlock()
	if open {
		a.ext.RenderPanel(panelID, "Tasked phases", []string{"Plan state could not be restored for this session."}, "Escape closes this panel")
	}
}

type chromeContent struct {
	status string
	lines  []string
}

func buildChrome(state PlanState) (chromeContent, bool) {
	// An empty state is the normal idle state, not useful persistent chrome.
	if !hasStoredPlan(state) {
		return chromeContent{}, false
	}

	status := ""
	if isPlanClosed(state) {
		status = "closed: " + state.ClosedSummary
	} else if done, total := getPlanProgress(state); total > 0 {
		status = fmt.Sprintf("%d/%d tasks checked", done, total)
	} else {
		status = "no active plan"
	}
	return chromeContent{
		status: status,
		lines:  strings.Split(buildCompactSummary(state), "\n"),
	}, true
}

type chromeHost interface {
	SetStatus(key, text string)
	ClearStatus(key string)
	SetWidget(id, position, title string, lines []string)
	ClearWidget(id string)
}

func applyChrome(host chromeHost, state PlanState) {
	chrome, ok := buildChrome(state)
	if !ok {
		host.ClearStatus("progress")
		host.ClearWidget("plan")
		return
	}
	host.SetStatus("progress", chrome.status)
	host.SetWidget("plan", "above_input", "Tasked phases", chrome.lines)
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
