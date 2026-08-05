package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bnema/zut/packages/agent/modes/bot"
	"github.com/bnema/zut/packages/provider"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func telegramResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func testClient(transport http.RoundTripper) *Client {
	return &Client{
		token: "test",
		http:  &http.Client{Transport: transport},
	}
}

func TestClientDeleteWebhookPreservesPendingUpdates(t *testing.T) {
	client := testClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", req.Method)
		}
		if req.URL.Path != "/bottest/deleteWebhook" {
			t.Fatalf("path = %q, want deleteWebhook endpoint", req.URL.Path)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]bool
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got["drop_pending_updates"] {
			t.Fatal("drop_pending_updates = true, want false")
		}
		return telegramResponse(`{"ok":true,"result":true}`), nil
	}))

	if err := client.DeleteWebhook(context.Background(), false); err != nil {
		t.Fatalf("DeleteWebhook() error = %v", err)
	}
}

func TestClientDeleteWebhookRejectsAPIFailure(t *testing.T) {
	client := testClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return telegramResponse(`{"ok":false,"description":"not allowed","result":false}`), nil
	}))

	err := client.DeleteWebhook(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "deleteWebhook: not allowed") {
		t.Fatalf("DeleteWebhook() error = %v, want Telegram API error", err)
	}
}

func TestAdapterCommandsAreCaseInsensitive(t *testing.T) {
	cfg := Config{AllowedUserID: 42}
	adapter := NewAdapter(nil, &cfg, func(Config) error { return nil })
	var got bot.Command
	var called bool
	adapter.handleUpdate(context.Background(), Update{Message: &Message{
		MessageID: 1,
		From:      &User{ID: 42},
		Chat:      Chat{ID: 7, Type: "private"},
		Text:      "/STOP",
	}}, func(bot.InboundMessage) {
		t.Fatal("uppercase slash command was forwarded as a prompt")
	}, func(command bot.Command, _ bot.InboundMessage) {
		got = command
		called = true
	})
	if !called || got != bot.CmdStop {
		t.Fatalf("command = %v, called = %v, want CmdStop", got, called)
	}
}

func TestAdapterRunDeletesWebhookBeforePolling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		mu    sync.Mutex
		calls []string
	)
	client := testClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		endpoint := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
		mu.Lock()
		calls = append(calls, endpoint)
		mu.Unlock()
		switch endpoint {
		case "getMe":
			return telegramResponse(`{"ok":true,"result":{"id":1,"is_bot":true,"username":"zut_test"}}`), nil
		case "deleteWebhook":
			return telegramResponse(`{"ok":true,"result":true}`), nil
		case "getUpdates":
			cancel()
			return telegramResponse(`{"ok":true,"result":[]}`), nil
		default:
			t.Fatalf("unexpected endpoint %q", endpoint)
			return nil, errors.New("unreachable")
		}
	}))
	cfg := Config{BotToken: "test", BotID: 1, BotUsername: "zut_test"}
	adapter := NewAdapter(client, &cfg, func(Config) error { return nil })

	err := adapter.Run(ctx, func(bot.InboundMessage) {}, func(bot.Command, bot.InboundMessage) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context canceled", err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"getMe", "deleteWebhook", "getUpdates"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("API calls = %v, want %v", calls, want)
	}
}

func TestBridgeStartDeletesWebhookBeforePolling(t *testing.T) {
	pollStarted := make(chan struct{})
	var pollOnce sync.Once
	var (
		mu    sync.Mutex
		calls []string
	)
	client := testClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		endpoint := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
		mu.Lock()
		calls = append(calls, endpoint)
		mu.Unlock()
		switch endpoint {
		case "getMe":
			return telegramResponse(`{"ok":true,"result":{"id":1,"is_bot":true,"username":"zut_test"}}`), nil
		case "deleteWebhook":
			return telegramResponse(`{"ok":true,"result":true}`), nil
		case "getUpdates":
			pollOnce.Do(func() { close(pollStarted) })
			<-req.Context().Done()
			return nil, req.Context().Err()
		default:
			t.Fatalf("unexpected endpoint %q", endpoint)
			return nil, errors.New("unreachable")
		}
	}))
	bridge := &Bridge{
		Client: client,
		Config: Config{BotToken: "test", BotID: 1, BotUsername: "zut_test"},
		Save:   func(Config) error { return nil },
		Host:   testBridgeHost{},
	}

	if err := bridge.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	select {
	case <-pollStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Telegram polling to start")
	}
	bridge.Stop()

	mu.Lock()
	defer mu.Unlock()
	want := []string{"getMe", "deleteWebhook", "getUpdates"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("API calls = %v, want %v", calls, want)
	}
}

type testBridgeHost struct{}

func (testBridgeHost) SubmitOrQueue(string, []provider.ImageBlock) {}
func (testBridgeHost) CancelTurn()                                 {}
func (testBridgeHost) Status() string                              { return "" }
func (testBridgeHost) Notify(string, string)                       {}

func TestAdapterRunFailsBeforePollingWhenWebhookRemovalFails(t *testing.T) {
	var calls bytes.Buffer
	client := testClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		endpoint := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
		calls.WriteString(endpoint + "\n")
		if endpoint == "getMe" {
			return telegramResponse(`{"ok":true,"result":{"id":1,"is_bot":true,"username":"zut_test"}}`), nil
		}
		return telegramResponse(`{"ok":false,"description":"webhook removal denied","result":false}`), nil
	}))
	cfg := Config{BotToken: "test"}
	adapter := NewAdapter(client, &cfg, func(Config) error { return nil })

	err := adapter.Run(context.Background(), func(bot.InboundMessage) {}, func(bot.Command, bot.InboundMessage) {})
	if err == nil || !strings.Contains(err.Error(), "remove telegram webhook before polling") {
		t.Fatalf("Run() error = %v, want actionable webhook error", err)
	}
	if got, want := calls.String(), "getMe\ndeleteWebhook\n"; got != want {
		t.Fatalf("API calls = %q, want %q", got, want)
	}
}
