---
name: tasked-phases
description: Use when the user wants a spec turned into phased work with checklist tasks tracked by the tasked_phases tool, or when ongoing work should be tracked phase by phase.
---

# Tasked Phases

Use the `tasked_phases` tool as the source of truth for the spec, phases, current focus, and checklist progress.

## Workflow

1. Clarify missing requirements when the spec is ambiguous.
2. Save the accepted spec with `tasked_phases` using `set_spec`.
3. Create or materially revise the plan with `replace_plan`.
4. Keep phases small and tasks concrete enough to check off.
5. Set the focus with `set_current_phase` and pass the target `phaseId`.
6. Call `set_task_checked` immediately after each checklist task is completed.
7. Use `set_phase_checked` to complete or reopen every task in a phase. The phase must already contain at least one task.
8. When every task is checked, treat the plan as closed. For unrelated work, call `clear` to discard the plan, or call `set_spec` immediately followed by `replace_plan` to record a new spec and a new set of phases.

The right-bar summary and status bar are the persistent UI: both refresh after
each successful mutation and session restore and use `p done/total | t done/total`.
Active plans also publish their phase and task rows below the compact title; the
host dims rows outside the current phase. Use `[x]`, `[ ]`, and `[>]` markers.
Use `get_status` or `/phases` when the complete checklist is needed. Persistent
chrome is cleared automatically once every phase and task is complete. Keep
phase titles to 32 characters or fewer and task text to 64 characters or fewer
in plans and detailed views. Routine successful mutations return one concise
progress line; use `get_status` or `replace_plan` when detailed output is needed.
`set_task_checked` changes one task. `set_phase_checked` intentionally checks
or unchecks every task in one phase, so use it only for an intentional phase-wide
update. Multiple individual checks in one turn are sequential calls, not an
automatic bulk update.

## Planning rules

- Prefer 3–7 phases unless the task is tiny.
- Each task should describe an observable outcome.
- Avoid vague tasks such as “work on this” or “finish implementation”.
- Keep the stored spec and plan concise and operational.

## Tool guidance

- Use `get_status` before relying on remembered state or revising a long-running plan.
- Use `replace_plan` for a substantial restructuring; use `add_*`, `update_*`, and `remove_*` for small changes.
- Pass `phaseId` as the raw ID shown in brackets, without the brackets. The tool also accepts a complete phase label or a unique phase title when recovering from copied output.
- Do not rely on prose alone for completion state; update the tool after each completed task.
