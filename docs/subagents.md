# Named subagent profiles

zot can discover reusable named subagent definitions from markdown files with a frontmatter block. This is a common agent-profile layout and is not tied to a particular host application.

## Discovery

The default directory is:

```text
~/.agents/agents/*.md
```

zot also reads these optional locations:

- directories listed in `ZOT_AGENT_PROFILES` (use the platform path-list separator)
- `~/.pi/agent/agents/*.md` as a compatibility fallback

Profiles are read-only inputs. zot does not execute files from these directories. Project-local profile directories are not scanned automatically; use `ZOT_AGENT_PROFILES` when a project intentionally opts into an additional profile directory.

## File format

```markdown
---
name: reviewer
description: Read-only code reviewer
tools: read, grep, find, ls, diagnostics, code_search
model: openai-codex/gpt-5.6-luna
thinking: max
systemPromptMode: replace
inheritProjectContext: false
inheritSkills: false
---

You are a review worker. Inspect the requested scope, report evidence-backed findings, and do not edit files.
```

Supported metadata:

| Field | Meaning |
|---|---|
| `name` | Name passed to `swarm_spawn`'s `agent` field. Falls back to the filename. |
| `description` | Short description shown to the main agent. |
| `tools` | Comma-separated or list-form tool names. zot currently enforces its own `read`, `write`, `edit`, and `bash` registry; unknown names do not grant capabilities. |
| `model` | Optional model ID. A qualified value such as `openai-codex/gpt-5.6-luna` selects both provider and model. |
| `provider` | Optional separate provider ID for a model without a provider prefix. |
| `thinking` / `reasoning` | Optional reasoning level: `off`, `minimum`, `low`, `medium`, `high`, `xhigh`, or `max`. |
| `systemPromptMode` | `append` (default) or `replace`. |
| `inheritProjectContext` | Set to `false` to omit `AGENTS.md` context from the child. |
| `inheritSkills` | Set to `false` to omit skill discovery from the child. |

Other frontmatter from another agent host is ignored when zot does not have an equivalent setting. For example, `maxSubagentDepth` is currently not enforced by zot.

## Selecting a profile

When **auto-swarm** is enabled in `/settings`, the main agent receives a compact `[subagents_list]` section in its system prompt and can select a profile:

```json
{
  "task": "Review the authentication package and report correctness or security issues.",
  "agent": "reviewer"
}
```

The selected profile's body, model, thinking level, system-prompt mode, context inheritance, and tool selection are applied to the child. The parent prompt contains only profile metadata; the full body is loaded by the child after explicit selection.

A per-spawn reasoning override is also accepted:

```json
{
  "task": "Implement the parser change and add regression tests.",
  "agent": "implementer",
  "reasoning": "high"
}
```

`thinking` is accepted as an alias for `reasoning`. If neither is supplied, the child inherits the host reasoning level for an unnamed spawn, or the profile's `thinking` value for a named spawn.

The interactive command also supports the same selection explicitly:

```text
/swarm new --agent reviewer Review the authentication package
/swarm new --agent implementer --reasoning high Implement the parser change
```

All swarm children still share the host working directory. Named profiles change the child's instructions and configuration; they do not create a worktree, branch, or security sandbox.
