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
  api_key: ""
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

## Workspace Reuse

Workspaces are preserved across retries instead of being re-cloned. When `PrepareClone` runs, it
checks whether the workspace already contains a `.git` directory:

- **Healthy repo**: fetches all remotes and checks out the branch. No re-clone.
- **Corrupt repo** (e.g., `git rev-parse --git-dir` fails): removes and re-clones.
- **Transient failure** (fetch or checkout fails, but repo is healthy): preserves the workspace and
  returns an error. This avoids destroying local work on a temporary network or auth issue.

This means retries resume from the prior workspace state, which is important for the 6-phase agent
prompts that check `git log` and `git status` to determine whether prior work exists.

## Docker-backed Harnesses

If an agent type has a `docker:` block, Maestro still runs the scheduler, tracker sync, lifecycle
updates, prompt rendering, and approval routing on the host. Only the harness process is
containerized.

Operational notes:

- the prepared workspace is bind-mounted into the container, so git changes remain visible on the host
- prefer `docker.secrets` and `docker.tools` to explicitly allow extra env vars and read-only mounts into the container
- `docker.env_passthrough` and `docker.mounts` remain available for compatibility, but they are the broader legacy path
- Docker reuse is opt-in via `docker.reuse.mode`:
  - `none` keeps the current fresh-container behavior
  - `stateless` reuses a trusted shared container for matching profiles and is best for investigative/reporting agents
  - `lineage` reuses only within the same issue/workspace lineage and is the safer choice for coding retries and continuations
- for Claude, bearer-token proxy auth uses `ANTHROPIC_AUTH_TOKEN`, plus `ANTHROPIC_BASE_URL` when the proxy is not `https://api.anthropic.com`
- for Codex proxy/API-key mode, pass `OPENAI_API_KEY`, set the proxy URL through `codex.extra_args` as `openai_base_url="..."`, and force API auth with `forced_login_method="api"`
- for mounted CLI auth, prefer the built-in `docker.auth` presets or narrow `docker.secrets.mounts` entries rather than mounting your whole home directory
- `maestro doctor` shows the effective Docker env injections and read-only secret/tool mounts for each Docker-backed agent
- if `HOME` is not set explicitly for the container, Maestro provides a writable container-local `HOME`

## Run Log Persistence

After each run completes (success or failure), agent stdout and stderr are saved to:

- `{state.dir}/runs/{run-id}/stdout.log`
- `{state.dir}/runs/{run-id}/stderr.log`

These persist across restarts and are useful for post-mortem debugging. Only runs that produced output
generate log files.

## State And Logs

Important directories:

- `workspace.root`: checked out workspaces
- `state.dir`: persisted `runs.json` and `runs/{run-id}/` log directories
- `logging.dir`: log files
- `logging.max_files`: local log retention cap; older `*.log` files are pruned on startup

Lifecycle labels use a configurable prefix set by `defaults.label_prefix` (default: `maestro`) and
can be overridden per source with `sources[].label_prefix`. The default labels are
`{prefix}:active`, `{prefix}:done`, `{prefix}:failed`, and `{prefix}:retry`.
When `defaults.on_dispatch`, `defaults.on_complete`, or `defaults.on_failure` are configured, those
hooks apply to every source unless a source overrides the same hook locally. `on_dispatch` supports
both `add_labels`/`remove_labels` and `state`. When custom labels are provided in `on_complete` or
`on_failure`, no default `{prefix}:done` or `{prefix}:failed` label is added — only the explicit
labels from the hook are applied. `{prefix}:active` is always removed on completion or failure. See
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

- `sources[].max_active_runs`
- `agent_types[].max_concurrent`
- `defaults.max_concurrent_global`

The effective concurrency for a source is the smallest of those three limits.

When a retry becomes due, Maestro re-fetches the issue from the tracker. If the issue is terminal
or no longer matches the source filter, the retry is automatically discarded instead of dispatched.
This prevents stale retries from re-running work that was externally resolved.

