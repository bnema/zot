# tasked-phases

A zot extension for keeping a spec, phased plan, and checklist in one place.
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

## Build and run

From the zot repository:

```bash
cd examples/extensions/tasked-phases
go build -o tasked-phases .

# One-run development load, launched from the project you want to track:
cd /path/to/project
zot --no-ext --ext /path/to/zot/examples/extensions/tasked-phases

# Or install it globally (run this from the extension directory):
cd /path/to/zot/examples/extensions/tasked-phases
zot ext install --build=go .
```

`--build=go` explicitly builds the manifest-declared executable in the staged
installation. If an incomplete copy was installed already, remove it with
`zot ext remove tasked-phases --yes` before reinstalling. You can also build
manually with `go build -o tasked-phases .` and then use `zot ext install .`.

Then use `/phases` or ask the model to create a spec and phased checklist with
`tasked_phases`. The extension keys its durable state by the host-reported
project CWD.

The extension bundles its workflow skill under `skills/tasked-phases/`, so
`zot ext install` makes the guidance available automatically. The standalone
copy at `examples/skills/tasked-phases/SKILL.md` remains useful when you want
the same workflow without installing the extension.

## Persistence and turn focus

The extension stores one restrictive state file per project CWD as a fallback
for hosts that do not persist sessions. When zot session persistence is
available, the plan is also stored as extension-owned state on each session
branch and restored when sessions are opened, switched, or forked.

Before each model turn, the extension supplies only the active phase, its goal,
and a bounded set of incomplete tasks as hidden context. The same context
reminds the model to update the checklist continuously, mark each task as soon
as it is completed, and advance the current phase when work moves forward.
Completed phases and future phases are not repeated in every provider request;
use `get_status` or `/phases` when the full checklist is needed.

When a plan is active, the extension publishes its compact checklist through the
host's generic `right_bar` widget position. zot keeps it beside the transcript on
wide terminals and automatically falls back to the normal above-input widget on
narrow terminals. The extension does not implement any terminal layout code.
