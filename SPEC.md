# Maestro Service Specification

Status: Draft v1

Purpose: Define a service that orchestrates AI coding and operations agents across multiple work
sources, agent harnesses, and communication channels — on behalf of individual users.

## 1. Problem Statement

Maestro is a long-running daemon that continuously reads work from one or more issue trackers
(GitLab projects, GitLab epics, Linear projects), matches work items to specialized agent types,
provisions an appropriate workspace, and runs an agent session using the user's preferred harness
(Claude Code, Codex, or future harnesses).

The service solves six operational problems:

- It unifies work intake across multiple trackers and project management tools into a single
  orchestration loop, enabling users who work across GitLab and Linear (or future trackers) to
  manage agents from one place.
- It supports multiple agent harnesses (Claude Code, Codex) so teams can use the tools they prefer
  without being locked into a single vendor.
- It enables human-in-the-loop communication via the user's preferred channel (Slack DM, Teams DM,
  GitLab issue comments, TUI, or GUI), allowing agents to ask questions, request approval, and
  report status without requiring the user to watch a terminal.
- It supports diverse work types beyond code PRs — operations tasks (firewall rules, config
  changes), triage and investigation, and documentation — each with their own agent type definition
  including tools, MCPs, prompts, and security policies.
- It acts on behalf of individual users (using their credentials), not as a generic bot, preserving
  attribution and access control while still tracking which agent instance performed each action.
- It provides both TUI and web GUI interfaces for observability, agent management, and approval
  workflows.

Important boundaries:

- Maestro is a scheduler/runner, tracker reader, and approval router.
- Maestro may perform limited tracker writes for orchestration lifecycle purposes only: lifecycle
  labels and operational/audit comments needed to manage runs.
- Task-progress writes remain agent-owned: work documentation comments, merge request creation,
  branch links, task state transitions, and any domain-specific tracker edits are performed by the
  agent using tools available in the agent's configured environment.
- A successful run may end at a workflow-defined handoff state (for example `Human Review` or
  setting a label), not necessarily closing the issue.
- Maestro does not implement business logic for how to edit tickets, create MRs, or manage
  infrastructure. That logic lives in agent type definitions (prompts, tools, MCPs).

## 2. Goals and Non-Goals

### 2.1 Goals

- Poll multiple work sources (GitLab projects, GitLab epics, Linear projects) on configurable
  cadences and dispatch work with bounded concurrency.
- Route work items to the correct agent type based on source configuration, labels, assignees, and
  other filter criteria.
- Support multiple agent harnesses (Claude Code, Codex) with a clean integration contract that
  allows adding future harnesses.
- Support multiple communication channels (Slack, Teams, GitLab comments, TUI, GUI) for
  agent-to-human messaging and approval routing.
- Act on behalf of individual users using their credentials, with configurable identity per agent
  type.
- Provision appropriate workspaces per agent type (git clone, empty directory, or no workspace).
- Intercept and route agent approval requests to the user's preferred channel.
- Provide named agent instances (user-defined names for singletons, auto-generated for N-of) for
  easy identification in communication channels.
- Recover from transient failures with exponential backoff.
- Expose operator-visible observability via TUI (bubbletea), web GUI (React), and structured logs.
- Support restart recovery using a lightweight local state file for run metadata while keeping the
  tracker as the source of truth for work state.
- Build cross-platform: macOS (primary), Windows, Linux (containerization).

### 2.2 Non-Goals

- General-purpose workflow engine or distributed job scheduler.
- Multi-tenant SaaS control plane. Maestro runs as a single user's daemon.
- Built-in business logic for how to edit tickets, create MRs, manage infrastructure, or write
  firewall rules. That logic lives in agent type prompts and tools.
- Persistent state database. The source of truth is the trackers and the agent's documented progress
  in issues. v1 may persist lightweight local recovery metadata such as active runs and retry
  schedules.
- Inbound HTTP webhook-driven dispatch. Polling is the primary intake mechanism for v1.
- Running agents on remote hosts (SSH workers). Agents run on the same machine as the maestro
  daemon for v1.
- Real-time streaming of agent output to the GUI/TUI. Status updates are polled or event-driven at
  a summary level, not token-by-token.

## 3. System Overview

### 3.1 Main Components

1. `Config Loader`
   - Reads `maestro.yaml` from the XDG config directory.
   - Parses and validates the full configuration: sources, agent types, channels, defaults.
   - Resolves environment variable references for secrets.
   - Returns typed configuration used by all other components.

2. `Work Source Registry`
   - Maintains a set of configured tracker adapters, one per source entry in config.
   - Each source has its own poll interval, filter criteria, and agent type routing.
   - Sources are independent — a GitLab source failing does not block a Linear source.

3. `Tracker Adapters` (pluggable)
   - GitLab adapter: REST/GraphQL API for projects and epics, filtering by labels and assignees.
   - Linear adapter: GraphQL API for projects, filtering by states and labels.
   - Each adapter normalizes tracker-specific data into the shared Issue model.
   - Future adapters (Jira, GitHub Issues) implement the same contract.

4. `Orchestrator`
   - Owns the poll-dispatch-reconcile loop.
   - Owns in-memory runtime state (active runs, claimed issues, retry queue).
   - Routes work items from sources to agent types.
   - Manages concurrency limits (global and per-agent-type).
   - Performs reconciliation: stops agents when issues move to terminal states.

5. `Agent Type Registry`
   - Loaded from configuration. Each agent type defines:
     - Which harness to use (claude-code, codex).
     - Workspace strategy (git-clone, none).
     - Tools, MCPs, and environment available to the agent.
     - Prompt template.
     - Approval policy (auto, manual, destructive-only).
     - Communication channel preference.
     - Concurrency limits.
     - Credential strategy (user credentials, service account, read-only).

6. `Harness Adapters` (pluggable)
   - Claude Code adapter: spawns `claude` subprocess, manages lifecycle, intercepts approval
     requests via hooks.
   - Codex adapter: spawns `codex app-server` subprocess, manages JSON-RPC protocol over stdio.
   - Each adapter normalizes harness-specific events into the shared Run model.

7. `Workspace Manager`
   - Provisions workspaces based on agent type strategy.
   - Git-clone strategy: clones repo, creates branch, manages cleanup.
   - None strategy: provides a temporary directory or no directory at all.
   - Runs lifecycle hooks (after_create, before_run, after_run, before_remove).

8. `Communication Bus`
   - Routes messages between agents and their maestro (the human user).
   - Each active agent has a dedicated communication thread (Slack DM thread, Teams conversation,
     GitLab issue comment chain).
   - Messages include context: agent name, issue link, executive summary, the actual
     question/status.

9. `Approval Gateway`
   - Intercepts approval requests from agent harnesses.
   - Routes to the user via their configured channel.
   - Returns the user's response back to the agent.
   - Optional per agent type — some agent types run fully autonomously.

10. `Agent Namer`
    - Assigns names to agent instances based on configuration.
    - Singleton agents: use `instance_name` when set, otherwise `name` (e.g., "phoenix").
    - N-of agents: use `instance_name` or `name` as prefix with auto-generated suffix (e.g.,
      "coder-a3f1").

11. `TUI` (bubbletea/lipgloss)
    - Dashboard: active agents, status, recent events.
    - Agent detail view: logs, current activity, approval prompts.
    - Inline approval: approve/reject directly from the terminal.

12. `API Server` (REST + WebSocket)
    - Exposes orchestrator state for the web GUI.
    - Endpoints for agent status, source status, approval queue, configuration.
    - WebSocket for live updates.

13. `Logging`
    - Structured logs with context (agent_name, issue_id, source_name, harness).
    - Configurable sinks (file, stdout).

### 3.2 Abstraction Layers

1. `Configuration Layer`
   - Parses maestro.yaml into typed runtime settings.
   - Handles defaults, environment variable resolution, and path normalization.
   - Watches for config file changes and applies them dynamically.

2. `Integration Layer` (tracker + channel adapters)
   - API calls and normalization for tracker data (GitLab, Linear).
   - API calls for communication channels (Slack, Teams).
   - Each adapter is a standalone implementation of a shared interface.

3. `Coordination Layer` (orchestrator)
   - Polling loop, work routing, agent type matching, concurrency, retries, reconciliation.

4. `Execution Layer` (harness + workspace)
   - Workspace provisioning, agent subprocess lifecycle, approval interception.

5. `Presentation Layer` (TUI + API + GUI)
   - Operator visibility into orchestrator and agent behavior.

