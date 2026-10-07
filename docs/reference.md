# Maestro reference

Detailed configuration, CLI, and integration reference. Start with the [project overview](../README.md) or the [setup guide](getting-started.md).

## Config Reference

Every field available in `maestro.yaml`, derived from the config schema in `internal/config/types.go`.

```yaml
# ---------------------------------------------------------------------------
# Global defaults — apply to all sources unless overridden per-source.
# ---------------------------------------------------------------------------
defaults:
  poll_interval: 30s              # how often to poll each source
  max_concurrent_global: 5        # max agents running across ALL sources
  stall_timeout: 30m              # kill agent if no stdout/stderr for this long
  label_prefix: maestro           # prefix for lifecycle labels (maestro:active, maestro:done, etc.)

  # Default lifecycle transitions (overridable per-source).
  on_dispatch:                    # applied when an issue is dispatched to an agent
    state: "In Progress"          # change tracker issue state (Linear/GitLab)
    add_labels: []                # labels to add (overrides default maestro:active if set)
    remove_labels: []             # labels to remove (overrides default removal of retry/done/failed)
  on_complete:                    # applied when agent exits successfully
    state: "Done"                 # change tracker issue state
    add_labels: []                # labels to add (omit to let default maestro:done apply)
    remove_labels: []             # labels to remove
  on_failure:                     # applied when agent fails
    state: "Rework"
    add_labels: []
    remove_labels: []

# ---------------------------------------------------------------------------
# Harness model defaults — merged with per-agent overrides.
# ---------------------------------------------------------------------------
codex_defaults:                   # defaults for all codex agents
  model: gpt-5.4                  # (default)
  reasoning: high                 # (default)
  max_turns: 1                    # default; raise to enable continuation
  extra_args: []                  # additional CLI flags
  # thread_sandbox: workspaceWrite       # optional override (derived from approval_policy)
  # turn_sandbox_policy:                 # optional override (derived from approval_policy)
  #   type: dangerFullAccess

claude_defaults:                  # defaults for all claude-code agents
  model: claude-opus-4-6          # (default)
  reasoning: high                 # (default)
  max_turns: 1                    # default; raise to enable session-resumed continuation
  extra_args: []

# ---------------------------------------------------------------------------
# User identity — available in prompt templates as {{.User}}.
# ---------------------------------------------------------------------------
user:
  name: "Your Name"
  gitlab_username: "you"          # optional, for GitLab assignee filtering
  linear_username: "you@email"    # optional, for Linear assignee filtering

# ---------------------------------------------------------------------------
# Agent packs directory — where to find agent pack folders.
# ---------------------------------------------------------------------------
agent_packs_dir: agents           # relative to config file location

# ---------------------------------------------------------------------------
# Sources — each source polls one tracker with one filter and dispatches
#            to one agent type. Multiple sources enable workflow chaining.
# ---------------------------------------------------------------------------
sources:
  - name: my-source               # unique name, shown in TUI
    display_group: ""              # optional grouping for TUI display
    tags: []                       # optional tags for filtering

    tracker: linear                # "gitlab", "gitlab-epic", or "linear"
    label_prefix: maestro          # optional per-source lifecycle-label prefix override

    connection:
      base_url: https://gitlab.com # GitLab only: instance URL
      token_env: $LINEAR_API_KEY    # env var holding the API token
      project: "My Project"        # Linear: project name. GitLab: "group/project"
      group: ""                    # GitLab epic: group path
      team: ""                     # Linear: team ID (optional if project_url set)

    project_url: ""                # optional: project URL shown in TUI.
                                   # Linear: also used to resolve project by slug
                                   # (e.g., https://linear.app/team/project/slug/issues)

    repo: https://github.com/org/repo.git  # cloned for each workspace

    filter:
      states: [todo]               # issue states to match (case-insensitive)
      labels: []                   # required labels (all must match)
      assignee: ""                 # filter by assignee email/username (GitLab: any assignee may match)
      iids: []                     # GitLab only: specific issue IIDs

    # GitLab epic sources support separate filters for epics vs linked issues.
    epic_filter:                   # overrides filter for epic-level matching
      labels: []
      iids: []                     # target specific epic IIDs
    issue_filter:                  # overrides filter for linked child issues
      states: []
      assignee: ""

    agent_type: dev-codex          # which agent_type to dispatch
    max_active_runs: 3             # max concurrent runs for this source

    poll_interval: 10s             # override defaults.poll_interval
    stall_timeout: 2h              # override this source's agent inactivity timeout
    max_attempts: 3                # max retries before marking terminal
    retry_base: 30s                # initial retry delay
    max_retry_backoff: 10m         # max retry delay after exponential backoff
    respect_blockers: true         # skip dispatch while tracker-reported blockers are non-terminal

    # Per-source lifecycle transitions (override defaults).
    on_dispatch:
      state: "In Progress"
    on_complete:
      state: "Human Review"
      add_labels: []               # empty = no lifecycle label added (enables chaining)
    on_failure:
      state: "Rework"

# ---------------------------------------------------------------------------
# Agent types — define how an agent runs. Referenced by sources via agent_type.
# ---------------------------------------------------------------------------
agent_types:
  - name: dev-codex                # unique name, referenced by sources
    agent_pack: dev-codex          # pack directory under agent_packs_dir
    description: ""                # optional
    instance_name: dev-codex       # shown in prompts as {{.Agent.InstanceName}}
    harness: codex                 # "codex" or "claude-code"
    workspace: git-clone           # "git-clone" (clone repo) or "none" (empty dir)
    prompt: prompt.md              # path within pack, Go template
    approval_policy: auto          # "auto" or "manual"
    approval_timeout: 1h           # timeout for pending approvals
    communication: ""              # channel name (e.g., "slack-dm") for approval routing
    max_concurrent: 5              # max concurrent runs of this agent type
    stall_timeout: 30m             # override defaults.stall_timeout
    env: {}                        # extra env vars passed to the agent process
    tools: []                      # Codex: tools to inject
    skills: []                     # Codex: skills to inject
    context_files: [context.md]    # additional context files from pack dir

    # Harness-specific config (only one applies based on harness).
    codex:
      model: gpt-5.4
      reasoning: high
      max_turns: 1
      extra_args: []
      # thread_sandbox and turn_sandbox_policy are derived from approval_policy.
      # Only set these to decouple sandbox from approval behavior.
    claude:
      model: claude-opus-4-6
      reasoning: high
      extra_args: []

    # Optional: run only the harness process inside Docker while Maestro stays on the host.
    docker:
      image: ghcr.io/acme/maestro-claude@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
      image_pin_mode: require             # allow (default) or require
      workspace_mount_path: /workspace   # default: /workspace
      pull_policy: missing                # missing (default), always, or never
      network: bridge                    # bridge (default), none, or host
      cpus: 2
      memory: 4g
      pids_limit: 256
      auth:
        mode: claude-proxy
        source: ANTHROPIC_AUTH_TOKEN
      secrets:
        env:
          - preset: anthropic-base-url
        mounts:
          - preset: netrc
            source: ~/.netrc
      tools:
        mounts:
          - preset: git-config
            source: ~/.gitconfig
      security:
        preset: default                  # default, locked-down, or compat
        read_only_root_fs: true
        tmpfs: [/tmp]
      cache:
        profiles: [claude-cache]

# ---------------------------------------------------------------------------
# Source defaults — shared settings applied per-tracker-type to reduce
#                   repetition in multi-source configs.
# ---------------------------------------------------------------------------
source_defaults:
  gitlab:
    connection:
      base_url: https://gitlab.com
      token_env: $GITLAB_TOKEN
    repo: https://gitlab.com/group/project.git
    stall_timeout: 2h              # default for GitLab sources; source field wins
  linear:
    connection:
      token_env: $LINEAR_API_KEY
  gitlab_epic:
    connection:
      base_url: https://gitlab.com
      token_env: $GITLAB_TOKEN

# Agent defaults — shared settings for all agent types.
agent_defaults:
  harness: claude-code
  workspace: git-clone
  approval_policy: auto
  max_concurrent: 3
  stall_timeout: 30m

# Docker execution notes:
# - Maestro still polls trackers, prepares/reuses workspaces, renders prompts, routes approvals/messages,
#   and persists state on the host.
# - Only the harness process runs in the container.
# - Docker is selected per `agent_type`, so one Maestro instance can mix host-run and Docker-run agents
#   across different sources.
# - The prepared host workspace is bind-mounted into the container so git changes remain visible on the host.
# - Claude `approval_policy: manual` currently reruns an approved turn with `bypassPermissions`;
#   it does not enforce approval on every subsequent tool action. See FINDINGS.md (S2).
# - Prefer `docker.secrets` and `docker.tools` for explicit allowlists. Raw `docker.env_passthrough`
#   and `docker.mounts` remain supported for compatibility.
# - Docker defaults are hardened: no-new-privileges, read-only rootfs, cap-drop ALL, and tmpfs /tmp.
# - Named security presets are available under `docker.security.preset`:
#   `default` keeps the existing hardened baseline, `locked-down` adds `/var/tmp` tmpfs,
#   and `compat` keeps `no-new-privileges` but relaxes the read-only rootfs/cap-drop/tmpfs defaults.
# - Raw `docker.security.*` fields still override the selected preset field-by-field.
# - `maestro doctor` warns when a Docker image is not digest-pinned; set `docker.image_pin_mode: require`
#   to make digest pinning mandatory for a given agent type.
# - `docker.network` still supports coarse `bridge` / `none` / `host` modes.
# - `docker.network_policy` offers network modes and proxy routing:
#   `mode: none`, `mode: bridge`, or `mode: allowlist` with explicit allowed hosts/domains.
# - `mode: allowlist` routes proxy-aware HTTP/HTTPS clients through a Maestro-managed proxy.
#   It does not block direct sockets on Docker bridge; use `none` or an external firewall for isolation.
#   Conflicting proxy env overrides are rejected because Maestro owns those variables in that mode.
# - Do not mount your full home directory by default; prefer narrow `docker.secrets` / `docker.tools`
#   entries or minimal auth presets.
# - Claude can use direct API keys (`docker.auth.mode: claude-api-key`) or bearer-token proxy auth
#   (`docker.auth.mode: claude-proxy` plus ANTHROPIC_BASE_URL).
# - Codex can use mounted CLI auth or API-key auth (`docker.auth.mode: codex-api-key`).
#   For OpenAI-compatible proxies, pass OPENAI_API_KEY and set `openai_base_url` via `codex.extra_args`.
# - `maestro doctor` shows the effective Docker env injections and read-only mounts per agent.
# - Cache presets are available for common language/tool caches via `docker.cache.profiles`.
# - When no HOME is provided explicitly, Maestro gives the container a writable local HOME automatically.
# - Docker reuse is opt-in via `docker.reuse.mode`:
#   `none` keeps fresh containers, `stateless` reuses a trusted shared container for matching profiles,
#   and `lineage` reuses only within the same issue/workspace lineage.

# Example: Dockerized Claude agent in the same Maestro process as host-run agents
#
# agent_types:
#   - name: dev-claude-docker
#     agent_pack: dev-claude
#     harness: claude-code
#     workspace: git-clone
#     approval_policy: manual
#     claude:
#       model: claude-opus-4-6
#       reasoning: high
#     docker:
#       image: ghcr.io/acme/maestro-claude:latest
#       network: bridge
#       auth:
#         mode: claude-proxy
#         source: ANTHROPIC_AUTH_TOKEN
#       secrets:
#         env:
#           - preset: anthropic-base-url
#       cache:
#         profiles: [claude-cache]
#
# Example: Dockerized Codex agent with allowlisted HTTP/HTTPS egress
#
# agent_types:
#   - name: dev-codex-allowlist
#     agent_pack: dev-codex
#     harness: codex
#     workspace: git-clone
#     approval_policy: manual
#     docker:
#       image: ghcr.io/acme/maestro-codex@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
#       network_policy:
#         mode: allowlist
#         allow:
#           - api.openai.com
#           - "*.openai.com"
#       auth:
#         mode: codex-api-key
#         source: OPENAI_API_KEY
#       cache:
#         profiles: [codex-cache]
#
# Example: Dockerized Codex agent using an OpenAI-compatible proxy
#
# agent_types:
#   - name: dev-codex-docker
#     agent_pack: dev-codex
#     harness: codex
#     workspace: git-clone
#     approval_policy: manual
#     codex:
#       model: openai/gpt-5-mini
#       reasoning: high
#       extra_args:
#         - --config
#         - forced_login_method="api"
#         - --config
#         - openai_base_url="https://llm-proxy.example.com"
#     docker:
#       image: ghcr.io/acme/maestro-codex:latest
#       network: bridge
#       auth:
#         mode: codex-api-key
#         source: OPENAI_API_KEY
#       cache:
#         profiles: [codex-cache]

# ---------------------------------------------------------------------------
# Workspace — where cloned repos live.
# ---------------------------------------------------------------------------
workspace:
  root: ./var/workspaces           # workspaces created under this dir

# ---------------------------------------------------------------------------
# State — persistence for claimed issues, retries, and active runs.
# ---------------------------------------------------------------------------
state:
  dir: ./var/state                 # runs.json and run logs stored here
  retry_base: 30s                  # default retry delay (overridable per-source)
  max_retry_backoff: 10m           # max retry delay
  max_attempts: 3                  # max retries before terminal failure

# ---------------------------------------------------------------------------
# Hooks — shell commands run at workspace lifecycle points.
# ---------------------------------------------------------------------------
hooks:
  after_create: ""                 # runs after workspace is created (first time only)
  before_run: ""                   # runs before agent starts (every dispatch)
  after_run: ""                    # runs after agent exits (best-effort)
  timeout: 10m                     # hook execution timeout
  execution: host                 # "host" (default) or "container"

  # Hook env vars: MAESTRO_RUN_ID, MAESTRO_ISSUE_ID, MAESTRO_ISSUE_IDENTIFIER,
  # MAESTRO_AGENT_NAME, MAESTRO_AGENT_TYPE, MAESTRO_RUN_STAGE,
  # MAESTRO_RUN_STATUS, MAESTRO_WORKSPACE_PATH

# ---------------------------------------------------------------------------
# Controls — operator gates in the dispatch pipeline.
# ---------------------------------------------------------------------------
controls:
  before_work:
    enabled: false                 # pause after workspace prep, before agent starts
    mode: ""                       # "review" or "reply"; default is confirm/start
    prompt: ""                     # custom prompt shown to operator

# ---------------------------------------------------------------------------
# Channels — communication channels for approval routing.
# ---------------------------------------------------------------------------
channels:
  - name: slack-dm
    kind: slack                    # currently only "slack" is supported
    config:
      mode: dm                     # "dm" or "channel"
      token_env: $SLACK_BOT_TOKEN
      app_token_env: $SLACK_APP_TOKEN
      user_id_env: $SLACK_USER_ID
      channel_id: ""               # for mode: channel

# ---------------------------------------------------------------------------
# Server — web dashboard and API.
# ---------------------------------------------------------------------------
server:
  enabled: true
  host: 127.0.0.1
  port: 7777
  api_key: ""                     # optional; required for non-loopback binds

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------
logging:
  level: info                      # debug, info, warn, error
  dir: ./var/logs
  max_files: 20                    # rotated log file retention
```

