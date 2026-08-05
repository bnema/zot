package extproto

import (
	"encoding/json"
	"testing"
)

func TestNormalizeWidgetPosition(t *testing.T) {
	cases := map[string]string{
		WidgetPositionRightBar:   WidgetPositionRightBar,
		" RIGHT_BAR ":            WidgetPositionRightBar,
		WidgetPositionAboveInput: WidgetPositionAboveInput,
		"":                       WidgetPositionAboveInput,
		"unknown":                WidgetPositionAboveInput,
	}
	for input, want := range cases {
		if got := NormalizeWidgetPosition(input); got != want {
			t.Errorf("NormalizeWidgetPosition(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWidgetFrameKeepsUnknownAndMissingPositionsCompatible(t *testing.T) {
	var missing WidgetFromExt
	if err := json.Unmarshal([]byte(`{"type":"widget","id":"plan","title":"Plan"}`), &missing); err != nil {
		t.Fatal(err)
	}
	if got := NormalizeWidgetPosition(missing.Position); got != WidgetPositionAboveInput {
		t.Fatalf("missing position normalized to %q, want %q", got, WidgetPositionAboveInput)
	}

	var rightBar WidgetFromExt
	if err := json.Unmarshal([]byte(`{"type":"widget","id":"plan","position":"right_bar","title":"Plan"}`), &rightBar); err != nil {
		t.Fatal(err)
	}
	if got := NormalizeWidgetPosition(rightBar.Position); got != WidgetPositionRightBar {
		t.Fatalf("right_bar position normalized to %q, want %q", got, WidgetPositionRightBar)
	}
}