### 3.3 External Dependencies

- Issue tracker APIs: GitLab REST/GraphQL API, Linear GraphQL API.
- Communication APIs: Slack Web API + Socket Mode, Microsoft Teams Bot Framework / webhooks.
- Agent harness executables: `claude` CLI (Claude Code), `codex` CLI (Codex).
- Local filesystem for workspaces and logs.
- Git CLI for workspace provisioning (when using git-clone strategy).
- Host environment credentials for trackers, channels, and agent harnesses.

### 3.4 Shared HTTP Client

All HTTP-based integrations should use a shared Maestro HTTP client wrapper rather than each adapter
implementing retry, auth, and logging independently.

Responsibilities:

- **Retry with backoff**:
  - Retry transient failures such as transport errors and HTTP `5xx` responses.
  - Use bounded exponential backoff with jitter.
  - Do not retry most `4xx` responses except where the upstream API explicitly indicates a
    retryable condition such as `429 Too Many Requests`.
- **Rate limit handling**:
  - Parse upstream rate-limit headers where available.
  - Respect explicit retry timing such as `Retry-After`.
  - Surface rate-limit state to callers so source polling and channel delivery can back off without
    guessing.
- **Auth injection**:
  - Attach the correct authentication mechanism for the target integration based on resolved config:
    bearer token, private token header, webhook secret, or other adapter-specific auth.
  - Never log raw credentials.
- **Structured logging**:
  - Log request/response metadata with structured fields such as adapter kind, source name,
    operation, URL host, HTTP method, status code, duration, retry count, and rate-limit signals.
  - Request/response bodies are not logged by default. Sensitive headers must be redacted.

Behavioral rules:

- The shared HTTP client is a transport utility, not a policy engine. Adapters still decide whether
  a failed operation should fail the source tick, trigger message redelivery, or be surfaced to the
  approval gateway.
- Retry behavior must remain bounded so a single slow upstream does not block the orchestrator
  indefinitely.
- Auth configuration errors are treated as non-retryable validation failures.

## 4. Core Domain Model

### 4.1 Entities

#### 4.1.1 Issue

Normalized work item from any tracker.

Fields:

- `id` (string)
  - Maestro-internal composite ID: `<tracker_kind>:<external_id>` (e.g., `gitlab:12345`).
- `external_id` (string)
  - Tracker-native ID.
- `identifier` (string)
  - Human-readable key (e.g., `infra/firewall#42`, `ENG-123`).
- `tracker_kind` (string)
  - Source tracker type: `gitlab`, `linear`.
- `source_name` (string)
  - Name of the source configuration entry that produced this issue.
- `title` (string)
- `description` (string or null)
- `priority` (integer or null)
  - Lower numbers are higher priority in dispatch sorting.
- `state` (string)
  - Tracker-native state name, normalized to lowercase.
- `labels` (list of strings)
  - Normalized to lowercase.
- `assignee` (string or null)
  - Username or email of the assigned user.
- `url` (string or null)
  - Web URL to the issue in the tracker.
- `blocked_by` (list of blocker refs, optional)
  - Each ref: `{id, identifier, state}`.
- `created_at` (timestamp or null)
- `updated_at` (timestamp or null)
- `meta` (map of string to string)
  - Tracker-specific metadata not covered by the normalized fields.
  - Examples: GitLab project path, Linear team key, epic ID.

#### 4.1.2 Agent Type Definition

Configured agent archetype loaded from maestro.yaml.

Fields:

- `name` (string)
  - Identifier for this agent type (e.g., "firewall", "code-pr", "triage").
- `instance_name` (string, optional)
  - Human-facing agent instance name override.
  - If `max_concurrent` is `1`, this value is used directly as the singleton agent name.
  - If `max_concurrent` is greater than `1`, this value is used as the prefix for generated
    instance names.
  - Defaults to `name` when omitted.
- `harness` (string)
  - Which harness to use: `claude-code`, `codex`.
- `workspace` (WorkspaceStrategy)
  - `git-clone`: clone a repo and create a branch.
  - `none`: no workspace (ops/triage agents that use tools/APIs directly).
- `agent_pack` (string, optional)
  - Pack reference for agent defaults and harness-native config.
  - Bare names resolve under `agent_packs_dir`.
  - Path-like values resolve relative to the config file.
  - `repo:<path>` defers resolution until after clone and loads agent environment files from the
    cloned repository at `<path>`.
  - `repo:` with no path uses `.maestro/`.
- `tools` (list of strings, optional)
  - CLI tools available to the agent.
- `mcps` (list of strings, optional)
  - MCP server names available to the agent.
- `prompt` (string)
  - Path to the prompt template file, relative to config directory, for local packs and direct
    config.
  - For `agent_pack: repo:...`, prompt resolution is deferred until after clone and comes from the
    repo pack's `prompt.md`.
- `approval_policy` (string)
  - `auto`: no human approval needed. Agent runs autonomously.
  - `manual`: all tool calls require approval.
  - `destructive-only`: only destructive/write actions need approval.
- `communication` (string, optional)
  - Override the default communication channel for this agent type.
  - Value references a channel name from the `channels` config section.
- `max_concurrent` (integer)
  - Maximum simultaneous instances of this agent type.
- `credentials` (string)
  - `user`: use the maestro user's credentials.
  - `service-account`: use a configured service account.
  - `readonly`: use read-only credentials.
- `hooks` (HooksConfig, optional)
  - Lifecycle hooks specific to this agent type.
- `max_turns` (integer, optional)
  - Maximum agent turns per session. Default: 20.
- `env` (map of string to string, optional)
  - Additional environment variables to inject into the agent subprocess.
- `codex` (map, optional)
  - Harness-specific config for Codex agents. Only valid when `harness: codex`.
  - `model` (string, optional): model name. Default from `codex_defaults.model` (`gpt-5.4`).
  - `reasoning` (string, optional): reasoning effort. Default from `codex_defaults.reasoning` (`high`).
  - `max_turns` (integer, optional): max continuation turns. Default from `codex_defaults.max_turns` (`20`).
  - `thread_sandbox` (string, optional): Codex thread sandbox mode. Default from `codex_defaults.thread_sandbox` (`workspace-write`).
  - `turn_sandbox_policy` (string, optional): Codex per-turn sandbox policy.
  - `extra_args` (list of strings, optional): additional CLI arguments passed to the harness.
- `claude` (map, optional)
  - Harness-specific config for Claude Code agents. Only valid when `harness: claude-code`.
  - `model` (string, optional): model name. Default from `claude_defaults.model` (`opus-4.6`).
  - `reasoning` (string, optional): reasoning effort. Default from `claude_defaults.reasoning` (`high`).
  - `max_turns` (integer, optional): max agent turns. Default from `claude_defaults.max_turns` (`1`). The effective value must currently be `1`; multi-turn Claude sessions are not yet supported.
  - `extra_args` (list of strings, optional): additional CLI arguments passed to the harness.

Cross-validation: `codex:` is rejected if `harness` is not `codex`. `claude:` is rejected if
`harness` is not `claude-code`.

Pack directories:

- Local packs may include `claude/` and `codex/` directories.
- Repo packs may include `claude/` and `codex/` under the repo pack directory.
- Maestro copies these into the prepared workspace as `.claude/` and `.codex/` before the harness
  starts.
- For repo packs, orchestration fields such as `harness`, `workspace`, `approval_policy`, and
  `max_concurrent` still come from `maestro.yaml` because they are needed before clone.

#### 4.1.3 Source Definition

Configured work source loaded from maestro.yaml.

Fields:

- `name` (string)
  - Human-readable name for this source (e.g., "firewall-access", "platform-dev").
- `tracker` (string)
  - Tracker kind: `gitlab`, `linear`.
- `connection` (map)
  - Tracker-specific connection details.
  - GitLab: `base_url`, `token_env`, `project` or `epic`.
  - Linear: `token_env`, `team` or `project`.
- `repo` (string, optional)
  - Repository clone locator used when the routed agent type requires `workspace: git-clone`.
  - Required for Linear sources routed to `git-clone` agents because Linear does not define repo
    identity for Maestro.
  - Ignored for sources routed to `workspace: none` agents.
  - Ignored for GitLab project sources because the repository is derived from the GitLab
    connection/project metadata.
  - For GitLab epic sources, Maestro should derive the repository per issue from issue/project
    metadata. If repo identity is ambiguous or unavailable for a specific issue, skip that issue and
    emit a warning.
