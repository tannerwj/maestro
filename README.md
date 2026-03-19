# Maestro

Maestro is a single-machine orchestration daemon that polls a tracker, prepares the run environment, renders an agent prompt, runs an agent, and keeps enough local state to survive restart and avoid duplicate dispatch.

The current build is a working POC. It is intentionally narrow in surface area, but the runtime now supports multiple sources, multiple agent mappings, and bounded parallel dispatch with local state and a terminal UI.

## What Works Today

- GitLab project issue polling
- GitLab epic bucket polling via linked child issues
- Linear project issue polling
- Claude runs end to end
- Codex runs end to end
- Claude manual approval flow
- Slack DM threads for approval prompts and run status updates
- Local workspace cloning and branch creation
- Prompt templating
- Retry with persisted `runs.json`
- Restart recovery of interrupted active runs
- Stall detection for inactive runs
- Configurable workspace lifecycle hooks
- Persisted approval history and richer approval TUI state
- Tracker writeback and reconciliation for GitLab and Linear
- Multiple sources and agent mappings in one config
- Source-level tracker defaults and agent defaults to reduce config repetition
- Bounded parallel dispatch via `defaults.max_concurrent_global` and `agent_types[].max_concurrent`

## Current Limits

- GitLab project issues and GitLab epic-backed child issues are supported
- Codex manual approval is wired in code but the current local app-server build does not emit approval requests in this environment
- Shell hook support is currently limited to `after_create`, `before_run`, and `after_run`
- Maestro control points currently include `controls.before_work` plus runtime approvals and operator message replies
- Slack supports status updates, approval buttons, `before_work` control prompts, and typed thread replies for pending Maestro control messages
- Dynamic config reload and tracker-specific completion workflows are still out of scope

## Quick Start

1. Copy a sample from [examples/gitlab-claude-auto.yaml](examples/gitlab-claude-auto.yaml) or [examples/linear-claude-auto.yaml](examples/linear-claude-auto.yaml).
2. Set the required token env var referenced by `connection.token_env`.
3. Keep `repo` URLs credential-free. Maestro injects tracker auth from `connection.token_env` and redacts token-shaped values from logs.
4. Update the tracker project, filters, user fields, and `agent_packs_dir` if you move the built-in packs.
5. Adjust `defaults.stall_timeout` or per-agent `stall_timeout` if you want a different inactivity watchdog window.
6. Run:

```bash
go run ./cmd/maestro --config /path/to/maestro.yaml
```

Use `--no-tui` if you want a plain process without the terminal UI:

```bash
go run ./cmd/maestro --config /path/to/maestro.yaml --no-tui
```

Repo entrypoints:

```bash
make test
make build
make install
make release VERSION=v0.1.0
make run CONFIG=demo/gitlab-claude-auto/maestro.yaml
make inspect-config CONFIG=demo/gitlab-claude-auto/maestro.yaml
make inspect-state CONFIG=demo/gitlab-claude-auto/maestro.yaml
make inspect-runs CONFIG=demo/gitlab-claude-auto/maestro.yaml
make reset-issue CONFIG=demo/gitlab-claude-auto/maestro.yaml ISSUE=group/project#123
make cleanup-workspaces CONFIG=demo/gitlab-claude-auto/maestro.yaml
make smoke-multi-source
make smoke-many-sources
```

## Samples

