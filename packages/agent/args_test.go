package agent

import "testing"

func TestParseArgsSubagentAndReasoning(t *testing.T) {
	args, err := ParseArgs([]string{"--swarm-agent", "/tmp/in.sock", "--subagent", "reviewer", "--reasoning", "high", "task"})
	if err != nil {
		t.Fatal(err)
	}
	if args.Mode != ModeSwarmAgent || args.Subagent != "reviewer" || args.Reasoning != "high" || args.Prompt != "task" {
		t.Fatalf("parsed args = mode=%q subagent=%q reasoning=%q prompt=%q", args.Mode, args.Subagent, args.Reasoning, args.Prompt)
	}
}

func TestParseArgsTemperatureAllowsZero(t *testing.T) {
	args, err := ParseArgs([]string{"--temperature", "0"})
	if err != nil {
		t.Fatalf("ParseArgs returned %v", err)
	}
	if args.Temperature == nil || *args.Temperature != 0 {
		t.Fatalf("Temperature = %v; want 0", args.Temperature)
	}
}

func TestParseArgsTemperatureRejectsOutOfRange(t *testing.T) {
	if _, err := ParseArgs([]string{"--temperature", "2.1"}); err == nil {
		t.Fatal("ParseArgs accepted out-of-range temperature")
	}
}

func TestParseArgsYes(t *testing.T) {
	for _, flag := range []string{"-y", "--yes"} {
		args, err := ParseArgs([]string{flag, "--print", "hi"})
		if err != nil {
			t.Fatalf("ParseArgs(%q): %v", flag, err)
		}
		if !args.Yes {
			t.Fatalf("ParseArgs(%q): Yes = false", flag)
		}
		if args.Mode != ModePrint || args.Prompt != "hi" {
			t.Fatalf("ParseArgs(%q): Mode=%q Prompt=%q", flag, args.Mode, args.Prompt)
		}
	}
}

func TestParseArgsStatsRequiresPrintMode(t *testing.T) {
	args, err := ParseArgs([]string{"-p", "--stats", "stats.json", "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if args.StatsPath != "stats.json" || args.Mode != ModePrint {
		t.Fatalf("StatsPath=%q Mode=%q", args.StatsPath, args.Mode)
	}

	if _, err := ParseArgs([]string{"--stats", "stats.json", "hi"}); err == nil {
		t.Fatal("ParseArgs accepted --stats without print mode")
	}
}

func TestParseArgsStream(t *testing.T) {
	args, err := ParseArgs([]string{"--stream", "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if args.Mode != ModeStream || args.Prompt != "hi" {
		t.Fatalf("Mode=%q Prompt=%q", args.Mode, args.Prompt)
	}
}
