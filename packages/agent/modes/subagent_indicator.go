package modes

import (
	"fmt"
	"strings"
	"time"

	"github.com/bnema/zut/packages/agent/subagents"
	"github.com/bnema/zut/packages/tui"
	"github.com/mattn/go-runewidth"
)

// renderSubagentActivityLines returns one compact, input-adjacent row for each
// active delegated turn. Terminal and idle workers are intentionally omitted:
// the /subagents dashboard remains the durable history, while this surface is
// only a live "is it still moving?" signal.
func renderSubagentActivityLines(th tui.Theme, spinnerGlyph string, snapshots []subagents.AgentSnapshot, width int, now time.Time) []string {
	lines := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if !subagentTurnIsActive(snapshot) {
			continue
		}
		line := renderSubagentActivityLine(th, spinnerGlyph, snapshot, width, now)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func subagentTurnIsActive(snapshot subagents.AgentSnapshot) bool {
	switch snapshot.ProcessState {
	case subagents.ProcessPending, subagents.ProcessStarting, subagents.ProcessAlive:
	default:
		// A detached worker has no confirmed live connection. Keep it in the
		// dashboard, rather than presenting an endlessly animated row beside
		// the input as if it were still making progress.
		return false
	}

	switch snapshot.TurnState {
	case subagents.TurnSucceeded, subagents.TurnFailed, subagents.TurnCanceled:
		return false
	}
	switch snapshot.Status {
	case subagents.StatusPending:
		return true
	case subagents.StatusRunning:
	default:
		return false
	}

	switch snapshot.TurnState {
	case subagents.TurnQueued, subagents.TurnRunning, subagents.TurnCanceling:
		return true
	}

	// The worker enters TurnIdle while it waits for a follow-up prompt. Keep a
	// brief "starting" phase visible, but remove the row as soon as it reports
	// ordinary idle so an already-completed task never looks active.
	return strings.TrimSpace(snapshot.Activity) != "" && strings.TrimSpace(snapshot.Activity) != "idle"
}

func renderSubagentActivityLine(th tui.Theme, spinnerGlyph string, snapshot subagents.AgentSnapshot, width int, now time.Time) string {
	name := sanitizeSubagentIndicatorText(snapshot.Subagent)
	if name == "" {
		name = sanitizeSubagentIndicatorText(snapshot.ID)
	}
	if name == "" {
		name = "subagent"
	}
	spinnerGlyph = strings.TrimSpace(spinnerGlyph)
	if spinnerGlyph == "" {
		spinnerGlyph = "."
	}
	activity := compactSubagentActivity(snapshot)
	age := formatSubagentActivityAge(snapshot, now)
	plain, layout := fitSubagentActivityLine(spinnerGlyph, name, activity, age, width-2)
	if plain == "" {
		return ""
	}

	switch layout {
	case subagentActivityFull:
		parts := strings.SplitN(plain, " · ", 3)
		if len(parts) != 3 {
			return "  " + th.FGColor(th.Muted, plain)
		}
		name = strings.TrimPrefix(parts[0], spinnerGlyph+" ")
		return "  " + th.FGColor(th.Spinner, spinnerGlyph) + " " +
			th.FGColor(th.Assistant, name) + th.FGColor(th.Muted, " · ") +
			th.FGColor(th.FG, parts[1]) + th.FGColor(th.Muted, " · "+parts[2])
	case subagentActivityNameAge:
		parts := strings.SplitN(plain, " · ", 2)
		if len(parts) != 2 {
			return "  " + th.FGColor(th.Muted, plain)
		}
		name = strings.TrimPrefix(parts[0], spinnerGlyph+" ")
		return "  " + th.FGColor(th.Spinner, spinnerGlyph) + " " +
			th.FGColor(th.Assistant, name) + th.FGColor(th.Muted, " · "+parts[1])
	case subagentActivityCompact:
		prefix := spinnerGlyph + " "
		suffix := " " + age
		if strings.HasPrefix(plain, prefix) && strings.HasSuffix(plain, suffix) {
			name = strings.TrimSuffix(strings.TrimPrefix(plain, prefix), suffix)
			return "  " + th.FGColor(th.Spinner, spinnerGlyph) + " " +
				th.FGColor(th.Assistant, name) + " " + th.FGColor(th.Muted, age)
		}
	}
	return "  " + th.FGColor(th.Muted, plain)
}

type subagentActivityLayout uint8

const (
	subagentActivityFull subagentActivityLayout = iota
	subagentActivityNameAge
	subagentActivityCompact
)

func fitSubagentActivityLine(spinnerGlyph, name, activity, age string, width int) (string, subagentActivityLayout) {
	spinnerGlyph = strings.TrimSpace(spinnerGlyph)
	if spinnerGlyph == "" {
		spinnerGlyph = "."
	}
	if width <= 0 {
		return "", subagentActivityCompact
	}

	full := spinnerGlyph + " " + name + " · " + activity + " · " + age
	if runewidth.StringWidth(full) <= width {
		return full, subagentActivityFull
	}

	nameAge := spinnerGlyph + " " + name + " · " + age
	if activityWidth := width - runewidth.StringWidth(nameAge) - runewidth.StringWidth(" · "); activityWidth >= 4 {
		activity = truncateSubagentIndicatorText(activity, activityWidth)
		return spinnerGlyph + " " + name + " · " + activity + " · " + age, subagentActivityFull
	}
	if runewidth.StringWidth(nameAge) <= width {
		return nameAge, subagentActivityNameAge
	}

	prefix := spinnerGlyph + " "
	suffix := " " + age
	nameWidth := width - runewidth.StringWidth(prefix) - runewidth.StringWidth(suffix)
	if nameWidth >= 1 {
		return prefix + truncateSubagentIndicatorText(name, nameWidth) + suffix, subagentActivityCompact
	}
	if runewidth.StringWidth(age) <= width {
		return age, subagentActivityCompact
	}
	return truncateSubagentIndicatorText(spinnerGlyph, width), subagentActivityCompact
}

func compactSubagentActivity(snapshot subagents.AgentSnapshot) string {
	activity := sanitizeSubagentIndicatorText(snapshot.Activity)
	// Heartbeats report "idle" even while a delegated turn is still running.
	// Keep the row meaningful in that interval instead of contradicting its
	// spinner and turn state.
	if activity != "" && (activity != "idle" || snapshot.TurnState != subagents.TurnRunning) {
		return activity
	}
	switch snapshot.TurnState {
	case subagents.TurnQueued:
		return "queued"
	case subagents.TurnCanceling:
		return "cancelling"
	default:
		return "working"
	}
}

func formatSubagentActivityAge(snapshot subagents.AgentSnapshot, now time.Time) string {
	last := snapshot.LastActivity
	if last.IsZero() {
		last = snapshot.UpdatedAt
	}
	if last.IsZero() {
		last = snapshot.Started
	}
	if last.IsZero() {
		return "-"
	}
	if now.IsZero() {
		now = time.Now()
	}
	elapsed := now.Sub(last)
	if elapsed < 0 {
		elapsed = 0
	}
	seconds := int(elapsed.Seconds())
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 60*60:
		return fmt.Sprintf("%dm%02ds", seconds/60, seconds%60)
	case seconds < 24*60*60:
		minutes := (seconds % (60 * 60)) / 60
		return fmt.Sprintf("%dh%02dm", seconds/(60*60), minutes)
	default:
		hours := (seconds % (24 * 60 * 60)) / (60 * 60)
		return fmt.Sprintf("%dd%02dh", seconds/(24*60*60), hours)
	}
}