- `filter` (FilterConfig)
  - Criteria to select which issues to pick up.
  - `labels` (list of strings, optional): issue must have all listed labels.
  - `assignee` (string, optional): issue must be assigned to this user. `$MAESTRO_USER` expands
    to the configured user identity.
  - `states` (list of strings, optional): issue must be in one of these states.
  - `exclude_labels` (list of strings, optional): skip issues with any of these labels.
- `agent_type` (string)
  - Name of the agent type definition to use for issues from this source.
- `poll_interval` (duration string, optional)
  - Override the global poll interval for this source. Examples: `30s`, `2m`.
- `retry_base` (duration string, optional)
  - Override the global failure retry base delay for this source.
- `max_retry_backoff` (duration string, optional)
  - Override the global maximum failure retry backoff for this source.
- `max_attempts` (integer, optional)
  - Override the global maximum number of attempts for issues from this source.
- `on_dispatch` (map, optional)
  - Lifecycle transition hook executed when a run is dispatched.
  - `state` (string, optional): best-effort tracker metadata update on dispatch. This is not part
    of the portable routing contract.
- `on_complete` (map, optional)
  - Lifecycle transition hook executed when a run completes successfully.
  - `add_labels` (list of strings, optional): labels to add to the tracker issue.
  - `remove_labels` (list of strings, optional): labels to remove from the tracker issue.
  - `state` (string, optional): best-effort tracker metadata update.
  - When omitted, the default behavior adds `{prefix}:done` and removes `{prefix}:active`.
- `on_failure` (map, optional)
  - Lifecycle transition hook executed when a run fails.
  - `add_labels` (list of strings, optional): labels to add to the tracker issue.
  - `remove_labels` (list of strings, optional): labels to remove from the tracker issue.
  - `state` (string, optional): best-effort tracker metadata update.
  - When omitted, the default behavior adds `{prefix}:failed` and removes `{prefix}:active`.

Lifecycle execution sequence:

- **Dispatch**: add `{prefix}:active`, remove `{prefix}:retry`/`done`/`failed`, optionally change
  state via `on_dispatch.state`.
- **Complete (custom)**: remove `{prefix}:active`, add `on_complete.add_labels`, remove
  `on_complete.remove_labels`, optionally change state.
- **Complete (default)**: remove `{prefix}:active`, add `{prefix}:done`.
- **Failure (custom)**: remove `{prefix}:active`, add `on_failure.add_labels`, remove
  `on_failure.remove_labels`, optionally change state.
- **Failure (default)**: remove `{prefix}:active`, add `{prefix}:failed`.
- **Retry**: remove `{prefix}:active`, add `{prefix}:retry` (always default, not configurable).

Global lifecycle defaults:

- `defaults.on_dispatch`, `defaults.on_complete`, and `defaults.on_failure` apply to every source.
- `sources[].on_dispatch`, `sources[].on_complete`, and `sources[].on_failure` override those
  defaults for that source.
- Resolution order is: source hook override -> global lifecycle default -> built-in behavior.
- For a given hook, `state` overrides per field, while `add_labels` and `remove_labels` replace the
  inherited lists when explicitly set.

Reserved versus routing labels:

- Exact reserved lifecycle labels are `{prefix}:active`, `{prefix}:done`, `{prefix}:failed`, and
  `{prefix}:retry`.
- Other labels in the same namespace such as `{prefix}:coding` or `{prefix}:review` are treated as
  ordinary routing labels and remain visible to source filters.
- Intake logic ignores the exact reserved lifecycle labels but does not strip or block other
  `{prefix}:*` labels.
- Tracker state changes in lifecycle hooks are best-effort metadata only; label transitions are the
  orchestration contract.

Pipeline example using lifecycle transitions to chain sources:

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

#### 4.1.4 Channel Definition

Configured communication channel loaded from maestro.yaml.

Fields:

- `name` (string)
  - Identifier for this channel (e.g., "slack-dm", "gitlab-comment").
- `kind` (string)
  - Channel type: `slack`, `teams`, `gitlab`, `tui`.
- `config` (map)
  - Channel-specific configuration.
  - Slack: `token_env`, `mode` (dm).
  - Teams: `webhook_url_env`, `mode` (dm).
  - GitLab: reuses the tracker connection for posting comments.
  - TUI: no additional config needed.

#### 4.1.5 Agent Run

One execution of an agent against an issue.

Fields:

- `id` (string)
  - Unique run identifier.
- `agent_name` (string)
  - The named instance (e.g., "phoenix", "coder-a3f1").
- `agent_type` (string)
  - The agent type definition name.
- `issue` (Issue)
  - The work item being worked on.
- `source_name` (string)
  - Which source configuration produced this work.
- `harness_kind` (string)
  - Which harness is running this agent.
- `workspace_path` (string or empty)
  - Working directory, empty if workspace strategy is `none`.
- `comm_thread_id` (string or empty)
  - Channel-specific thread or conversation identifier used to continue the same human
    communication thread across retries and restarts.
- `status` (RunStatus)
- `started_at` (timestamp)
- `last_activity_at` (timestamp or null)
  - Last time the harness emitted observable activity. Used for stall detection and recovery.
- `attempt` (integer)
  - 0 for first run, incremented on retry.
- `error` (string or null)

#### 4.1.6 Run Status

Agent run lifecycle states (maestro-internal, not tracker states):

- `pending`: run is queued, workspace not yet provisioned.
- `preparing`: workspace provisioning in progress.
- `active`: agent subprocess is running.
- `awaiting_approval`: agent has requested human approval, waiting for response.
- `done`: agent completed successfully.
- `failed`: agent failed (error, timeout, stall).
- `cancelled`: run was stopped by reconciliation (issue state changed externally).

#### 4.1.7 Retry Entry

Scheduled retry for an issue.

Fields:

- `issue_id` (string)
- `agent_name` (string)
- `attempt` (integer, 1-based)
- `due_at` (timestamp)
- `error` (string or null, reason for the retry)

#### 4.1.8 Orchestrator Runtime State

Single authoritative in-memory state for the live process.

Fields:

- `running` (map: issue_id → AgentRun)
- `claimed` (set of issue_id): reserved, running, or retrying.
- `retry_queue` (map: issue_id → RetryEntry)
- `agent_counts` (map: agent_type → current count): for concurrency enforcement.

Persisted recovery metadata:

- Maestro also stores a lightweight recovery snapshot in `runs.json`.
- `runs.json` is not the source of truth for whether work should continue. It exists to restore
  operational metadata after restart: active run records, retry schedules, workspace mappings, and
  communication thread IDs.
- After restart, Maestro loads `runs.json`, then reconciles it against fresh tracker poll results
  before dispatching new work. If tracker state and `runs.json` disagree, the tracker wins.
- `runs.json` must never contain secrets, full tracker issue bodies, full agent transcripts, or
  prompt contents.

Recommended contents of `runs.json`:

- `version` (integer): schema version for future migrations.
- `saved_at` (timestamp): when the file was last written.
- `runs` (list of persisted AgentRun subset):
  - `id`, `issue.id`, `agent_name`, `agent_type`, `source_name`, `status`, `workspace_path`,
    `comm_thread_id`, `started_at`, `last_activity_at`, `attempt`, `error`
- `retry_queue` (list of RetryEntry)

Write policy:

- Rewrite `runs.json` atomically on meaningful state changes: dispatch, approval wait start/end,
  retry scheduling, cancellation, completion, and graceful shutdown.
- Best effort only: failure to write `runs.json` must emit a warning but must not crash the
  service.

### 4.2 Stable Identifiers and Normalization Rules

- `Issue ID`: composite `<tracker_kind>:<external_id>`. Used for internal map keys and
  deduplication.
- `Issue Identifier`: human-readable tracker key. Used for logs, agent names, workspace naming.
- `Workspace Key`: derived from issue identifier by replacing characters not in `[A-Za-z0-9._-]`
  with `_`.
- `Normalized State`: compare after lowercase.
- `Normalized Labels`: compare after lowercase.
- `Agent Instance Name`: for singletons, `instance_name` when set, otherwise `name`. For N-of,
  `<instance_name_or_name>-<short_id>` where short_id is a 4-character hex string derived from the
  run ID.

## 5. Configuration Specification

### 5.1 File Location

Maestro follows the XDG Base Directory Specification:

- Config file: `$XDG_CONFIG_HOME/maestro/maestro.yaml`
  - Default: `~/.config/maestro/maestro.yaml`
