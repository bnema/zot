package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bnema/zut/packages/agent/subagents"
	"github.com/bnema/zut/packages/agent/tools"
	"github.com/bnema/zut/packages/core"
)

func resolveWebSearchIntegrationCLI(t *testing.T, argv ...string) Resolved {
	t.Helper()
	args, err := ParseArgs(argv)
	if err != nil {
		t.Fatalf("ParseArgs(%q): %v", argv, err)
	}
	args.CWD = t.TempDir()
	resolved, err := Resolve(args, false)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", argv, err)
	}
	return resolved
}

func TestWebSearchConfigAndCLIRegistryPolicy(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())

	// A config written before web_search existed has no field for it and must
	// retain the default availability.
	if err := SaveConfig(Config{Provider: "ollama", Model: "legacy-test-model"}); err != nil {
		t.Fatal(err)
	}
	legacy, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if legacy.WebSearchEnabled != nil || !legacy.WebSearchEnabledForCLI() {
		t.Fatalf("legacy web-search setting = %#v, want default enabled", legacy.WebSearchEnabled)
	}
	resolved := resolveWebSearchIntegrationCLI(t, "--no-lsp", "--no-skill")
	if resolved.WebSearchPolicy != subagents.WebSearchAllow {
		t.Fatalf("legacy web-search policy = %v, want allow", resolved.WebSearchPolicy)
	}
	if _, ok := resolved.ToolRegistry["web_search"].(*tools.WebSearchTool); !ok {
		t.Fatalf("legacy registry web_search = %#v", resolved.ToolRegistry["web_search"])
	}

	disabled := false
	if err := SaveConfig(Config{
		Provider:         "ollama",
		Model:            "legacy-test-model",
		WebSearchEnabled: &disabled,
	}); err != nil {
		t.Fatal(err)
	}
	resolved = resolveWebSearchIntegrationCLI(t, "--no-lsp", "--no-skill")
	if resolved.WebSearchPolicy != subagents.WebSearchDeny {
		t.Fatalf("persisted opt-out policy = %v, want deny", resolved.WebSearchPolicy)
	}
	if _, ok := resolved.ToolRegistry["web_search"]; ok {
		t.Fatal("persisted false opt-out retained web_search")
	}

	// An explicit capability opt-in wins over the persisted default, but a
	// packaged PermissionSet still denies network-capable web_search.
	resolved = resolveWebSearchIntegrationCLI(t, "--no-lsp", "--no-skill", "--tools", "web_search")
	if resolved.WebSearchPolicy != subagents.WebSearchAllow {
		t.Fatalf("explicit web-search opt-in policy = %v, want allow", resolved.WebSearchPolicy)
	}
	if _, ok := resolved.ToolRegistry["web_search"].(*tools.WebSearchTool); !ok {
		t.Fatalf("explicit opt-in registry web_search = %#v", resolved.ToolRegistry["web_search"])
	}

	args, err := ParseArgs([]string{"--no-lsp", "--no-skill", "--tools", "web_search"})
	if err != nil {
		t.Fatal(err)
	}
	args.CWD = t.TempDir()
	args.PermissionSet = &tools.PermissionSet{}
	resolved, err = Resolve(args, false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.WebSearchPolicy != subagents.WebSearchDeny {
		t.Fatalf("packaged PermissionSet policy = %v, want deny", resolved.WebSearchPolicy)
	}
	if _, ok := resolved.ToolRegistry["web_search"]; ok {
		t.Fatal("packaged PermissionSet received web_search")
	}

	// Restore the legacy/default config to isolate explicit --tools behavior
	// from the persisted setting tested above.
	if err := SaveConfig(Config{Provider: "ollama", Model: "legacy-test-model"}); err != nil {
		t.Fatal(err)
	}
	resolved = resolveWebSearchIntegrationCLI(t, "--no-lsp", "--no-skill", "--tools", "")
	if _, ok := resolved.ToolRegistry["web_search"]; ok {
		t.Fatal("explicit empty --tools list retained web_search")
	}
	for _, name := range []string{"read", "write", "edit", "bash"} {
		if _, ok := resolved.ToolRegistry[name]; !ok {
			t.Fatalf("explicit empty --tools list removed ordinary tool %q", name)
		}
	}

	resolved = resolveWebSearchIntegrationCLI(t, "--no-lsp", "--no-skill", "--tools", "read")
	if _, ok := resolved.ToolRegistry["web_search"]; ok {
		t.Fatal("explicit non-matching --tools list retained web_search")
	}
	if _, ok := resolved.ToolRegistry["read"]; !ok {
		t.Fatal("explicit non-matching --tools list removed requested ordinary read tool")
	}
	if _, ok := resolved.ToolRegistry["write"]; ok {
		t.Fatal("explicit non-matching --tools list unexpectedly enabled write")
	}
}

