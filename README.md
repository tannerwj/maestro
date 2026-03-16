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

- No API server or web UI yet
- GitLab project issues and GitLab epic-backed child issues are supported
- Codex manual approval is wired in code but the current local app-server build does not emit approval requests in this environment
- Hook support is currently limited to `after_create`, `before_run`, and `after_run`
- Slack, API server, web UI, dynamic config reload, and tracker-specific completion workflows are still out of scope

## Quick Start

1. Copy a sample from [examples/gitlab-claude-auto.yaml](/Users/tjohnson/repos/maestro/examples/gitlab-claude-auto.yaml) or [examples/linear-claude-auto.yaml](/Users/tjohnson/repos/maestro/examples/linear-claude-auto.yaml).
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

- GitLab + Claude auto: [examples/gitlab-claude-auto.yaml](/Users/tjohnson/repos/maestro/examples/gitlab-claude-auto.yaml)
- GitLab + Claude manual: [examples/gitlab-claude-manual.yaml](/Users/tjohnson/repos/maestro/examples/gitlab-claude-manual.yaml)
- GitLab + Codex auto: [examples/gitlab-codex-auto.yaml](/Users/tjohnson/repos/maestro/examples/gitlab-codex-auto.yaml)
- GitLab epic + Claude auto: [examples/gitlab-epic-claude-auto.yaml](/Users/tjohnson/repos/maestro/examples/gitlab-epic-claude-auto.yaml)
- GitLab + repo-maintainer pack: [examples/gitlab-repo-maintainer.yaml](/Users/tjohnson/repos/maestro/examples/gitlab-repo-maintainer.yaml)
- Linear + Claude auto: [examples/linear-claude-auto.yaml](/Users/tjohnson/repos/maestro/examples/linear-claude-auto.yaml)
- Linear + Claude manual: [examples/linear-claude-manual.yaml](/Users/tjohnson/repos/maestro/examples/linear-claude-manual.yaml)
- Linear + Codex auto: [examples/linear-codex-auto.yaml](/Users/tjohnson/repos/maestro/examples/linear-codex-auto.yaml)
- Linear + triage pack: [examples/linear-triage.yaml](/Users/tjohnson/repos/maestro/examples/linear-triage.yaml)
- Multi-source GitLab project + GitLab epic + Linear: [examples/multi-source-claude-auto.yaml](/Users/tjohnson/repos/maestro/examples/multi-source-claude-auto.yaml)
- Many sources with shared defaults: [examples/many-sources-claude-auto.yaml](/Users/tjohnson/repos/maestro/examples/many-sources-claude-auto.yaml)

Built-in agent packs:

- Code change: [agents/code-pr/agent.yaml](/Users/tjohnson/repos/maestro/agents/code-pr/agent.yaml)
- Repo maintainer: [agents/repo-maintainer/agent.yaml](/Users/tjohnson/repos/maestro/agents/repo-maintainer/agent.yaml)
- Triage: [agents/triage/agent.yaml](/Users/tjohnson/repos/maestro/agents/triage/agent.yaml)

## Smoke Scripts

- GitLab smoke: [scripts/smoke_gitlab.sh](/Users/tjohnson/repos/maestro/scripts/smoke_gitlab.sh)
- Linear smoke: [scripts/smoke_linear.sh](/Users/tjohnson/repos/maestro/scripts/smoke_linear.sh)
- Three-source smoke: [scripts/smoke_multi_source.sh](/Users/tjohnson/repos/maestro/scripts/smoke_multi_source.sh)
- Many-sources smoke with fresh fixtures: [scripts/smoke_many_sources.sh](/Users/tjohnson/repos/maestro/scripts/smoke_many_sources.sh)

These scripts default to `approval_policy=auto`. They create a temporary config, run Maestro, wait for marker files in the workspace, and print the artifact paths.

## Demo Configs

- GitLab demo: [demo/gitlab-claude-auto/maestro.yaml](/Users/tjohnson/repos/maestro/demo/gitlab-claude-auto/maestro.yaml)
- Linear demo: [demo/linear-claude-auto/maestro.yaml](/Users/tjohnson/repos/maestro/demo/linear-claude-auto/maestro.yaml)

These keep logs, state, and workspaces under `demo/*/var/` so you can inspect and reset them easily.

## Operational Notes

- `defaults.stall_timeout` sets the inactivity window before Maestro stops a run and queues a retry.
- `agent_types[].stall_timeout` overrides that value for a specific agent.
- `hooks.after_create`, `hooks.before_run`, and `hooks.after_run` run as shell commands with the timeout from `hooks.timeout`.
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
- The TUI supports `/` for search, `f` to cycle source groups, and `c` to clear filters.

## Docs

- Setup and first run: [docs/getting-started.md](/Users/tjohnson/repos/maestro/docs/getting-started.md)
- Tracker behavior, including GitLab project issues vs epics: [docs/trackers.md](/Users/tjohnson/repos/maestro/docs/trackers.md)
- Agent packs, context, tools, and prompt design: [docs/agents.md](/Users/tjohnson/repos/maestro/docs/agents.md)
- Day-to-day operation: [docs/operator-guide.md](/Users/tjohnson/repos/maestro/docs/operator-guide.md)
- Canonical demo flows: [docs/demo-walkthroughs.md](/Users/tjohnson/repos/maestro/docs/demo-walkthroughs.md)
- Release checklist and packaging: [docs/release.md](/Users/tjohnson/repos/maestro/docs/release.md)
- Test matrix: [TESTING.md](/Users/tjohnson/repos/maestro/TESTING.md)
- Release notes: [CHANGELOG.md](/Users/tjohnson/repos/maestro/CHANGELOG.md)
