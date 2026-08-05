package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bnema/zut/packages/agent/extproto"
)

func TestRPCExtAlertIsJSONOnly(t *testing.T) {
	var out bytes.Buffer
	hooks := &rpcExtHooks{server: &rpcServer{out: &out, started: true, authed: true}}
	hooks.Alert("question-ext", extproto.AlertRequest{Kind: extproto.AlertKindBell, Reason: "question_ready"})

	if strings.ContainsRune(out.String(), '\a') {
		t.Fatalf("RPC alert contains raw BEL: %q", out.String())
	}
	var frame map[string]any
	if err := json.Unmarshal(out.Bytes(), &frame); err != nil {
		t.Fatalf("decode RPC alert: %v", err)
	}
	if frame["type"] != "ext_alert" || frame["extension"] != "question-ext" || frame["kind"] != extproto.AlertKindBell || frame["reason"] != "question_ready" {
		t.Fatalf("RPC alert frame = %#v", frame)
	}
}

func TestRPCExtAlertWaitsForAuthentication(t *testing.T) {
	t.Setenv("ZUTCORE_RPC_TOKEN", "synthetic-token")

	var out bytes.Buffer
	server := &rpcServer{out: &out, provider: "test", model: "test-model", version: "test-version"}
	hooks := &rpcExtHooks{server: server}
	hooks.Alert("question-ext", extproto.AlertRequest{Kind: extproto.AlertKindBell, Reason: "question_ready"})
	if out.Len() != 0 {
		t.Fatalf("pre-auth RPC output = %q, want empty", out.String())
	}

	if err := server.run(strings.NewReader(`{"type":"hello","id":"1","token":"synthetic-token"}` + "\n")); err != nil {
		t.Fatalf("run RPC server: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"type":"response"`) || !strings.Contains(lines[1], `"type":"ext_alert"`) {
		t.Fatalf("authenticated RPC frames = %q, want response followed by ext_alert", out.String())
	}
}
