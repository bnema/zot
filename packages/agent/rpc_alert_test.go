package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/patriceckhart/zot/packages/agent/extproto"
)

func TestRPCExtAlertIsJSONOnly(t *testing.T) {
	var out bytes.Buffer
	hooks := &rpcExtHooks{server: &rpcServer{out: &out}}
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
