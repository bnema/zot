package modes

import (
	"context"
	"testing"

	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
)

func TestSessionsSlashUsesAgentSessionRoot(t *testing.T) {
	zotHome := t.TempDir()
	agentRoot := t.TempDir()
	cwd := t.TempDir()

	session, err := core.NewSession(agentRoot, cwd, "test", "test-model", "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendMessage(provider.Message{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "agent session"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	i := NewInteractive(InteractiveConfig{
		ZotHome:      zotHome,
		SessionsRoot: agentRoot,
		CWD:          cwd,
	})
	i.runSlash(context.Background(), "/sessions")
	defer i.sessionDialog.Close()
	for event := range i.sessionLoads {
		i.sessionDialog.ApplyLoad(event)
	}

	if got := len(i.sessionDialog.sessions); got != 1 {
		t.Fatalf("session picker entries = %d, want 1 from agent session root", got)
	}
	if got := i.sessionDialog.sessions[0].Path; got != session.Path {
		t.Fatalf("session picker path = %q, want %q", got, session.Path)
	}
}

func TestSessionsRootDefaultsToZotHome(t *testing.T) {
	i := &Interactive{cfg: InteractiveConfig{ZotHome: "/zot/home"}}
	if got := i.sessionsRoot(); got != "/zot/home" {
		t.Fatalf("sessions root = %q, want ZotHome fallback", got)
	}
}
