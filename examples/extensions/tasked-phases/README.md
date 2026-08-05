# tasked-phases

A zut extension for keeping a spec, phased plan, and checklist in one place.
It provides:

- the `tasked_phases` tool for persistent spec/phase/task state;
- `/phases` for a full interactive panel;
- compact mutation results and full `get_status` output;
- project-scoped JSON persistence under the extension data directory.

The tool actions are:

```text
get_status       set_spec          replace_plan
add_phase        update_phase      remove_phase
add_task         update_task      remove_task
set_current_phase set_task_checked set_phase_checked
clear
```

Successful mutations return one progress line; use `get_status` or
`replace_plan` for details. `set_task_checked` changes one task;
`set_phase_checked` changes every task in a phase.

## Build and run

From the zut repository:

```bash
cd examples/extensions/tasked-phases
go build -o tasked-phases .

# One-run development load, launched from the project you want to track:
cd /path/to/project
zut --no-ext --ext /path/to/zut/examples/extensions/tasked-phases

# Or install it globally (run this from the extension directory):
cd /path/to/zut/examples/extensions/tasked-phases
zut ext install --build=go .
```

`--build=go` explicitly builds the manifest-declared executable in the staged
installation. If an incomplete copy was installed already, remove it with
`zut ext remove tasked-phases --yes` before reinstalling. You can also build
manually with `go build -o tasked-phases .` and then use `zut ext install .`.

Then use `/phases` or ask the model to create a spec and phased checklist with
`tasked_phases`. The extension keys its durable state by the host-reported
project CWD.

The extension bundles its workflow skill under `skills/tasked-phases/`, so
`zut ext install` makes the guidance available automatically. The standalone
copy at `examples/skills/tasked-phases/SKILL.md` remains useful when you want
the same workflow without installing the extension.

## Persistence and turn focus

The extension stores one restrictive state file per project CWD as a fallback
for hosts that do not persist sessions. When zut session persistence is
available, the plan is also stored as extension-owned state on each session
branch and restored when sessions are opened, switched, or forked.

Before each model turn, the extension supplies only the active phase, its goal,
and a bounded set of incomplete tasks as hidden context. The same context
reminds the model to update the checklist continuously, mark each task as soon
as it is completed, and advance the current phase when work moves forward.
Completed phases and future phases are not repeated in every provider request;
use `get_status` or `/phases` when the full checklist is needed.

Active plans publish `p done/total | t done/total` and checklist rows through
`right_bar`. The host dims inactive phases, uses a bounded above-input fallback
on narrow terminals or when `Ctrl+B` hides the rail, and clears the chrome when
the plan is complete. Use `get_status` or `/phases` for full details.
