package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/bnema/zut/packages/agent/modes"
	"github.com/bnema/zut/packages/core"
	"github.com/bnema/zut/packages/provider"
)

func TestTrimMessagesForResumeCarriesDeferredToolActivation(t *testing.T) {
	msgs := make([]provider.Message, 0, 101)
	msgs = append(msgs, provider.Message{
		Role:           provider.RoleTool,
		AddedToolNames: []string{"lookup_weather"},
		Content:        []provider.Content{provider.ToolResultBlock{CallID: "old-call"}},
	})
	for idx := 1; idx < 101; idx++ {
		msgs = append(msgs, provider.Message{
			Role:    provider.RoleUser,
			Content: []provider.Content{provider.TextBlock{Text: "message"}},
		})
	}
	trimmed := trimMessagesForResume(msgs, 100)
	if len(trimmed) != 100 {
		t.Fatalf("trimmed message count = %d, want 100", len(trimmed))
	}
	if len(trimmed[0].AddedToolNames) != 1 || trimmed[0].AddedToolNames[0] != "lookup_weather" {
		t.Fatalf("trimmed activation markers = %v, want lookup_weather", trimmed[0].AddedToolNames)
	}
}

func TestTrimMessagesForResumeKeepsCompactionSummaryAtTailBoundary(t *testing.T) {
	for _, total := range []int{100, 101, 102} {
		t.Run(fmt.Sprintf("%d messages", total), func(t *testing.T) {
			msgs := make([]provider.Message, 0, total)
			msgs = append(msgs, provider.Message{
				Role: provider.RoleUser,
				Meta: map[string]string{"compaction": "true"},
				Content: []provider.Content{
					provider.TextBlock{Text: "## Context Summary (compacted)"},
				},
			})
			for idx := 1; idx < total; idx++ {
				msgs = append(msgs, provider.Message{
					Role:    provider.RoleUser,
					Content: []provider.Content{provider.TextBlock{Text: fmt.Sprintf("message-%d", idx)}},
				})
			}

			trimmed := trimMessagesForResume(msgs, 100)
			if len(trimmed) > 100 {
				t.Fatalf("trimmed message count = %d, want at most 100", len(trimmed))
			}
			if len(trimmed) == 0 || trimmed[0].Meta["compaction"] != "true" {
				t.Fatalf("trimmed transcript lost compaction summary: %+v", trimmed)
			}
		})
	}
}

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

func TestPersistModelCallbackDoesNotReenterSessionTransition(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())
	sess, err := core.NewSession(t.TempDir(), t.TempDir(), "old-provider", "old-model", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	var persistMu sync.Mutex
	activeProvider, activeModel := "old-provider", "old-model"
	persistModel := newPersistModelCallback(&persistMu, &sess, &activeProvider, &activeModel, nil)

	var transitionMu sync.RWMutex
	sessionTransition := newSessionTransition(&transitionMu)
	done := make(chan struct{})
	go func() {
		sessionTransition(func() {
			persistModel("new-provider", "new-model")
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("model persistence deadlocked inside the session transition")
	}
	if sess.Meta.Provider != "new-provider" || sess.Meta.Model != "new-model" {
		t.Fatalf("session model = %q/%q, want new-provider/new-model", sess.Meta.Provider, sess.Meta.Model)
	}
	if activeProvider != "new-provider" || activeModel != "new-model" {
		t.Fatalf("active model = %q/%q, want new-provider/new-model", activeProvider, activeModel)
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

func TestPrepareSessionResumePreservesCompactHandoffMetadata(t *testing.T) {
	path := syntheticSession(t, "stored-provider", "stored-model", provider.Usage{})
	session, _, err := core.OpenSession(path)
	if err != nil {
		t.Fatal(err)
	}
	handoff := json.RawMessage(`{"version":1,"reason":"status_rescue","rescue_attempts":1}`)
	if err := session.UpdateCompactHandoff(handoff); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	current := core.NewAgent(nil, "current-model", "", nil)
	candidate, err := prepareSessionResume(path, current, "stored-provider", "stored-model", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.session.Close()
	if got := string(candidate.session.Meta.CompactHandoff); got != string(handoff) {
		t.Fatalf("candidate compact handoff = %q, want %q", got, handoff)
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

func TestPrepareSessionResumeHonorsExplicitProviderModelFields(t *testing.T) {
	for _, tc := range []struct {
		name             string
		explicitProvider bool
		explicitModel    bool
		wantProvider     string
		wantModel        string
		wantBuild        bool
	}{
		{name: "both", explicitProvider: true, explicitModel: true, wantProvider: "current-provider", wantModel: "current-model"},
		{name: "provider-only", explicitProvider: true, wantProvider: "current-provider", wantModel: "stored-model", wantBuild: true},
		{name: "model-only", explicitModel: true, wantProvider: "stored-provider", wantModel: "current-model", wantBuild: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := core.NewAgent(nil, "old-model", "", nil)
			path := syntheticSession(t, "stored-provider", "stored-model", provider.Usage{})
			var gotProvider, gotModel string
			candidate, err := prepareSessionResumeWithOptions(path, old, "current-provider", "current-model", tc.explicitProvider, tc.explicitModel, func(providerName, model string) (*core.Agent, string, string, error) {
				gotProvider, gotModel = providerName, model
				return core.NewAgent(nil, model, "", nil), providerName, model, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer candidate.session.Close()
			if tc.wantBuild {
				if gotProvider != tc.wantProvider || gotModel != tc.wantModel {
					t.Fatalf("builder selection = %q/%q, want %q/%q", gotProvider, gotModel, tc.wantProvider, tc.wantModel)
				}
			} else if candidate.provider != tc.wantProvider || candidate.model != tc.wantModel || candidate.rebuilt {
				t.Fatalf("candidate selection = %q/%q rebuilt=%v, want %q/%q without rebuild", candidate.provider, candidate.model, candidate.rebuilt, tc.wantProvider, tc.wantModel)
			}
		})
	}
}