Stall detection uses the configured inactivity timeout:

- `defaults.stall_timeout` for the shared default
- `agent_types[].stall_timeout` for a per-agent override

If a run stops producing observable output for longer than that window, Maestro stops it and schedules a retry.

Force-poll actions from the TUI, web, and API bypass the normal poll interval when they are not debounced. A successful force poll runs immediately and resets the next scheduled poll window from that completed poll.

Supported hook phases:

- shell hooks:
- `hooks.after_create`
- `hooks.before_run`
- `hooks.after_run`

By default hooks run through the host shell and share `hooks.timeout`. If you
set `hooks.execution: container`, Maestro runs the hook command in the same
Docker environment as the harness for that agent type.

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
- `POST /api/v1/runs/<run_id>/stop`
- `GET /api/v1/config/raw`
- `POST /api/v1/config/validate`
- `POST /api/v1/config/dry-run`
- `POST /api/v1/config/save`
- `GET /api/v1/config/backups`
- `POST /api/v1/config/backups/create`
- `GET /api/v1/config/backups/<backup_id>`
- `POST /api/v1/packs/save`

The built-in dashboard at `/` uses those resource endpoints directly and listens to `/api/v1/stream` over Server-Sent Events so the page refreshes on runtime changes without a fixed polling loop. The browser UI is dark by default, has a light theme toggle, and supports source/run selection, quick filtering, sorting, retries, approvals, and a context-aware event timeline.

Loopback binds (`127.0.0.1`, `localhost`, `::1`) do not require API auth. Non-loopback binds require `server.api_key`. When auth is enabled, API clients must send `Authorization: Bearer <key>`, and the dashboard can be opened once with `?api_key=<key>` so it can store the key in session storage.

For remote access, terminate TLS at a trusted proxy and protect the key: it allows config and run-control operations, not just viewing status. See [finding S7](../FINDINGS.md).

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

That configured DM user is also the authorized operator for Slack actions and replies.

For a fixed channel, use:

- `channels[].config.mode: channel`
- `channels[].config.channel_id` or `channel_id_env`
- `channels[].config.authorized_user_ids` or `authorized_user_ids_env`

In channel mode, only explicitly authorized Slack users can approve, reject, stop runs, or answer
pending control messages. Unauthorized users receive a thread reply explaining that they are not
allowed to operate Maestro from that thread.

Current limits:

- Slack thread replies now resolve pending Maestro control messages and generic runtime message requests, but there is still no broad free-form agent chat surface
- there is no Teams equivalent yet
- Slack state is persisted locally in `state.dir/slack.json`

## Agent Environment

Agent processes do not inherit the full Maestro process environment. Maestro passes a curated
baseline such as `PATH`, `HOME`, locale/XDG/temp vars, and common proxy/cert vars, then merges any
explicit `agent_types[].env` entries on top. Add required secrets or custom variables to the agent
config explicitly instead of relying on ambient shell inheritance.

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

Shutdown enforces a 5-second timeout. If the harness does not stop within that window, Maestro
exits anyway. This prevents `Ctrl-C` from hanging indefinitely on a stuck agent process.

## Doctor

Run `maestro doctor --config /path/to/maestro.yaml` to validate your setup. It checks:

- config loads and passes validation
- likely overlapping source routes that could cause ambiguous or duplicate dispatch
- required host harness binaries (`claude`, `codex`) are available in `PATH`
- Docker-backed agent images are present or pullable, the Docker daemon/context is reachable, the visible `DOCKER_*` client env looks sane, mutable non-digest image references are flagged, and Docker network-policy support is checked where possible

Use this before your first run or after changing `agent_types[].harness` entries or Docker-backed agent settings.

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
maestro doctor --config /path/to/maestro.yaml
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
- Claude can demonstrate the approval UI, but an approved turn resumes with `bypassPermissions`. Do not treat that as per-action authorization; see [finding S2](../FINDINGS.md).
