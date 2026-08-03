package modes

import (
	"bytes"
	"context"
	"testing"

	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
)

type printTestClient struct{}

func (printTestClient) Name() string { return "test" }

func (printTestClient) Stream(context.Context, provider.Request) (<-chan provider.Event, error) {
	events := make(chan provider.Event, 3)
	events <- provider.EventStart{Model: "test-model", Provider: "test"}
	events <- provider.EventUsage{Usage: provider.Usage{
		InputTokens:          12,
		OutputTokens:         9,
		ReasoningTokens:      6,
		ReasoningTokensKnown: true,
	}}
	events <- provider.EventDone{
		Stop: provider.StopEnd,
		Message: provider.Message{Role: provider.RoleAssistant, Content: []provider.Content{
			provider.TextBlock{Text: "answer"},
		}},
	}
	close(events)
	return events, nil
}

func TestRunPrintReturnsInvocationUsage(t *testing.T) {
	agent := core.NewAgent(printTestClient{}, "test-model", "", core.Registry{})
	agent.SeedCost(provider.Usage{InputTokens: 100, OutputTokens: 50})
	var output bytes.Buffer

	usage, err := RunPrint(context.Background(), agent, "question", nil, &output)
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != "answer\n" {
		t.Fatalf("output = %q", output.String())
	}
	if usage.InputTokens != 12 || usage.OutputTokens != 9 || usage.ReasoningTokens != 6 || !usage.ReasoningTokensKnown {
		t.Fatalf("usage = %+v", usage)
	}
}
