# Getting Started

## Prerequisites

- Go 1.26+
- `git`
- One authenticated harness:
  - `claude`
  - `codex`
- Model defaults are configurable via `codex_defaults` and `claude_defaults` in `maestro.yaml` — no need to set environment variables for model selection
- One tracker token:
  - GitLab personal access token for project issue polling
  - Linear API token for project issue polling
- Optional communication channel:
  - Slack bot token plus Slack app-level token for DM or channel-thread approvals/status
- Optional for containerized harness runs:
  - local `docker` client and reachable daemon/context
  - a container image that already contains `claude` or `codex`

## Docker-backed Harnesses

If you set `agent_types[].docker`, Maestro keeps orchestration on the host and runs only the
harness process in Docker. The prepared workspace is bind-mounted into the container, so edits stay
visible on the host immediately.

Common auth patterns:

- Claude direct API: `docker.auth.mode: claude-api-key`
- Claude proxy/gateway: `docker.auth.mode: claude-proxy` plus `docker.secrets.env: [{preset: anthropic-base-url}]` when the gateway is not the default Anthropic API
- Codex direct API: `docker.auth.mode: codex-api-key`
- Codex compatible gateway: `docker.auth.mode: codex-api-key` plus `codex.extra_args` for `forced_login_method="api"` and `openai_base_url="https://llm-proxy.example.com"`
- CLI subscription/config auth: use `docker.auth.mode: claude-config-mount` or `docker.auth.mode: codex-config-mount` with a minimal read-only auth/config source path

Maestro does not mount your full home directory by default. If you do not provide a `HOME`
explicitly, Maestro supplies a writable container-local `HOME` automatically.
Prefer `docker.secrets` and `docker.tools` for any extra env vars or read-only mounts you want the
container to see. `docker.env_passthrough` and `docker.mounts` still work, but they are the
broader compatibility path.

Docker reuse is opt-in. `docker.reuse.mode: none` keeps the current fresh-container behavior,
`stateless` reuses a trusted shared container for matching Docker profiles, and `lineage` reuses
only within the same issue/workspace lineage for retries and continuations. Use `stateless`
primarily for investigative or reporting agents; use `lineage` for coding flows that benefit from
preserved workspace/home/cache state.

## Minimal GitLab Setup

1. Create or choose a GitLab project with at least one open issue.
2. Add a filter label such as `agent:ready`.
3. Export your token:

```bash
export MAESTRO_GITLAB_TOKEN=...
```

4. Edit [examples/gitlab-claude-auto.yaml](../examples/gitlab-claude-auto.yaml) in place, or copy it and update `agent_packs_dir` to point to your packs. Set:
   - `agent_packs_dir` if you move the built-in packs
   - `user`
   - `sources[0].connection.base_url`
   - `sources[0].connection.project`
   - `sources[0].filter`
   - `sources[0].stall_timeout` if this workflow needs a different inactivity timeout

5. Run:

```bash
make run CONFIG=examples/gitlab-claude-auto.yaml
```

## Minimal GitLab Epic Setup

1. Create or choose a GitLab group or subgroup with epics enabled.
2. Create at least one open epic in that group.
3. Link one or more open project issues to that epic. Those linked issues are the actual work items Maestro will dispatch.
4. Export your token:

```bash
export MAESTRO_GITLAB_TOKEN=...
```

5. Edit [examples/gitlab-epic-claude-auto.yaml](../examples/gitlab-epic-claude-auto.yaml) in place, or copy it and update `agent_packs_dir` to point to your packs. Set:
   - `agent_packs_dir` if you move the built-in packs
   - `user`
   - `sources[0].connection.base_url`
   - `sources[0].connection.group`
   - `sources[0].repo` with a plain URL, not an embedded token
   - `sources[0].epic_filter`
     - optionally `sources[0].epic_filter.iids` if you want to pin the source to exact epic IIDs
   - `sources[0].issue_filter`
   - `sources[0].stall_timeout` if this workflow needs a different inactivity timeout

6. Run:

```bash
go run ./cmd/maestro run --config examples/gitlab-epic-claude-auto.yaml
```

If you want the local web/API surface too, add:

```yaml
server:
  enabled: true
  host: 127.0.0.1
  port: 8742
  api_key: ""
```

