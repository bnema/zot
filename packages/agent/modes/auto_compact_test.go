package modes

import "testing"

func TestNormalizeAutoCompactThreshold(t *testing.T) {
	tests := []struct {
		name  string
		value *int
		want  int
	}{
		{name: "missing", value: nil, want: 85},
		{name: "off", value: autoCompactIntPtr(0), want: 0},
		{name: "seventy", value: autoCompactIntPtr(70), want: 70},
		{name: "eighty", value: autoCompactIntPtr(80), want: 80},
		{name: "eighty five", value: autoCompactIntPtr(85), want: 85},
		{name: "ninety", value: autoCompactIntPtr(90), want: 90},
		{name: "invalid low", value: autoCompactIntPtr(42), want: 85},
		{name: "invalid high", value: autoCompactIntPtr(100), want: 85},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeAutoCompactThreshold(tt.value); got != tt.want {
				t.Fatalf("normalizeAutoCompactThreshold(%v) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

func TestShouldAutoCompactUsesConfiguredThreshold(t *testing.T) {
	tests := []struct {
		name      string
		used      int
		window    int
		threshold int
		want      bool
	}{
		{name: "below threshold", used: 84, window: 100, threshold: 85, want: false},
		{name: "at threshold", used: 85, window: 100, threshold: 85, want: true},
		{name: "above threshold", used: 86, window: 100, threshold: 85, want: true},
		{name: "seventy percent preset", used: 70, window: 100, threshold: 70, want: true},
		{name: "eighty percent preset", used: 80, window: 100, threshold: 80, want: true},
		{name: "ninety percent preset", used: 90, window: 100, threshold: 90, want: true},
		{name: "off", used: 100, window: 100, threshold: 0, want: false},
		{name: "missing usage", used: 0, window: 100, threshold: 85, want: false},
		{name: "missing window", used: 85, window: 0, threshold: 85, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldAutoCompact(tt.used, tt.window, tt.threshold); got != tt.want {
				t.Fatalf("shouldAutoCompact(%d, %d, %d) = %t, want %t", tt.used, tt.window, tt.threshold, got, tt.want)
			}
		})
	}
}

func TestSettingsDialogOffersAutoCompactThresholdPresets(t *testing.T) {
	interactive := NewInteractive(InteractiveConfig{})
	interactive.openSettingsDialog()

	var thresholdItem *settingsItem
	for idx := range interactive.settingsDialog.items {
		item := &interactive.settingsDialog.items[idx]
		if item.key == "auto_compact_threshold" {
			thresholdItem = item
			break
		}
	}
	if thresholdItem == nil {
		t.Fatal("settings dialog is missing auto-compact threshold")
	}

	wantValues := []string{"0", "70", "80", "85", "90"}
	if len(thresholdItem.options) != len(wantValues) {
		t.Fatalf("auto-compact options = %d, want %d", len(thresholdItem.options), len(wantValues))
	}
	for idx, want := range wantValues {
		if got := thresholdItem.options[idx].value; got != want {
			t.Fatalf("auto-compact option %d = %q, want %q", idx, got, want)
		}
	}
	if got := thresholdItem.options[thresholdItem.choice].value; got != "85" {
		t.Fatalf("default auto-compact choice = %q, want 85", got)
	}
}

func autoCompactIntPtr(value int) *int {
	return &value
}
