# Maestro

Single-machine orchestration daemon that polls issue trackers, dispatches AI agents, and routes approvals. It connects your project management (GitLab, Linear) to your AI coding agents (Claude Code, Codex) with configurable lifecycle transitions, workspace management, and retry logic.

Maestro is the scheduler and approval router. Agents own task progress — PR creation, code changes, domain logic. Maestro dispatches them, watches for completion or failure, and manages the issue lifecycle.

## How It Works

```
maestro.yaml          issue tracker          agent (Claude/Codex)
    │                      │                        │
    ▼                      │                        │
 Poll ──────── filter ─────┘                        │
    │                                               │
 Dispatch ── prepare workspace ── render prompt ────┘
    │                                               │
 Monitor ── reconcile state ── detect stalls ───────┘
    │                                               │
 Complete ── apply lifecycle ── update tracker state
```

Each **source** defines a tracker + filter + agent type. Maestro polls for eligible issues, claims them, prepares a workspace (git clone or reuse), renders the agent prompt with issue context, and launches the agent. When the agent exits, Maestro applies lifecycle transitions (labels, state changes) and optionally hands off to the next source in a pipeline.

**Workflow chaining**: multiple sources can form a pipeline. Source A's `on_complete` changes the issue state so Source B's filter picks it up. Example: implement → review → merge.

## Quick Start

### Prerequisites

- Go 1.26+
- `claude` CLI (for claude-code agents) and/or `codex` CLI (for codex agents) in PATH
- A tracker token (GitLab personal access token or Linear API key)

### Install

```bash
git clone https://github.com/tjohnson/maestro.git
cd maestro
make build        # builds bin/maestro with embedded web UI
make install      # installs to $GOPATH/bin
```

### Configure

Create a `maestro.yaml` (or copy from `examples/`):

```bash
cp examples/maestro.yaml maestro.yaml
# Edit: set your tracker token env var, project, filters, agent type
```

### Run

```bash
# Set your tracker token
export LINEAR_API_KEY="lin_api_..."
# or
export GITLAB_TOKEN="glpat-..."

# Run with TUI
bin/maestro --config maestro.yaml

# Run without TUI (logs to stdout)
bin/maestro --config maestro.yaml --no-tui
```

### Verify

```bash
# Check config and harness binaries
bin/maestro doctor --config maestro.yaml
```

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
  max_turns: 20                   # (default) multi-turn continuation
  extra_args: []                  # additional CLI flags
  # thread_sandbox: workspaceWrite       # optional override (derived from approval_policy)
  # turn_sandbox_policy:                 # optional override (derived from approval_policy)
  #   type: dangerFullAccess

claude_defaults:                  # defaults for all claude-code agents
  model: claude-opus-4-6          # (default)
  reasoning: high                 # (default)
  max_turns: 1                    # (default) single-turn; multi-turn not yet supported
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
      assignee: ""                 # filter by assignee email/username
      iids: []                     # GitLab only: specific issue IIDs

    # GitLab epic sources support separate filters for epics vs linked issues.
    epic_filter:                   # overrides filter for epic-level matching
      labels: []
      iids: []                     # target specific epic IIDs
    issue_filter:                  # overrides filter for linked child issues
      states: []
      assignee: ""

    agent_type: dev-codex          # which agent_type to dispatch

    poll_interval: 10s             # override defaults.poll_interval
    max_attempts: 3                # max retries before marking terminal
    retry_base: 30s                # initial retry delay
    max_retry_backoff: 10m         # max retry delay after exponential backoff

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
      max_turns: 20
      extra_args: []
      # thread_sandbox and turn_sandbox_policy are derived from approval_policy.
      # Only set these to decouple sandbox from approval behavior.
    claude:
      model: claude-opus-4-6
      reasoning: high
      extra_args: []

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
| `dev-claude` | claude-code | Same workflow as dev-codex but single-turn Claude Code. |
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