- Agent prompt templates: `$XDG_CONFIG_HOME/maestro/agents/<agent_type>/prompt.md`
- Log files: `$XDG_STATE_HOME/maestro/logs/`
  - Default: `~/.local/state/maestro/logs/`
- Run recovery state: `$XDG_STATE_HOME/maestro/runs.json`
  - Default: `~/.local/state/maestro/runs.json`
- Workspace root: `$XDG_DATA_HOME/maestro/workspaces/`
  - Default: `~/.local/share/maestro/workspaces/`
- Runtime data (pid file, socket): `$XDG_RUNTIME_DIR/maestro/`

On Windows, Maestro uses the standard equivalent paths:
- Config: `%APPDATA%\maestro\maestro.yaml`
- Data: `%LOCALAPPDATA%\maestro\`

CLI flag `--config` overrides the config file location.

### 5.2 Config File Schema

```yaml
# maestro.yaml

# Global defaults
defaults:
  poll_interval: 60s
  harness: claude-code
  approval_policy: destructive-only
  credentials: user
  communication: slack-dm
  max_concurrent_global: 5
  max_turns: 20
  label_prefix: maestro             # prefix for lifecycle labels (default: "maestro")
  on_failure:
    add_labels: [maestro:needs-attention]

# Harness defaults (applied to all agents using the respective harness)
codex_defaults:
  model: gpt-5.4
  reasoning: high
  max_turns: 20
  thread_sandbox: workspace-write

claude_defaults:
  model: opus-4.6
  reasoning: high
  max_turns: 1

# User identity
user:
  name: "TJ"                          # display name for agents/messages
  gitlab_username: tjohnson            # for assignee matching
  linear_username: tj                  # for assignee matching

# Work sources
sources:
  - name: firewall-access
    tracker: gitlab
    connection:
      base_url: https://gitlab.example.com
      token_env: GITLAB_TOKEN
      project: infra/firewall-access
    filter:
      labels: [agent:ready]
      assignee: $MAESTRO_USER
    agent_type: firewall
    poll_interval: 30s
    max_attempts: 2
    on_complete:
      add_labels: [review-ready]
      remove_labels: [agent:ready]
    on_failure:
      add_labels: [agent:failed]
      remove_labels: [agent:ready]

  - name: platform-dev
    tracker: gitlab
    connection:
      base_url: https://gitlab.example.com
      token_env: GITLAB_TOKEN
      epic: group/project&42
    filter:
      labels: [ready-for-dev]
    agent_type: code-pr
    poll_interval: 60s
    retry_base: 30s
    max_retry_backoff: 10m

  - name: personal-linear
    tracker: linear
    connection:
      token_env: LINEAR_API_KEY
      team: PERSONAL
    repo: git@gitlab.example.com:tj/personal-app.git
    filter:
      states: [Todo]
    agent_type: code-pr
    poll_interval: 30s

# Agent type definitions
agent_types:
  - name: firewall
    harness: claude-code
    workspace: none
    tools: [firewall-cli, ssh]
    mcps: [firewall-mcp]
    prompt: agents/firewall/prompt.md
    approval_policy: manual
    communication: slack-dm
    max_concurrent: 2
    credentials: user
    max_turns: 10

  - name: code-pr
    instance_name: coder
    harness: claude-code
    workspace: git-clone
    prompt: agents/code-pr/prompt.md
    approval_policy: auto
    max_concurrent: 5
    credentials: user
    claude:
      model: opus-4.6
      extra_args: ["--verbose"]

  - name: triage
    harness: claude-code
    workspace: none
    tools: [ssh, kubectl, grafana-cli]
    mcps: [observability-mcp]
    prompt: agents/triage/prompt.md
    approval_policy: auto
    communication: gitlab-comment
    max_concurrent: 3
    credentials: readonly

# Communication channels
channels:
  - name: slack-dm
    kind: slack
    config:
      token_env: SLACK_BOT_TOKEN
      mode: dm

  - name: teams-dm
    kind: teams
    config:
      webhook_url_env: TEAMS_WEBHOOK_URL
      mode: dm

  - name: gitlab-comment
    kind: gitlab
    config:
      # reuses the tracker connection for the relevant source

  - name: tui
    kind: tui

# Workspace settings
workspace:
  root: ~/.local/share/maestro/workspaces
  cleanup_after: 24h

# Hooks (global defaults, can be overridden per agent type)
hooks:
  after_create: ""
  before_run: ""
  after_run: ""
  before_remove: ""
  timeout: 60s

# Server settings (for web GUI API)
server:
  enabled: true
  port: 8420
  host: 127.0.0.1

# Observability
logging:
  level: info
  dir: ~/.local/state/maestro/logs
```

### 5.3 Config Resolution Order

1. CLI flags (e.g., `--config`, `--port`).
2. Environment variables (e.g., `MAESTRO_CONFIG`).
3. Config file values.
4. Built-in defaults.

### 5.4 Environment Variable Indirection

Fields ending in `_env` contain the *name* of an environment variable, not the secret itself.
Resolution: read the env var at runtime. If the env var is unset or empty, treat the value as
missing. Secrets are never written to config files or logs.

### 5.5 Dynamic Reload

- Maestro watches the config file for changes.
- On change: re-parse, validate, and apply without restart.
- Changes to sources, agent types, channels, and defaults take effect on the next poll tick.
- Invalid reloads do not crash the service. Keep operating with the last known good config and
  emit a warning.
- In-flight agent runs are not affected by config changes. New config applies to new dispatches.

### 5.6 Dispatch Preflight Validation

Before dispatching work, the orchestrator validates:

- At least one source is configured and its tracker connection is valid.
- Referenced agent types exist in the config.
- Referenced channels exist in the config.
- Required credentials (token env vars) are present and non-empty.
- Prompt template files exist and are readable.
- Required harness executables for referenced agent types are present on `PATH`.
  - `claude-code` agent types require the `claude` executable.
  - `codex` agent types require the `codex` executable.
  - Missing harness binaries invalidate dispatch for sources routed to the affected agent type but do
    not stop the service globally.
- `defaults.max_concurrent_global`, if set, is an integer greater than or equal to 1.
- Each `agent_type.max_concurrent` is an integer greater than or equal to 1.
- If a source routes to an agent type with `workspace: git-clone` and the tracker is `linear`,
  `source.repo` must be present and non-empty.
- If a source routes to an agent type with `workspace: git-clone` and the tracker is `gitlab`,
  repository identity must be derivable from the GitLab project or issue metadata.
- If `source.repo` is set for a GitLab source or for a source routed to `workspace: none`, Maestro
  ignores it and may emit a warning.
- If `agent_pack` uses the `repo:` form, the routed agent type must use `workspace: git-clone`.
- Local-pack prompt paths are validated at config load time. Repo-pack prompts are validated after
  clone when the repo pack is resolved.
- If an agent type's `max_concurrent` exceeds `defaults.max_concurrent_global`, Maestro permits the
  config and treats the global limit as the effective cap.
- If the sum of all per-agent-type `max_concurrent` values is much higher than
  `defaults.max_concurrent_global`, Maestro emits a warning only. This may be intentional for
  fairness shaping.

Validation failures skip dispatch for the affected source but do not stop the service.

## 6. Agent Type Definitions

Agent types are the core abstraction that makes Maestro flexible. Each defines a complete recipe
for how an agent should behave.

### 6.1 Prompt Templates

Prompt templates are Markdown files stored under the config directory:

```
~/.config/maestro/agents/
├── firewall/
│   └── prompt.md
├── code-pr/
│   └── prompt.md
└── triage/
    └── prompt.md
```

Templates support variable interpolation using Go's `text/template` syntax:

```markdown
You are a firewall operations agent working on behalf of {{.User.Name}}.

## Issue
- **ID**: {{.Issue.Identifier}}
- **Title**: {{.Issue.Title}}
- **Description**: {{.Issue.Description}}
- **Labels**: {{range .Issue.Labels}}{{.}} {{end}}
- **URL**: {{.Issue.URL}}

## Your Task
Review the firewall rule change request and implement it using the firewall-cli tool.

## Documentation Requirements
After completing the change, document the following in the issue as a comment:
- What rule was added/modified/removed
- Which firewall/zone was affected
- Verification steps taken
- Any warnings or concerns

