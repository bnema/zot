# Terminal Alerts and Extension Bells

## Goal

Provide a reusable host alert boundary for the interactive terminal. The first alert kind is a terminal bell, emitted as `\a` only by the interactive host.

The same alert policy serves the main agent session and trusted, user-installed extensions that need to draw attention to an interactive question or panel.

## User Experience

The main interactive session emits one bell after a submitted main-agent run becomes idle and the paced final response has finished rendering when the outcome needs attention:

- normal final answer
- output-limit/truncated response
- non-recoverable provider error
- rescue dialog opened after a recoverable provider error

The bell is suppressed for explicit cancellation, automatic compaction/retry, and work immediately followed by another queued turn. `/btw`, Telegram, SDK, JSON, print, stream, RPC, and swarm-agent modes do not write raw terminal bell bytes.

Users can disable both main-agent and extension terminal alerts from `/settings`. Alerts are enabled by default when the configuration field is missing.

## Public Extension Surface

Extensions send a fire-and-forget structured alert frame:

```json
{"type":"alert","kind":"bell","reason":"question_ready"}
```

The Go extension SDK exposes the equivalent `Extension.Alert(AlertRequest)` method. The host-side alert hook is optional so existing `HostHooks` embedders remain compatible. `kind` is initially `bell`; `reason` is semantic metadata and is not rendered as terminal text. Extensions never provide raw terminal bytes, ANSI sequences, counts, or sound patterns.

The host owns the output policy. Interactive mode writes a standalone BEL byte through its injected `tui.Terminal`; non-interactive hosts remain byte-clean and may ignore or translate the request.

## Configuration

Persist the shared policy in `$ZOT_HOME/config.json`:

```json
{"terminal_alerts_enabled": true}
```

The optional boolean resolves as follows:

- missing or `null`: enabled
- `true`: enabled
- `false`: disabled

The setting applies immediately to the current interactive session and persists for future sessions.

## Lifecycle Boundary

The main-agent alert is scheduled from the interactive `startTurnRequest` completion path, not from `core.Agent` events. `EvTurnEnd` is one model step and `EvDone` is not emitted on every error path. The final outcome is tracked from both the last turn event and the returned `Prompt`/`Continue` error.

A pending main-agent alert waits for the streaming pacer to drain `streamPending`. If a new queued/recovery turn begins before the drain completes, the pending alert is discarded. The bell is emitted only after the host is truly idle.

## Extension Routing

`packages/agent/extproto` defines the alert wire frame. `packages/agent/extensions` routes it through the optional `AlertHostHooks` extension to `HostHooks`. Interactive mode applies the shared setting and terminal output. RPC mode translates extension alerts into an `ext_alert` JSON event; print, stream, JSON, Telegram, and other non-interactive hosts do not write BEL to their data streams.

Unknown alert kinds are ignored by the interactive renderer. The wire shape is additive and permits future host alert kinds without exposing terminal implementation details.

## Tests

Cover:

- BEL writes as a standalone byte through an injected writer
- extension SDK emits the structured alert frame
- extension manager routes alert frames to host hooks
- disabled terminal alerts suppress both main and extension alerts
- missing configuration enables alerts
- main completion alerts wait for paced output to drain
- cancellation, recovery, queue, and side-chat paths do not produce an unwanted main bell
- RPC alert translation does not emit raw BEL

## Documentation

Document the `/settings` terminal-alert toggle and the extension alert frame/SDK method in `README.md` and `docs/extensions.md`. State that terminal bells depend on terminal emulator settings and may be audible, visual, or suppressed by the host terminal.
