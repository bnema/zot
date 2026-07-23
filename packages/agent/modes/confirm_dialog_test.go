package modes

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/tui"
)

func TestConfirmToolCallAttachesDiffBeforeDecision(t *testing.T) {
	dialog := newConfirmDialog()
	btw := &btwDialog{
		active:   true,
		toolView: &tui.View{Theme: tui.Dark},
		turns: []btwTurn{{Tools: []tui.ToolCallView{{
			ID: "call-1", Name: "edit",
		}}}},
		editor: tui.NewEditor(""),
	}
	i := &Interactive{
		toolCalls: map[string]*tui.ToolCallView{
			"call-1": {ID: "call-1", Name: "edit"},
		},
		btwDialog:     btw,
		confirmDialog: dialog,
		dirty:         make(chan struct{}, 1),
	}
	decision := make(chan core.ConfirmDecision, 1)
	go func() {
		decision <- i.ConfirmToolCall(core.ToolCallConfirmation{
			ID:      "call-1",
			Name:    "edit",
			Summary: "sample.go",
			Content: "-old\n+new\n",
		})
	}()

	deadline := time.Now().Add(time.Second)
	for !dialog.Active() && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if !dialog.Active() {
		t.Fatal("confirmation dialog did not open")
	}
	i.mu.Lock()
	preview := i.toolCalls["call-1"].Preview
	i.mu.Unlock()
	if preview != "-old\n+new\n" {
		t.Fatalf("tool preview = %q", preview)
	}
	btw.mu.Lock()
	btwPreview := btw.turns[0].Tools[0].Preview
	btw.mu.Unlock()
	if btwPreview != "-old\n+new\n" {
		t.Fatalf("/btw tool preview = %q", btwPreview)
	}
	combined := strings.Join(renderBtwConfirmation(tui.Dark, 80, btw, dialog), "\n")
	for _, want := range []string{"btw - side chat", "old", "new", "confirm tool call"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("combined /btw confirmation missing %q:\n%s", want, combined)
		}
	}

	dialog.HandleKey(tui.Key{Kind: tui.KeyEnter})
	select {
	case got := <-decision:
		if !got.Allow {
			t.Fatalf("confirmation decision = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("confirmation did not return")
	}
}

func TestConfirmDialogAllowsToolExpansion(t *testing.T) {
	resp := make(chan core.ConfirmDecision, 1)
	dialog := newConfirmDialog()
	dialog.Enqueue(&confirmRequest{
		toolName: "edit",
		preview:  "large edit",
		resp:     resp,
	})
	args, err := json.Marshal(map[string]any{
		"path": "sample.ts",
		"edits": []map[string]string{{
			"oldText": "old",
			"newText": strings.Repeat("const value = 1\n", 20),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	view := &tui.View{
		ToolCalls: []tui.ToolCallView{{
			ID:         "call-1",
			Name:       "edit",
			RawJSONBuf: string(args),
			LivePath:   "sample.ts",
		}},
	}
	i := &Interactive{
		view:          view,
		confirmDialog: dialog,
		dirty:         make(chan struct{}, 1),
	}
	if rendered := strings.Join(view.Build(80), "\n"); !strings.Contains(rendered, "ctrl+o to expand") {
		t.Fatalf("test preview was not initially collapsed:\n%s", rendered)
	}

	i.handleKey(context.Background(), tui.Key{Kind: tui.KeyCtrlO})

	if !i.view.ExpandAll {
		t.Fatal("ctrl+o did not expand tool previews while confirmation was active")
	}
	if rendered := strings.Join(view.Build(80), "\n"); strings.Contains(rendered, "ctrl+o to expand") {
		t.Fatalf("live edit preview remained collapsed after ctrl+o:\n%s", rendered)
	}
	if !dialog.Active() {
		t.Fatal("ctrl+o closed the confirmation dialog")
	}
	select {
	case decision := <-resp:
		t.Fatalf("ctrl+o unexpectedly answered confirmation: %+v", decision)
	default:
	}

	i.handleKey(context.Background(), tui.Key{Kind: tui.KeyCtrlO})
	if i.view.ExpandAll {
		t.Fatal("second ctrl+o did not collapse tool previews during confirmation")
	}
	if rendered := strings.Join(view.Build(80), "\n"); !strings.Contains(rendered, "ctrl+o to expand") {
		t.Fatalf("live edit preview remained expanded after second ctrl+o:\n%s", rendered)
	}
	if !dialog.Active() {
		t.Fatal("second ctrl+o closed the confirmation dialog")
	}
}
