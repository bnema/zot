package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/bnema/zut/packages/provider"
)

func TestReadSessionHistoryRetainsPreCompactionSegments(t *testing.T) {
	root := t.TempDir()
	cwd := "/workspace/history"
	session, err := NewSession(root, cwd, "test", "model", "test")
	if err != nil {
		t.Fatal(err)
	}
	oldPrompt := textMessage(provider.RoleUser, "old prompt")
	oldAnswer := textMessage(provider.RoleAssistant, "old answer")
	toolCall := provider.Message{
		Role:    provider.RoleAssistant,
		Content: []provider.Content{provider.ToolCallBlock{ID: "call-1", Name: "read"}},
	}
	toolResult := provider.Message{
		Role: provider.RoleTool,
		Content: []provider.Content{provider.ToolResultBlock{
			CallID:  "call-1",
			Content: []provider.Content{provider.TextBlock{Text: "old file"}},
		}},
	}
	for _, message := range []provider.Message{oldPrompt, oldAnswer, toolCall, toolResult} {
		if err := session.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.AppendCompaction([]provider.Message{
		{
			Role: provider.RoleUser,
			Meta: map[string]string{"compaction": "true"},
			Content: []provider.Content{
				provider.TextBlock{Text: "summary"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendMessage(textMessage(provider.RoleUser, "new prompt")); err != nil {
		t.Fatal(err)
	}
	path := session.Path
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	history, err := ReadSessionHistory(path)
	if err != nil {
		t.Fatalf("ReadSessionHistory: %v", err)
	}
	if len(history.Segments) != 2 {
		t.Fatalf("history segments = %d, want 2", len(history.Segments))
	}
	if history.Segments[0].Compacted {
		t.Fatal("pre-compaction segment marked compacted")
	}
	if !history.Segments[1].Compacted {
		t.Fatal("replacement segment not marked compacted")
	}
	assertMessageTexts(t, history.Segments[0].Messages, []string{"old prompt", "old answer", "", ""})
	assertMessageTexts(t, history.Segments[1].Messages, []string{"summary", "new prompt"})

	branchPath, err := BranchSessionHiddenFromHistory(path, root, cwd, "test", history.Segments[0], 1)
	if err != nil {
		t.Fatalf("BranchSessionHiddenFromHistory: %v", err)
	}
	branch, messages, err := OpenSession(branchPath)
	if err != nil {
		t.Fatalf("OpenSession branch: %v", err)
	}
	if err := branch.Close(); err != nil {
		t.Fatal(err)
	}
	assertMessageTexts(t, messages, []string{"old prompt"})
	if branch.Meta.Parent != history.Meta.ID {
		t.Fatalf("branch parent = %q, want %q", branch.Meta.Parent, history.Meta.ID)
	}
}

func TestReadSessionHistoryIgnoresExtensionStateRows(t *testing.T) {
	root := t.TempDir()
	session, err := NewSession(root, "/workspace/history-extension-state", "test", "model", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendMessage(textMessage(provider.RoleUser, "prompt")); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendExtensionState("tasked-phases", json.RawMessage(`{"version":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendMessage(textMessage(provider.RoleAssistant, "answer")); err != nil {
		t.Fatal(err)
	}
	path := session.Path
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	history, err := ReadSessionHistory(path)
	if err != nil {
		t.Fatalf("ReadSessionHistory: %v", err)
	}
	if len(history.Segments) != 1 {
		t.Fatalf("history segments = %d, want 1", len(history.Segments))
	}
	assertMessageTexts(t, history.Segments[0].Messages, []string{"prompt", "answer"})
}

func TestReadSessionHistoryRepairsEachSegmentIndependently(t *testing.T) {
	root := t.TempDir()
	session, err := NewSession(root, "/workspace/history-repair", "test", "model", "test")
	if err != nil {
		t.Fatal(err)
	}
	call := provider.Message{
		Role:    provider.RoleAssistant,
		Content: []provider.Content{provider.ToolCallBlock{ID: "call-1", Name: "bash"}},
		Time:    time.Unix(1, 0),
	}
	if err := session.AppendMessage(call); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendCompaction([]provider.Message{textMessage(provider.RoleUser, "summary")}); err != nil {
		t.Fatal(err)
	}
	path := session.Path
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	history, err := ReadSessionHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Segments) != 2 {
		t.Fatalf("history segments = %d, want 2", len(history.Segments))
	}
	if len(history.Segments[0].Messages) != 2 {
		t.Fatalf("repaired old segment messages = %d, want assistant plus stub result", len(history.Segments[0].Messages))
	}
	if history.Segments[0].Messages[1].Role != provider.RoleTool {
		t.Fatalf("repaired old segment tail role = %q, want tool", history.Segments[0].Messages[1].Role)
	}
}
