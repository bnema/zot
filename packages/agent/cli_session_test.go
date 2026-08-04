package agent

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/patriceckhart/zot/packages/agent/modes"
	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
)

func TestLiveInteractiveAgentUsesReplacementAgentForSessionResume(t *testing.T) {
	startup := core.NewAgent(nil, "startup-model", "", nil)
	startup.SetMessages([]provider.Message{{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "startup transcript"}},
	}})

	replacement := core.NewAgent(nil, "replacement-model", "", nil)
	iv := modes.NewInteractive(modes.InteractiveConfig{Agent: replacement})

	resumed := []provider.Message{{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "resumed transcript"}},
	}}
	liveInteractiveAgent(iv, startup).SetMessages(resumed)

	if got := firstMessageText(replacement.Messages()); got != "resumed transcript" {
		t.Fatalf("replacement agent transcript = %q, want resumed transcript", got)
	}
	if got := firstMessageText(startup.Messages()); got != "startup transcript" {
		t.Fatalf("startup agent transcript changed to %q", got)
	}
}

func TestLiveInteractiveAgentFallsBackBeforeInteractiveConstruction(t *testing.T) {
	startup := core.NewAgent(nil, "startup-model", "", nil)
	if got := liveInteractiveAgent(nil, startup); got != startup {
		t.Fatalf("fallback agent = %p, want %p", got, startup)
	}
}

func firstMessageText(messages []provider.Message) string {
	if len(messages) == 0 || len(messages[0].Content) == 0 {
		return ""
	}
	text, _ := messages[0].Content[0].(provider.TextBlock)
	return text.Text
}

func syntheticSession(t *testing.T, providerName, model string, usage provider.Usage) string {
	t.Helper()
	root := t.TempDir()
	cwd := t.TempDir()
	sess, err := core.NewSession(root, cwd, providerName, model, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendMessage(provider.Message{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "resumed transcript"}},
	}); err != nil {
		t.Fatal(err)
	}
	if usage != (provider.Usage{}) {
		if err := sess.AppendUsage(usage, usage); err != nil {
			t.Fatal(err)
		}
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	return sess.Path
}

func TestApplyInitialSessionResumeKeepsFreshEmptySessionOwned(t *testing.T) {
	root := t.TempDir()
	sess, err := core.NewSession(root, "/workspace", "provider", "model", "test")
	if err != nil {
		t.Fatal(err)
	}
	ag := core.NewAgent(nil, "model", "", nil)
	gotSess, gotAgent, providerName, model, err := applyInitialSessionResume(context.Background(), Args{}, Resolved{Provider: "provider", Model: "model"}, nil, sess, ag)
	if err != nil {
		t.Fatal(err)
	}
	if gotSess != sess || gotAgent != ag || providerName != "provider" || model != "model" {
		t.Fatalf("empty startup resume = session %p/%p agent %p/%p provider/model %q/%q", gotSess, sess, gotAgent, ag, providerName, model)
	}
	if _, err := os.Stat(sess.Path); err != nil {
		t.Fatalf("fresh session disappeared before close: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sess.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fresh empty session remained after close: %v", err)
	}
}

func TestPrepareSessionResumeRebuildsForStoredProviderAndModel(t *testing.T) {
	old := core.NewAgent(nil, "old-model", "", nil)
	old.SetMessages([]provider.Message{{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "current transcript"}},
	}})
	path := syntheticSession(t, "stored-provider", "stored-model", provider.Usage{
		InputTokens:  12,
		OutputTokens: 7,
	})

	var gotProvider, gotModel string
	candidate, err := prepareSessionResume(path, old, "current-provider", "old-model", func(providerName, model string) (*core.Agent, string, string, error) {
		gotProvider, gotModel = providerName, model
		return core.NewAgent(nil, model, "", nil), providerName, model, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.session.Close()

	if gotProvider != "stored-provider" || gotModel != "stored-model" {
		t.Fatalf("builder selection = %q/%q, want stored-provider/stored-model", gotProvider, gotModel)
	}
	if !candidate.rebuilt || candidate.agent == old {
		t.Fatalf("candidate rebuilt = %v, agent=%p, old=%p", candidate.rebuilt, candidate.agent, old)
	}
	if got := firstMessageText(candidate.agent.Messages()); got != "resumed transcript" {
		t.Fatalf("candidate transcript = %q, want resumed transcript", got)
	}
	if got := candidate.agent.Cost(); got.InputTokens != 12 || got.OutputTokens != 7 {
		t.Fatalf("candidate usage = %+v, want input=12 output=7", got)
	}
	if got := old.Messages(); len(got) != 1 || firstMessageText(got) != "current transcript" {
		t.Fatalf("current transcript changed during preparation: %+v", got)
	}
}

func TestPrepareSessionResumePreservesLegacyMissingMetadata(t *testing.T) {
	old := core.NewAgent(nil, "old-model", "", nil)
	old.SetMessages([]provider.Message{{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "current transcript"}},
	}})
	path := syntheticSession(t, "", "", provider.Usage{})

	candidate, err := prepareSessionResume(path, old, "current-provider", "old-model", func(string, string) (*core.Agent, string, string, error) {
		t.Fatal("legacy session unexpectedly requested a rebuild")
		return nil, "", "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.session.Close()

	if candidate.rebuilt || candidate.agent != old {
		t.Fatalf("legacy candidate = rebuilt %v, agent=%p; want current agent without rebuild", candidate.rebuilt, candidate.agent)
	}
	if got := firstMessageText(old.Messages()); got != "current transcript" {
		t.Fatalf("current transcript changed before commit: %q", got)
	}
	if got := firstMessageText(candidate.messages); got != "resumed transcript" {
		t.Fatalf("legacy candidate transcript = %q, want resumed transcript", got)
	}
}

func TestPrepareSessionResumeFailureLeavesCurrentAndCandidateFileUsable(t *testing.T) {
	old := core.NewAgent(nil, "old-model", "", nil)
	old.SetMessages([]provider.Message{{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "current transcript"}},
	}})
	path := syntheticSession(t, "stored-provider", "stored-model", provider.Usage{})
	wantErr := errors.New("synthetic builder failure")

	if _, err := prepareSessionResume(path, old, "current-provider", "old-model", func(string, string) (*core.Agent, string, string, error) {
		return nil, "", "", wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("prepare error = %v, want %v", err, wantErr)
	}
	if got := firstMessageText(old.Messages()); got != "current transcript" {
		t.Fatalf("current transcript changed after failed preparation: %q", got)
	}

	// The failed candidate must have released its append handle, leaving
	// the selected file readable and reopenable for a later retry.
	reopened, _, err := core.OpenSession(path)
	if err != nil {
		t.Fatalf("reopen candidate after failed preparation: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("candidate session disappeared after failed preparation: %v", err)
	}
}
