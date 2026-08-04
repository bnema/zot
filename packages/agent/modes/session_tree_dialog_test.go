package modes

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
	"github.com/patriceckhart/zot/packages/tui"
)

func TestSessionTreeTargetCarriesEffectiveBoundaryAndDraft(t *testing.T) {
	msgs := []provider.Message{
		treeTestMessage(provider.RoleUser, provider.TextBlock{Text: "first"}),
		treeTestMessage(provider.RoleAssistant, provider.TextBlock{Text: "answer"}),
		treeTestMessage(provider.RoleUser,
			provider.TextBlock{Text: "line one"},
			provider.ImageBlock{MimeType: "image/png", Data: []byte{1, 2}},
			provider.TextBlock{Text: "line two"}),
	}

	d := newSessionTreeDialog()
	if !d.OpenMessages(msgs) {
		t.Fatal("OpenMessages returned false")
	}
	d.cursor = 2
	act := d.HandleKey(tui.Key{Kind: tui.KeyEnter})
	if !act.Select {
		t.Fatal("enter did not select a target")
	}
	got := act.Target
	if got.SourcePath != "" || got.EffectiveIndex != 2 || got.SelectionBoundary != 2 {
		t.Fatalf("target indices/path = %+v", got)
	}
	if got.Role != provider.RoleUser || got.Boundary != sessionTreeMessageBoundary {
		t.Fatalf("target role/boundary = %+v", got)
	}
	if got.UserDraft != "line one\nline two" {
		t.Fatalf("user draft = %q", got.UserDraft)
	}
	// The scalar fields remain usable by the current integration layer.
	if act.MessageIdx != 2 || act.Role != provider.RoleUser || act.Prompt != got.UserDraft {
		t.Fatalf("legacy action fields = %+v", act)
	}
}

