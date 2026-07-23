package modes

import "testing"

func TestSlashSuggesterHidesUnjailUntilJailed(t *testing.T) {
	s := newSlashSuggester()

	if got := commandNames(s.matches("/unj")); contains(got, "/unjail") {
		t.Fatalf("/unjail should be hidden while not jailed, got %v", got)
	}
	if got := commandNames(s.matches("/ja")); !contains(got, "/jail") {
		t.Fatalf("/jail should be visible while not jailed, got %v", got)
	}

	s.SetJailed(true)
	if got := commandNames(s.matches("/unj")); !contains(got, "/unjail") {
		t.Fatalf("/unjail should be visible while jailed, got %v", got)
	}
	if got := commandNames(s.matches("/ja")); contains(got, "/jail") {
		t.Fatalf("/jail should be hidden while jailed, got %v", got)
	}
}

func TestSlashSuggesterShowsLlamaOnlyWhenConfigured(t *testing.T) {
	s := newSlashSuggester()
	if got := commandNames(s.matches("/llama")); contains(got, "/llama") {
		t.Fatalf("/llama visible without login: %v", got)
	}
	s.SetLlamaConfigured(true)
	if got := commandNames(s.matches("/llama")); !contains(got, "/llama") {
		t.Fatalf("/llama missing with login: %v", got)
	}
}

func TestSlashSuggesterHasSwarm(t *testing.T) {
	s := newSlashSuggester()
	if got := commandNames(s.matches("/sw")); !contains(got, "/swarm") {
		t.Fatalf("/swarm missing from suggestions, got %v", got)
	}
}

func commandNames(cmds []slashCommand) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		if !c.Header {
			out = append(out, c.Name)
		}
	}
	return out
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
