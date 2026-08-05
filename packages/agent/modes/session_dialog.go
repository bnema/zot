package modes

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/tui"
)

type sessionLoadEventKind uint8

const (
	sessionLoadStarted sessionLoadEventKind = iota
	sessionLoadEntry
	sessionLoadFinished
)

type sessionLoadEvent struct {
	kind       sessionLoadEventKind
	generation uint64
	total      int
	index      int
	summary    core.SessionSummary
}

type sessionLoadSlot struct {
	loaded  bool
	summary core.SessionSummary
}

type sessionLoadJob struct {
	index int
	path  string
}

const maxSessionLoadWorkers = 8

// sessionDialog is the inline picker shown when the user runs /sessions.
type sessionDialog struct {
	active   bool
	sessions []core.SessionSummary
	cursor   int
	renaming bool
	rename   string

	loading          bool
	loadingDone      int
	loadingTotal     int
	loadingStartedAt time.Time
	loadGeneration   uint64
	loadCancel       context.CancelFunc
	loadSlots        []sessionLoadSlot

	// MaxRows is the maximum number of session rows the dialog
	// will render in a single frame. Set by the host right before
	// Render based on the available chat space; if 0, the dialog
	// falls back to rendering every row (original behaviour).
	// When the list is longer than MaxRows the dialog scrolls so
	// the cursor stays visible and tags the first/last visible
	// entry with a muted "↑ N more" / "↓ N more" row so the user
	// knows there's offscreen content.
	MaxRows int

	// viewTop is the index of the first session currently drawn.
	// Adjusted to follow the cursor on up/down moves.
	viewTop int
}

// sessionDialogAction is returned by HandleKey.
type sessionDialogAction struct {
	Select      bool
	Path        string
	Close       bool
	Renamed     bool
	RenameTitle string
	Err         error
}

func newSessionDialog() *sessionDialog { return &sessionDialog{} }

