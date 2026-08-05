package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patriceckhart/zot/packages/provider"
)

func TestAppendMessageRejectsEmptyContent(t *testing.T) {
	session, err := NewSession(t.TempDir(), "/workspace", "provider", "model", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendMessage(provider.Message{Role: provider.RoleUser}); err == nil {
		t.Fatal("empty message content was accepted")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReadSessionSnapshotUsesLatestCompactionAndRepairsToolPairs(t *testing.T) {
	root := t.TempDir()
	session, err := NewSession(root, "/workspace", "anthropic", "claude", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendMessage(textMessage(provider.RoleUser, "old history")); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendCompaction([]provider.Message{
		textMessage(provider.RoleAssistant, "summary"),
		{Role: provider.RoleAssistant, Content: []provider.Content{
			provider.ToolCallBlock{ID: "call-1", Name: "read", Arguments: []byte(`{"path":"/tmp/file"}`)},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendUsage(provider.Usage{InputTokens: 10}, provider.Usage{InputTokens: 10}); err != nil {
		t.Fatal(err)
	}
	path := session.Path
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := ReadSessionSnapshot(path)
	if err != nil {
		t.Fatalf("ReadSessionSnapshot: %v", err)
	}
	if snapshot.Meta.ID == "" {
		t.Fatal("snapshot lost session metadata")
	}
	assertMessageTexts(t, snapshot.Messages, []string{"summary", "", ""})
	stub := snapshot.Messages[2].Content[0].(provider.ToolResultBlock).Content[0].(provider.TextBlock)
	if stub.Text != "tool call was aborted; no result recorded." {
		t.Errorf("repaired result text = %q", stub.Text)
	}
	if got := snapshot.Messages[1].Role; got != provider.RoleAssistant {
		t.Fatalf("repaired transcript message 1 role = %q, want assistant", got)
	}
	if got := snapshot.Messages[2].Role; got != provider.RoleTool {
		t.Fatalf("repaired transcript message 2 role = %q, want tool", got)
	}
	if got := snapshot.Messages[2].Content[0].(provider.ToolResultBlock).CallID; got != "call-1" {
		t.Errorf("repaired result call id = %q, want call-1", got)
	}
	if len(snapshot.UsageCheckpoints) != 1 {
		t.Fatalf("usage checkpoints = %d, want 1", len(snapshot.UsageCheckpoints))
	}
	if got := snapshot.UsageCheckpoints[0].MessageCount; got != 3 {
		t.Fatalf("usage checkpoint message count = %d, want repaired count 3", got)
	}
	if got := snapshot.UsageCheckpoints[0].Cumulative.InputTokens; got != 10 {
		t.Errorf("checkpoint cumulative input = %d, want 10", got)
	}

	opened, messages, err := OpenSession(path)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer opened.Close()
	if len(messages) != len(snapshot.Messages) {
		t.Fatalf("OpenSession messages = %d, snapshot messages = %d", len(messages), len(snapshot.Messages))
	}
}

func TestReadSessionSnapshotMergesExistingToolResultWithoutDuplicateRepair(t *testing.T) {
	root := t.TempDir()
	session, err := NewSession(root, "/workspace", "anthropic", "claude", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendMessage(provider.Message{
		Role: provider.RoleAssistant,
		Content: []provider.Content{
			provider.ToolCallBlock{ID: "call-1", Name: "read"},
			provider.ToolCallBlock{ID: "call-2", Name: "bash"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendMessage(provider.Message{
		Role: provider.RoleTool,
		Content: []provider.Content{provider.ToolResultBlock{
			CallID:  "call-1",
			Content: []provider.Content{provider.TextBlock{Text: "ok"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	path := session.Path
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := ReadSessionSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 2 {
		t.Fatalf("messages = %d, want existing tool row plus assistant row", len(snapshot.Messages))
	}
	tool := snapshot.Messages[1]
	if len(tool.Content) != 2 {
		t.Fatalf("tool result blocks = %d, want 2", len(tool.Content))
	}
	if got := tool.Content[0].(provider.ToolResultBlock).CallID; got != "call-1" {
		t.Errorf("existing result call id = %q", got)
	}
	if got := tool.Content[1].(provider.ToolResultBlock).CallID; got != "call-2" {
		t.Errorf("repaired result call id = %q", got)
	}
}

func TestReadSessionSnapshotRecordsCheckpointsAtEffectiveBoundaries(t *testing.T) {
	root := t.TempDir()
	session, err := NewSession(root, "/workspace", "anthropic", "claude", "test")
	if err != nil {
		t.Fatal(err)
	}
	_ = session.AppendMessage(textMessage(provider.RoleUser, "one"))
	_ = session.AppendUsage(provider.Usage{InputTokens: 3}, provider.Usage{InputTokens: 3})
	_ = session.AppendMessage(textMessage(provider.RoleAssistant, "two"))
	_ = session.AppendUsage(provider.Usage{InputTokens: 4}, provider.Usage{InputTokens: 7})
	_ = session.AppendCompaction([]provider.Message{textMessage(provider.RoleAssistant, "compact")})
	_ = session.AppendMessage(textMessage(provider.RoleUser, "tail"))
	_ = session.AppendUsage(provider.Usage{InputTokens: 2}, provider.Usage{InputTokens: 9})
	path := session.Path
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := ReadSessionSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	assertMessageTexts(t, snapshot.Messages, []string{"compact", "tail"})
	if len(snapshot.UsageCheckpoints) != 3 {
		t.Fatalf("checkpoints = %d, want 3", len(snapshot.UsageCheckpoints))
	}
	wantCounts := []int{1, 2, 2}
	wantCosts := []int{3, 7, 9}
	for i, checkpoint := range snapshot.UsageCheckpoints {
		if checkpoint.MessageCount != wantCounts[i] {
			t.Errorf("checkpoint %d count = %d, want %d", i, checkpoint.MessageCount, wantCounts[i])
		}
		if checkpoint.Cumulative.InputTokens != wantCosts[i] {
			t.Errorf("checkpoint %d input = %d, want %d", i, checkpoint.Cumulative.InputTokens, wantCosts[i])
		}
	}
}

func TestReadSessionSnapshotRejectsMalformedRows(t *testing.T) {
	meta := SessionMeta{ID: "session-1", CWD: "/workspace", Started: time.Now().UTC()}
	metaLine, err := json.Marshal(sessionLine{Type: "meta", Meta: &meta})
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{
		string(metaLine) + "\nnot-json\n",
		string(metaLine) + `
{"type":"message","message":{"role":"user"}}
`,
		string(metaLine) + `
{"type":"usage","cumulative":null}
`,
		string(metaLine) + `
{"type":"future-row"}
`,
	}
	for idx, contents := range cases {
		path := filepath.Join(t.TempDir(), "session.jsonl")
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadSessionSnapshot(path); err == nil {
			t.Errorf("case %d: ReadSessionSnapshot returned nil error", idx)
		}
		if _, _, err := OpenSession(path); err == nil {
			t.Errorf("case %d: OpenSession returned nil error", idx)
		}
	}
}

func TestReadSessionSnapshotKeepsOldOptionalMetadataReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.jsonl")
	contents := `{"type":"meta","meta":{"id":"old-id","cwd":"/workspace","model":"claude","provider":"anthropic"}}
{"type":"message","message":{"role":"user","content":[{"text":"hello"}]}}
`
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadSessionSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Meta.Parent != "" || snapshot.Meta.HideFromSessions {
		t.Fatalf("old optional metadata unexpectedly populated: %+v", snapshot.Meta)
	}
	assertMessageTexts(t, snapshot.Messages, []string{"hello"})
}

func TestBranchAfterCompactionRetainsPreCompactionUsageAtReplacementBoundary(t *testing.T) {
	root := t.TempDir()
	parent, err := NewSession(root, "/workspace", "test", "model", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"one", "two", "three"} {
		_ = parent.AppendMessage(textMessage(provider.RoleUser, text))
	}
	_ = parent.AppendUsage(provider.Usage{InputTokens: 10}, provider.Usage{InputTokens: 10})
	_ = parent.AppendCompaction([]provider.Message{textMessage(provider.RoleAssistant, "summary")})
	path := parent.Path
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	branchPath, err := BranchSessionHidden(path, root, "/workspace", "test", 1)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := SessionUsage(branchPath)
	if err != nil {
		t.Fatal(err)
	}
	if usage.InputTokens != 10 {
		t.Fatalf("branch usage input = %d, want pre-compaction cumulative usage", usage.InputTokens)
	}
}

func TestBranchSessionUsesSharedSnapshotAndPreservesUsage(t *testing.T) {
	root := t.TempDir()
	parent, err := NewSession(root, "/workspace", "anthropic", "claude", "test")
	if err != nil {
		t.Fatal(err)
	}
	_ = parent.AppendMessage(textMessage(provider.RoleUser, "first"))
	_ = parent.AppendUsage(provider.Usage{InputTokens: 11}, provider.Usage{InputTokens: 11})
	_ = parent.AppendMessage(textMessage(provider.RoleAssistant, "reply"))
	_ = parent.AppendUsage(provider.Usage{InputTokens: 12}, provider.Usage{InputTokens: 23})
	parentPath := parent.Path
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}

	branchPath, err := BranchSessionHidden(parentPath, root, "/workspace", "test", 1)
	if err != nil {
		t.Fatalf("BranchSessionHidden: %v", err)
	}
	branch, messages, err := OpenSession(branchPath)
	if err != nil {
		t.Fatal(err)
	}
	branch.Close()
	assertMessageTexts(t, messages, []string{"first"})
	if branch.Meta.ForkPoint != 1 {
		t.Errorf("fork point = %d, want 1", branch.Meta.ForkPoint)
	}
	if !branch.Meta.HideFromSessions {
		t.Error("hidden branch did not set HideFromSessions")
	}
	usage, err := SessionUsage(branchPath)
	if err != nil {
		t.Fatal(err)
	}
	if usage.InputTokens != 11 {
		t.Errorf("branch usage input = %d, want 11", usage.InputTokens)
	}

	if _, err := BranchSession(parentPath, root, "/workspace", "test", 99); err == nil {
		t.Error("out-of-range fork returned nil error")
	}
}

func TestBranchSessionRepairsToolBoundaryAndSupportsZeroAndEnd(t *testing.T) {
	root := t.TempDir()
	parent, err := NewSession(root, "/workspace", "anthropic", "claude", "test")
	if err != nil {
		t.Fatal(err)
	}
	_ = parent.AppendMessage(textMessage(provider.RoleUser, "before"))
	_ = parent.AppendMessage(provider.Message{Role: provider.RoleAssistant, Content: []provider.Content{
		provider.ToolCallBlock{ID: "call-1", Name: "read"},
	}})
	_ = parent.AppendMessage(provider.Message{Role: provider.RoleTool, Content: []provider.Content{
		provider.ToolResultBlock{CallID: "call-1", Content: []provider.Content{provider.TextBlock{Text: "result"}}},
	}})
	_ = parent.AppendMessage(textMessage(provider.RoleUser, "after"))
	path := parent.Path
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}

	toolBranchPath, err := BranchSession(parent.Path, root, "/workspace", "test", 2)
	if err != nil {
		t.Fatal(err)
	}
	toolBranch, toolMessages, err := OpenSession(toolBranchPath)
	if err != nil {
		t.Fatal(err)
	}
	toolBranch.Close()
	if len(toolMessages) != 3 {
		t.Fatalf("tool boundary branch messages = %d, want 3", len(toolMessages))
	}
	if toolBranch.Meta.ForkPoint != 3 {
		t.Errorf("tool boundary fork point = %d, want 3", toolBranch.Meta.ForkPoint)
	}

	zeroPath, err := BranchSessionHidden(path, root, "/workspace", "test", 0)
	if err != nil {
		t.Fatal(err)
	}
	zero, zeroMessages, err := OpenSession(zeroPath)
	if err != nil {
		t.Fatal(err)
	}
	zero.Close()
	if len(zeroMessages) != 0 || zero.Meta.ForkPoint != 0 {
		t.Errorf("zero branch = %d messages, fork %d", len(zeroMessages), zero.Meta.ForkPoint)
	}

	endPath, err := BranchSession(path, root, "/workspace", "test", 4)
	if err != nil {
		t.Fatal(err)
	}
	end, endMessages, err := OpenSession(endPath)
	if err != nil {
		t.Fatal(err)
	}
	end.Close()
	if len(endMessages) != 4 || end.Meta.ForkPoint != 4 {
		t.Errorf("end branch = %d messages, fork %d", len(endMessages), end.Meta.ForkPoint)
	}
}

func TestHiddenBranchesAreTreeVisibleButFlatListHidden(t *testing.T) {
	root := t.TempDir()
	parent, err := NewSession(root, "/workspace", "anthropic", "claude", "test")
	if err != nil {
		t.Fatal(err)
	}
	_ = parent.AppendMessage(textMessage(provider.RoleUser, "root"))
	parentPath := parent.Path
	parentID := parent.ID
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	childPath, err := BranchSessionHidden(parentPath, root, "/workspace", "test", 1)
	if err != nil {
		t.Fatal(err)
	}
	if summary := DescribeSession(childPath); !summary.HideFromSessions {
		t.Fatalf("DescribeSession hidden flag = %v, want true", summary.HideFromSessions)
	}
	if got := DescribeSessions(root, "/workspace"); len(got) != 1 || got[0].Path != parentPath {
		t.Fatalf("DescribeSessions = %+v, want only parent", got)
	}
	tree := BuildSessionTree(root, "/workspace")
	if len(tree) != 1 || tree[0].Meta.ID != parentID {
		t.Fatalf("tree roots = %+v, want parent root", tree)
	}
	if len(tree[0].Children) != 1 || tree[0].Children[0].Summary.Path != childPath {
		t.Fatalf("tree children = %+v, want hidden child", tree[0].Children)
	}
}

func TestDescribeSessionContextHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	session, err := NewSession(root, "/workspace", "test", "model", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendMessage(textMessage(provider.RoleUser, "cancel me")); err != nil {
		t.Fatal(err)
	}
	path := session.Path
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	summary := DescribeSessionContext(ctx, path)
	if summary.Path != path || summary.MessageCount != 0 {
		t.Fatalf("canceled summary = %+v, want path only", summary)
	}
}

func TestBuildSessionTreeStrictRejectsMalformedSessionMember(t *testing.T) {
	root := t.TempDir()
	cwd := "/workspace/strict-tree"
	parent, err := NewSession(root, cwd, "test", "model", "test")
	if err != nil {
		t.Fatal(err)
	}
	_ = parent.AppendMessage(textMessage(provider.RoleUser, "parent"))
	parentPath := parent.Path
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(SessionsDir(root, cwd), "bad.jsonl")
	if err := os.WriteFile(badPath, []byte("{not-json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSessionTreeStrict(root, cwd); err == nil {
		t.Fatal("BuildSessionTreeStrict accepted a malformed session member")
	}
	if _, err := os.Stat(parentPath); err != nil {
		t.Fatalf("valid parent disappeared: %v", err)
	}
}

func TestSessionUsageDetailUsesCurrentCacheAfterCounterReset(t *testing.T) {
	root := t.TempDir()
	session, err := NewSession(root, "/workspace", "anthropic", "model", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendMessage(textMessage(provider.RoleUser, "prompt")); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendUsage(provider.Usage{}, provider.Usage{
		InputTokens:          10,
		OutputTokens:         4,
		ReasoningTokens:      2,
		ReasoningTokensKnown: true,
		CacheReadTokens:      100,
		CacheWriteTokens:     40,
		CostUSD:              0.5,
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendUsage(provider.Usage{}, provider.Usage{
		InputTokens:          13,
		OutputTokens:         7,
		ReasoningTokens:      3,
		ReasoningTokensKnown: true,
		CacheReadTokens:      6,
		CacheWriteTokens:     2,
		CostUSD:              0.7,
	}); err != nil {
		t.Fatal(err)
	}
	path := session.Path
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	cumulative, last, err := SessionUsageDetail(path)
	if err != nil {
		t.Fatal(err)
	}
	if cumulative.CacheReadTokens != 6 || cumulative.CacheWriteTokens != 2 {
		t.Fatalf("cumulative cache = (%d, %d), want (6, 2)", cumulative.CacheReadTokens, cumulative.CacheWriteTokens)
	}
	if last.InputTokens != 3 || last.OutputTokens != 3 || last.ReasoningTokens != 1 {
		t.Fatalf("last token delta = %+v, want input=3 output=3 reasoning=1", last)
	}
	if last.CacheReadTokens != 6 || last.CacheWriteTokens != 2 {
		t.Fatalf("last cache = (%d, %d), want current checkpoint values (6, 2)", last.CacheReadTokens, last.CacheWriteTokens)
	}
	if !last.ReasoningTokensKnown {
		t.Fatal("last-turn reasoning-token known flag was lost")
	}
}

func TestSnapshotRemovesOrphanedToolResultsAndMapsCheckpointCount(t *testing.T) {
	root := t.TempDir()
	session, err := NewSession(root, "/workspace", "anthropic", "model", "test")
	if err != nil {
		t.Fatal(err)
	}
	_ = session.AppendMessage(textMessage(provider.RoleUser, "prompt"))
	_ = session.AppendMessage(provider.Message{Role: provider.RoleAssistant, Content: []provider.Content{
		provider.ToolCallBlock{ID: "call-1", Name: "read"},
	}})
	_ = session.AppendMessage(provider.Message{Role: provider.RoleTool, Content: []provider.Content{
		provider.ToolResultBlock{CallID: "orphan", Content: []provider.Content{provider.TextBlock{Text: "stale"}}},
	}})
	_ = session.AppendUsage(provider.Usage{}, provider.Usage{InputTokens: 9})
	path := session.Path
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot, err := ReadSessionSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 3 {
		t.Fatalf("repaired messages = %d, want 3", len(snapshot.Messages))
	}
	tool := snapshot.Messages[2]
	if len(tool.Content) != 1 {
		t.Fatalf("repaired tool blocks = %d, want synthetic result only", len(tool.Content))
	}
	result, ok := tool.Content[0].(provider.ToolResultBlock)
	if !ok || result.CallID != "call-1" {
		t.Fatalf("repaired result = %#v, want call-1", tool.Content[0])
	}
	if snapshot.UsageCheckpoints[0].MessageCount != 3 {
		t.Fatalf("checkpoint message count = %d, want repaired count 3", snapshot.UsageCheckpoints[0].MessageCount)
	}

	branchPath, err := BranchSessionHidden(path, root, "/workspace", "test", len(snapshot.Messages))
	if err != nil {
		t.Fatal(err)
	}
	branchSession, branchMessages, err := OpenSession(branchPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := branchSession.Close(); err != nil {
		t.Fatal(err)
	}
	if len(branchMessages) != 3 {
		t.Fatalf("branch messages = %d, want 3", len(branchMessages))
	}
	branchResult := branchMessages[2].Content[0].(provider.ToolResultBlock)
	if branchResult.CallID != "call-1" {
		t.Fatalf("branch kept orphan result call id %q", branchResult.CallID)
	}
}

func TestAppendCompactionEmptyAndHistoricalNullRowsRoundTrip(t *testing.T) {
	root := t.TempDir()
	session, err := NewSession(root, "/workspace", "anthropic", "model", "test")
	if err != nil {
		t.Fatal(err)
	}
	_ = session.AppendMessage(textMessage(provider.RoleUser, "before"))
	if err := session.AppendCompaction(nil); err != nil {
		t.Fatal(err)
	}
	if err := session.AppendCompaction([]provider.Message{}); err != nil {
		t.Fatal(err)
	}
	path := session.Path
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("empty compaction session was removed: %v", err)
	}
	snapshot, err := ReadSessionSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 0 {
		t.Fatalf("empty compaction messages = %d, want 0", len(snapshot.Messages))
	}

	meta := SessionMeta{ID: "historical-empty", CWD: "/workspace"}
	metaLine, _ := json.Marshal(sessionLine{Type: "meta", Meta: &meta})
	for _, compaction := range []string{`{"type":"compaction"}`, `{"type":"compaction","messages":null}`} {
		historicalPath := filepath.Join(t.TempDir(), "historical.jsonl")
		contents := string(metaLine) + "\n" + compaction + "\n"
		if err := os.WriteFile(historicalPath, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		historical, err := ReadSessionSnapshot(historicalPath)
		if err != nil {
			t.Fatalf("historical %s: %v", compaction, err)
		}
		if len(historical.Messages) != 0 {
			t.Fatalf("historical %s messages = %d, want 0", compaction, len(historical.Messages))
		}
	}
}

func TestBranchPreservesCumulativeCheckpointsForLastTurnDelta(t *testing.T) {
	root := t.TempDir()
	parent, err := NewSession(root, "/workspace", "anthropic", "model", "test")
	if err != nil {
		t.Fatal(err)
	}
	_ = parent.AppendMessage(textMessage(provider.RoleUser, "first"))
	_ = parent.AppendUsage(provider.Usage{}, provider.Usage{InputTokens: 10, CacheReadTokens: 4})
	_ = parent.AppendMessage(textMessage(provider.RoleAssistant, "second"))
	_ = parent.AppendUsage(provider.Usage{}, provider.Usage{InputTokens: 25, CacheReadTokens: 7})
	parentPath := parent.Path
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}

	branchPath, err := BranchSession(parentPath, root, "/workspace", "test", 2)
	if err != nil {
		t.Fatal(err)
	}
	cumulative, last, err := SessionUsageDetail(branchPath)
	if err != nil {
		t.Fatal(err)
	}
	if cumulative.InputTokens != 25 || cumulative.CacheReadTokens != 7 {
		t.Fatalf("branch cumulative = %+v, want input=25 cache_read=7", cumulative)
	}
	if last.InputTokens != 15 || last.CacheReadTokens != 3 {
		t.Fatalf("branch last turn = %+v, want input=15 cache_read=3", last)
	}
}

func textMessage(role provider.Role, text string) provider.Message {
	return provider.Message{Role: role, Content: []provider.Content{provider.TextBlock{Text: text}}}
}

func TestSnapshotMessagesDoNotExposeMutableRawCompactionState(t *testing.T) {
	// This is a small API contract check: callers can safely materialize a
	// branch from the returned slice without changing the source file.
	root := t.TempDir()
	session, err := NewSession(root, "/workspace", "anthropic", "claude", "test")
	if err != nil {
		t.Fatal(err)
	}
	_ = session.AppendMessage(textMessage(provider.RoleUser, "original"))
	path := session.Path
	_ = session.Close()
	snapshot, err := ReadSessionSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Messages[0].Content[0] = provider.TextBlock{Text: "changed in memory"}
	again, err := ReadSessionSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := extractText(again.Messages[0]); strings.Contains(got, "changed") {
		t.Fatal("mutating snapshot changed on-disk transcript")
	}
}