{{if .Attempt}}
## Retry Context
This is retry attempt #{{.Attempt}}. Review the issue comments for your previous progress
and continue from where you left off.
{{end}}
```

Template input variables:

- `Issue` (Issue object): all normalized fields.
- `User` (User object): maestro user identity.
- `Agent` (AgentType object): the agent type configuration.
- `Source` (Source object): the source that produced this work.
- `Attempt` (integer or 0): 0 on first run, incremented on retry.
- `AgentName` (string): the named instance (e.g., "phoenix", "coder-a3f1").

### 6.2 Approval Policies

Three policies, configured per agent type:

- `auto`: agent runs fully autonomously. No approval interception. Suitable for read-only
  triage agents or well-scoped coding tasks where the MR review is the approval gate.
- `destructive-only`: only destructive or write actions trigger approval requests. The harness
  adapter determines which actions are destructive based on the harness's own categorization.
  For Claude Code, this maps to hooks on write/execute tool calls.
- `manual`: all tool calls require explicit approval. Suitable for high-stakes operations like
  firewall changes or production config modifications.

When approval is required, the flow is:

1. Harness adapter detects an action requiring approval.
2. Harness emits an ApprovalRequest to the Approval Gateway.
3. Gateway routes the request to the user via the agent type's configured communication channel.
4. User approves or rejects (with optional reason).
5. Gateway returns the response to the harness adapter.
6. Harness adapter forwards the response to the agent subprocess.

If the user does not respond within a configurable timeout, Maestro records the request as timed
out and fails the active run.

### 6.3 Agent Type Extensibility

New agent types are added purely through configuration — no code changes required. A team lead
could add a new agent type for "database-migration" by:

1. Creating a prompt template at `~/.config/maestro/agents/db-migration/prompt.md`.
2. Adding an agent type entry in `maestro.yaml`.
3. Adding a source entry that routes the appropriate issues to it.

## 7. Orchestration State Machine

### 7.1 Issue Orchestration States

Internal claim states (not tracker states):

1. `Unclaimed`: issue is visible but not being worked on and has no retry scheduled.
2. `Claimed`: orchestrator has reserved the issue to prevent duplicate dispatch.
3. `Running`: agent subprocess is active and the issue is in the `running` map.
4. `AwaitingApproval`: agent is paused waiting for human approval. The issue remains claimed.
5. `RetryQueued`: agent is not running, but a retry timer exists.
6. `Released`: claim removed because issue is terminal, filtered out, or no longer visible.

### 7.2 Run Attempt Lifecycle

A run attempt transitions through these phases:

1. `Claimed`: issue selected for dispatch, added to claimed set.
2. `PreparingWorkspace`: workspace provisioned per agent type strategy.
3. `RunningHooks`: `before_run` hook executing (if configured).
4. `BuildingPrompt`: prompt template rendered with issue data.
5. `LaunchingAgent`: harness adapter starting the agent subprocess.
6. `Active`: agent is executing. May transition to AwaitingApproval and back.
7. `AwaitingApproval`: agent requested approval, paused. Returns to Active on response.
8. Terminal states:
   - `Succeeded`: agent completed normally.
   - `Failed`: agent errored, timed out, or stalled.
   - `Cancelled`: stopped by reconciliation (issue state changed externally).
9. `RunningPostHooks`: `after_run` hook executing.
10. `Released`: claim released, workspace optionally cleaned up.

### 7.3 Transition Triggers

- **Poll tick**: new issues discovered, reconciliation runs.
- **Agent exit**: subprocess terminated (success or failure).
- **Approval response**: user approved or rejected, agent resumes or fails.
- **Retry timer**: scheduled retry fires, issue re-dispatched if still eligible.
- **Reconciliation**: issue state changed in tracker, agent stopped.
- **User action**: manual stop via TUI/GUI.

### 7.4 Idempotency Rules

- The orchestrator is the single authority for scheduling state mutations.
- An issue can have at most one active run at a time.
- Claimed issues are not re-dispatched.
- Reconciliation completes before dispatch on every tick to release stale claims.

## 8. Polling, Scheduling, and Routing

### 8.1 Per-Tick Flow

Each poll tick follows this sequence:

1. **Poll**: for each source whose poll timer is due, fetch candidate issues from the tracker.
2. **Reconcile**: for each active run belonging to a source with a successful poll this tick, check
   whether the issue is still eligible using the poll results for that source.
   - If the issue appears in the poll results, reconcile directly from the polled issue snapshot.
   - If the issue does not appear in the poll results, perform a direct tracker `Get` for that issue
     as a fallback reconciliation path.
   - If the issue is in a terminal state → stop agent, clean workspace, release claim.
   - If the issue no longer matches the source filter → stop agent, release claim.
   - If the issue remains active and eligible → refresh the in-memory run metadata and continue.
   - If direct `Get` fails transiently, keep the run and try reconciliation again on a later tick.
3. **Validate**: check dispatch preflight validity for each source and routed agent type.
4. **Deduplicate**: skip issues already claimed.
5. **Route**: for each new issue, resolve the agent type from the source config.
6. **Sort**: sort candidates by priority (ascending), then created_at (ascending).
7. **Dispatch**: for each candidate, check concurrency limits (global + per-agent-type).
   If under limits, dispatch. If not, skip until next tick.

### 8.2 Multi-Source Polling

Sources poll independently. Each source has its own poll interval, and poll ticks are staggered
to avoid thundering herd. A failing source does not block other sources from polling.

### 8.3 Concurrency Control

Two levels of concurrency limits:

- **Per-agent-type**: `max_concurrent` in the agent type definition. Each agent type can run up to
  this many simultaneous instances.
- **Global**: `defaults.max_concurrent_global` caps the total number of simultaneous runs across
  all agent types. Default: `5`.

Runs count against concurrency limits while they are in any resource-consuming live state:

- `preparing`
- `active`
- `awaiting_approval`

Runs in `pending`, `done`, `failed`, and `cancelled` do not count against concurrency limits.

Dispatch is allowed only when both limits have remaining capacity:

- current live run count is less than `defaults.max_concurrent_global`
- current live run count for the target agent type is less than `agent_type.max_concurrent`

If the global limit and a per-agent-type limit disagree, the effective dispatch capacity is the
minimum remaining capacity across the two checks.

### 8.4 Dispatch Routing

When an issue is fetched from a source, the source config determines which agent type handles it:

```
Source "firewall-access" → agent_type: "firewall"
Source "platform-dev"    → agent_type: "code-pr"
Source "personal-linear" → agent_type: "code-pr"
```

This is a static mapping — each source routes to exactly one agent type. If a source needs to
route different issues to different agent types (e.g., based on labels), configure multiple source
entries with different label filters pointing to different agent types.

### 8.5 Retry and Backoff

Two retry modes:

- **Continuation retry**: agent completed normally, issue is still in an active state. Retry after
  a short delay (1-5 seconds) to re-check and potentially start a new session.
- **Failure retry**: agent failed, timed out, or stalled. Retry with exponential backoff:
  `base * 2^(attempt-1)`, capped at `max_retry_backoff`. Default base: 10s. Default cap: 5m.

Retry policy defaults come from global state config:

- `state.retry_base`
- `state.max_retry_backoff`
- `state.max_attempts`

Each source may override those values with:

- `sources[].retry_base`
- `sources[].max_retry_backoff`
- `sources[].max_attempts`

If a source override is omitted, Maestro falls back to the corresponding global state value.

Retry scheduling adds the issue to the retry queue with a `due_at` timestamp. The retry timer
fires on the next tick after `due_at`.

### 8.6 Reconciliation

Every tick, the orchestrator reconciles active runs:

1. **Stall detection**: if the harness reports no agent activity for longer than the stall
   timeout, stop the agent and schedule a failure retry.
2. **Poll-driven reconciliation**: for each active run whose source polled successfully this tick,
   compare the run against that source's returned issue set.
   - If the issue appears in the poll results, use the polled issue snapshot as the source of truth
     for reconciliation.
   - If the issue is missing from the successful poll results for that source, perform a direct
     tracker `Get` call for that issue.
3. **Direct-refresh fallback**:
   - If direct `Get` confirms the issue is terminal or no longer matches the source filter, stop
     the agent, release the claim, and clean up as appropriate.
   - If direct `Get` confirms the issue is still active, update the in-memory run metadata and
     continue.
   - If direct `Get` fails transiently, keep the run active and retry reconciliation on the next
     successful poll tick for that source.

Important rules:

- Issue absence is meaningful only relative to a successful poll for that run's own source.
- If a source poll fails for a tick, Maestro must not treat issue absence from that source as a
  reconciliation signal.
- Reconciliation must complete before new dispatches are launched for that source tick.

## 9. Workspace Management

### 9.1 Workspace Strategies

- **git-clone**: clone a repository, create a working branch. Suitable for code changes.
  - Repo URL comes from source metadata: GitLab connection or issue/project metadata, or
    `source.repo` for trackers such as Linear that do not provide repository identity directly.
  - Branch name: `maestro/<agent_name>/<issue_identifier>`.
  - Base branch: configurable, defaults to `main`.
- **none**: no workspace provisioning. Agent receives a temporary directory path (or empty string)
  and uses its own tools (SSH, APIs, CLIs) to interact with systems. Suitable for ops and triage.

### 9.2 Workspace Layout

```
<workspace.root>/
├── <workspace_key_1>/      # sanitized issue identifier
│   └── (repo contents or temp files)
├── <workspace_key_2>/
└── ...
```

### 9.3 Workspace Lifecycle Hooks

Hooks run as shell commands in the workspace directory with the configured timeout:

- `after_create`: runs once when a new workspace directory is created.
- `before_run`: runs before each agent attempt.
- `after_run`: runs after each agent attempt (success, failure, or cancellation).
- `before_remove`: runs before workspace deletion.

Hooks can be configured globally or per-agent-type (agent type overrides global).

### 9.4 Workspace Safety

- Workspace paths must stay under the configured workspace root.
- Workspace key is sanitized: characters not in `[A-Za-z0-9._-]` replaced with `_`.
- Symlink traversal is validated on workspace creation.
- Hooks run with a timeout; hanging hooks are killed.

### 9.5 Workspace Cleanup

- On successful completion: workspace is preserved for the `cleanup_after` duration, then deleted.
- On cancellation: workspace is preserved (human may want to inspect).
- On failure: workspace is preserved for retry.
- Manual cleanup: user can trigger via TUI/GUI.

## 10. Harness Integration Contract

### 10.1 Interface

Every harness adapter implements:

```
Kind() → string
Start(ctx, RunConfig) → (Run, error)
Stop(ctx, runID) → error
Approvals() → channel of ApprovalRequest (or nil if not supported)
Approve(ctx, request, response) → error
```

### 10.2 Claude Code Adapter

- **Launch**: spawn `claude` CLI subprocess with appropriate flags.
  - `--print` mode for non-interactive execution, or
  - SDK-based integration for richer control.
- **Prompt delivery**: passed as stdin or CLI argument.
- **Workspace**: set working directory to workspace path.
- **Approval interception**: use Claude Code hooks (PreToolUse) to intercept tool calls.
  - Hook script pauses the agent and emits an approval request to the Approval Gateway.
  - Gateway response is written back, and the hook script returns the appropriate exit code.
- **Environment**: inject configured tools, MCPs, and env vars.
- **Completion detection**: process exit code + output parsing.
- **Stall detection**: monitor process output; if no output for stall timeout, consider stalled.
- **Harness config**: model, reasoning, max_turns, and extra_args are read from `claude_defaults`
  merged with pack `claude:` defaults and then per-agent `claude:` overrides. Per-agent values win
  over pack values, and pack values win over `claude_defaults`.

#### 10.2.1 Claude Hook Approval Mechanism

For approval policies that require interception (`manual`, `destructive-only`), Maestro integrates
with Claude Code through a synchronous local approval broker.

- **Hook entrypoint**: the Claude `PreToolUse` hook invokes a Maestro-provided helper script or
  binary.
- **Transport**: the hook communicates only with the local Maestro process over IPC.
  - macOS/Linux: Unix domain socket.
  - Windows: named pipe.
- **Channel isolation**: the hook never talks directly to Slack, Teams, TUI, or the GUI. All human
  communication is routed by Maestro after the hook request is received.
- **Blocking behavior**: the hook call is synchronous. Claude remains paused until Maestro returns a
  decision or the approval timeout expires.

Hook request payload:

- `request_id` (string): unique ID for this approval decision.
- `run_id` (string): current agent run ID.
- `agent_name` (string)
- `issue_id` (string)
- `tool_name` (string)
- `tool_input` (string or structured JSON): serialized tool arguments as received from Claude.
- `approval_policy` (string): `manual` or `destructive-only`.
- `requested_at` (timestamp)

Hook response payload:

- `request_id` (string)
- `decision` (string): `approve` or `reject`
- `reason` (string or empty): optional human-entered reason or system-generated timeout/failure
  reason
- `timed_out` (boolean)

Exit code mapping:

- `0`: approved, Claude may proceed with the tool call.
- `10`: explicitly rejected by the human reviewer.
- `11`: approval timed out.
- `12`: Maestro unavailable or IPC failure.
- `13`: invalid hook payload or internal hook error.

Failure handling is fail-closed:

- If the hook cannot connect to Maestro, deny the action.
- If Maestro is unavailable after connection establishment, deny the action.
- If the hook payload is malformed or incomplete, deny the action.
- If the approval channel cannot deliver the request, Maestro may continue retrying delivery until
  the approval timeout expires, then mark the approval as timed out and fail the run.
- If multiple responses arrive for the same `request_id`, the first valid response wins and later
  responses are ignored.

Observability requirements:

- Maestro logs every approval request and response with `request_id`, `run_id`, `tool_name`, final
  decision, and exit code.
- Distinct non-zero exit codes are preserved for debugging even if the Claude hook contract only
  distinguishes allow vs deny.

### 10.3 Codex Adapter

- **Launch**: spawn `codex app-server` via `bash -lc`, communicate over stdin/stdout with
  line-delimited JSON.
- **Protocol**: JSON-RPC-like app-server protocol.
  - `initialize` → `thread/start` → `turn/start` → stream events → turn completion.
- **Approval interception**: the app-server protocol has native approval events.
- **Multi-turn**: reuse thread ID for continuation turns within the same session. `max_turns` is
  configurable (default: 20). Between turns, Maestro sends a continuation prompt that includes
  refreshed issue state from the tracker. This allows Codex to react to label/state changes made by
  the operator or other pipeline stages during a long-running session.
- **Stall detection**: monitor codex events; no events for stall timeout → stalled.
- **Harness config**: model, reasoning, max_turns, thread_sandbox, turn_sandbox_policy, and
  extra_args are read from `codex_defaults` merged with per-agent `codex:` overrides. Per-agent
  values win over defaults.

### 10.4 Future Harnesses

New harnesses (e.g., Cursor agent, Windsurf agent, Aider) would implement the same interface.
The harness contract is intentionally minimal: start, stop, approval channel.

## 11. Tracker Integration Contract

### 11.1 Interface

Every tracker adapter implements:

```
Kind() → string
Poll(ctx) → ([]Issue, error)
Get(ctx, issueID) → (Issue, error)
PostOperationalComment(ctx, issueID, body) → error
AddLifecycleLabel(ctx, issueID, label) → error
RemoveLifecycleLabel(ctx, issueID, label) → error
```

Tracker write responsibility matrix:

- Maestro-owned writes:
  - Lifecycle labels used for orchestration state. The label prefix is configurable via
    `defaults.label_prefix` (default: `maestro`). Default labels are `{prefix}:active`,
    `{prefix}:done`, `{prefix}:failed`, and `{prefix}:retry`. When `on_complete` or `on_failure`
    hooks are configured on a source, custom labels replace the defaults (except `{prefix}:active`
    which is always applied on dispatch and removed on completion/failure). Other labels in the same
    namespace such as `{prefix}:coding` are user-managed routing labels, not reserved lifecycle
    labels.
  - Operational or audit comments emitted by the orchestrator such as agent started, approval
    requested, timed out, cancelled, or failed.
- Agent-owned writes:
  - Work-progress comments documenting what changed and why.
  - Task state transitions that represent business workflow progress.
  - Merge request creation, branch links, and any domain-specific tracker edits.

This boundary is intentional: tracker adapters expose only the write operations the orchestrator
needs for lifecycle management. All task execution and business-logic writes remain inside the
agent's harness environment.

### 11.2 GitLab Adapter

- **API**: GitLab REST API v4 (with optional GraphQL for complex queries).
- **Auth**: Private token via `token_env` environment variable.
- **Project issues**: `GET /projects/:id/issues` with label and assignee filters.
- **Epic issues**: `GET /groups/:id/epics/:epic_iid/issues` with label filters.
- **Single issue refresh**: `GET /projects/:id/issues/:iid` for reconciliation when needed.
- **State mapping**:
  - GitLab `opened` → maestro `open`.
  - GitLab `closed` → maestro `done` (or `cancelled` based on labels).
  - Intermediate states are represented by labels (e.g., `in-progress`, `review`).
- **Operational comments**: `POST /projects/:id/issues/:iid/notes`.
- **Lifecycle labels**: `PUT /projects/:id/issues/:iid` to add/remove orchestrator-owned labels.
- **Task state transitions and work comments**: owned by the agent, not by the tracker adapter.
- **Pagination**: handle paginated responses transparently.
- **Rate limiting**: respect GitLab rate limit headers, back off when approaching limits.

### 11.3 Linear Adapter

- **API**: GraphQL endpoint (default: `https://api.linear.app/graphql`).
- **Auth**: Bearer token via `token_env` environment variable.
- **Queries**: fetch issues by team, state, and labels.
- **Single issue refresh**: issue query by ID for reconciliation when needed.
- **State mapping**: direct mapping from Linear states to maestro states.
- **Operational comments**: create comment mutation.
- **Lifecycle labels**: apply or remove orchestrator-owned labels through the relevant issue update
  mutation.