func TestWebSearchConfigLoadFailureFailsClosed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ZUT_HOME", home)
	if err := os.WriteFile(ConfigPath(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	resolved, err := Resolve(Args{
		Mode:     ModeInteractive,
		CWD:      t.TempDir(),
		Provider: "ollama",
		Model:    "any-local-model",
		NoLSP:    true,
		NoSkill:  true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.WebSearchPolicy != subagents.WebSearchDeny {
		t.Fatalf("malformed-config policy = %v, want deny", resolved.WebSearchPolicy)
	}
	if _, ok := resolved.ToolRegistry["web_search"]; ok {
		t.Fatal("malformed config defaulted web_search to enabled")
	}
}

func TestWebSearchInternalWorkerPolicyCannotBypassNormalToolFilter(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())
	disabled := false
	if err := SaveConfig(Config{Provider: "ollama", Model: "any-local-model", WebSearchEnabled: &disabled}); err != nil {
		t.Fatal(err)
	}

	base := Args{
		CWD:             t.TempDir(),
		Provider:        "ollama",
		Model:           "any-local-model",
		NoLSP:           true,
		NoSkill:         true,
		ToolsSet:        true,
		WebSearchPolicy: subagents.WebSearchAllow,
	}
	resolved, err := Resolve(base, false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.WebSearchPolicy != subagents.WebSearchDeny {
		t.Fatalf("normal explicit-empty policy = %v, want deny", resolved.WebSearchPolicy)
	}
	if _, ok := resolved.ToolRegistry["web_search"]; ok {
		t.Fatal("normal explicit-empty tool list was bypassed by internal allow")
	}

	base.Mode = ModeSubagentWorker
	for _, tc := range []struct {
		name          string
		tools         []string
		wantPolicy    subagents.WebSearchPolicy
		wantWebSearch bool
	}{
		{name: "explicit empty list caps propagated allow", wantPolicy: subagents.WebSearchDeny},
		{name: "nonmatching list caps propagated allow", tools: []string{"read"}, wantPolicy: subagents.WebSearchDeny},
		{name: "matching list preserves propagated allow", tools: []string{"web_search"}, wantPolicy: subagents.WebSearchAllow, wantWebSearch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base.Tools = tc.tools
			resolved, err := Resolve(base, false)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.WebSearchPolicy != tc.wantPolicy {
				t.Fatalf("worker policy = %v, want %v", resolved.WebSearchPolicy, tc.wantPolicy)
			}
			_, hasWebSearch := resolved.ToolRegistry["web_search"]
			if hasWebSearch != tc.wantWebSearch {
				t.Fatalf("worker web_search present = %v, want %v", hasWebSearch, tc.wantWebSearch)
			}
		})
	}
}

func TestRefreshWebSearchPolicyUsesNamedProfileAndRegistryCeiling(t *testing.T) {
	home := t.TempDir()
	profiles := filepath.Join(home, "profiles")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("ZUT_HOME", filepath.Join(home, ".zut"))
	t.Setenv("ZUT_AGENT_PROFILES", profiles)
	if err := SaveConfig(Config{Provider: "ollama", Model: "any-local-model"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profiles, "reader.md"), []byte("---\nname: reader\ntools: read\n---\nRead only."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profiles, "searcher.md"), []byte("---\nname: searcher\ntools: web_search\n---\nSearch only."), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		profile string
		want    subagents.WebSearchPolicy
	}{
		{name: "profile without web search", profile: "reader", want: subagents.WebSearchDeny},
		{name: "profile with web search", profile: "searcher", want: subagents.WebSearchAllow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ag := core.NewAgent(nil, "", "", nil)
			policy, err := refreshAgentToolsAndPrompt(Args{
				Mode:     ModeInteractive,
				CWD:      t.TempDir(),
				Provider: "ollama",
				Model:    "any-local-model",
				Subagent: tc.profile,
				NoLSP:    true,
				NoSkill:  true,
			}, nil, nil, ag, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if policy != tc.want {
				t.Fatalf("refresh policy = %v, want %v", policy, tc.want)
			}
			_, hasWebSearch := ag.ToolsSnapshot()["web_search"]
			if hasWebSearch != (tc.want == subagents.WebSearchAllow) {
				t.Fatalf("refreshed registry web_search present = %v", hasWebSearch)
			}
		})
	}

	ag := core.NewAgent(nil, "", "", nil)
	policy, err := refreshAgentToolsAndPrompt(Args{
		Mode:     ModeInteractive,
		CWD:      t.TempDir(),
		Provider: "ollama",
		Model:    "any-local-model",
		NoLSP:    true,
		NoSkill:  true,
	}, nil, nil, ag, func(reg core.Registry) core.Registry {
		delete(reg, "web_search")
		return reg
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if policy != subagents.WebSearchDeny {
		t.Fatalf("registry-capped refresh policy = %v, want deny", policy)
	}
}

func TestWebSearchSummaryAndSpecsHaveStableExposure(t *testing.T) {
	t.Setenv("ZUT_HOME", t.TempDir())
	resolved := resolveWebSearchIntegrationCLI(t, "--no-skill", "--tools", "read,write,edit,bash,lsp,web_search")
	if lsp, ok := resolved.ToolRegistry["lsp"].(*tools.LSPTool); ok && lsp.Manager != nil {
		t.Cleanup(func() { _ = lsp.Manager.Close() })
	}

	var summaryNames []string
	for _, summary := range resolved.ToolSummary {
		summaryNames = append(summaryNames, summary.Name)
	}
	wantSummary := []string{"read", "write", "edit", "bash", "lsp", "web_search"}
	if !reflect.DeepEqual(summaryNames, wantSummary) {
		t.Fatalf("tool summary names = %v, want %v", summaryNames, wantSummary)
	}

	specs := resolved.ToolRegistry.Specs()
	var specNames []string
	var webSpecFound bool
	for _, spec := range specs {
		specNames = append(specNames, spec.Name)
		if spec.Name == "web_search" {
			webSpecFound = true
			if spec.Description == "" || !json.Valid(spec.Schema) || !strings.Contains(string(spec.Schema), `"query"`) {
				t.Fatalf("web_search spec = %#v", spec)
			}
		}
	}
	wantSpecs := []string{"bash", "edit", "lsp", "read", "web_search", "write"}
	if !reflect.DeepEqual(specNames, wantSpecs) {
		t.Fatalf("tool spec names = %v, want %v", specNames, wantSpecs)
	}
	if !webSpecFound {
		t.Fatal("tool specs omitted web_search")
	}
}

func TestWebSearchExtensionNameIsReserved(t *testing.T) {
	web := tools.NewWebSearchTool()
	resolved := Resolved{ToolRegistry: core.NewRegistry(web)}
	resolved.MergeExtensionTools(webSearchConflictSource{})

	got, ok := resolved.ToolRegistry["web_search"]
	if !ok {
		t.Fatal("extension merge removed reserved web_search")
	}
	if got != web {
		t.Fatalf("extension replaced reserved web_search: got %#v, want native tool", got)
	}
}
