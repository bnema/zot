# Named subagent profiles

zut can discover reusable named subagent definitions from markdown files with a frontmatter block. This is a common agent-profile layout and is not tied to a particular host application.

## Discovery

The default directory is:

```text
~/.agents/agents/*.md
```

zut also reads these optional locations:

- directories listed in `ZUT_AGENT_PROFILES` (use the platform path-list separator)
- `~/.pi/agent/agents/*.md` as a compatibility fallback

Profiles are read-only inputs. zut does not execute files from these directories. Project-local profile directories are not scanned automatically; use `ZUT_AGENT_PROFILES` when a project intentionally opts into an additional profile directory.

## File format

```markdown
---
name: reviewer
description: Read-only code reviewer
tools: read
model: openai-codex/gpt-5.6-luna
thinking: max
systemPromptMode: replace
inheritProjectContext: false
inheritSkills: false
fastMode: false
---

You are a review worker. Inspect the requested scope, report evidence-backed findings, and do not edit files.
```

Supported metadata:

| Field | Meaning |
|---|---|
| `name` | Name passed to `subagent_spawn`'s `agent` field. Falls back to the filename. |
| `description` | Short description shown to the main agent. |
| `tools` | Comma-separated or list-form tool names. zut enforces its built-in `read`, `write`, `edit`, `bash`, and `lsp` registry; the conditional `skill` tool is available when skills are enabled. Unknown names do not grant capabilities. |
| `model` | Optional model ID. A qualified value such as `openai-codex/gpt-5.6-luna` selects both provider and model. |
| `provider` | Optional separate provider ID for a model without a provider prefix. |
| `thinking` / `reasoning` | Optional reasoning level: `off`, `minimum`, `low`, `medium`, `high`, `xhigh`, or `max`. |
| `systemPromptMode` | `append` (default) or `replace`. |
| `inheritProjectContext` | Set to `false` to omit `AGENTS.md` context from the child. |
| `inheritSkills` | Set to `false` to omit skill discovery and the conditional `skill` loader from the child; `--no-skill` has the same effect for a run. |
| `fastMode` | Optional fast-mode override. Omit it to inherit the host setting; `false` disables fast mode for this profile; `true` enables it for this profile even when the host setting is off. |

Other frontmatter from another agent host is ignored when zut does not have an equivalent setting. Recursive child spawning is disabled in v1; a worker cannot invoke `subagent_spawn` to create descendants.

## Selecting a profile

When **auto-subagents** is enabled in `/settings`, the main agent receives a compact `[subagents_list]` section in its system prompt and can select a profile for `subagent_spawn`:

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
/subagents new --agent reviewer Review the authentication package
/subagents new --agent implementer --reasoning high Implement the parser change
```

Shared mode preserves the historical host working directory. For parallel coding, pass `isolation:"worktree"` to `subagent_spawn`; zut creates a detached Git worktree, captures changed files and a patch, and never merges automatically. Named profiles change the child's instructions and configuration; they are not a security sandbox. A profile's `systemPromptMode` controls its own body relative to the built-in identity; globally appended instructions, including enabled Ponytail coding guidance, remain present. Child credentials are transferred over stdin rather than argv or persisted metadata, and the active provider endpoint/TLS setting is inherited only when the child uses that provider. Fast mode is inherited by default. A profile with `fastMode: false` opts out, while `fastMode: true` enables fast mode even when the host setting is off; the `subagent_spawn` result warns the parent session when this override occurs. Child providers that do not yet support fast mode return an unsupported-provider error instead of silently ignoring an enabled setting.

## Lifecycle, results, and recovery

Every child has independent process and turn state. A process may be `alive` while its turn is `idle`; a supervisor restart marks the process `detached` without claiming that its last turn failed. Durable manifests include the task, parent/root session identity, workspace mode, attempt, process/turn state, heartbeat timestamps, and logical result references.

During an active delegated turn, interactive mode shows compact rows beside the input area: a themed spinner, the named profile (or agent ID), current activity, and elapsed time since the latest activity or heartbeat. Rows default below the input and can move above it through **running subagent position** in `/settings` → **tui settings**. On short terminals, omitted workers collapse into one count summary so the editor remains visible. Rows disappear when the turn becomes idle, the process detaches, or the turn reaches a terminal state; use `/subagents` for durable history and results.

The worker and supervisor communicate over newline-delimited JSON. Current messages use version `1` envelopes with `version`, `message_id`, `agent_id`, `turn_id`, `timestamp`, and `payload`. Unknown event names and payload fields are retained. Only versioned JSONL envelopes are accepted on the worker protocol.

A completed worker emits a `turn.result` event and writes `result.json`. The inline output is bounded; inspect the full session through the stable references:

If a provider rejects a child request because its payload or context window is too large, the worker compacts its persisted transcript and continues the same request once. If compaction or that retry cannot fit, the result uses error code `context_limit` and directs the caller to narrow the task or reduce gathered context; it does not include the provider's raw request error.

```text
subagent://<id>
subagent://<id>/history
subagent://<id>/result
subagent://<id>/patch
```

Use `/subagents resume-session <id>` to continue the existing session without replaying its original task. Use `/subagents restart-task <id>` only when intentionally starting the stored task again. Cancellation requests graceful shutdown first, then forcefully cancels after the configured grace period.

When auto-subagents is enabled, the model also receives the read-only `subagent_status` tool. Call it with `{}` to list workers visible to the active supervisor session, or pass `{"agent_id":"<id-or-unique-prefix>"}` to query one worker. Queries use in-process snapshots and never wait for a worker turn or process to finish. The JSON response contains the worker id, a normalized lifecycle state (`starting`, `running`, `completed`, `failed`, `cancelled`, or `detached`), the underlying `process_state` and `turn_state`, start/update/finish timestamps, a bounded first-line task summary, and terminal-result metadata/reference when available. It intentionally omits prompts, transcripts, result output, credentials, provider settings, and filesystem paths. An unknown or ambiguous id is returned as a model-visible tool error; the existing `/subagents` dashboard and final-result notifications remain unchanged. An explicit `--tools` allowlist must include `subagent_status` when the status tool is needed.

## Resource policy

The persisted `subagents` config object supports `max_concurrent`, `max_concurrent_per_parent`, `max_total_spawned`, `queue_timeout`, `default_timeout`, `max_turns`, output caps, allowed tools/roots, heartbeat and idle timeouts. A missing or non-positive configured `max_turns` uses the default ceiling of 3. The `max_turns` field in `subagent_spawn` is optional: omit it to use the policy ceiling, or provide a value from 1 through the configured maximum. Limits apply to slash commands, `subagent_spawn`, and batch operations. A child cannot create descendants in v1. Per-agent timeouts are retained across reload/resume. Packaged `zut run` agents keep their declared capability ceiling by disabling subagent delegation; profiles are not a substitute for that permission boundary.