func truncateSubagentIndicatorText(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	return runewidth.Truncate(s, width, "...")
}

// sanitizeSubagentIndicatorText keeps worker-derived labels on one safe
// terminal row before they are measured or styled. A worker's profile, id, or
// tool activity is data, never terminal control output.
func sanitizeSubagentIndicatorText(s string) string {
	return sanitizeSessionTreeText(s)
}

// limitSubagentActivityLines reserves the input's terminal rows before adding
// activity rows. When the frame has room for only part of the live set, retain
// the earliest rows and make the omitted count explicit in one final row.
func limitSubagentActivityLines(th tui.Theme, lines []string, maxRows, width int) []string {
	if maxRows <= 0 || len(lines) == 0 {
		return nil
	}
	if len(lines) <= maxRows {
		return lines
	}
	if maxRows == 1 {
		return []string{subagentActivityOverflowLine(th, len(lines), width)}
	}
	out := append([]string(nil), lines[:maxRows-1]...)
	return append(out, subagentActivityOverflowLine(th, len(lines)-len(out), width))
}

func subagentActivityOverflowLine(th tui.Theme, hidden, width int) string {
	text := fmt.Sprintf("  … %d more active subagents", hidden)
	return th.FGColor(th.Muted, truncateSubagentIndicatorText(text, width))
}

// activeSubagentActivitySnapshots builds the compact state needed by the
// input indicator without copying transcript buffers. It mirrors the
// supervisor's live-session filtering while deliberately omitting completed
// workers, which belong in the dashboard and completion notification instead.
func (i *Interactive) activeSubagentActivitySnapshots() []subagents.AgentSnapshot {
	supervisor := i.cfg.Supervisor
	if supervisor == nil {
		return nil
	}

	activeSession := supervisor.ActiveSession()
	agents := supervisor.List()
	out := make([]subagents.AgentSnapshot, 0, len(agents))
	for _, agent := range agents {
		if activeSession != "" && agent.SessionID != "" && agent.SessionID != activeSession {
			continue
		}
		snapshot := subagents.AgentSnapshot{
			ID:           agent.ID,
			Status:       agent.Status(),
			ProcessState: agent.ProcessState(),
			TurnState:    agent.TurnState(),
			Activity:     agent.Activity(),
			Started:      agent.Started,
			UpdatedAt:    agent.UpdatedAt(),
			LastActivity: agent.LastActivity(),
			Subagent:     agent.Subagent,
		}
		if subagentTurnIsActive(snapshot) {
			out = append(out, snapshot)
		}
	}
	return out
}
