# Configuration reference

An annotated configuration example for `maestro.yaml`. Start with the [project overview](../README.md) or the [setup guide](getting-started.md).

## Config Reference

A broad annotated YAML example for `maestro.yaml`. The current schema and validation live in [`internal/config/types.go`](../internal/config/types.go) and [`internal/config/validate.go`](../internal/config/validate.go); use `maestro doctor` to check a real config.

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
  api_key: ""                     # optional; non-loopback binds generate an ephemeral key if empty

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------
logging:
  level: info                      # debug, info, warn, error
  dir: ./var/logs
  max_files: 20                    # rotated log file retention
```

## Related guides

- [Getting Started](getting-started.md) for a first tracker and agent setup
- [Agents](agents.md) for packs, prompt templates, Docker, and harness configuration
- [Trackers](trackers.md) for GitLab and Linear behavior
- [Operator Guide](operator-guide.md) for CLI commands, lifecycle routing, workspaces, the dashboard, and recovery
- [Testing](../TESTING.md) for local and live verification