// Open shows the dialog immediately and loads session entries on bounded
// background workers. Results are delivered to the caller so the UI goroutine
// remains responsible for mutating dialog state and rendering it.
func (d *sessionDialog) Open(parent context.Context, root, cwd string) <-chan sessionLoadEvent {
	if d.loadCancel != nil {
		d.loadCancel()
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	d.loadCancel = cancel
	d.loadGeneration++
	generation := d.loadGeneration
	d.sessions = nil
	d.cursor = 0
	d.viewTop = 0
	d.renaming = false
	d.rename = ""
	d.loading = true
	d.loadingDone = 0
	d.loadingTotal = 0
	d.loadingStartedAt = time.Now()
	d.loadSlots = nil
	d.active = true

	events := make(chan sessionLoadEvent, 1)
	send := func(event sessionLoadEvent) bool {
		select {
		case events <- event:
			return true
		case <-ctx.Done():
			return false
		}
	}
	go func() {
		defer close(events)
		select {
		case <-ctx.Done():
			return
		default:
		}
		paths := core.ListSessions(root, cwd)
		if !send(sessionLoadEvent{
			kind:       sessionLoadStarted,
			generation: generation,
			total:      len(paths),
		}) {
			return
		}

		workerCount := len(paths)
		if workerCount > maxSessionLoadWorkers {
			workerCount = maxSessionLoadWorkers
		}
		jobs := make(chan sessionLoadJob)
		var wg sync.WaitGroup
		wg.Add(workerCount)
		for worker := 0; worker < workerCount; worker++ {
			go func() {
				defer wg.Done()
				for {
					select {
					case <-ctx.Done():
						return
					case job, ok := <-jobs:
						if !ok {
							return
						}
						summary := core.DescribeSessionContext(ctx, job.path)
						select {
						case <-ctx.Done():
							return
						default:
						}
						if !send(sessionLoadEvent{
							kind:       sessionLoadEntry,
							generation: generation,
							index:      job.index,
							summary:    summary,
						}) {
							return
						}
					}
				}
			}()
		}
	dispatch:
		for index, path := range paths {
			select {
			case jobs <- sessionLoadJob{index: index, path: path}:
			case <-ctx.Done():
				break dispatch
			}
		}
		close(jobs)
		wg.Wait()
		send(sessionLoadEvent{kind: sessionLoadFinished, generation: generation})
	}()
	return events
}

// ApplyLoad incorporates one result from Open. It must be called by the UI
// goroutine so Render and HandleKey never race with background file reads.
func (d *sessionDialog) ApplyLoad(event sessionLoadEvent) {
	if !d.active || event.generation != d.loadGeneration {
		return
	}
	switch event.kind {
	case sessionLoadStarted:
		d.loadingTotal = event.total
		d.loadingDone = 0
		d.loadSlots = make([]sessionLoadSlot, event.total)
		d.sessions = nil
	case sessionLoadEntry:
		if event.index < 0 || event.index >= len(d.loadSlots) || d.loadSlots[event.index].loaded {
			return
		}
		d.loadSlots[event.index] = sessionLoadSlot{loaded: true, summary: event.summary}
		d.loadingDone++
	case sessionLoadFinished:
		d.rebuildLoadedSessions()
		d.loading = false
		d.loadSlots = nil
		if d.loadCancel != nil {
			d.loadCancel()
			d.loadCancel = nil
		}
	}
}

func (d *sessionDialog) rebuildLoadedSessions() {
	filtered := make([]core.SessionSummary, 0, len(d.loadSlots))
	for _, slot := range d.loadSlots {
		if !slot.loaded || slot.summary.HideFromSessions || slot.summary.MessageCount == 0 {
			continue
		}
		filtered = append(filtered, slot.summary)
	}
	d.sessions = filtered
	if d.cursor >= len(d.sessions) {
		d.cursor = len(d.sessions) - 1
	}
	if d.cursor < 0 {
		d.cursor = 0
	}
}

// CursorPos returns the row/col for the terminal cursor when in
// rename mode. Returns -1, -1 otherwise.
func (d *sessionDialog) CursorPos() (row, col int) {
	if !d.renaming {
		return -1, -1
	}
	// Row: frameHeader + padDialogFrame blank + rename hint = row 3 (0-indexed).
	// Col: "  ▌ " prefix + text length.
	return 3, 4 + len([]rune(d.rename))
}

// Close hides the dialog and cancels any in-flight session reads.
func (d *sessionDialog) Close() {
	if d.loadCancel != nil {
		d.loadCancel()
		d.loadCancel = nil
	}
	d.loading = false
	d.loadSlots = nil
	d.active = false
}

// Active reports whether the dialog is visible and consumes input.
func (d *sessionDialog) Active() bool { return d != nil && d.active }

// Loading reports whether the dialog still has session entries in flight.
func (d *sessionDialog) Loading() bool { return d != nil && d.active && d.loading }

// Render returns the dialog lines.
func (d *sessionDialog) Render(th tui.Theme, width int) []string {
	if !d.Active() {
		return nil
	}
	var lines []string
	lines = append(lines, frameHeader(th, "sessions", width))
	if d.loading {
		lines = append(lines, th.FG256(th.Muted, d.loadingMessage(th)))
		if len(d.sessions) == 0 {
			lines = append(lines, th.FG256(th.Muted, "session entries will appear when loading completes"))
			lines = append(lines, frameRule(th, width))
			return lines
		}
	} else if len(d.sessions) == 0 {
		lines = append(lines, th.FG256(th.Muted, "no previous sessions for this directory"))
		lines = append(lines, th.FG256(th.Muted, "press esc to close"))
		lines = append(lines, frameRule(th, width))
		return lines
	}
	if d.renaming {
		lines = append(lines, th.FG256(th.Muted, "rename session (enter save, esc cancel):"))
		text := d.rename
		if text == "" {
			text = th.FG256(th.Muted, "session name")
		} else {
			text = th.FG256(th.FG, text)
		}
		lines = append(lines, "  "+th.FG256(th.Accent, "▌ ")+text)
		lines = append(lines, frameRule(th, width))
		return lines
	}
	if !d.loading {
		lines = append(lines, th.FG256(th.Muted, "pick a session (↑/↓, pgup/pgdn, enter resume, r rename, esc cancel)"))
	}

	// Viewport: windowed slice of d.sessions around d.cursor so a
	// list taller than the terminal still scrolls. Caller sets
	// MaxRows to the number of rows available for session entries
	// (i.e. excluding the header, hint, chrome). When it's zero or
	// bigger than the list, we draw everything.
	total := len(d.sessions)
	window := d.MaxRows
	if window <= 0 || window >= total {
		window = total
	}
	d.viewTop = clampViewTop(d.viewTop, d.cursor, window, total)
	viewBot := d.viewTop + window
	if viewBot > total {
		viewBot = total
	}

	// Top indicator: how many rows are above the viewport.
	if d.viewTop > 0 {
		hidden := d.viewTop
		lines = append(lines, th.FG256(th.Muted, fmt.Sprintf("  ↑ %d more above", hidden)))
	}
	for i := d.viewTop; i < viewBot; i++ {
		s := d.sessions[i]
		plain := "  " + formatSessionRowPlain(s, width-2)
		if i == d.cursor {
			lines = append(lines, th.PadHighlight(plain, width))
		} else {
			lines = append(lines, th.FG256(th.Muted, plain))
		}
	}
	// Bottom indicator: how many rows are below the viewport.
	if viewBot < total {
		hidden := total - viewBot
		lines = append(lines, th.FG256(th.Muted, fmt.Sprintf("  ↓ %d more below", hidden)))
	}
	lines = append(lines, frameRule(th, width))
	return lines
}

// clampViewTop returns a viewTop that keeps cursor visible in a
// window of the given size over a list of `total` rows. Leaves one
// row of padding above/below where possible so moving the cursor
// doesn't land right on the top/bottom edge — easier to see what
// direction you're moving.
func clampViewTop(viewTop, cursor, window, total int) int {
	if window <= 0 || total <= 0 {
		return 0
	}
	if window >= total {
		return 0
	}
	pad := 2
	if window < 6 {
		pad = 0
	}
	if cursor < viewTop+pad {
		viewTop = cursor - pad
	}
	if cursor >= viewTop+window-pad {
		viewTop = cursor - window + pad + 1
	}
	if viewTop < 0 {
		viewTop = 0
	}
	if viewTop+window > total {
		viewTop = total - window
	}
	return viewTop
}

// formatSessionRowPlain returns the session row body without any ANSI
// styling so the caller can wrap it in either a plain mute color or a
// full-row selection highlight. The returned string is guaranteed to
// fit within maxWidth visible characters so the terminal never soft-
// wraps it into the next row.
func formatSessionRowPlain(s core.SessionSummary, maxWidth int) string {
	when := formatRelative(s.Started)
	summary := strings.TrimSpace(s.Title)
	if summary == "" {
		summary = strings.TrimSpace(s.FirstUserText)
	}
	if summary == "" {
		summary = "(empty)"
	}
	summary = strings.ReplaceAll(summary, "\n", " ")
	left := fmt.Sprintf("%-14s  %s/%s  %d msgs  $%.4f  ",
		when, s.Provider, s.Model, s.MessageCount, s.TotalCost)
	room := maxWidth - len([]rune(left))
	if room < 4 {
		room = 4
	}
	runes := []rune(summary)
	if len(runes) > room {
		summary = string(runes[:room-3]) + "..."
	}
	row := left + summary
	// Hard clamp: ensure the full row never exceeds maxWidth.
	rowRunes := []rune(row)
	if len(rowRunes) > maxWidth {
		if maxWidth <= 3 {
			row = strings.Repeat(".", maxWidth)
		} else {
			row = string(rowRunes[:maxWidth-3]) + "..."
		}
	}
	return row
}

func (d *sessionDialog) loadingMessage(th tui.Theme) string {
	frames := th.SpinnerFrames
	if len(frames) == 0 {
		frames = []string{"⠋", "⠙", "⠚", "⠞", "⠖", "⠦", "⠴", "⠲", "⠳", "⠓"}
	}
	interval := th.SpinnerIntervalMS
	if interval <= 0 {
		interval = 80
	}
	idx := 0
	if !d.loadingStartedAt.IsZero() {
		elapsed := time.Since(d.loadingStartedAt)
		if elapsed < 0 {
			elapsed = 0
		}
		idx = int(elapsed/(time.Duration(interval)*time.Millisecond)) % len(frames)
	}
	progress := "loading sessions"
	if d.loadingTotal > 0 {
		progress = fmt.Sprintf("loading sessions (%d/%d)", d.loadingDone, d.loadingTotal)
	}
	return th.FG256(th.Spinner, frames[idx]) + " " + progress + " (esc cancel)"
}

func formatRelative(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d d ago", int(d.Hours()/24))
	default:
		return t.Local().Format("2006-01-02")
	}
}

