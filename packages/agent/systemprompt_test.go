package agent

import (
	"strings"
	"testing"
)

func TestBuildSystemPromptAddsCompactionHandoffToCustomPrompt(t *testing.T) {
	prompt := BuildSystemPrompt(SystemPromptOpts{Custom: "custom identity"})
	if !strings.Contains(prompt, "custom identity") {
		t.Fatalf("custom prompt missing:\n%s", prompt)
	}
	if !strings.Contains(prompt, compactedSummaryHandoffInstruction) {
		t.Fatalf("custom prompt missing compaction handoff:\n%s", prompt)
	}
	if count := strings.Count(prompt, compactedSummaryHandoffInstruction); count != 1 {
		t.Fatalf("compaction handoff count = %d, want 1:\n%s", count, prompt)
	}
}