- **Task state transitions and work comments**: owned by the agent, not by the tracker adapter.
- **Pagination**: cursor-based pagination.

### 11.4 Future Trackers

New trackers implement the same interface. The interface is intentionally minimal:
poll, single-issue refresh, and orchestrator lifecycle writes only. Complex tracker-specific
operations (task state transitions, creating MRs, linking issues) are handled by the agent via its
configured tools, not by the tracker adapter.

## 12. Communication Channel Contract

### 12.1 Interface

Every channel adapter implements:

```
Kind() → string
Notify(ctx, userID, Message) → error
Ask(ctx, userID, Message) → (Reply, error)
RequestApproval(ctx, userID, Message) → (Approval, error)
```

### 12.2 Message Structure

All messages from agents to humans include:

- **Agent name**: the named instance (e.g., "phoenix", "coder-a3f1").
- **Issue link**: URL to the issue in the tracker.
- **Executive summary**: 1-2 sentence context of what the agent is working on and where it is
  in the process. This enables the human to context-switch efficiently.
- **Body**: the actual message, question, or approval request.
- **Action buttons** (where supported): approve/reject, reply, view issue, view agent logs.

### 12.3 Slack Adapter

- **Mode**: Direct message (DM) to the maestro user.
- **Threading**: each agent instance gets its own message thread in the DM. The first message
  creates the thread; subsequent messages reply in-thread.