// HandleKey advances the dialog and returns an action to apply, if any.
func (d *sessionDialog) HandleKey(k tui.Key) sessionDialogAction {
	// Rename mode: type the new name.
	if d.renaming {
		switch k.Kind {
		case tui.KeyEnter:
			title := core.NormalizeSessionTitle(d.rename)
			path := ""
			renamed := false
			var renameErr error
			if title != "" && d.cursor >= 0 && d.cursor < len(d.sessions) {
				path = d.sessions[d.cursor].Path
				if err := core.RenameSession(path, title); err != nil {
					renameErr = err
				} else {
					d.sessions[d.cursor].Title = title
					renamed = true
				}
			}
			d.renaming = false
			d.rename = ""
			return sessionDialogAction{Renamed: renamed, Path: path, RenameTitle: title, Err: renameErr}
		case tui.KeyEsc:
			d.renaming = false
			d.rename = ""
			return sessionDialogAction{}
		case tui.KeyBackspace:
			if len(d.rename) > 0 {
				r := []rune(d.rename)
				d.rename = string(r[:len(r)-1])
			}
			return sessionDialogAction{}
		case tui.KeyPaste:
			d.rename += k.Paste
			return sessionDialogAction{}
		case tui.KeyRune:
			if k.Rune != 0 {
				d.rename += string(k.Rune)
			}
			return sessionDialogAction{}
		}
		return sessionDialogAction{}
	}

	if d.loading {
		if k.Kind == tui.KeyEsc {
			d.Close()
			return sessionDialogAction{Close: true}
		}
		return sessionDialogAction{}
	}

	page := d.MaxRows
	if page <= 0 {
		page = 10
	}
	if page > 1 {
		page--
	}
	switch k.Kind {
	case tui.KeyUp:
		if d.cursor > 0 {
			d.cursor--
		}
	case tui.KeyDown:
		if d.cursor < len(d.sessions)-1 {
			d.cursor++
		}
	case tui.KeyPageUp:
		d.cursor -= page
		if d.cursor < 0 {
			d.cursor = 0
		}
	case tui.KeyPageDown:
		d.cursor += page
		if d.cursor >= len(d.sessions) {
			d.cursor = len(d.sessions) - 1
			if d.cursor < 0 {
				d.cursor = 0
			}
		}
	case tui.KeyHome:
		d.cursor = 0
	case tui.KeyEnd:
		if len(d.sessions) > 0 {
			d.cursor = len(d.sessions) - 1
		}
	case tui.KeyEsc:
		d.Close()
		return sessionDialogAction{Close: true}
	case tui.KeyEnter:
		if len(d.sessions) == 0 {
			d.Close()
			return sessionDialogAction{Close: true}
		}
		s := d.sessions[d.cursor]
		d.Close()
		return sessionDialogAction{Select: true, Path: s.Path}
	case tui.KeyRune:
		if k.Rune == 'r' && len(d.sessions) > 0 {
			s := d.sessions[d.cursor]
			d.renaming = true
			if s.Title != "" {
				d.rename = s.Title
			} else {
				d.rename = ""
			}
			return sessionDialogAction{}
		}
	}
	return sessionDialogAction{}
}