func TestSessionTreeUsesCurrentFamilyAndNoRootFallback(t *testing.T) {
	root := t.TempDir()
	cwd := "/workspace/current-family"
	first := newTreeTestSession(t, root, cwd, []provider.Message{
		treeTestMessage(provider.RoleUser, provider.TextBlock{Text: "unrelated root"}),
	})
	unrelatedFile, err := os.OpenFile(first, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unrelatedFile.WriteString("{unrelated corruption}\n"); err != nil {
		_ = unrelatedFile.Close()
		t.Fatal(err)
	}
	if err := unrelatedFile.Close(); err != nil {
		t.Fatal(err)
	}
	current := newTreeTestSession(t, root, cwd, []provider.Message{
		treeTestMessage(provider.RoleUser, provider.TextBlock{Text: "current root"}),
	})

	d := newSessionTreeDialog()
	if !d.OpenSessionFamily(root, cwd, current) {
		t.Fatal("OpenSessionFamily returned false")
	}
	for _, item := range d.items {
		if item.path != current {
			t.Fatalf("item from unrelated family: %+v (first root %q)", item, first)
		}
	}
	if !d.Active() {
		t.Fatal("dialog is not active")
	}
	oldItems := append([]sessionTreeItem(nil), d.items...)
	if d.OpenSessionFamily(root, cwd, t.TempDir()+"/not-a-session.jsonl") {
		t.Fatal("missing current path selected a forest root")
	}
	if !d.Active() || len(d.items) != len(oldItems) || d.items[0].path != oldItems[0].path {
		t.Fatal("failed open changed the active dialog")
	}
}

func TestSessionTreeShowsEmptyAndDetachedBoundaries(t *testing.T) {
	root := t.TempDir()
	cwd := "/workspace/stale-compaction"
	parent := newTreeTestSession(t, root, cwd, []provider.Message{
		treeTestMessage(provider.RoleUser, provider.TextBlock{Text: "one"}),
		treeTestMessage(provider.RoleAssistant, provider.TextBlock{Text: "two"}),
		treeTestMessage(provider.RoleUser, provider.TextBlock{Text: "three"}),
	})

	// Both children are created against the old effective parent. The first
	// has no post-fork rows; the second's fork point becomes stale after the
	// parent is compacted to one message.
	emptyChild, err := core.BranchSessionHidden(parent, root, cwd, "test", 1)
	if err != nil {
		t.Fatal(err)
	}
	staleChild, err := core.BranchSessionHidden(parent, root, cwd, "test", 3)
	if err != nil {
		t.Fatal(err)
	}
	parentSession, _, err := core.OpenSession(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := parentSession.AppendCompaction([]provider.Message{
		treeTestMessage(provider.RoleUser, provider.TextBlock{Text: "compacted"}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := parentSession.Close(); err != nil {
		t.Fatal(err)
	}

	d := newSessionTreeDialog()
	if !d.OpenSessionFamily(root, cwd, parent) {
		t.Fatal("OpenSessionFamily returned false after compaction")
	}
	var empty, detached *sessionTreeItem
	staleMessageRows := 0
	for i := range d.items {
		item := &d.items[i]
		switch {
		case item.target.IsEmptyBoundary() && item.path == emptyChild:
			empty = item
		case item.target.IsDetachedBoundary() && item.path == staleChild:
			detached = item
		case item.path == staleChild && !item.target.IsBoundary():
			staleMessageRows++
		}
	}
	if empty == nil || detached == nil {
		t.Fatalf("boundaries missing from tree: empty=%v detached=%v items=%+v", empty != nil, detached != nil, d.items)
	}
	if staleMessageRows != 3 {
		t.Fatalf("detached branch rendered %d message rows, want its complete snapshot (3)", staleMessageRows)
	}
	if empty.target.SelectionBoundary != 1 || detached.target.SelectionBoundary != 3 {
		t.Fatalf("boundary indices = empty %+v detached %+v", empty.target, detached.target)
	}
	if !strings.Contains(strings.ToLower(empty.label), "empty") || !strings.Contains(strings.ToLower(detached.label), "detached") {
		t.Fatalf("boundary labels = %q / %q", empty.label, detached.label)
	}
}

func TestSessionTreePreflightIsAtomicOnMalformedFamilyMember(t *testing.T) {
	root := t.TempDir()
	cwd := "/workspace/atomic-tree"
	parent := newTreeTestSession(t, root, cwd, []provider.Message{
		treeTestMessage(provider.RoleUser, provider.TextBlock{Text: "parent"}),
	})
	child, err := core.BranchSessionHidden(parent, root, cwd, "test", 1)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(child, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{not valid json}\n"); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	d := newSessionTreeDialog()
	if !d.OpenMessages([]provider.Message{treeTestMessage(provider.RoleUser, provider.TextBlock{Text: "keep"})}) {
		t.Fatal("failed to seed dialog")
	}
	before := append([]sessionTreeItem(nil), d.items...)
	if d.OpenSessionFamily(root, cwd, parent) {
		t.Fatal("malformed family member passed preflight")
	}
	if !d.Active() || len(d.items) != len(before) || d.items[0].label != before[0].label {
		t.Fatalf("preflight failure changed dialog: active=%v items=%+v", d.Active(), d.items)
	}
}

func TestSessionTreeRenderAndPagingAreWidthAndHeightSafe(t *testing.T) {
	msgs := make([]provider.Message, 0, 20)
	for i := 0; i < 20; i++ {
		msgs = append(msgs, treeTestMessage(provider.RoleUser,
			provider.TextBlock{Text: strings.Repeat("界", 4) + " long row"}))
	}
	d := newSessionTreeDialog()
	d.MaxRows = 2
	if !d.OpenMessages(msgs) {
		t.Fatal("OpenMessages returned false")
	}
	if got := d.HandleKey(tui.Key{Kind: tui.KeyPageUp}); !got.Select && d.cursor != 17 {
		t.Fatalf("page up cursor = %d, action=%+v", d.cursor, got)
	}
	for _, width := range []int{0, 1, 7, 17} {
		for _, line := range d.Render(tui.Theme{}, width) {
			if got := sessionTreeANSIWidth(line); got > width {
				t.Fatalf("width %d rendered %d cells: %q", width, got, line)
			}
		}
	}
	if d.viewTop < 0 || d.viewTop+d.MaxRows > len(d.items) {
		t.Fatalf("invalid viewport top=%d rows=%d items=%d", d.viewTop, d.MaxRows, len(d.items))
	}
}

func TestSessionTreeCheckoutRefreshesLastTurnUsageIncludingZero(t *testing.T) {
	root := t.TempDir()
	cwd := "/workspace/tree-usage"
	path := newTreeTestSession(t, root, cwd, []provider.Message{
		treeTestMessage(provider.RoleUser, provider.TextBlock{Text: "question"}),
		treeTestMessage(provider.RoleAssistant, provider.TextBlock{Text: "answer"}),
	})

	initial := core.NewAgent(nil, "model", "", nil)
	initial.SeedLastTurnUsage(provider.Usage{InputTokens: 99})
	selected := core.NewAgent(nil, "model", "", nil)
	selected.SetMessages(initial.Messages())
	selected.SeedLastTurnUsage(provider.Usage{
		InputTokens:      7,
		CacheReadTokens:  11,
		CacheWriteTokens: 13,
	})

	i := NewInteractive(InteractiveConfig{
		Agent:              initial,
		CWD:                cwd,
		SessionsRoot:       root,
		CurrentSessionPath: func() string { return path },
	})
	i.cfg.LoadSession = func(string) error {
		i.agent = selected
		return nil
	}

	target := sessionTreeTarget{
		SourcePath:        path,
		EffectiveIndex:    1,
		SelectionBoundary: 2,
		Role:              provider.RoleAssistant,
	}
	i.applySessionTreeTarget(target, 1)
	if i.lastCtxInput != 31 {
		t.Fatalf("checked-out context usage = %d, want 31", i.lastCtxInput)
	}

	selected.SeedLastTurnUsage(provider.Usage{})
	i.applySessionTreeTarget(target, 1)
	if i.lastCtxInput != 0 {
		t.Fatalf("checked-out zero usage left stale context value %d", i.lastCtxInput)
	}
}

func treeTestMessage(role provider.Role, content ...provider.Content) provider.Message {
	return provider.Message{Role: role, Content: content, Time: time.Unix(1, 0)}
}

func newTreeTestSession(t *testing.T, root, cwd string, msgs []provider.Message) string {
	t.Helper()
	sess, err := core.NewSession(root, cwd, "test", "model", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range msgs {
		if err := sess.AppendMessage(msg); err != nil {
			_ = sess.Close()
			t.Fatal(err)
		}
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	return sess.Path
}
