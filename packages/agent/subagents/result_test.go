package subagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTurnResultValidationAndBoundedOutput(t *testing.T) {
	result := &TurnResult{
		Version:    ProtocolVersion,
		AgentID:    "agent-1",
		TurnID:     "turn-1",
		Status:     ResultSucceeded,
		Output:     "one\ntwo\nthree",
		Structured: json.RawMessage(`{"ok":true}`),
	}
	if err := result.Validate(100, 3); err != nil {
		t.Fatal(err)
	}
	bounded := result.Bounded(32, 2)
	if bounded == result || !strings.Contains(bounded.Output, "truncated") {
		t.Fatalf("bounded result = %#v", bounded)
	}
	if result.Output != "one\ntwo\nthree" {
		t.Fatal("Bounded mutated the original result")
	}
}

func TestTurnResultBoundedHonorsLineAndUTF8Limits(t *testing.T) {
	result := &TurnResult{
		Version: ProtocolVersion, AgentID: "a", TurnID: "t", Status: ResultSucceeded,
		Output: "😀\n第二行\n第三行",
	}
	bounded := result.Bounded(10, 2)
	if countLines(bounded.Output) > 2 {
		t.Fatalf("bounded lines = %d: %q", countLines(bounded.Output), bounded.Output)
	}
	if len([]byte(bounded.Output)) > 10 {
		t.Fatalf("bounded bytes = %d: %q", len([]byte(bounded.Output)), bounded.Output)
	}
	if !utf8.ValidString(bounded.Output) {
		t.Fatal("bounded output is not valid UTF-8")
	}
}

func TestDecodeTurnResultEventValidatesIdentityAndStatus(t *testing.T) {
	invalid := NewEvent(EventTurnResult, map[string]any{
		"status":  "unknown",
		"turn_id": "turn-1",
	})
	if _, err := decodeTurnResultEvent(invalid, "agent-1", 100, 10); err == nil {
		t.Fatal("invalid result status was accepted")
	}
	mismatched := NewEvent(EventTurnResult, map[string]any{
		"agent_id": "other",
		"status":   string(ResultSucceeded),
		"turn_id":  "turn-1",
	})
	if _, err := decodeTurnResultEvent(mismatched, "agent-1", 100, 10); err == nil {
		t.Fatal("mismatched result agent was accepted")
	}
	valid := NewEvent(EventTurnResult, map[string]any{"status": string(ResultSucceeded)})
	valid.AgentID = "agent-1"
	valid.TurnID = "turn-1"
	result, err := decodeTurnResultEvent(valid, "agent-1", 100, 10)
	if err != nil || result.AgentID != "agent-1" || result.TurnID != "turn-1" {
		t.Fatalf("valid result = %#v, err=%v", result, err)
	}
}

func TestTurnResultReferencesAndPersistence(t *testing.T) {
	if got := AgentRef("a"); got != "subagent://a" {
		t.Fatal(got)
	}
	if got := HistoryRef("a"); got != "subagent://a/history" {
		t.Fatal(got)
	}
	if got := ResultRef("a"); got != "subagent://a/result" {
		t.Fatal(got)
	}
	if got := PatchRef("a"); got != "subagent://a/patch" {
		t.Fatal(got)
	}
	state := t.TempDir()
	want := &TurnResult{AgentID: "a", TurnID: "t", Status: ResultFailed, Error: &ResultError{Code: "x", Message: "failed"}}
	if err := writeTurnResult(state, want); err != nil {
		t.Fatal(err)
	}
	got, err := readTurnResult(state)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != want.Status || got.Error == nil || got.Error.Code != "x" {
		t.Fatalf("got = %#v", got)
	}
	info, err := os.Stat(filepath.Join(state, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("result permissions = %o, want 600", info.Mode().Perm())
	}
}
