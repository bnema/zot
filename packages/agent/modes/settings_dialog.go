package modes

import (
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/patriceckhart/zot/packages/tui"
)

type settingsDialog struct {
	active       bool
	title        string
	items        []settingsItem
	cursor       int
	selecting    bool
	direct       bool
	optionCursor int
	parentItems  []settingsItem
	parentCursor int
}

type settingsItem struct {
	key      string
	label    string
	desc     string
	value    bool
	options  []settingsOption
	children []settingsItem
	picker   bool
	choice   int
	disabled bool
	hint     string
}

type settingsOption struct {
	value string
	label string
	desc  string
}

type settingsAction struct {
	Toggle            bool
	Key               string
	Value             bool
	StringValue       string
	ModelShortcutSlot int
	Close             bool
}

func newSettingsDialog() *settingsDialog { return &settingsDialog{} }

func (d *settingsDialog) Open(items []settingsItem) bool {
	if len(items) == 0 {
		return false
	}
	d.title = "settings"
	d.items = items
	d.cursor = 0
	d.selecting = false
	d.direct = false
	d.optionCursor = 0
	d.parentItems = nil
	d.parentCursor = 0
	d.active = true
	return true
}

func (d *settingsDialog) OpenDirectOption(item settingsItem) bool {
	if len(item.options) == 0 {
		return false
	}
	d.title = item.label
	d.items = []settingsItem{item}
	d.cursor = 0
	d.selecting = true
	d.direct = true
	d.optionCursor = item.choice
	if d.optionCursor < 0 || d.optionCursor >= len(item.options) {
		d.optionCursor = 0
	}
	d.parentItems = nil
	d.parentCursor = 0
	d.active = true
	return true
}

func (d *settingsDialog) Close() {
	d.active = false
	d.selecting = false
	d.direct = false
	d.parentItems = nil
}
func (d *settingsDialog) Active() bool { return d != nil && d.active }

func (d *settingsDialog) HandleKey(k tui.Key) settingsAction {
	if d.selecting {
		return d.handleOptionKey(k)
	}
	switch k.Kind {
	case tui.KeyUp:
		if d.cursor > 0 {
			d.cursor--
		}
	case tui.KeyDown:
		if d.cursor < len(d.items)-1 {
			d.cursor++
		}
	case tui.KeyBackspace:
		if len(d.items) > 0 {
			it := d.items[d.cursor]
			if strings.HasPrefix(it.key, "quick_model_") {
				return settingsAction{Toggle: true, Key: it.key, StringValue: ""}
			}
		}
	case tui.KeyEsc:
		if len(d.parentItems) > 0 {
			d.items = d.parentItems
			d.cursor = d.parentCursor
			d.parentItems = nil
			d.parentCursor = 0
			d.title = "settings"
			return settingsAction{}
		}
		d.Close()
		return settingsAction{Close: true}
	case tui.KeyEnter:
		return d.toggleCurrent()
	case tui.KeyRune:
		if k.Rune == ' ' {
			return d.toggleCurrent()
		}
	}
	return settingsAction{}
}

func (d *settingsDialog) handleOptionKey(k tui.Key) settingsAction {
	it := d.items[d.cursor]
	switch k.Kind {
	case tui.KeyUp:
		if d.optionCursor > 0 {
			d.optionCursor--
		}
	case tui.KeyDown:
		if d.optionCursor < len(it.options)-1 {
			d.optionCursor++
		}
	case tui.KeyEsc:
		if d.direct {
			d.Close()
			return settingsAction{Close: true}
		}
		d.selecting = false
	case tui.KeyEnter:
		return d.selectCurrentOption()
	case tui.KeyRune:
		if k.Rune == ' ' {
			return d.selectCurrentOption()
		}
	}
	return settingsAction{}
}

func (d *settingsDialog) toggleCurrent() settingsAction {
	if len(d.items) == 0 {
		d.Close()
		return settingsAction{Close: true}
	}
	it := d.items[d.cursor]
	if it.disabled {
		return settingsAction{}
	}
	if it.picker {
		slotText := strings.TrimPrefix(it.key, "quick_model_")
		slot := 0
		for _, r := range slotText {
			if r < '0' || r > '9' {
				slot = 0
				break
			}
			slot = slot*10 + int(r-'0')
		}
		return settingsAction{ModelShortcutSlot: slot}
	}
	if len(it.children) > 0 {
		d.parentItems = d.items
		d.parentCursor = d.cursor
		d.items = it.children
		d.cursor = 0
		d.optionCursor = 0
		d.title = "settings: " + it.label
		return settingsAction{}
	}
	if len(it.options) > 0 {
		d.optionCursor = it.choice
		if d.optionCursor < 0 || d.optionCursor >= len(it.options) {
			d.optionCursor = 0
		}
		d.selecting = true
		return settingsAction{}
	}
	it.value = !it.value
	d.items[d.cursor] = it
	return settingsAction{Toggle: true, Key: it.key, Value: it.value}
}