- **Path**: `{workspace.root}/{sanitized-issue-identifier}` (e.g., `var/workspaces/TAN-42`)
- **Branch**: `maestro/{agent-name}/{sanitized-issue-identifier}`
- **Reuse**: if a workspace exists from a previous run, Maestro reuses it (fetches latest, checks out the agent branch). Agent's local commits are preserved across retries.
- **Fallback**: corrupt repos are detected and re-cloned. Transient failures (network, auth) preserve the workspace and return an error.
- **Cross-instance**: if a different Maestro instance picks up the same issue, it does a fresh clone but checks out the existing remote branch — prior pushed work is preserved.

## CLI Commands

```bash
maestro --config maestro.yaml              # run with TUI
maestro --config maestro.yaml --no-tui     # run without TUI
maestro doctor --config maestro.yaml       # validate config + check harness binaries
maestro inspect config --config maestro.yaml         # dump resolved config
maestro inspect state --config maestro.yaml          # dump state (claimed, retries, finished)
maestro inspect runs --config maestro.yaml           # run-centric view
maestro reset issue --config maestro.yaml ISSUE-ID   # clear issue from state
maestro cleanup workspaces --config maestro.yaml     # remove non-active workspaces
```

## Run Logs

Agent stdout/stderr is persisted after each run:

```
{state.dir}/runs/{run-id}/stdout.log
{state.dir}/runs/{run-id}/stderr.log
```

Review what an agent did: `cat var/state/runs/run-20260321-*/stdout.log`

## TUI

The terminal UI shows live status with lipgloss-styled panels:

- **Header**: sources active, agents running, retries queued, next poll countdown, web URL
- **Sources**: health status, filter states, active/retry counts
- **Active Runs**: columnar table with issue, agent, status, age, idle time
- **Retry Queue**: queued retries with due time and error
- **Events**: last 5 log events

### Keybindings

| Key | Action |
|-----|--------|
| `tab` | Switch focus between panels |
| `j/k` | Navigate within focused panel |
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
```

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/healthz` | Health check |
| `GET` | `/api/v1/stream` | Server-Sent Events for live updates |
| `GET` | `/api/v1/status` | Current status snapshot |
| `GET` | `/api/v1/config` | Resolved config |
| `GET` | `/api/v1/sources` | Source summaries |
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
| [maestro.yaml](examples/maestro.yaml) | Minimal starter template |
| [gitlab-claude-auto.yaml](examples/gitlab-claude-auto.yaml) | GitLab + Claude Code, simplest setup |
| [gitlab-codex-auto.yaml](examples/gitlab-codex-auto.yaml) | GitLab + Codex |
| [gitlab-pipeline.yaml](examples/gitlab-pipeline.yaml) | Workflow chaining: implement → review → merge |
| [gitlab-claude-slack-manual.yaml](examples/gitlab-claude-slack-manual.yaml) | Slack approval flow |
| [gitlab-epic-claude-auto.yaml](examples/gitlab-epic-claude-auto.yaml) | GitLab epic workflow |
| [linear-codex-auto.yaml](examples/linear-codex-auto.yaml) | Linear + Codex |
| [multi-source-claude-auto.yaml](examples/multi-source-claude-auto.yaml) | Multiple trackers in one config |
| [many-sources-claude-auto.yaml](examples/many-sources-claude-auto.yaml) | Source/agent defaults for large configs |
| [swiftoot.yaml](examples/swiftoot.yaml) | Linear + Codex/Claude pipeline: implement → review |

## Further Reading

- [Getting Started](docs/getting-started.md) — setup and first run walkthrough
- [Trackers](docs/trackers.md) — tracker behavior, GitLab epics, Linear details
- [Agents](docs/agents.md) — agent packs, prompt design, context files
- [Operator Guide](docs/operator-guide.md) — day-to-day operation, workspace management, troubleshooting
- [Demo Walkthroughs](docs/demo-walkthroughs.md) — step-by-step demo flows
- [Testing](TESTING.md) — test matrix and conventions
- [Future Improvements](docs/future-improvements.md) — deferred features and roadmap ideas