- GitLab + Claude auto: [examples/gitlab-claude-auto.yaml](examples/gitlab-claude-auto.yaml)
- GitLab + Claude manual: [examples/gitlab-claude-manual.yaml](examples/gitlab-claude-manual.yaml)
- GitLab + Claude manual + Slack DM approvals: [examples/gitlab-claude-slack-manual.yaml](examples/gitlab-claude-slack-manual.yaml)
- GitLab + Codex auto: [examples/gitlab-codex-auto.yaml](examples/gitlab-codex-auto.yaml)
- GitLab epic + Claude auto: [examples/gitlab-epic-claude-auto.yaml](examples/gitlab-epic-claude-auto.yaml)
- GitLab + repo-maintainer pack: [examples/gitlab-repo-maintainer.yaml](examples/gitlab-repo-maintainer.yaml)
- Linear + Claude auto: [examples/linear-claude-auto.yaml](examples/linear-claude-auto.yaml)
- Linear + Claude manual: [examples/linear-claude-manual.yaml](examples/linear-claude-manual.yaml)
- Linear + Codex auto: [examples/linear-codex-auto.yaml](examples/linear-codex-auto.yaml)
- Linear + triage pack: [examples/linear-triage.yaml](examples/linear-triage.yaml)
- Multi-source GitLab project + GitLab epic + Linear: [examples/multi-source-claude-auto.yaml](examples/multi-source-claude-auto.yaml)
- Many sources with shared defaults: [examples/many-sources-claude-auto.yaml](examples/many-sources-claude-auto.yaml)

Built-in agent packs:

- Code change: [agents/code-pr/agent.yaml](agents/code-pr/agent.yaml)
- Repo maintainer: [agents/repo-maintainer/agent.yaml](agents/repo-maintainer/agent.yaml)
- Triage: [agents/triage/agent.yaml](agents/triage/agent.yaml)

## Smoke Scripts

- GitLab smoke: [scripts/smoke_gitlab.sh](scripts/smoke_gitlab.sh)
- Linear smoke: [scripts/smoke_linear.sh](scripts/smoke_linear.sh)
- Three-source smoke: [scripts/smoke_multi_source.sh](scripts/smoke_multi_source.sh)
- Many-sources smoke with fresh fixtures: [scripts/smoke_many_sources.sh](scripts/smoke_many_sources.sh)

These scripts default to `approval_policy=auto`. They create a temporary config, run Maestro, wait for marker files in the workspace, and print the artifact paths. The GitLab smoke provisions and closes its own disposable issue by default so it does not depend on a pre-labeled tracker fixture already being open.
The Linear smoke now does the same: it provisions a disposable issue in the configured project, moves it into the target state, and marks it completed during cleanup.

## Demo Configs

- GitLab demo: [demo/gitlab-claude-auto/maestro.yaml](demo/gitlab-claude-auto/maestro.yaml)
- Linear demo: [demo/linear-claude-auto/maestro.yaml](demo/linear-claude-auto/maestro.yaml)

These keep logs, state, and workspaces under `demo/*/var/` so you can inspect and reset them easily.

## Operational Notes

- `defaults.stall_timeout` sets the inactivity window before Maestro stops a run and queues a retry.
- `agent_types[].stall_timeout` overrides that value for a specific agent.
- `hooks.after_create`, `hooks.before_run`, and `hooks.after_run` run as shell commands with the timeout from `hooks.timeout`.
- `controls.before_work` is a Maestro-managed operator gate after claim/workspace prep and before the agent starts.
- Operator replies to `before_work` are injected into the run prompt as structured operator guidance.
- Hook commands receive `MAESTRO_RUN_ID`, `MAESTRO_ISSUE_ID`, `MAESTRO_ISSUE_IDENTIFIER`, `MAESTRO_AGENT_NAME`, `MAESTRO_AGENT_TYPE`, `MAESTRO_RUN_STAGE`, `MAESTRO_RUN_STATUS`, and `MAESTRO_WORKSPACE_PATH`.
- `hooks.before_remove` is reserved in config but not implemented yet.
- `maestro inspect runs` gives a run-centric view over active, retry, and finished items.
- `maestro inspect runs` and `maestro inspect state` now include per-source health rollups and the latest sanitized error.
- `maestro reset issue <id-or-identifier>` removes that issue from local terminal/retry/approval state and can remove its workspace.
- `maestro cleanup workspaces` removes non-active workspaces under `workspace.root`.
- Multi-source configs keep separate state files under `state.dir/<source-name>/runs.json`.
- `gitlab-epic` sources now support separate `epic_filter` and `issue_filter` criteria.
- `gitlab-epic` sources can optionally target exact epic IIDs with `epic_filter.iids`.
- `source_defaults` and `agent_defaults` let you share tracker connection settings, common filters, and common agent runtime settings across large configs.
- `display_group` and `tags` let you organize source-heavy configs in the TUI without affecting dispatch behavior.
- `server.enabled` starts a local web/API surface on `server.host:server.port` with a built-in dashboard and approval actions.
- `agent_types[].communication` can route a run into a named channel such as Slack.
- Slack channels currently support DM or fixed-channel threads, approval buttons, typed replies for pending control prompts, and status updates.
- Slack requires a bot token plus an app-level token for Socket Mode. Use `channels[].config.token_env` and `channels[].config.app_token_env`.
- The TUI supports `tab` to switch focus between sources, active runs, retries, and approvals, `/` for search, `f` to cycle source groups, `u` for attention-only filtering, `w` for awaiting-approval filtering, `c` to clear filters, `o` and `O` to change sort order, and `v` to toggle compact mode.
- The source pane now includes a selected-source detail view with tracker, group, tags, poll stats, visible work counts, and recent source events.
- The active-runs pane now includes a selected-run detail view with source, issue, timestamps, approval state, workspace path, error context, and live stdout/stderr tails.
- The retries pane now shows queued reruns with due time, attempt number, and the last error.