- **Approval requests**: sent as interactive messages with Approve/Reject buttons.
- **Questions**: sent as messages; human replies in-thread; adapter receives replies via Socket
  Mode.
- **Status updates**: notifications posted to the thread (agent started, completed, failed).
- **API**: Slack Web API for sending and Slack Socket Mode for receiving replies and interactive
  actions.

### 12.4 Teams Adapter

- **Mode**: Direct message or Adaptive Card in a designated channel.
- **Approval requests**: Adaptive Cards with action buttons.
- **Questions**: messages with reply capability.
- **API**: Microsoft Teams Bot Framework or incoming/outgoing webhooks.

### 12.5 GitLab Comment Adapter

- **Mode**: comments on the issue itself.
- **Threading**: not threaded (GitLab doesn't support comment threads natively). Each message
  is a new comment, prefixed with the agent name for identification.
- **Approval requests**: posted as a comment with instructions for the human to reply with
  `/approve` or `/reject`. Adapter polls for new comments matching the pattern.
- **Useful when**: the team lives in GitLab and prefers not to context-switch to Slack.

### 12.6 TUI Channel

- **Mode**: inline in the terminal.
- **Approval requests**: prompt in the TUI with y/n/reason input.
- **Questions**: displayed in the TUI; human types a response.
- **Useful for**: developers who are actively watching the maestro TUI.

## 13. Approval Gateway

### 13.1 Overview

The Approval Gateway sits between harness adapters and communication channels. It is not a
separate component — it is logic within the orchestrator that routes approval requests.

### 13.2 Flow

1. Harness adapter emits an `ApprovalRequest` (tool name, arguments, description).
2. Orchestrator looks up the agent type's approval policy:
   - `auto`: immediately approve. No routing.
   - `manual`: route all requests.
   - `destructive-only`: check if the tool/action is classified as destructive by the harness.
     If yes, route. If no, auto-approve.
3. If routing: construct a `Message` with the approval context and send via the agent type's
   configured communication channel.
4. Wait for the human's response (with timeout).
5. Return `ApprovalResponse` to the harness adapter.
6. Harness adapter forwards the response to the agent subprocess.

### 13.3 Timeout Behavior

If the human does not respond within the approval timeout (default: 24 hours, configurable per
agent type), Maestro records the approval request as timed out and fails the run. The approval
history entry uses reason `"approval timeout"` and outcome `timed_out`.

### 13.4 Audit Trail

All approval requests and responses are logged with:
- Agent name and run ID.
- Issue identifier.
- Tool/action requested.
- Approval decision and reason.
- Response time.

This creates an audit trail for compliance-sensitive operations (e.g., firewall changes).

## 14. Agent Naming and Identity

### 14.1 Naming Rules

- **Singleton agents** (`max_concurrent: 1`): use `instance_name` when set, otherwise `name`.
  - Example: agent type "firewall" with `max_concurrent: 1` → agent name "firewall".
  - Example: agent type "firewall" with `max_concurrent: 1` and `instance_name: phoenix` →
    agent name "phoenix".
- **N-of agents** (`max_concurrent > 1`): use `instance_name` when set, otherwise the agent type
  name, as prefix with a short
  unique suffix.
  - Example: agent type "code-pr" with `max_concurrent: 5` → "code-pr-a3f1", "code-pr-b72e".
  - Example: agent type "code-pr" with `instance_name: coder` and `max_concurrent: 5` →
    "coder-a3f1", "coder-b72e".
  - Suffix is derived from the run ID (first 4 hex characters) for uniqueness.

### 14.2 Identity in Communication

When an agent messages its maestro, the message identifies the agent by name. This enables
the human to track multiple concurrent agents:

Example below assumes a singleton firewall agent configured with `instance_name: phoenix`.

```
🔧 phoenix (firewall agent)
Working on: infra/firewall-access#42 — "Add egress rule for monitoring service"

I need approval to execute:
  firewall-cli add-rule --zone=prod --direction=egress --port=9090 --target=10.0.1.0/24

[Approve] [Reject]
```

### 14.3 Identity in Tracker

When an agent documents its work in an issue comment, it identifies itself:

Example below assumes a singleton firewall agent configured with `instance_name: phoenix`.

```markdown
## Agent: phoenix (firewall)

### Actions Taken
- Added egress rule: prod zone, port 9090, target 10.0.1.0/24
- Verified rule propagation across 3 firewall nodes
- Confirmed monitoring service connectivity

### Evidence
[command output / logs]
```

### 14.4 Identity in Git

For agents that create branches and commits:

- Branch name: `maestro/<agent_name>/<issue_identifier>`
- Commit trailer: `Maestro-Agent: <agent_name> (<agent_type>)`

## 15. Observability

### 15.1 TUI (bubbletea)

The TUI provides a real-time view of the orchestrator:

- **Dashboard view**: table of active agents with columns: name, type, issue, status, duration.
- **Agent detail view**: selected agent's logs, current activity, pending approvals.
- **Approval view**: pending approval requests with inline approve/reject.
- **Source view**: status of each work source (last poll, issues found, errors).
- **Key bindings**: navigate between views, approve/reject, stop agents, refresh.

### 15.2 Web GUI (React/TypeScript)

The web GUI communicates with the maestro daemon via the API server:

- Dashboard with agent cards and status indicators.
- Agent detail page with logs and activity timeline.
- Approval queue with context and action buttons.
- Configuration viewer.
- Connected via WebSocket for live updates.

### 15.3 API Server

REST + WebSocket API on configurable port (default: 8420):

- `GET /api/v1/status` → orchestrator summary (running counts, retry queue size).
- `GET /api/v1/agents` → list of active agent runs.
- `GET /api/v1/agents/:id` → agent detail.
- `GET /api/v1/sources` → source status (last poll, errors).
- `GET /api/v1/approvals` → pending approval requests.
- `POST /api/v1/approvals/:id` → approve or reject.
- `POST /api/v1/agents/:id/stop` → stop an agent.
- `POST /api/v1/refresh` → trigger immediate poll.
- `WS /api/v1/ws` → live event stream.

### 15.4 Structured Logging

All log entries include:

- `timestamp`
- `level` (debug, info, warn, error)
- `component` (orchestrator, tracker, harness, channel, workspace)
- `agent_name` (when applicable)
- `issue_id` (when applicable)
- `source_name` (when applicable)
- `message`

Log files are rotated and stored under the configured log directory.

## 16. Security and Safety

### 16.1 Credential Management

- Secrets are stored in environment variables, referenced by name in config (`token_env` fields).
- Maestro never logs, displays, or persists secret values.
- Per-agent credential scoping: different agent types can use different credentials.
  - `user`: full user credentials. Agent acts as the user.
  - `service-account`: shared service account with defined permissions.
  - `readonly`: read-only credentials for investigation/triage agents.

### 16.2 Agent Sandboxing

Maestro delegates sandboxing to the agent harness:

- **Claude Code**: sandbox configuration via Claude Code's own settings and hooks.
- **Codex**: sandbox configuration via approval_policy and turn_sandbox_policy.

The `approval_policy` per agent type provides an additional layer: even if the harness allows
an action, maestro can require human approval before it proceeds.

### 16.3 Workspace Safety

See Section 9.4.

### 16.4 Communication Security

- Slack/Teams tokens are stored in environment variables, not config files.
- API server binds to localhost by default. Exposing to network requires explicit configuration.
- WebSocket connections are unauthenticated in v1 (localhost-only assumption). Future versions
  may add auth for remote GUI access.

### 16.5 Audit Trail

All agent actions that modify external systems are logged:

- Approval requests and responses (Section 13.4).
- Agent state transitions.
- Tracker state changes.
- Communication messages sent.

This is especially important for operations tasks where compliance requires a record of who
(which agent, on behalf of which user) did what and when.

## 17. Failure Model and Recovery

### 17.1 Failure Classes

1. **Config failures**: invalid config file, missing env vars.
   - Effect: skip dispatch for affected sources. Service stays alive.
   - Recovery: fix config file (auto-detected via file watch).

2. **Tracker failures**: API errors, rate limiting, network issues.
   - Effect: skip poll for the affected source this tick.
   - Recovery: automatic retry on next tick.

3. **Agent failures**: subprocess crash, timeout, stall.
   - Effect: run marked as failed, retry scheduled with backoff.
   - Recovery: automatic retry. Agent resumes from issue state (documented progress).

4. **Workspace failures**: disk full, permission errors.
   - Effect: run fails at workspace preparation.
   - Recovery: retry with backoff. Operator intervention if persistent.

5. **Channel failures**: Slack API down, Teams webhook error.
   - Effect: message/approval not delivered.
   - Recovery: retry delivery. If approval, timeout triggers rejection.

6. **Orchestrator crash**: process killed, machine restart.
   - Effect: live in-memory state is lost. Recovery metadata may remain in `runs.json`.
   - Recovery: on restart, load `runs.json`, then perform a fresh poll. Persisted retry schedules,
     communication thread IDs, and workspace paths are restored when the tracker still shows the
     issue as active. Agents document progress in issues, so re-dispatched agents can resume from
     documented state. If `runs.json` disagrees with tracker state, the tracker wins.

7. **Recovery state corruption**: `runs.json` is unreadable, partially written, or schema-invalid.
   - Effect: recovery metadata cannot be trusted for startup restoration.
   - Recovery: log a warning, discard the corrupted file contents, and start with fresh in-memory
     state. Normal polling still re-discovers work from the trackers.

### 17.1.6 Recovery Startup Flow

On process startup:

1. Load configuration and validate required credentials.
2. Attempt to read `runs.json`.
3. If `runs.json` is missing, continue with empty recovery metadata.
4. If `runs.json` is unreadable or invalid, log a warning and continue with empty recovery
   metadata.
5. Poll all eligible sources.
6. Reconcile persisted runs and retry entries against polled tracker state.
   - If an issue is still eligible, restore its workspace mapping, communication thread ID, and
     retry schedule metadata.
   - If an issue is no longer eligible or is already terminal, discard the persisted entry.
7. Begin normal dispatch using the reconciled in-memory state.

### 17.2 Graceful Shutdown

On SIGTERM/SIGINT:

1. Stop accepting new dispatches.
2. Send stop signal to all active agent subprocesses.
3. Wait for agents to exit (with timeout).
4. Run `after_run` hooks for active runs.
5. Persist `runs.json`.
6. Exit.

## 18. Cross-Platform Considerations

### 18.1 macOS (Primary)

- Full support. Primary development and testing platform.
- Claude Code and Codex CLIs available natively.

### 18.2 Windows

- Config paths use Windows conventions (Section 5.1).
- Shell hooks run via `cmd.exe` or PowerShell (configurable).
- Agent subprocess management via Windows process APIs.
- Git operations work via Git for Windows.
- TUI works via Windows Terminal (bubbletea supports Windows).

### 18.3 Linux

- Full support. Target for containerized deployment.
- Docker/container deployment guide provided.

### 18.4 Build Matrix

Cross-compilation via Go + goreleaser:

- `darwin/amd64`, `darwin/arm64`
- `linux/amd64`, `linux/arm64`
- `windows/amd64`, `windows/arm64`

## 19. Test Strategy

### 19.1 Unit Tests

- Config parsing and validation.
- Agent naming logic.
- Issue normalization across trackers.
- Prompt template rendering.
- Workspace key sanitization.
- Concurrency limit enforcement.
- Retry backoff calculation.

### 19.2 Integration Tests (with Mocks)

- Orchestrator poll-dispatch-reconcile loop with mock tracker and harness.
- Approval routing with mock channel.
- Multi-source polling with mixed tracker types.
- Agent lifecycle (start, approval, complete, retry).
- Config reload during operation.
- `T15` Approval timeout: pending approval request receives no response before timeout and is
  rejected with the correct run/state transition.
- `T19` Graceful shutdown: active runs are stopped, hooks run, and `runs.json` is persisted before
  process exit.
- `T20` `runs.json` recovery: restart loads persisted runs and retry entries, reconciles them
  against fresh poll results, and restores workspace/thread metadata only when tracker state still
  matches.
- `T21` Multi-agent approval contention: multiple simultaneous approval requests from different
  agent runs remain correctly correlated and do not cross-deliver responses.
- `T24` Shared HTTP client: retries transient `5xx` failures, respects rate-limit headers, injects
  auth correctly, and emits structured request logs with redacted credentials.

### 19.3 End-to-End Tests

- Real GitLab API with test project (skipped if no credentials).
- Real Slack API with test workspace (skipped if no credentials).
- Real Claude Code subprocess with a trivial task.
- Full workflow: issue created → agent dispatched → work done → issue updated.

### 19.4 Platform Tests

- Build and basic operation tests on macOS, Windows, Linux via CI matrix.

## 20. Implementation Phases

### Phase 1: Core Loop (MVP)

- Config parser (single source, single agent type).
- GitLab adapter (poll project issues by label).
- Claude Code harness (spawn subprocess, manage lifecycle).
- Basic TUI (agent list, status, logs).
- Slack DM integration (agent can message its maestro).
- One agent type: `code-pr` (clone, branch, code, MR).
- Unit and integration tests with mocks.

### Phase 2: Multi-Source, Multi-Agent

- Multiple sources in config with independent polling.
- Agent type definitions + routing.
- Approval gateway (TUI + Slack).
- Linear adapter.
- Ops agent type (no workspace, tool-based).
- Agent naming (singleton + N-of).

### Phase 3: Polish and Extend

- Web GUI (React/TypeScript).
- Codex harness adapter.
- Teams integration.
- Retry with exponential backoff.
- Stall detection.
- Full reconciliation loop.
- Config file watching and dynamic reload.

### Phase 4: Production Hardening

- Windows support and testing.
- Linux containerization.
- Audit trail and compliance logging.
- Performance testing with many concurrent agents.
- Documentation: user guide, agent type cookbook, troubleshooting.
- goreleaser setup for cross-platform releases.

## 21. Follow-On Work

These items are intentionally deferred from the core spec review and MVP definition, but should be
addressed during implementation hardening:

- **Slack Socket Mode reconnection handling**
  - Define retry and at-least-once delivery behavior for approval requests that are pending during a
    Socket Mode disconnect.
  - The Slack SDK may reconnect automatically, but Maestro must ensure pending approval prompts are
    re-sent or otherwise recoverable so agents do not remain stuck indefinitely.
- **Multi-agent approval UX**
  - Design the TUI and Slack experience for multiple simultaneous approval requests.
  - Prioritization, ordering, visual differentiation, and possible batch actions should be defined
    before scaling multi-agent usage beyond early MVP scenarios.
