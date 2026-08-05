package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bnema/zut/packages/provider"
)

func TestWritePrintStats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	usage := provider.Usage{
		InputTokens:          100,
		OutputTokens:         40,
		ReasoningTokens:      15,
		ReasoningTokensKnown: true,
		CacheReadTokens:      20,
		CacheWriteTokens:     5,
	}
	if err := writePrintStats(path, "openai", "gpt-test", usage, 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got printStats
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Provider != "openai" || got.Model != "gpt-test" {
		t.Fatalf("provider/model = %q/%q", got.Provider, got.Model)
	}
	if got.PromptTokens != 125 || got.ReasoningTokens == nil || *got.ReasoningTokens != 15 {
		t.Fatalf("token stats = %+v", got)
	}
	if got.GeneratedOutputTokens != 25 || got.ElapsedMS != 1500 {
		t.Fatalf("output/time stats = %+v", got)
	}
}

func TestWritePrintStatsUsesNullForUnreportedReasoning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stats.json")
	usage := provider.Usage{InputTokens: 10, OutputTokens: 7}
	if err := writePrintStats(path, "anthropic", "claude-test", usage, time.Second); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["reasoning_tokens"] != nil {
		t.Fatalf("reasoning_tokens = %#v, want null", got["reasoning_tokens"])
	}
	if got["generated_output_tokens"] != float64(7) {
		t.Fatalf("generated_output_tokens = %#v", got["generated_output_tokens"])
	}
}
