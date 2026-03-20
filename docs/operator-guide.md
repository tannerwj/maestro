# Operator Guide

## Run Modes

TUI mode:

```bash
go run ./cmd/maestro run --config /path/to/maestro.yaml
```

Headless mode:

```bash
go run ./cmd/maestro run --config /path/to/maestro.yaml --no-tui
```

Installed binary:

```bash
maestro run --config /path/to/maestro.yaml
```

To enable the local web/API surface during a normal run:

```yaml
server:
  enabled: true
  host: 127.0.0.1
  port: 8742
```

Then open [http://127.0.0.1:8742](http://127.0.0.1:8742).

## TUI Controls

- `tab` switches focus between sources, active runs, pending messages, retries, and pending approvals
- `j` or down arrow moves within the focused list
- `k` or up arrow moves within the focused list
- `a` approves the selected pending approval
- `r` rejects the selected pending approval
- `e` enters reply mode for the selected pending message
- `s` sends a quick `start` reply for the selected pending message
- `/` enters search mode
- `f` cycles source-group filters
- `u` toggles the attention-only filter
- `w` toggles the awaiting-approval filter
- `c` clears source filters
- `o` cycles active-run sort order
- `O` cycles retry sort order
- `v` toggles compact mode
- `q` quit

The TUI now shows:

- an overview line with visible source, active-run, approval, and retry counts
- grouped per-source status lines with health badges for at-a-glance health
- a selectable `Sources` list with a `Selected source` detail pane for tracker, group, tags, last poll, visible work counts, and recent source events
- a selectable `Active runs` list
- a selectable `Retries` list for delayed reruns
- a `Selected run` detail pane with run ID, source, issue, title, URL, agent, harness, approval state, timestamps, workspace path, error, and live stdout/stderr tails
- a `Selected retry` detail pane with source, issue, attempt, due time, and error
- a selectable approval list plus detailed approval pane

## State And Logs

Important directories:

- `workspace.root`: checked out workspaces
- `state.dir`: persisted `runs.json`
- `logging.dir`: log files
- `logging.max_files`: local log retention cap; older `*.log` files are pruned on startup

Lifecycle labels use a configurable prefix set by `defaults.label_prefix` (default: `maestro`). The
default labels are `{prefix}:active`, `{prefix}:done`, `{prefix}:failed`, and `{prefix}:retry`.
When `defaults.on_dispatch`, `defaults.on_complete`, or `defaults.on_failure` are configured, those
hooks apply to every source unless a source overrides the same hook locally. Custom labels from
`add_labels`/`remove_labels` replace the built-in done/failed defaults, but `{prefix}:active` is
always applied on dispatch and removed on completion or failure. See
[getting-started.md](getting-started.md#lifecycle-transitions) for pipeline examples.
Labels such as `{prefix}:coding` or `{prefix}:review` are not reserved; they remain visible to
source filters and are intended for pipeline routing. Only the exact reserved lifecycle labels are
ignored by intake logic. Lifecycle `state` updates are best-effort tracker metadata and should not
be used as the primary routing contract.

If the config has multiple sources, Maestro stores state per source under:

- `state.dir/<source-name>/runs.json`

The process keeps enough state to:

- retry failed runs
- retry runs stopped by stall detection
- suppress already-finished issues until the tracker item changes
- recover an interrupted active run as a retry after restart
- preserve recent approval history across restart

The current runtime allows multiple sources in one config. Concurrency is bounded by:

- `defaults.max_concurrent_global`
- `agent_types[].max_concurrent`

Stall detection uses the configured inactivity timeout:

- `defaults.stall_timeout` for the shared default
- `agent_types[].stall_timeout` for a per-agent override

If a run stops producing observable output for longer than that window, Maestro stops it and schedules a retry.

Supported hook phases:

- shell hooks:
- `hooks.after_create`
- `hooks.before_run`
- `hooks.after_run`

All hooks run through the local shell and share `hooks.timeout`.

Current Maestro control points:

- `controls.before_work`
- runtime approval requests
- runtime message requests and operator replies

`controls.before_work` is a blocking Maestro-managed gate after the issue is claimed and the workspace is prepared, but before the harness starts. Use it when you want the operator to review the work item, add instructions, or stop the run before any agent work begins.

## Local Web/API

The first API slice is read-mostly with approval actions:

- `GET /healthz`
- `GET /api/v1/stream`
- `GET /api/v1/status`
- `GET /api/v1/config`
- `GET /api/v1/sources`
- `GET /api/v1/runs`
- `GET /api/v1/retries`
- `GET /api/v1/events`
- `GET /api/v1/approvals`
- `GET /api/v1/messages`
- `POST /api/v1/approvals/<request_id>/approve`
- `POST /api/v1/approvals/<request_id>/reject`
- `POST /api/v1/messages/<request_id>/reply`

The built-in dashboard at `/` uses those resource endpoints directly and listens to `/api/v1/stream` over Server-Sent Events so the page refreshes on runtime changes without a fixed polling loop. The browser UI is dark by default, has a light theme toggle, and supports source/run selection, quick filtering, sorting, retries, approvals, and a context-aware event timeline.

## Slack Operations

Slack is now available as a communication channel for workflows that need remote approval handling.

Current supported behavior:

- DM or fixed-channel workflow threads
- approval requests with interactive `Approve` and `Reject` buttons
- pending Maestro control messages such as `before_work`
- Slack thread replies routed into pending Maestro control messages and generic runtime message requests
- workflow status updates for completion, failure, retries, and stops
- `Stop workflow` from the Slack thread

Required Slack config:

- a channel entry under `channels`
- `agent_types[].communication` pointing at that channel name
- a bot token in `channels[].config.token_env`
- an app-level Socket Mode token in `channels[].config.app_token_env`

Slack app setup checklist:

- enable Socket Mode
- create an app token with `connections:write`
- add bot scopes:
  - `chat:write`
  - `im:write`
  - `im:history`
- enable Interactivity
- enable Event Subscriptions
- subscribe to bot event `message.im`
- enable the Messages tab setting that allows users to send messages to the app
- reinstall the app after changing scopes or subscriptions

For DM routing, set either:

- `channels[].config.user_id`
- or `channels[].config.user_id_env`

For a fixed channel, use:

- `channels[].config.mode: channel`
- `channels[].config.channel_id` or `channel_id_env`

Current limits:

- Slack thread replies now resolve pending Maestro control messages and generic runtime message requests, but there is still no broad free-form agent chat surface
- there is no Teams equivalent yet
- Slack state is persisted locally in `state.dir/slack.json`

## Normal Demo Flow

1. Create or label one tracker issue so it matches the source filter.
2. Start Maestro.
3. Watch the active run in the TUI or logs.
4. If using manual approval, approve the request.
5. Inspect the workspace branch and tracker comments/labels.

If Maestro restarts while an approval is pending, the request is preserved in local state as stale history and the interrupted run is recovered as a retry. That gives the operator context without pretending the old approval can still be resolved.

## Shutdown

Use `Ctrl-C` or stop the process cleanly. Maestro will:

- stop the active harness
- persist state
- pick the run back up as a retry on the next start if needed

## Troubleshooting

No issues found:

- verify the tracker token env var is set
- verify the filter matches a real open issue
- verify the tracker project ID or path is correct

Run never starts:

- verify the harness binary is installed and authenticated
- for local packs, verify the prompt path or `agent_pack` path is valid
- for repo packs, verify the source repo cloned successfully and the repo contains the expected `.maestro/` pack files
- verify the source repo metadata is present

Run restarts unexpectedly:

- inspect `state.dir/runs.json`
- inspect the latest log file in `logging.dir`

Run stalls and gets retried:

- inspect recent log events for the last observed activity and stop reason
- increase `stall_timeout` if the agent regularly spends long periods without output
- move long local setup into `hooks.before_run` only if it is expected and bounded by `hooks.timeout`

Useful inspection commands:

```bash
maestro inspect config --config /path/to/maestro.yaml
maestro inspect state --config /path/to/maestro.yaml
maestro inspect state --state-dir /path/to/state-dir
maestro inspect runs --config /path/to/maestro.yaml
```

`inspect runs` summarizes active, retry, finished, done, and failed counts per source, along with the latest sanitized error and a health status such as `active`, `retrying`, or `degraded`.

`inspect state` gives the same per-source health rollup over persisted `runs.json`, including pending approvals and approval history counts.

Useful recovery commands:

```bash
maestro reset issue --config /path/to/maestro.yaml group/project#123
maestro cleanup workspaces --config /path/to/maestro.yaml --dry-run
maestro cleanup workspaces --config /path/to/maestro.yaml
```

`reset issue` only touches local state. It does not change the tracker item. It refuses to reset the currently active run.

`cleanup workspaces` removes workspace directories under `workspace.root` except for the currently active workspace recorded in `runs.json`.

Active run should have stopped but kept going:

- check whether the tracker item was marked `maestro:done` or `maestro:failed`
- check whether the issue or epic bucket became terminal
- inspect recent events in the TUI; reconciliation stops are logged there explicitly

Large multi-source config is hard to scan in the TUI:

- press `/` to search by source name, tracker, group, tag, active issue, title, or error text
- use `tab` to move between sources, active runs, retries, and approvals
- press `u` to narrow the view to sources and work that need attention
- press `w` to narrow the view to approval-driven work
- press `f` to cycle source groups
- press `c` to clear all source filters

Hooks behave unexpectedly:

- check the shell command under `hooks.after_create`, `hooks.before_run`, or `hooks.after_run`
- check `hooks.timeout`
- inspect logs for the sanitized hook stderr/stdout
- remember that `hooks.before_remove` is not wired yet

Codex manual approval does not appear:

- this is a known limitation of the currently tested app-server behavior
- use Claude manual approval if you need a live approval demo today
