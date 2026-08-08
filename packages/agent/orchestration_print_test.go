package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRunOrchestratedPrintEmitsOnlyFinalAnswer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZUT_HOME", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"final answer\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	runErr := runOrchestratedPrintMode(context.Background(), Args{
		Mode:        ModePrint,
		Orchestrate: true,
		Provider:    "openai",
		Model:       "gpt-5",
		BaseURL:     server.URL,
		APIKey:      "test-key",
		Prompt:      "say hello",
	}, "test")
	_ = writer.Close()
	os.Stdout = oldStdout
	output, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if runErr != nil {
		t.Fatalf("runOrchestratedPrintMode error: %v", runErr)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	if got := string(output); got != "final answer\n" {
		t.Fatalf("stdout = %q, want final answer only", got)
	}
}

func TestRunOrchestratedPrintPropagatesConfigLoadError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZUT_HOME", home)
	if err := os.WriteFile(ConfigPath(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := runOrchestratedPrintMode(context.Background(), Args{
		Mode:        ModePrint,
		Orchestrate: true,
		Provider:    "ollama",
		Model:       "llama3",
		Prompt:      "test",
	}, "test")
	if err == nil || !strings.Contains(err.Error(), "load config for orchestrated print") {
		t.Fatalf("runOrchestratedPrintMode error = %v, want config load error", err)
	}
}
