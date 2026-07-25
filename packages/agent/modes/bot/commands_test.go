package bot

import "testing"

func TestParseCommandCaseInsensitive(t *testing.T) {
	tests := []struct {
		text string
		want Command
		ok   bool
	}{
		{text: "/start", want: CmdStart, ok: true},
		{text: " /HELP ", want: CmdHelp, ok: true},
		{text: "/Status", want: CmdStatus, ok: true},
		{text: "/STOP", want: CmdStop, ok: true},
		{text: "/stop please", want: CmdStop, ok: true},
		{text: "status", ok: false},
		{text: "", ok: false},
	}
	for _, tt := range tests {
		got, ok := ParseCommand(tt.text)
		if ok != tt.ok || ok && got != tt.want {
			t.Errorf("ParseCommand(%q) = (%v, %v), want (%v, %v)", tt.text, got, ok, tt.want, tt.ok)
		}
	}
}

func TestIsStopCommand(t *testing.T) {
	for _, text := range []string{"stop", " STOP ", "Stop"} {
		if !IsStopCommand(text) {
			t.Errorf("IsStopCommand(%q) = false", text)
		}
	}
	for _, text := range []string{"/stop", "stop please", "please stop", ""} {
		if IsStopCommand(text) {
			t.Errorf("IsStopCommand(%q) = true", text)
		}
	}
}
