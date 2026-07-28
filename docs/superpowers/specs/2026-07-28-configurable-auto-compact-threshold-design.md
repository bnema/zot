# Configurable Auto-Compact Threshold

## Goal

Let users choose when Zot automatically compacts an interactive transcript from the existing `/settings` dialog, while preserving the current 85% behavior by default.

## User Experience

Add a top-level `/settings` option named **auto-compact threshold** with these choices:

- `off`
- `70%`
- `80%`
- `85%` (default)
- `90%`

Changing the option takes effect for the next turn without restarting Zot.

The `off` choice disables context-percentage triggers before and after turns. It does not disable:

- manual `/compact`
- explicit RPC or SDK compaction
- automatic compaction and retry after an HTTP 413 or equivalent payload-too-large response

## Configuration

Persist the selected percentage in `$ZOT_HOME/config.json` as:

```json
{
  "auto_compact_threshold": 85
}
```

The field is an optional integer percentage:

- A missing field resolves to `85` for backward compatibility.
- `0` represents `off`.
- Valid non-zero values are `70`, `80`, `85`, and `90`.
- Any other persisted value resolves to `85` at runtime.
- When a user selects a setting in the TUI, Zot writes only one of the supported values.

## Implementation Boundaries

The interactive host continues to own automatic triggering. `core.Agent.Compact`, RPC, SDK, bot, print, and JSON mode behavior remain unchanged.

Replace the hard-coded `0.85` comparison with a helper that resolves the configured integer percentage and compares the most recent context usage against the model's advertised context window. A resolved value of `0` returns false for percentage-based checks.

The payload-too-large recovery path continues to invoke automatic compaction independently of the configured percentage.

The settings dialog uses its existing fixed-option picker. No free-form numeric input or new dialog control is introduced.

## Error Handling

Configuration writes use the existing settings-store error path and show failures in the TUI status area. An invalid value already present in `config.json` does not prevent startup; Zot falls back to 85%.

## Tests

Add focused tests covering:

- missing configuration resolves to 85%
- every supported preset resolves correctly
- invalid persisted values resolve to 85%
- `off` suppresses percentage-triggered compaction
- threshold comparison uses the selected percentage
- the settings store persists the selected integer
- the settings dialog exposes all presets with 85% selected by default

Run the focused package tests during development, then `gofmt`, `go test ./...`, `go vet ./...`, `git diff --check`, and final status/diff inspection.

## Documentation

Update the README sections for `/settings` and `/compact` to list the presets, default, immediate application, and the fact that `off` does not disable payload-too-large recovery.
