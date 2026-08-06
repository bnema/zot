package subagents

import "testing"

func TestSupervisorResolvesWebSearchPolicy(t *testing.T) {
	cases := []struct {
		name   string
		config Config
		req    SpawnRequest
		want   WebSearchPolicy
	}{
		{
			name:   "generic child inherits allowed parent",
			config: Config{WebSearchPolicy: WebSearchAllow},
			want:   WebSearchAllow,
		},
		{
			name:   "missing parent policy denies",
			config: Config{},
			want:   WebSearchDeny,
		},
		{
			name:   "unknown parent policy denies",
			config: Config{WebSearchPolicy: WebSearchPolicy(99)},
			want:   WebSearchDeny,
		},
		{
			name:   "parent denial wins",
			config: Config{WebSearchPolicy: WebSearchDeny},
			want:   WebSearchDeny,
		},
		{
			name: "allowed tools ceiling denies generic child",
			config: Config{
				WebSearchPolicy: WebSearchAllow,
				Policy:          SubagentPolicy{AllowedTools: []string{"read"}},
			},
			want: WebSearchDeny,
		},
		{
			name:   "named profile without tools denies",
			config: Config{WebSearchPolicy: WebSearchAllow},
			req:    SpawnRequest{Subagent: "reviewer"},
			want:   WebSearchDeny,
		},
		{
			name:   "named profile explicit tool allows",
			config: Config{WebSearchPolicy: WebSearchAllow},
			req:    SpawnRequest{Subagent: "researcher", Tools: []string{"read", "web_search"}},
			want:   WebSearchAllow,
		},
		{
			name:   "named profile cannot exceed denied parent",
			config: Config{WebSearchPolicy: WebSearchDeny},
			req:    SpawnRequest{Subagent: "researcher", Tools: []string{"web_search"}},
			want:   WebSearchDeny,
		},
		{
			name:   "invalid request policy denies generic child",
			config: Config{WebSearchPolicy: WebSearchAllow},
			req:    SpawnRequest{WebSearchPolicy: WebSearchPolicy(99)},
			want:   WebSearchDeny,
		},
		{
			name:   "invalid request policy denies named child",
			config: Config{WebSearchPolicy: WebSearchAllow},
			req:    SpawnRequest{WebSearchPolicy: WebSearchPolicy(99), Subagent: "researcher", Tools: []string{"web_search"}},
			want:   WebSearchDeny,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := New(tc.config)
			if got := f.resolveWebSearchPolicy(tc.req); got != tc.want {
				t.Fatalf("policy = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWorkerWebSearchPolicyIsExplicitAndFailClosed(t *testing.T) {
	cases := []struct {
		name     string
		policy   WebSearchPolicy
		subagent string
		tools    []string
		want     string
	}{
		{name: "missing generic policy", policy: WebSearchInherit, want: "deny"},
		{name: "unknown generic policy", policy: WebSearchPolicy(99), want: "deny"},
		{name: "named policy without opt in", policy: WebSearchAllow, subagent: "researcher", want: "deny"},
		{name: "named policy with explicit opt in", policy: WebSearchAllow, subagent: "researcher", tools: []string{"read", "web_search"}, want: "allow"},
		{name: "invalid named policy cannot use profile opt in", policy: WebSearchPolicy(99), subagent: "researcher", tools: []string{"web_search"}, want: "deny"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := subagentWorkerArgs(subagentWorkerArgsOpts{
				Exe:             "/zut",
				Dir:             "/work",
				SessionPath:     "/state/session.json",
				InboxPath:       "/state/inbox.sock",
				Subagent:        tc.subagent,
				Tools:           tc.tools,
				WebSearchPolicy: tc.policy,
			})
			idx := indexOf(args, "--web-search-policy")
			if idx < 0 || safeAt(args, idx+1) != tc.want {
				t.Fatalf("argv = %v, want policy %q", args, tc.want)
			}
		})
	}

	if got := WebSearchPolicy(99).String(); got != "deny" {
		t.Fatalf("unknown policy string = %q, want deny", got)
	}
}
