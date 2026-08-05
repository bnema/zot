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

Successful mutations return one progress line; use `get_status` or
`replace_plan` for details. `set_task_checked` changes one task;
`set_phase_checked` changes every task in a phase.

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
