package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/patriceckhart/zot/packages/agent/subagents"
	"github.com/patriceckhart/zot/packages/agent/swarm"
	"github.com/patriceckhart/zot/packages/core"
	"github.com/patriceckhart/zot/packages/provider"
)

// SwarmSpawnTool lets the main agent fork a background sub-agent
// against the host's cwd via swarm.Swarm.SpawnReq. The sub-agent runs
// in parallel: the tool returns the agent id immediately and the main
// turn continues uninterrupted. The user can monitor / chat with the
// spawned agent via /swarm.
//
// Gated by the auto_swarm_enabled config flag at call time so a user
// can flip it off mid-session and the next call refuses cleanly
// without re-registering the tool.
type SwarmSpawnTool struct {
	// Swarm is the supervisor used to spawn agents. Nil means
	// "auto-swarm not available in this mode" and the tool always
	// errors.
	Swarm *swarm.Swarm

	// Enabled reads the live config flag. Lets users toggle from
	// /settings without rebuilding the agent. When nil, the tool
	// is treated as disabled.
	Enabled func() bool

	// DefaultModel and DefaultProvider return the host agent's resolved
	// model and provider. They are used when the tool call omits both
	// fields and does not select a named profile, so auto-swarm follows
	// the same auth route as the user sees in the parent session.
	DefaultModel     func() string
	DefaultProvider  func() string
	DefaultReasoning func() string

	// ResolveSubagent validates and resolves a named markdown profile.
	// The child receives only the name and loads the profile itself.
	ResolveSubagent func(name string) (*subagents.Profile, error)

	// OnSpawned, if set, is called after every successful spawn with
	// the new agent + the task it was started with. Used by the
	// interactive host to track agents and surface a summary back
	// in chat when all sub-agents finish.
	OnSpawned func(agent *swarm.Agent, task string)
}