## Agent Packs

Agent packs define how an agent behaves. Each pack is a directory with a prompt template and config:

```
agents/<pack-name>/
├── agent.yaml       # required: name, harness, workspace, approval_policy, prompt
├── prompt.md        # required: Go template rendered with issue context
├── context.md       # optional: operating context appended to prompt
└── context/         # optional: additional context files
```

### Prompt Templates

`prompt.md` uses Go `text/template` syntax. Available data:

| Variable | Description |
|----------|-------------|
| `{{.Issue.Identifier}}` | Issue ID (e.g., TAN-116, group/project#42) |
| `{{.Issue.Title}}` | Issue title |
| `{{.Issue.Description}}` | Issue body/description |
| `{{.Issue.State}}` | Current state (e.g., "todo", "in progress") |
| `{{.Issue.Labels}}` | Labels as string slice |
| `{{.Issue.URL}}` | Issue URL |
| `{{.User.Name}}` | Operator name from config |
| `{{.Agent.Name}}` | Agent type name |
| `{{.Agent.InstanceName}}` | Agent instance name |
| `{{.Source.Name}}` | Source name |
| `{{.Attempt}}` | Retry attempt number (0 = first run) |
| `{{.OperatorInstruction}}` | Operator guidance from before_work gate |

Template functions: `default`, `join`, `lower`, `upper`, `trim`, `contains`, `hasPrefix`, `indent`.

### Built-in Packs

| Pack | Harness | Description |
|------|---------|-------------|
| `dev-codex` | codex | Full implementation agent. Plans, codes, tests, creates PRs. Multi-turn. |
| `dev-claude` | claude-code | Same workflow as dev-codex with Claude Code session continuation. |
| `review-claude` | claude-code | Automated reviewer. Reviews PRs, runs tests, squash-merges passing work. |
| `code-pr` | claude-code | Lightweight code change agent. |
| `triage` | claude-code | Issue triage and labeling. |
| `repo-maintainer` | claude-code | Repository maintenance (deps, CI, docs). Manual approval. |
| `access-reviewer` | claude-code | Access and permission review. |
| `query-optimizer` | claude-code | SQL/query optimization. |
| `vuln-triage` | claude-code | Security vulnerability triage. |
| `demo-app-bootstrap` | claude-code | Demo app scaffolding. |

### Creating a Custom Pack

1. Create a directory under `agent_packs_dir`:
   ```bash
   mkdir -p agents/my-agent
   ```

2. Create `agent.yaml`:
   ```yaml
   name: my-agent
   description: What this agent does.
   harness: claude-code          # or codex
   workspace: git-clone          # or none
   prompt: prompt.md
   approval_policy: auto         # auto or manual
   max_concurrent: 3
   context_files:
     - context.md
   ```

3. Create `prompt.md` with the agent's instructions using template variables.

4. Reference it in `maestro.yaml`:
   ```yaml
   agent_types:
     - name: my-agent
       agent_pack: my-agent
   ```

## Lifecycle & Workflow Chaining

Maestro manages issue lifecycle through labels and state transitions:

**On dispatch** (default, no `on_dispatch` configured):
- Adds `{prefix}:active` label
- Removes `{prefix}:retry`, `{prefix}:done`, `{prefix}:failed`

**On success** (default, no `on_complete` configured):
- Removes `{prefix}:active`
- Adds `{prefix}:done`

**On failure** (default, no `on_failure` configured):
- Removes `{prefix}:active`
- Adds `{prefix}:failed`

When `on_dispatch`, `on_complete`, or `on_failure` is configured with explicit `add_labels`/`remove_labels`, **only those labels are applied** — the defaults are skipped.

### Chaining Example

Two sources forming a pipeline — implement then review:

```yaml
sources:
  - name: implement
    filter:
      states: [todo, rework]
    agent_type: dev-codex
    on_dispatch:
      state: "In Progress"
    on_complete:
      state: "Human Review"
      add_labels: []          # no maestro:done — allows review source to pick it up
    on_failure:
      state: "Rework"

  - name: review
    filter:
      states: [human review]
    agent_type: review-claude
    on_complete:
      state: "Done"           # terminal — pipeline ends here
    on_failure:
      state: "Rework"         # cycles back to implement source
```

## Workspace Management

Maestro creates isolated workspaces per issue under `workspace.root`:

- **Path**: `{workspace.root}/{encoded-issue-identifier}` (e.g., `var/workspaces/TAN-42`); punctuation and non-ASCII bytes are encoded to prevent different identifiers from sharing a path.
- **Branch**: `maestro/{encoded-agent-name}/{encoded-issue-identifier}`.
- **Reuse**: if a workspace exists from a previous run, Maestro verifies its Git origin, fetches latest, and checks out the agent branch. Agent's local commits are preserved across retries. An origin mismatch returns an error and preserves the directory.
- **Fallback**: corrupt repos are detected and re-cloned. Transient failures (network, auth) preserve the workspace and return an error.
- **Cross-instance**: if a different Maestro instance picks up the same issue, it does a fresh clone but checks out the existing remote branch — prior pushed work is preserved.

Workspaces created before the identifier encoding change are left in place. Preserve and inspect local work before moving it to the new path; see [FINDINGS.md](../FINDINGS.md) for the migration and rollback steps.

## CLI Commands

```bash
maestro --config maestro.yaml              # run with TUI
maestro --config maestro.yaml --no-tui     # run without TUI
maestro --config maestro.yaml --dry-run    # poll once, preview dispatch, render prompts, do not launch agents
maestro doctor --config maestro.yaml       # validate config, warn on route collisions, check harness binaries
maestro inspect config --config maestro.yaml         # dump resolved config
maestro inspect state --config maestro.yaml          # dump state (claimed, retries, finished)
maestro inspect runs --config maestro.yaml           # run-centric view
maestro reset issue --config maestro.yaml ISSUE-ID   # clear issue from state
maestro cleanup workspaces --config maestro.yaml     # remove non-active workspaces
```

## Run Logs

Agent stdout/stderr streams to private files during the run. Each stream is capped at 16 MiB, with a truncation marker if output exceeds the cap:

```
{state.dir}/runs/{run-id}/stdout.log
{state.dir}/runs/{run-id}/stderr.log
```

Review what an agent did: `cat var/state/runs/run-20260321-*/stdout.log`

In a multi-source configuration, run logs are under `{state.dir}/{source-name}/runs/`. Raw logs may contain anything printed by an agent, so keep the state directory accessible only to the Maestro account.

## TUI

The terminal UI shows live status with lipgloss-styled panels:

- **Header**: sources active, agents running, retries queued, next poll countdown, web URL
- **Sources**: health status, filter states, active/retry counts
- **Active Runs**: columnar table with issue, agent, status, age, idle time
- **Retry Queue**: queued retries with due time and error
- **Events**: last 5 log events

Operators can force an immediate poll from the runtime surfaces without waiting for the normal interval. Requests are debounced briefly and routed through the existing service loop so tracker polls stay serialized.

### Keybindings

| Key | Action |
|-----|--------|
| `tab` | Switch focus between panels |
| `j/k` | Navigate within focused panel |
| `p` | Force-poll the selected source (or the only source) |
| `P` | Force-poll all sources |
| `a` | Approve selected approval |
| `r` | Reject selected approval |
| `e` | Reply to selected message |
| `s` | Send "start" to selected message |
| `v` | Toggle compact/expanded view |
| `/` | Search |
| `f` | Cycle source group filter |
| `u` | Toggle attention-only filter |
| `w` | Toggle awaiting-approval filter |
| `o` | Cycle run sort order |
| `O` | Cycle retry sort order |
| `c` | Clear all filters |
| `q` | Quit |

## Web Dashboard & API

Enable in config:

```yaml
server:
  enabled: true
  host: 127.0.0.1
  port: 7777
  api_key: ""
```

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/healthz` | Health check |
| `GET` | `/api/v1/stream` | Server-Sent Events for live updates |
| `GET` | `/api/v1/status` | Current status snapshot |
| `GET` | `/api/v1/config` | Resolved config |
| `POST` | `/api/v1/poll` | Request an immediate poll for all sources |
| `GET` | `/api/v1/sources` | Source summaries |
| `POST` | `/api/v1/sources/:name/poll` | Request an immediate poll for one source |
| `GET` | `/api/v1/runs` | Active and recent runs |
| `GET` | `/api/v1/retries` | Retry queue |
| `GET` | `/api/v1/events` | Recent events |
| `GET` | `/api/v1/approvals` | Pending approvals |
| `POST` | `/api/v1/approvals/:id/approve` | Approve |
| `POST` | `/api/v1/approvals/:id/reject` | Reject |
| `GET` | `/api/v1/messages` | Pending message requests |
| `POST` | `/api/v1/messages/:id/reply` | Reply to message |
| `POST` | `/api/v1/runs/:id/stop` | Stop a run |
| `GET` | `/api/v1/config/raw` | Raw config YAML |
| `POST` | `/api/v1/config/validate` | Validate config |
| `POST` | `/api/v1/config/dry-run` | Dry-run config changes |
| `POST` | `/api/v1/config/save` | Save config changes |
| `GET` | `/api/v1/config/backups` | List config backups |
| `POST` | `/api/v1/config/backups/create` | Create config backup |
| `GET` | `/api/v1/config/backups/:id` | Get specific backup |
| `POST` | `/api/v1/packs/save` | Save agent pack changes |

Open `http://127.0.0.1:7777` for the built-in dashboard.

Run snapshots include optional per-run metrics when the harness provides them: token input/output totals, derived total tokens, duration, and throughput. The dashboard also shows process-lifetime aggregate token totals for the current Maestro instance, plus source and harness breakdowns. Source summaries also include the latest tracker rate-limit snapshot when the tracker exposes one.

The overview includes `Poll all now`, and each workflow page includes `Poll now`, both backed by the same debounced runtime poll request path used by the TUI and API. A successful force poll runs immediately, bypasses the normal poll interval, and resets the next scheduled poll window from that completed poll.

When the server binds to loopback (`127.0.0.1`, `localhost`, or `::1`), API auth is optional so local use stays frictionless. If you bind the server to any non-loopback host, Maestro requires an API key. Set `server.api_key` for a stable key, or let Maestro generate an ephemeral one at startup.

When auth is enabled, API clients must send `Authorization: Bearer <key>`. The built-in dashboard can be opened with `?api_key=<key>` once; it stores the key in session storage and removes it from the URL.

## Slack Authorization

Slack approvals and thread replies are operator-gated.

- DM mode authorizes the configured target user automatically via `user_id` or `user_id_env`
- fixed channel mode requires an explicit allowlist via `authorized_user_ids` or `authorized_user_ids_env`
- unauthorized Slack users cannot approve, reject, stop runs, or answer pending control messages

Example fixed-channel config:

```yaml
channels:
  - name: slack-review
    kind: slack
    config:
      mode: channel
      channel_id_env: $MAESTRO_SLACK_CHANNEL_ID
      token_env: $MAESTRO_SLACK_BOT_TOKEN
      app_token_env: $MAESTRO_SLACK_APP_TOKEN
      authorized_user_ids_env: $MAESTRO_SLACK_ALLOWED_USERS
```

## Trackers

### GitLab

Polls project issues. Supports label and state filtering, assignee filtering, and lifecycle label management.

```yaml
tracker: gitlab
connection:
  base_url: https://gitlab.com
  token_env: $GITLAB_TOKEN
  project: "group/project"
```

### GitLab Epic

Polls epics in a group, then dispatches linked child issues. Supports separate `epic_filter` and `issue_filter`.

```yaml
tracker: gitlab-epic
connection:
  base_url: https://gitlab.com
  token_env: $GITLAB_TOKEN
  group: "my-group"
```

### Linear

Polls project issues. Supports state filtering and lifecycle label management. State transitions are fully supported (resolves team workflow state IDs automatically).

```yaml
tracker: linear
connection:
  token_env: $LINEAR_API_KEY
project_url: https://linear.app/team/project/slug/issues   # resolves project by slug
# OR
connection:
  token_env: $LINEAR_API_KEY
  project: "Project Name"       # resolves project by name
```

## Examples

| Example | What it shows |
|---------|---------------|
| [maestro.yaml](../examples/maestro.yaml) | Minimal starter template |
| [gitlab-claude-auto.yaml](../examples/gitlab-claude-auto.yaml) | GitLab + Claude Code, simplest setup |
| [gitlab-codex-auto.yaml](../examples/gitlab-codex-auto.yaml) | GitLab + Codex |
| [gitlab-pipeline.yaml](../examples/gitlab-pipeline.yaml) | Workflow chaining: implement → review → merge |
| [gitlab-claude-slack-manual.yaml](../examples/gitlab-claude-slack-manual.yaml) | Slack approval flow |
| [gitlab-epic-claude-auto.yaml](../examples/gitlab-epic-claude-auto.yaml) | GitLab epic workflow |
| [linear-codex-auto.yaml](../examples/linear-codex-auto.yaml) | Linear + Codex |
| [multi-source-claude-auto.yaml](../examples/multi-source-claude-auto.yaml) | Multiple trackers in one config |
| [many-sources-claude-auto.yaml](../examples/many-sources-claude-auto.yaml) | Source/agent defaults for large configs |
| [swiftoot.yaml](../examples/swiftoot.yaml) | Linear + Codex/Claude pipeline: implement → review |

## Further Reading

- [Getting Started](getting-started.md) — setup and first run walkthrough
- [Trackers](trackers.md) — tracker behavior, GitLab epics, Linear details
- [Agents](agents.md) — agent packs, prompt design, context files
- [Operator Guide](operator-guide.md) — day-to-day operation, workspace management, troubleshooting
- [Demo Walkthroughs](demo-walkthroughs.md) — step-by-step demo flows
- [Testing](../TESTING.md) — test matrix and conventions
- [Future Improvements](future-improvements.md) — deferred features and roadmap ideas
