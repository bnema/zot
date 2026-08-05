package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bnema/zut/packages/provider"
)

type titleTestClient struct {
	request provider.Request
	events  []provider.Event
	err     error
}

func (c *titleTestClient) Name() string { return "title-test" }

func (c *titleTestClient) Stream(_ context.Context, req provider.Request) (<-chan provider.Event, error) {
	c.request = req
	if c.err != nil {
		return nil, c.err
	}
	out := make(chan provider.Event, len(c.events))
	for _, event := range c.events {
		out <- event
	}
	close(out)
	return out, nil
}

type blockingTitleClient struct {
	started chan struct{}
}

func (c *blockingTitleClient) Name() string { return "blocking-title" }

func (c *blockingTitleClient) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, error) {
	close(c.started)
	out := make(chan provider.Event)
	go func() {
		<-ctx.Done()
		close(out)
	}()
	return out, nil
}

func TestGenerateSessionTitleUsesAHiddenMinimalRequest(t *testing.T) {
	client := &titleTestClient{events: []provider.Event{
		provider.EventTextDelta{Delta: "Fix "},
		provider.EventTextDelta{Delta: "login flow"},
		provider.EventDone{Stop: provider.StopEnd},
	}}

	got, err := GenerateSessionTitle(context.Background(), client, "test-model", "Please fix the login flow")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Fix login flow" {
		t.Fatalf("title = %q, want %q", got, "Fix login flow")
	}
	if client.request.Model != "test-model" {
		t.Fatalf("model = %q, want test-model", client.request.Model)
	}
	if client.request.System == "" {
		t.Fatal("title request has no system instruction")
	}
	if len(client.request.Tools) != 0 {
		t.Fatalf("title request advertised %d tools, want none", len(client.request.Tools))
	}
	if client.request.MaxTokens != 16 {
		t.Fatalf("max tokens = %d, want 16", client.request.MaxTokens)
	}
	if len(client.request.Messages) != 1 || client.request.Messages[0].Role != provider.RoleUser {
		t.Fatalf("title request messages = %#v, want one user message", client.request.Messages)
	}
}

func TestGenerateSessionTitleFallsBackToFinalMessageWhenNoDeltas(t *testing.T) {
	client := &titleTestClient{events: []provider.Event{
		provider.EventDone{Stop: provider.StopEnd, Message: provider.Message{
			Role:    provider.RoleAssistant,
			Content: []provider.Content{provider.TextBlock{Text: "Title: \"Review API\""}},
		}},
	}}

	got, err := GenerateSessionTitle(context.Background(), client, "test-model", "Review the API")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Review API" {
		t.Fatalf("title = %q, want %q", got, "Review API")
	}
}

func TestGenerateSessionTitlePropagatesStreamErrors(t *testing.T) {
	want := errors.New("provider unavailable")
	client := &titleTestClient{events: []provider.Event{
		provider.EventDone{Stop: provider.StopError, Err: want},
	}}
	if _, err := GenerateSessionTitle(context.Background(), client, "test-model", "Do work"); !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped %v", err, want)
	}
}

func TestGenerateSessionTitleRejectsIncompleteStream(t *testing.T) {
	client := &titleTestClient{}
	if _, err := GenerateSessionTitle(context.Background(), client, "test-model", "Do work"); err == nil {
		t.Fatal("incomplete stream returned nil error")
	}
}

func TestGenerateSessionTitleStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &blockingTitleClient{started: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		_, err := GenerateSessionTitle(ctx, client, "test-model", "Do work")
		result <- err
	}()
	<-client.started
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("title generation did not stop after context cancellation")
	}
}

func TestNormalizeSessionTitleIsConciseAndSafe(t *testing.T) {
	got := NormalizeSessionTitle("\n  Title: `A very long title that needs to be shortened because it is too long`  ")
	if got != "A very long title that needs to be shor…" {
		t.Fatalf("normalized title = %q", got)
	}
	if len([]rune(got)) != SessionTitleMaxChars {
		t.Fatalf("normalized title rune count = %d, want %d", len([]rune(got)), SessionTitleMaxChars)
	}
}

func TestOpenSessionRestoresAppendOnlyTitle(t *testing.T) {
	root := t.TempDir()
	session, err := NewSession(root, "/workspace", "test", "test-model", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendMessage(provider.Message{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "work"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.UpdateTitle("Fix login"); err != nil {
		t.Fatal(err)
	}
	path := session.Path
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, _, err := OpenSession(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Title != "Fix login" {
		t.Fatalf("restored title = %q, want %q", reopened.Title, "Fix login")
	}
	if reopened.Meta.Title != "" {
		t.Fatalf("meta title = %q, want append-only meta title to remain empty", reopened.Meta.Title)
	}
}

func TestUpdateTitleLeavesMemoryUnchangedWhenWriteFails(t *testing.T) {
	root := t.TempDir()
	session, err := NewSession(root, "/workspace", "test", "test-model", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AppendMessage(provider.Message{
		Role:    provider.RoleUser,
		Content: []provider.Content{provider.TextBlock{Text: "work"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	if err := session.UpdateTitle("unwritten title"); err == nil {
		t.Fatal("UpdateTitle returned nil after the session writer was closed")
	}
	if session.Title != "" {
		t.Fatalf("in-memory title = %q after failed write, want empty", session.Title)
	}
}

func TestNormalizeSessionTitlePreservesUnicodeBoundaries(t *testing.T) {
	got := NormalizeSessionTitle("éééééééééééééééééééééééééééééééééééééééééééé")
	if len([]rune(got)) != SessionTitleMaxChars {
		t.Fatalf("rune count = %d, want %d", len([]rune(got)), SessionTitleMaxChars)
	}
	if !json.Valid([]byte(`"` + got + `"`)) {
		t.Fatalf("normalized unicode title is not valid UTF-8 JSON: %q", got)
	}
}