Then open [http://127.0.0.1:8742](http://127.0.0.1:8742).

## Minimal Linear Setup

1. Create or choose a Linear project with at least one open issue in the target state.
2. Export your token:

```bash
export MAESTRO_LINEAR_TOKEN=...
```

3. Edit [examples/linear-codex-auto.yaml](../examples/linear-codex-auto.yaml) in place, or copy it and update `agent_packs_dir` to point to your packs. Set:
   - `agent_packs_dir` if you move the built-in packs
   - `user`
   - `sources[0].connection.project` with the exact project name or GraphQL project ID, or use `sources[0].project_url` with a Linear project URL instead
   - `sources[0].repo`
   - `sources[0].filter`
   - `sources[0].stall_timeout` if this workflow needs a different inactivity timeout

Do not embed credentials directly in `repo` URLs. Use `connection.token_env` and let Maestro handle clone auth.

4. Run:

```bash
go run ./cmd/maestro run --config examples/linear-codex-auto.yaml
```

If you want to containerize only the harness process, see the Docker examples in
[docs/agents.md](agents.md#docker-execution). The working proxy-backed patterns are:

- Claude: `ANTHROPIC_AUTH_TOKEN` plus `ANTHROPIC_BASE_URL`
- Codex: `OPENAI_API_KEY` plus `codex.extra_args` for `forced_login_method="api"` and `openai_base_url="https://llm-proxy.example.com"`

## Multiple Sources In One Config

You can now define multiple `sources` and multiple `agent_types` in one config.

Current runtime rules:

- `sources[].max_active_runs` bounds how many active runs one source may hold at once
- `defaults.max_concurrent_global` bounds the total number of active runs across the process
- `agent_types[].max_concurrent` bounds runs for that agent type across all sources using it
- the effective concurrency for a source is the smallest of those three limits
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
That includes per-source retry policy fields like `retry_base`, `max_retry_backoff`, and
`max_attempts`, so you can set global state defaults and only override the workflows that need
different retry behavior.

For large configs, `display_group` and `tags` are useful optional source metadata for the TUI and status views.

Each source keeps its own local state under:

- `state.dir/<source-name>/runs.json`

## Lifecycle Transitions

Sources support `on_dispatch`, `on_complete`, and `on_failure` hooks that manipulate tracker labels
and state on each lifecycle event. This enables pipeline-style workflows where completing one source
feeds work into the next.

You can define these hooks globally under `defaults` and override them per source. Resolution order
is:

1. source hook override
2. `defaults.on_dispatch` / `defaults.on_complete` / `defaults.on_failure`
3. built-in behavior

Default behavior (no hooks configured):

- Dispatch: add `{prefix}:active`, remove `{prefix}:retry`/`done`/`failed`
- Complete: remove `{prefix}:active`, add `{prefix}:done`
- Failure: remove `{prefix}:active`, add `{prefix}:failed`
- Retry: remove `{prefix}:active`, add `{prefix}:retry`

The label prefix defaults to `maestro` and is configurable globally via `defaults.label_prefix` or
per source via `sources[].label_prefix`.

Important distinction:

- Exact reserved lifecycle labels are `{prefix}:active`, `{prefix}:done`, `{prefix}:failed`, and `{prefix}:retry`.
- Other labels in the same namespace such as `{prefix}:coding` or `{prefix}:review` are treated as normal routing labels and remain visible to source filters.
- In practice, this means you can route with `{prefix}:coding` while Maestro uses only `{prefix}:active` as the shared "currently claimed" marker during execution.
- `state` inside these hooks is best-effort tracker metadata only. Label transitions are the portable routing contract.

Pipeline example — three sources chained via label transitions:

```yaml
defaults:
  label_prefix: maestro
  on_failure:
    add_labels: [maestro:needs-attention]

sources:
  - name: coding
    filter:
      labels: [maestro:coding]
    agent_type: dev-codex
    on_complete:
      add_labels: [maestro:review]
      remove_labels: [maestro:coding]
    on_failure:
      add_labels: [maestro:coding-failed]
      remove_labels: [maestro:coding]

  - name: code-review
    filter:
      labels: [maestro:review]
    agent_type: code-reviewer
    on_complete:
      add_labels: [maestro:security-review]
      remove_labels: [maestro:review]
    on_failure:
      add_labels: [maestro:review-failed]
      remove_labels: [maestro:review]

  - name: security-review
    filter:
      labels: [maestro:security-review]
    agent_type: security-reviewer
    on_complete:
      remove_labels: [maestro:security-review]
    on_failure:
      add_labels: [maestro:security-failed]
      remove_labels: [maestro:security-review]
```

When `coding` completes, it removes the `maestro:coding` label and adds `maestro:review`, which causes the
issue to match the `code-review` source on the next poll. Each stage hands off to the next via
labels. In this example, every source inherits the global `on_failure` behavior unless it
overrides that hook locally.

## Harness Configuration

Model, reasoning effort, and other harness-specific settings are configurable at two levels:

1. **Top-level defaults** apply to all agents using that harness:

```yaml
codex_defaults:
  model: gpt-5.4
  reasoning: high
  max_turns: 1

claude_defaults:
  model: claude-opus-4-6
  reasoning: high
  max_turns: 1
```

2. **Per-agent overrides** win over defaults:

```yaml
agent_types:
  - name: dev-codex
    harness: codex
    agent_pack: dev-codex
    codex:
      max_turns: 30
      reasoning: medium
```

Codex and Claude Code both support multi-turn execution. Between turns, Maestro sends a
continuation prompt with refreshed issue state so the agent can react to tracker changes
mid-session. Claude continuation resumes the saved Claude session with `--resume`.

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
maestro doctor --config /path/to/maestro.yaml
maestro inspect runs --config /path/to/maestro.yaml
maestro reset issue --config /path/to/maestro.yaml group/project#123
maestro cleanup workspaces --config /path/to/maestro.yaml --dry-run
```

`maestro doctor` validates the config, warns about likely source-route collisions and unpinned Docker images, and checks that all required harness binaries (`claude`, `codex`) are available in `PATH`.

`inspect runs` and `inspect state` include per-source health summaries so you can tell at a glance which source is active, retrying, degraded, or idle.

## Manual Approval

Manual approval requests can be routed through the TUI, but Claude's current adapter resumes an approved turn with `bypassPermissions`. Approval does not gate every later tool action in that turn. Do not use it as a per-action security boundary; see [finding S2](../FINDINGS.md). For a workflow demonstration, use one of the `*-manual.yaml` samples and run with the TUI enabled:

- `tab` switches focus between sources, active runs, retries, and approvals
- `a` approves the first pending request
- `r` rejects the first pending request
- `/` enters source search mode
- `f` cycles source-group filters
- `u` toggles the attention-only filter
- `w` toggles the awaiting-approval filter
- `c` clears source filters
- `o` cycles active-run sort order
- `O` cycles retry sort order
- `v` toggles compact mode
- `q` exits the TUI

The source pane now supports drill-down inspection of one source at a time, including poll stats and recent source events. The active-runs pane supports per-run inspection with live stdout/stderr tails, and the retries pane shows queued reruns with due times and prior errors. Attention and awaiting-approval quick filters, plus sort controls and compact mode, make it easier to scan large multi-source configs without losing access to the full detail panes.

## Slack Approval Setup

If you want approval prompts outside the terminal, start from [examples/gitlab-claude-slack-manual.yaml](../examples/gitlab-claude-slack-manual.yaml).

Required environment variables:

- `MAESTRO_SLACK_BOT_TOKEN`
- `MAESTRO_SLACK_APP_TOKEN`
- `MAESTRO_SLACK_USER_ID`

Minimal config shape:

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
      token_env: $MAESTRO_SLACK_BOT_TOKEN
      app_token_env: $MAESTRO_SLACK_APP_TOKEN
      user_id_env: $MAESTRO_SLACK_USER_ID
```

DM mode authorizes that configured Slack user automatically. If you use a fixed channel instead of a
DM, add an explicit allowlist with `authorized_user_ids` or `authorized_user_ids_env`. Unauthorized
users cannot approve, reject, stop runs, or answer pending control messages.

If you want Slack to ask a plain question and wait for a typed reply instead of showing `Approve` / `Reject`, enable:

```yaml
controls:
  before_work:
    enabled: true
    mode: reply
    prompt: "Ask the operator for the missing detail before work starts."
```

Current Slack behavior:

- starts a DM thread or fixed-channel thread for the matching workflow
- posts approval requests with buttons
- posts `controls.before_work` prompts and preserves the original question in the thread
- accepts typed Slack thread replies for pending Maestro control messages
- posts completion, failure, retry, and stop updates
- allows `Stop workflow` from the Slack thread

Current Slack limits:

- no broad free-form agent chat surface yet; replies are routed into explicit pending control messages
- no built-in hot reload of Slack channel config

Current runtime reload behavior:

- `maestro.yaml` and local agent pack edits hot-reload automatically
- invalid reloads are rejected and the last valid runtime stays in place
- removed or changed sources drain cleanly instead of killing active runs
- Slack bridge/channel wiring still follows the process-start config

Slack app checklist:

- enable Socket Mode
- create an app-level token with `connections:write`
- add bot scopes:
  - `chat:write`
  - `im:write`
  - `im:history`
- enable Interactivity
- enable Event Subscriptions
- subscribe to bot event `message.im`
- enable the Messages tab setting that allows users to send messages to the app
- reinstall the app after changing scopes or subscriptions

## Local Web/API

When `server.enabled` is true, Maestro serves a dashboard and API, including:

- a built-in dashboard at `/`
- `GET /healthz`
- `GET /api/v1/stream`
- `GET /api/v1/status`
- `GET /api/v1/config`
- `GET /api/v1/sources`
- `GET /api/v1/runs`
- `GET /api/v1/retries`
- `GET /api/v1/events`
- `GET /api/v1/approvals`
- `POST /api/v1/approvals/<request_id>/approve`
- `POST /api/v1/approvals/<request_id>/reject`

Bind it to `127.0.0.1` unless you have a specific reason to expose it more widely. Loopback binds do not require API auth. If you expose the server on a non-loopback host, set `server.api_key` or let Maestro generate an ephemeral one at startup. API clients then use `Authorization: Bearer <key>`, and the dashboard can be opened once with `?api_key=<key>` so it can store the key in session storage. The built-in dashboard uses Server-Sent Events from `/api/v1/stream` for live updates, defaults to dark theme, and includes a light theme toggle along with browser-side filtering and sorting controls.

For remote access, put a trusted TLS proxy in front of the API and protect the key. The bearer key grants access to config and control endpoints as well as status data; see [finding S7](../FINDINGS.md).

For Codex, the config path exists, but the current local app-server build did not emit approval requests during live validation on March 15, 2026.

## First Demo Path

For the cleanest first demo, use:

- GitLab + Claude auto, or
- Linear + Codex auto

Those are the least surprising paths and have full live smoke coverage.

## Agent Packs

The shipped configs now use `agent_pack` plus `agent_packs_dir`.

That lets you:

- reuse a default prompt and context bundle
- publish agent-specific tools and skills metadata
- override only the per-environment pieces in the live config

Pack examples live under:

- [agents/code-pr/agent.yaml](../agents/code-pr/agent.yaml)
- [agents/dev-claude/agent.yaml](../agents/dev-claude/agent.yaml)
- [agents/dev-codex/agent.yaml](../agents/dev-codex/agent.yaml)
- [agents/repo-maintainer/agent.yaml](../agents/repo-maintainer/agent.yaml)
- [agents/review-claude/agent.yaml](../agents/review-claude/agent.yaml)
- [agents/triage/agent.yaml](../agents/triage/agent.yaml)
- [agents/access-reviewer/agent.yaml](../agents/access-reviewer/agent.yaml)
- [agents/query-optimizer/agent.yaml](../agents/query-optimizer/agent.yaml)
- [agents/vuln-triage/agent.yaml](../agents/vuln-triage/agent.yaml)
- [agents/demo-app-bootstrap/agent.yaml](../agents/demo-app-bootstrap/agent.yaml)

## Hooks And Stall Detection

The current build supports these hook phases:

- shell hooks:
- `hooks.after_create`
- `hooks.before_run`
- `hooks.after_run`

By default hooks run through the host shell and share `hooks.timeout`. If you
set `hooks.execution: container`, Maestro runs the hook command in the same
Docker environment as the harness for that agent type.

Hook commands receive:

- `MAESTRO_RUN_ID`
- `MAESTRO_ISSUE_ID`
- `MAESTRO_ISSUE_IDENTIFIER`
- `MAESTRO_AGENT_NAME`
- `MAESTRO_AGENT_TYPE`
- `MAESTRO_RUN_STAGE`
- `MAESTRO_RUN_STATUS`
- `MAESTRO_WORKSPACE_PATH`

`defaults.stall_timeout` sets the inactivity timeout for runs. `agent_types[].stall_timeout` overrides it per agent; `sources[].stall_timeout` overrides both for one workflow. `source_defaults.<tracker>.stall_timeout` can supply a tracker-wide value when the source leaves it unset. The effective value must be positive.

Agent processes do not inherit the full parent shell environment. Maestro passes a curated runtime
baseline such as `PATH`, `HOME`, locale/XDG/temp vars, and common proxy/cert vars, then applies any
explicit `agent_types[].env` entries on top. If an agent needs an extra variable, add it to the
agent config explicitly.

`hooks.before_remove` is reserved in the config but is not implemented yet.

Maestro control points are separate from shell hooks. The first one is:

- `controls.before_work`

`before_work` pauses the workflow after claim/workspace prep and before the agent starts. The operator can reply with `start`, add instructions, or stop the run from the TUI, web UI, or Slack if a communication channel is configured.
