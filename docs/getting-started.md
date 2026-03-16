# Getting Started

## Prerequisites

- Go 1.24+
- `git`
- One authenticated harness:
  - `claude`
  - `codex`
- One tracker token:
  - GitLab personal access token for project issue polling
  - Linear API token for project issue polling

## Minimal GitLab Setup

1. Create or choose a GitLab project with at least one open issue.
2. Add a filter label such as `agent:ready`.
3. Export your token:

```bash
export MAESTRO_GITLAB_TOKEN=...
```

4. Copy [examples/gitlab-claude-auto.yaml](../examples/gitlab-claude-auto.yaml) and update:
   - `agent_packs_dir` if you move the built-in packs
   - `user`
   - `sources[0].connection.base_url`
   - `sources[0].connection.project`
   - `sources[0].filter`
   - `defaults.stall_timeout` or `agent_types[0].stall_timeout` if you want a different inactivity timeout

5. Run:

```bash
make run CONFIG=demo/gitlab-claude-auto/maestro.yaml
```

## Minimal GitLab Epic Setup

1. Create or choose a GitLab group or subgroup with epics enabled.
2. Create at least one open epic in that group.
3. Link one or more open project issues to that epic. Those linked issues are the actual work items Maestro will dispatch.
4. Export your token:

```bash
export MAESTRO_GITLAB_TOKEN=...
```

5. Copy [examples/gitlab-epic-claude-auto.yaml](../examples/gitlab-epic-claude-auto.yaml) and update:
   - `agent_packs_dir` if you move the built-in packs
   - `user`
   - `sources[0].connection.base_url`
   - `sources[0].connection.group`
   - `sources[0].repo` with a plain URL, not an embedded token
   - `sources[0].epic_filter`
     - optionally `sources[0].epic_filter.iids` if you want to pin the source to exact epic IIDs
   - `sources[0].issue_filter`
   - `defaults.stall_timeout` or `agent_types[0].stall_timeout` if you want a different inactivity timeout

6. Run:

```bash
go run ./cmd/maestro run --config /path/to/maestro.yaml
```

## Minimal Linear Setup

1. Create or choose a Linear project with at least one open issue in the target state.
2. Export your token:

```bash
export MAESTRO_LINEAR_TOKEN=...
```

3. Copy [examples/linear-claude-auto.yaml](../examples/linear-claude-auto.yaml) and update:
   - `agent_packs_dir` if you move the built-in packs
   - `user`
   - `sources[0].connection.project` with the exact project name or GraphQL project ID
   - `sources[0].repo`
   - `sources[0].filter`
   - `defaults.stall_timeout` or `agent_types[0].stall_timeout` if you want a different inactivity timeout

Do not embed credentials directly in `repo` URLs. Use `connection.token_env` and let Maestro handle clone auth.

4. Run:

```bash
go run ./cmd/maestro run --config /path/to/maestro.yaml
```

## Multiple Sources In One Config

You can now define multiple `sources` and multiple `agent_types` in one config.

Current runtime rules:

- `defaults.max_concurrent_global` bounds the total number of active runs across the process
- `agent_types[].max_concurrent` bounds runs for that agent type across all sources using it
- the shipped multi-source sample starts at `max_concurrent_global: 3` and `max_concurrent: 2` per agent type

That means multi-source configs are useful for:

- tracking several GitLab epics with different filters
- mixing GitLab and Linear intake in one daemon
- routing different sources to different agent packs or harnesses

Canonical example:

- [examples/multi-source-claude-auto.yaml](../examples/multi-source-claude-auto.yaml)
- [examples/many-sources-claude-auto.yaml](../examples/many-sources-claude-auto.yaml)

For larger configs, prefer:

- `source_defaults.gitlab`
- `source_defaults.gitlab_epic`
- `source_defaults.linear`
- `agent_defaults`

Those defaults fill missing fields on each source or agent type without overriding explicit entries.

For large configs, `display_group` and `tags` are useful optional source metadata for the TUI and status views.

Each source keeps its own local state under:

- `state.dir/<source-name>/runs.json`

## Build And Install

From the repo root:

```bash
make test
make build
make install
maestro version
```

The `Makefile` injects a build version from `git describe` when available.

To produce release archives:

```bash
make release VERSION=v0.1.0
```

Useful local operator commands after install:

```bash
maestro inspect runs --config /path/to/maestro.yaml
maestro reset issue --config /path/to/maestro.yaml group/project#123
maestro cleanup workspaces --config /path/to/maestro.yaml --dry-run
```

`inspect runs` and `inspect state` include per-source health summaries so you can tell at a glance which source is active, retrying, degraded, or idle.

## Manual Approval

Manual approval is now supported for Claude. Use one of the `*-manual.yaml` samples and run with the TUI enabled so you can approve or reject requests:

- `a` approves the first pending request
- `r` rejects the first pending request
- `/` enters source search mode
- `f` cycles source-group filters
- `c` clears source filters
- `q` exits the TUI

For Codex, the config path exists, but the current local app-server build did not emit approval requests during live validation on March 15, 2026.

## First Demo Path

For the cleanest first demo, use:

- GitLab + Claude auto, or
- Linear + Claude auto

Those are the least surprising paths and have full live smoke coverage.

## Agent Packs

The shipped configs now use `agent_pack` plus `agent_packs_dir`.

That lets you:

- reuse a default prompt and context bundle
- publish agent-specific tools and skills metadata
- override only the per-environment pieces in the live config

Pack examples live under:

- [agents/code-pr/agent.yaml](../agents/code-pr/agent.yaml)
- [agents/repo-maintainer/agent.yaml](../agents/repo-maintainer/agent.yaml)
- [agents/triage/agent.yaml](../agents/triage/agent.yaml)

## Hooks And Stall Detection

The current build supports these hook phases:

- `hooks.after_create`
- `hooks.before_run`
- `hooks.after_run`

All hooks run through the local shell and share `hooks.timeout`.

Hook commands receive:

- `MAESTRO_RUN_ID`
- `MAESTRO_ISSUE_ID`
- `MAESTRO_ISSUE_IDENTIFIER`
- `MAESTRO_AGENT_NAME`
- `MAESTRO_AGENT_TYPE`
- `MAESTRO_RUN_STAGE`
- `MAESTRO_RUN_STATUS`
- `MAESTRO_WORKSPACE_PATH`

`defaults.stall_timeout` sets the inactivity timeout for runs. You can override it per agent with `agent_types[].stall_timeout`.

`hooks.before_remove` is reserved in the config but is not implemented yet.