type swarmSpawnArgs struct {
	Task      string `json:"task"`
	Agent     string `json:"agent,omitempty"`
	Model     string `json:"model,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	Thinking  string `json:"thinking,omitempty"`
}

const swarmSpawnSchema = `{
  "type": "object",
  "properties": {
    "task": {
      "type": "string",
      "description": "The full task description for the sub-agent. Be specific: the child normally has the main agent's built-in tools, including lsp when enabled, but a selected profile can restrict its tools; it shares this working directory and starts with NO context from this conversation."
    },
    "agent": {
      "type": "string",
      "description": "Optional named markdown profile from [subagents_list]. The child applies that profile's system prompt, model, thinking level, and tool limits. Omit for a generic child."
    },
    "model": {
      "type": "string",
      "description": "Optional model id to pin the sub-agent to. Normally omit both model and provider so the sub-agent inherits the host session's resolved provider/model/auth route, or omit them when using an agent profile. Do not infer provider from model name. If you override this, also provide provider."
    },
    "provider": {
      "type": "string",
      "description": "Optional provider id. Normally omit both model and provider so the sub-agent inherits the host session. If you override this, also provide model. Note: openai means public OpenAI API-key auth; openai-codex means ChatGPT/Codex subscription auth."
    },
    "reasoning": {
      "type": "string",
      "enum": ["off", "minimum", "low", "medium", "high", "xhigh", "max"],
      "description": "Optional reasoning/thinking level for the child. Overrides the selected profile's thinking level when provided."
    },
    "thinking": {
      "type": "string",
      "enum": ["off", "minimum", "low", "medium", "high", "xhigh", "max"],
      "description": "Alias for reasoning, accepted for compatibility with common agent profile terminology. Prefer reasoning when both are available."
    }
  },
  "required": ["task"]
}`

func (t *SwarmSpawnTool) Name() string { return "swarm_spawn" }
func (t *SwarmSpawnTool) Description() string {
	return "Spawn a background sub-agent to work on a parallel sub-task. Optionally select a named markdown profile with agent and set its model/provider/reasoning. Returns the sub-agent id immediately; the sub-agent keeps running while this conversation continues. The sub-agent shares this working directory."
}
func (t *SwarmSpawnTool) Schema() json.RawMessage { return json.RawMessage(swarmSpawnSchema) }

func (t *SwarmSpawnTool) Execute(ctx context.Context, raw json.RawMessage, progress func(string)) (core.ToolResult, error) {
	if t.Swarm == nil {
		return protocolToolError("swarm_spawn: swarm supervisor not available in this mode")
	}
	if t.Enabled == nil || !t.Enabled() {
		return protocolToolError("swarm_spawn: auto-swarm is disabled. Ask the user to enable it from /settings before delegating sub-tasks.")
	}
	var a swarmSpawnArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return core.ToolResult{}, fmt.Errorf("invalid args: %w", err)
	}
	task := strings.TrimSpace(a.Task)
	if task == "" {
		return protocolToolError("swarm_spawn: task is required")
	}

	agentName := strings.TrimSpace(a.Agent)
	var profile *subagents.Profile
	if agentName != "" {
		if t.ResolveSubagent == nil {
			return protocolToolError("swarm_spawn: named subagent profiles are unavailable")
		}
		var err error
		profile, err = t.ResolveSubagent(agentName)
		if err != nil {
			return protocolToolError("swarm_spawn: " + err.Error())
		}
		if profile == nil {
			return protocolToolError("swarm_spawn: unknown subagent profile " + agentName)
		}
		agentName = profile.Name
	}

	model := strings.TrimSpace(a.Model)
	providerID := strings.TrimSpace(a.Provider)
	if (model == "") != (providerID == "") {
		return protocolToolError("swarm_spawn: omit both model/provider to inherit the host or profile, or provide both explicitly")
	}
	if model == "" && providerID == "" && profile != nil {
		// A fully-qualified profile model is useful metadata for the
		// supervisor and also lets it inherit the right credential route.
		// Bare profile model ids remain child-resolved so the configured
		// provider is preserved.
		profileProvider, profileModel := profile.ModelSelection()
		if profileProvider != "" && profileModel != "" {
			providerID, model = profileProvider, profileModel
		}
	}
	if model == "" && providerID == "" && profile == nil {
		if t.DefaultModel != nil {
			model = strings.TrimSpace(t.DefaultModel())
		}
		if t.DefaultProvider != nil {
			providerID = strings.TrimSpace(t.DefaultProvider())
		}
	}

	reasoning, err := reasoningOverride(a.Reasoning, a.Thinking)
	if err != nil {
		return protocolToolError("swarm_spawn: " + err.Error())
	}
	if reasoning == "" && profile != nil && strings.TrimSpace(profile.Thinking) != "" {
		reasoning, err = reasoningOverride(profile.Thinking, "")
		if err != nil {
			return protocolToolError("swarm_spawn: profile " + profile.Name + ": " + err.Error())
		}
	}
	if reasoning == "" && t.DefaultReasoning != nil {
		reasoning, err = reasoningOverride(t.DefaultReasoning(), "")
		if err != nil {
			return protocolToolError("swarm_spawn: host " + err.Error())
		}
	}

	agent, err := t.Swarm.SpawnReq(ctx, swarm.SpawnRequest{
		Task:      task,
		Model:     model,
		Provider:  providerID,
		Reasoning: reasoning,
		Subagent:  agentName,
	})
	if err != nil {
		return core.ToolResult{}, fmt.Errorf("swarm_spawn: %w", err)
	}
	if t.OnSpawned != nil {
		t.OnSpawned(agent, task)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "spawned sub-agent %s\n", agent.ID)
	fmt.Fprintf(&sb, "task: %s\n", truncateTask(task, 200))
	if agentName != "" {
		fmt.Fprintf(&sb, "agent: %s\n", agentName)
	}
	if model != "" {
		fmt.Fprintf(&sb, "model: %s\n", model)
	}
	if providerID != "" {
		fmt.Fprintf(&sb, "provider: %s\n", providerID)
	}
	if reasoning != "" {
		fmt.Fprintf(&sb, "reasoning: %s\n", reasoning)
	}
	if agent.FastMode {
		sb.WriteString("fast mode: enabled\n")
	}
	sb.WriteString("\nThe sub-agent is running in the background. Use /swarm in the TUI to monitor it. ")
	sb.WriteString("This conversation continues immediately; do not wait for the sub-agent to finish before working on the next thing.")
	return core.ToolResult{
		Content: []provider.Content{provider.TextBlock{Text: sb.String()}},
		Details: map[string]any{
			"agent_id":  agent.ID,
			"task":      task,
			"agent":     agentName,
			"model":     model,
			"provider":  providerID,
			"reasoning": reasoning,
			"fast_mode": agent.FastMode,
		},
	}, nil
}

func reasoningOverride(reasoning, thinking string) (string, error) {
	reasoning = strings.TrimSpace(reasoning)
	thinking = strings.TrimSpace(thinking)
	if reasoning != "" && thinking != "" && provider.NormalizeReasoning(reasoning) != provider.NormalizeReasoning(thinking) {
		return "", fmt.Errorf("reasoning and thinking disagree; provide only one")
	}
	value := reasoning
	if value == "" {
		value = thinking
	}
	if value == "" {
		return "", nil
	}
	normalized := provider.NormalizeReasoning(value)
	switch normalized {
	case "":
		return "off", nil
	case "minimum", "low", "medium", "high", "xhigh", "max":
		return normalized, nil
	default:
		return "", fmt.Errorf("reasoning must be off|minimum|low|medium|high|xhigh|max")
	}
}

// protocolToolError keeps model-visible validation failures in the
// ToolResult channel rather than treating them as host execution errors.
func protocolToolError(msg string) (core.ToolResult, error) {
	//nolint:nilerr // ToolResult.IsError is the established tool protocol.
	return toolErr(msg), nil
}

func toolErr(msg string) core.ToolResult {
	return core.ToolResult{
		Content: []provider.Content{provider.TextBlock{Text: msg}},
		IsError: true,
	}
}

func truncateTask(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