## Web/API

The first web/API slice is local-first and intentionally small:

- `GET /healthz`
- `GET /api/v1/stream`
- `GET /api/v1/status`
- `GET /api/v1/config`
- `GET /api/v1/sources`
- `GET /api/v1/runs`
- `GET /api/v1/retries`
- `GET /api/v1/events`
- `GET /api/v1/approvals`
- `POST /api/v1/approvals/:request_id/approve`
- `POST /api/v1/approvals/:request_id/reject`

Enable it in config:

```yaml
server:
  enabled: true
  host: 127.0.0.1
  port: 8742
```

Then open [http://127.0.0.1:8742](http://127.0.0.1:8742). The built-in dashboard now consumes the resource endpoints directly, uses Server-Sent Events from `/api/v1/stream` for live refresh, defaults to a dark theme, and supports a browser-side light theme toggle plus filtering, sorting, and detail panes.

## Slack

Slack is now available as a communication channel for approval-driven runs.

Current scope:

- open a DM thread or use a fixed channel thread for a workflow
- post approval requests with `Approve` and `Reject` buttons
- post `controls.before_work` prompts and keep the original question intact in the thread
- accept typed Slack thread replies for pending Maestro control messages and runtime message requests
- post status updates for completion, failure, retry scheduling, and stops
- allow `Stop workflow` directly from the Slack thread

Current limits:

- no broad free-form agent chat surface yet; replies are routed into explicit pending Maestro controls or runtime message requests
- no Teams equivalent yet
- no live Slack workspace test in the default repo test matrix yet

Minimal agent/channel wiring:

```yaml
agent_types:
  - name: repo-maintainer
    approval_policy: manual
    communication: slack-dm

channels:
  - name: slack-dm
    kind: slack
    config:
      mode: dm
      token_env: MAESTRO_SLACK_BOT_TOKEN
      app_token_env: MAESTRO_SLACK_APP_TOKEN
      user_id_env: MAESTRO_SLACK_USER_ID
```

This requires a Slack app with:

- `chat:write`
- `im:write`
- `im:history`
- Socket Mode enabled with an app token that has `connections:write`
- Interactivity enabled
- Event Subscriptions enabled with bot event `message.im`
- the Messages tab setting that allows users to send messages to the app
- a reinstall after scope or event changes

## Docs

- Setup and first run: [docs/getting-started.md](docs/getting-started.md)
- Tracker behavior, including GitLab project issues vs epics: [docs/trackers.md](docs/trackers.md)
- Agent packs, context, tools, and prompt design: [docs/agents.md](docs/agents.md)
- Day-to-day operation: [docs/operator-guide.md](docs/operator-guide.md)
- Canonical demo flows: [docs/demo-walkthroughs.md](docs/demo-walkthroughs.md)
- Release checklist and packaging: [docs/release.md](docs/release.md)
- Test matrix: [TESTING.md](TESTING.md)
- Release notes: [CHANGELOG.md](CHANGELOG.md)