func (d *settingsDialog) selectCurrentOption() settingsAction {
	if len(d.items) == 0 {
		d.Close()
		return settingsAction{Close: true}
	}
	it := d.items[d.cursor]
	if len(it.options) == 0 {
		d.selecting = false
		return settingsAction{}
	}
	if d.optionCursor < 0 || d.optionCursor >= len(it.options) {
		d.optionCursor = 0
	}
	it.choice = d.optionCursor
	d.items[d.cursor] = it
	d.selecting = false
	action := settingsAction{Toggle: true, Key: it.key, StringValue: it.options[it.choice].value}
	if d.direct {
		d.Close()
		action.Close = true
	}
	return action
}

func (d *settingsDialog) Render(th tui.Theme, width int) []string {
	if !d.Active() {
		return nil
	}
	if d.selecting {
		return d.renderOptions(th, width)
	}
	var lines []string
	lines = append(lines, frameHeader(th, d.title, width))
	if len(d.parentItems) > 0 {
		lines = append(lines, th.FG256(th.Muted, "change with enter/space, esc to go back:"))
	} else {
		lines = append(lines, th.FG256(th.Muted, "change with enter/space, esc to close:"))
	}
	for i, it := range d.items {
		box := "[ ]"
		if it.value {
			box = "[✓]"
		}
		plain := "  " + box + " " + it.label
		if it.picker || len(it.children) > 0 {
			box = "[→]"
			plain = "  " + box + " " + it.label
		} else if len(it.options) > 0 {
			box = "[→]"
			if it.choice < 0 || it.choice >= len(it.options) {
				it.choice = 0
			}
			plain = "  " + box + " " + it.label + ": " + it.options[it.choice].label
		}
		if it.hint != "" {
			plain += "  " + th.FG256(th.Muted, "("+it.hint+")")
		}
		if it.disabled {
			lines = append(lines, th.FG256(th.Muted, plain))
		} else if i == d.cursor {
			lines = append(lines, th.PadHighlight(plain, width))
		} else {
			lines = append(lines, plain)
		}
		if it.desc != "" {
			for _, desc := range wrapSettingDescription(it.desc, width, 6) {
				lines = append(lines, th.FG256(th.Muted, desc))
			}
		}
	}
	lines = append(lines, frameRule(th, width))
	return lines
}

func (d *settingsDialog) renderOptions(th tui.Theme, width int) []string {
	if len(d.items) == 0 || d.cursor < 0 || d.cursor >= len(d.items) {
		d.selecting = false
		return d.Render(th, width)
	}
	it := d.items[d.cursor]
	title := "settings: " + it.label
	if d.direct {
		title = d.title
	}
	lines := []string{frameHeader(th, title, width)}
	if it.desc != "" {
		lines = append(lines, th.FG256(th.Muted, it.desc))
	}
	lines = append(lines, th.FG256(th.Muted, "select with enter/space, esc to go back:"))
	for idx, opt := range it.options {
		marker := "  "
		if idx == it.choice {
			marker = "✓ "
		}
		plain := "  " + marker + opt.label
		if idx == d.optionCursor {
			lines = append(lines, th.PadHighlight(plain, width))
		} else {
			lines = append(lines, plain)
		}
		if opt.desc != "" {
			for _, desc := range wrapSettingDescription(opt.desc, width, 6) {
				lines = append(lines, th.FG256(th.Muted, desc))
			}
		}
	}
	lines = append(lines, frameRule(th, width))
	return lines
}

func wrapSettingDescription(desc string, width, indent int) []string {
	prefix := strings.Repeat(" ", indent)
	limit := width - indent
	if limit < 20 {
		limit = 20
	}
	words := strings.Fields(desc)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	line := words[0]
	for _, word := range words[1:] {
		candidate := line + " " + word
		if runewidth.StringWidth(candidate) <= limit {
			line = candidate
			continue
		}
		lines = append(lines, prefix+line)
		line = word
	}
	lines = append(lines, prefix+line)
	return lines
}
