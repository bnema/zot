package tui

import "testing"

func TestNormalizeSubagentPositionDefaultsBelowInput(t *testing.T) {
	for _, value := range []string{"", "below", "bottom", "below_input", "unexpected"} {
		if got := NormalizeSubagentPosition(value); got != SubagentPositionBelowInput {
			t.Fatalf("NormalizeSubagentPosition(%q) = %q, want %q", value, got, SubagentPositionBelowInput)
		}
	}
	if got := NormalizeSubagentPosition("above_input"); got != SubagentPositionAboveInput {
		t.Fatalf("NormalizeSubagentPosition(above_input) = %q, want %q", got, SubagentPositionAboveInput)
	}
}
