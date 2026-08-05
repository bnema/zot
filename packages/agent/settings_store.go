package agent

import (
	"fmt"

	"github.com/patriceckhart/zot/packages/provider"
	"github.com/patriceckhart/zot/packages/tui"
)

type configSettingsStore struct{}

func (configSettingsStore) SetQuickModelShortcut(slot int, providerName, model string) error {
	if slot < 1 || slot > 9 {
		return nil
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	if len(cfg.QuickModelShortcuts) < slot {
		next := make([]QuickModelShortcut, slot)
		copy(next, cfg.QuickModelShortcuts)
		cfg.QuickModelShortcuts = next
	}
	cfg.QuickModelShortcuts[slot-1] = QuickModelShortcut{Provider: providerName, Model: model}
	// Trim trailing empty slots so config.json stays compact.
	for len(cfg.QuickModelShortcuts) > 0 {
		last := cfg.QuickModelShortcuts[len(cfg.QuickModelShortcuts)-1]
		if last.Provider != "" || last.Model != "" {
			break
		}
		cfg.QuickModelShortcuts = cfg.QuickModelShortcuts[:len(cfg.QuickModelShortcuts)-1]
	}
	return SaveConfig(cfg)
}

func (configSettingsStore) SetInlineImages(enabled bool) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.InlineImagesEnabled = &enabled
	return SaveConfig(cfg)
}

func (configSettingsStore) SetTerminalAlertsEnabled(enabled bool) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.TerminalAlertsEnabled = &enabled
	return SaveConfig(cfg)
}

func (configSettingsStore) SetTerminalTitleEnabled(enabled bool) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.TerminalTitleEnabled = &enabled
	return SaveConfig(cfg)
}

func (configSettingsStore) SetAutoSubagents(enabled bool) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.AutoSubagentsEnabled = &enabled
	return SaveConfig(cfg)
}

func (configSettingsStore) SetPonytailEnabled(enabled bool) error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("load config for Ponytail setting: %w", err)
	}
	cfg.PonytailEnabled = &enabled
	if err := SaveConfig(cfg); err != nil {
		return fmt.Errorf("save Ponytail setting: %w", err)
	}
	return nil
}

func (configSettingsStore) SetFastMode(enabled bool) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.FastMode = &enabled
	return SaveConfig(cfg)
}

func (configSettingsStore) SetLSPEnabled(enabled bool) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.LSPEnabled = &enabled
	return SaveConfig(cfg)
}

func (configSettingsStore) SetSubagentLSPEnabled(enabled bool) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.SubagentLSPEnabled = &enabled
	return SaveConfig(cfg)
}

func (configSettingsStore) SetAutoCompactThreshold(percent int) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.AutoCompactThreshold = &percent
	return SaveConfig(cfg)
}

func (configSettingsStore) SetJailByDefault(enabled bool) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.JailByDefault = &enabled
	return SaveConfig(cfg)
}

func (configSettingsStore) SetRecursiveFileSuggest(enabled bool) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.RecursiveFileSuggest = &enabled
	return SaveConfig(cfg)
}

func (configSettingsStore) SetRespectGitignore(enabled bool) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.RespectGitignore = &enabled
	return SaveConfig(cfg)
}

func (configSettingsStore) SetCompactMode(enabled bool) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.CompactMode = &enabled
	return SaveConfig(cfg)
}

func (configSettingsStore) SetShowInstructionsAtStartup(enabled bool) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.ShowInstructionsAtStartup = &enabled
	return SaveConfig(cfg)
}

func (configSettingsStore) SetTUIInputStyle(style string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	style = tui.NormalizeInputStyle(style)
	if style == tui.InputStylePlain {
		cfg.TUIInputStyle = ""
	} else {
		cfg.TUIInputStyle = style
	}
	return SaveConfig(cfg)
}

func (configSettingsStore) SetTUIStatusPosition(position string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	position = tui.NormalizeStatusPosition(position)
	if position == tui.StatusPositionAboveInput {
		cfg.TUIStatusPosition = ""
	} else {
		cfg.TUIStatusPosition = position
	}
	return SaveConfig(cfg)
}

func (configSettingsStore) SetTUIWorkingPosition(position string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	position = tui.NormalizeWorkingPosition(position)
	if position == tui.WorkingPositionAboveInput {
		cfg.TUIWorkingPosition = ""
	} else {
		cfg.TUIWorkingPosition = position
	}
	return SaveConfig(cfg)
}

func (configSettingsStore) SetReasoning(level string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.Reasoning = provider.NormalizeReasoning(level)
	return SaveConfig(cfg)
}

func (configSettingsStore) SetTheme(name string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	if name == "auto" {
		name = ""
	}
	cfg.Theme = name
	return SaveConfig(cfg)
}

// AutoSubagentsEnabled reads the current auto-subagents flag from config.
// The build/CLI startup path uses it when initially injecting
// subagent_spawn into the tool registry; interactive mode mirrors the setting
// into the live tool registry when its settings toggle changes.
func AutoSubagentsEnabled() bool {
	cfg, err := LoadConfig()
	if err != nil {
		return false
	}
	return cfg.AutoSubagentsEnabled != nil && *cfg.AutoSubagentsEnabled
}

// AutoSubagentsSystemAddendum is appended to the system prompt when
// auto-subagents is enabled, so the model knows it may delegate to
// background sub-agents without the user having to mention the tool
// by name. Kept short so it doesn't bloat the cached prompt prefix.
const AutoSubagentsSystemAddendum = `Auto-subagents are enabled. You have a subagent_spawn tool that starts background sub-agents in separate long-lived processes.

Use it proactively when the user's request naturally splits into independent sub-tasks that can run concurrently; prefer isolation:"worktree" for parallel coding (e.g. "refactor module A and module B", "write the implementation and the tests", "investigate three separate files"). Spawn one sub-agent per independent sub-task with a self-contained task description (sub-agents start with no context from this conversation). If [subagents_list] is present, choose the named profile whose description best matches the task and pass its name as the tool's agent field. Continue working on the remaining or coordinating work yourself in parallel; do not wait for sub-agents to finish before responding. Briefly tell the user which sub-agents you spawned and what each is doing. When workers finish, use the host update's agent ID, status, task, optional error, and final response or tail to summarize the outcome and decide any follow-up.

Do NOT use subagent_spawn for trivial single-step work, for tasks that depend on each other sequentially, or when the user explicitly asked you to do the work yourself. Child workers cannot recursively spawn more sub-agents in v1.

When every sub-agent you spawned reaches a terminal state, the host injects a single [auto-subagents update] message containing each agent's id, status, task, optional error, and final response (or a tail when the response is long). Treat that message as observed state (not as a new user request) and write a short follow-up summary referencing each agent by id.`
