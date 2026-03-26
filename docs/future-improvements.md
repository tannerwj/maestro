# Future Improvements

Potential features, improvements, and extensions for Maestro. Each entry covers what it
is, why it matters, and what's needed to ship.

---

## Table of Contents

- [Deferred from v1](#deferred-from-v1)
- [Docker / Container Sandboxing](#docker--container-sandboxing)
- [Configuration & Developer Experience](#configuration--developer-experience)
- [Orchestration & Scheduling](#orchestration--scheduling)
- [Observability & Dashboard](#observability--dashboard)
- [Agent Capabilities](#agent-capabilities)
- [Workspace Management](#workspace-management)
- [Communication & Approval Flow](#communication--approval-flow)
- [Security](#security)
- [Tracker Integrations](#tracker-integrations)
- [Other Ideas](#other-ideas)

---

## Deferred from v1

### `destructive-only` Approval Policy

A third approval policy that auto-approves read-only actions and only routes
destructive/write actions for human approval. Sits between `auto` and `manual`.

**Why deferred**: Neither Claude Code nor Codex currently expose action classification
(read vs write/destructive) in a way Maestro can consume.

**To ship**: Add action classification to `harness.ApprovalRequest`, teach each adapter
to populate it, add routing logic in `orchestrator/approvals.go`. Claude Code would need
a tool-name allowlist (fragile) or upstream category tagging. Codex's RPC methods
partially distinguish command execution from file changes.

---

### `hooks.before_remove`

Shell hook that runs before a workspace is removed during cleanup.

**Why deferred**: Config field exists but no execution code is wired.

**To ship**: Wire hook execution in the cleanup command path with the same env vars as
other hooks.

---

## Docker / Container Sandboxing

### 🐳 Docker Harness Wrapper

Optional Docker-based isolation for agent execution. Instead of spawning `claude` or
`codex` directly on the host, Maestro creates a container with the agent binary, mounts
the workspace, applies resource limits, and streams logs back.

**Why**: All agents currently share the host filesystem, network, and credentials. A
prompt-injected agent could read other workspaces, exfiltrate tokens, or interfere with
other agents. Docker isolation eliminates these vectors and enables reproducible
per-agent-type environments (Node.js image vs Python image).

**Design**:

```
internal/harness/docker/
├── adapter.go       # Wraps any harness.Harness with container lifecycle
├── container.go     # Create, start, wait, stop, logs via Go Docker SDK
├── network.go       # Network policy / filtering proxy setup
└── pool.go          # Optional warm container pool (future)
```

The adapter wraps existing Claude/Codex adapters — the agent binary runs inside the
container, but log parsing, approval detection, and stream-json handling stay the same.

**Config**:

```yaml
agent_types:
  - name: code-agent
    harness: claude-code
    sandbox:
      enabled: true
      image: "maestro-agent:latest"
      resources:
        memory: "4Gi"
        cpu: 2
        pids: 4096
      network:
        mode: "filtered"         # none | filtered | host
        allowed_domains:
          - "gitlab.example.com"
          - "registry.npmjs.org"
      filesystem:
        readonly_root: true
        writable_paths: ["/tmp", "/workspace"]
      security:
        drop_all_caps: true
        no_new_privileges: true
        runtime: ""              # "" = default, "runsc" = gVisor
```

**To ship**: Go Docker SDK dep, container lifecycle management, log streaming via
`stdcopy.StdCopy`, volume-per-workspace mapping, tmpfs-based secrets, agent base
image(s), network filtering proxy, integration tests.

---

### 🐳 Resource Governance per Agent

Per-agent-type CPU, memory, disk I/O, and PID limits enforced by container cgroups.

**Why**: Prevents one agent from starving others. OOM kills stay isolated to the
offending container.

**Recommended baseline**: 4 GiB memory, 2 CPUs, 4096 PID limit, medium I/O weight.

---

### 🐳 Tiered Isolation Modes

Configurable isolation strength per agent type:

| Tier | Technology | Security | Use case |
|------|-----------|----------|----------|
| 0 | No isolation (current) | None | Dev / trusted environments |
| 1 | Docker + hardening | Good | Default for production |
| 2 | Docker + gVisor runtime | Strong | Untrusted code |
| 3 | Firecracker microVMs | Strongest | Enterprise / multi-tenant |

Docker's `HostConfig.Runtime` field swaps runtimes without changing the harness
interface. Tier 0 → 1 is the big lift; 1 → 2/3 is a config change.

---

### 🐳 Warm Container Pool

Pre-created containers in "created" state, ready for instant dispatch. On dispatch:
attach workspace volume, inject prompt, start. On completion: stop, detach, reset.

**Why**: Eliminates 5-30s cold start. Only relevant at scale.

**When**: After Docker Harness Wrapper ships and dispatch latency becomes a bottleneck.

---

## Configuration & Developer Experience

### 🔄 Hot Reload

Watch `maestro.yaml` and agent pack files for changes. Validate before applying — reject
invalid configs, keep current config running.

**Why**: Config changes currently require a full restart, which kills active runs.

**To ship**: fsnotify watcher, validation gate (reuse `ValidateMVP()`), diff generation,
graceful source transitions (new start, removed drain, modified restart), `SIGHUP`
fallback.

---

### 🧪 Dry-Run Mode

`maestro --dry-run` fetches matching issues, renders prompts, and prints what would be
dispatched — without launching agents.

**Why**: Debug prompt templates and filter configs without burning API credits or
creating noisy tracker comments. Essential for onboarding new agent types.

**To ship**: CLI flag, poll once, render prompts, print dispatch plan, exit. Minimal
code — wire existing functions with a no-op harness.

---

### 🔍 Route Collision Detection

`maestro --routes` prints all source→agent routing rules and warns about overlapping
label filters that could cause double-dispatch.

**Why**: With multiple sources and label-based routing, overlapping filters can cause the
same issue to be dispatched twice.

**To ship**: CLI flag, iterate sources, compare filter criteria, print table, warn on
overlaps.

---

### 📝 Workflow-as-Markdown

Support single `.md` files as self-contained agent definitions — YAML frontmatter
(config) + template body (prompt). Auto-discover from `workflows/` directory.

**Why**: Lower barrier to creating new agent types. A single file is easier to share,
version, and review than a pack directory. Coexists with current agent pack format.

**To ship**: Frontmatter parser, field mapping to `AgentTypeConfig`, auto-discovery via
glob, backward-compatible with packs.

---

### 📝 Templated Hooks

Workspace hooks that can reference issue fields using Go template syntax, e.g.,
`git checkout {{ .Issue.Meta.branch_name }}`.

**Why**: Current hooks are static shell scripts. Templated hooks enable issue-aware setup
without hardcoding.

**To ship**: Run hook scripts through `text/template` before execution, same context as
prompt rendering (`.Agent`, `.Issue`, `.User`).

---

## Orchestration & Scheduling

### 🔀 Subtask Orchestration

A dispatch mode that processes a parent issue's subtasks sequentially, with a fresh agent
session per subtask. Maintains a progress comment on the parent. After all subtasks: push
branch, create PR, transition parent to done.

**Why**: Complex issues decomposed into subtasks are common in Linear and GitLab. Current
Maestro dispatches one agent per issue — it can't orchestrate sequential subtask
execution with cross-subtask progress tracking.

**To ship**: Subtask fetching in tracker adapters, new dispatch mode in service loop,
progress comment upsert per subtask, continuation logic for remaining subtasks. Config:
`mode: subtask_loop`, `max_iterations: 5`.

---

### 🚦 Per-State Concurrency Limits

Different concurrency limits based on issue state: max 1 agent on "Todo" but max 3 on
"In Progress".

**Why**: Throttle new work intake while letting in-progress work run freely. Useful when
"Todo" items need triage before committing resources.

**To ship**: Extend limiter to accept state parameter, count active runs by issue state,
check per-state limits before dispatch.

---

### 🔗 Blocker-Aware Scheduling

Skip issues blocked by non-terminal issues. If B depends on A and A is open, don't
dispatch B.

**Why**: Prevents wasted agent runs on blocked work. Common in Linear (issue relations)
and GitLab (blocking issues).

**To ship**: Tracker adapter extension for blocking/blocked-by relations, filter
candidates during dispatch.

---

### ⚡ Webhook-Driven Dispatch

Accept inbound webhooks from trackers (GitLab, Linear) to trigger immediate dispatch
instead of waiting for the next poll cycle.

**Why**: Reduces dispatch latency from poll_interval (30-60s) to near-instant.

**To ship**: HTTP endpoint, webhook signature verification, event filtering, coexist with
polling. Config: `webhooks.enabled`, `webhooks.secret_env`.

---

### 🔄 Multi-Turn Claude Code

Claude Code runs currently execute as single-turn (`--print` mode). Add multi-turn
support similar to Codex's continuation loop.

**Why**: Complex tasks need multiple turns. Codex already supports this via
`ContinuationFunc`. Claude Code could use session resume (`--session-id`).

**To ship**: Capture session ID from output, continuation loop, `max_turns` config for
Claude.

---

## Observability & Dashboard

### 💰 Run Metrics & Cost Tracking

Per-run metrics parsed from harness output and surfaced in TUI, dashboard, and API:

| Metric | Source |
|--------|--------|
| **Tokens in / out / total** | Claude stream-json `result` event, Codex RPC response |
| **Cache tokens** (read + creation) | Claude stream-json `result` event |
| **Cost (USD)** | Claude `cost_usd` field, Codex equivalent |
| **Throughput (tokens/sec)** | Derived: output tokens / run wall-clock time |
| **Total run time** | `CompletedAt - StartedAt` (already tracked, not surfaced) |
| **Tracker rate limits** | Parse `X-RateLimit-Remaining` / `X-RateLimit-Limit` headers |

**Why**: Operators need visibility into agent costs, performance, and API quota. Run time
is already in `domain.AgentRun` but not shown in the dashboard. Token counts and
throughput are available in harness output but not parsed. Rate limits are silently hit
with no warning.

**To ship**:

- Parse token/cost fields from stream-json `result` events and Codex RPC responses
- Add `TokensIn`, `TokensOut`, `CacheTokens`, `CostUSD` to `domain.AgentRun`
- Compute throughput as `TokensOut / run_duration_seconds`
- Surface run time and throughput in TUI agent detail and web dashboard
- Aggregate per-source and global totals in supervisor snapshot
- Parse rate limit headers in tracker HTTP responses, expose in source status
- Show rate limit quota in TUI status bar and dashboard source cards

---

### ▶️ Force Poll from TUI / Dashboard

Trigger an immediate poll of all sources (or a specific source) from the TUI or web
dashboard, with a 2-second debounce to prevent spamming.

**Why**: When you've just updated an issue and want the agent to pick it up now, waiting
30-60s for the next poll cycle is frustrating. Keyboard shortcut in TUI (`r`) and button
in dashboard for instant feedback.

**To ship**: API endpoint `POST /api/v1/sources/poll` (optional `?source=name`). TUI
keybinding. Dashboard button. Debounce: ignore requests within 2s of the last poll.
Orchestrator exposes a `ForcePoll()` method that signals the tick loop to run
immediately.

---

### 🎨 Rich Run Status Indicators

Granular status taxonomy: streaming, waiting_input, needs_attention, ready, idle, failed
— each with distinct visual treatment (colors, pulses, badges).

**Why**: Current statuses (pending, preparing, active, awaiting_approval, done, failed)
don't distinguish "agent is thinking" from "agent needs human input" at a glance.

**To ship**: Extend `RunStatus` enum, harness adapters emit finer-grained status from
stream events, frontend renders distinct indicators.

---

### 📡 SSE Streaming for Web Dashboard

Server-Sent Events endpoint pushing snapshot updates every 1-2 seconds to all connected
web clients.

**Why**: Simpler than WebSocket for read-only dashboards, works through more
proxies/firewalls. Could coexist with WebSocket for bidirectional actions (approvals).

---

### 🔌 Late-Subscriber Catch-Up

When a client connects mid-run, immediately send a snapshot of current state including
buffered stream events, pending approvals, and active run context.

**Why**: Connecting to the dashboard mid-run currently shows limited context. Users must
wait for the next event to understand what's happening.

**To ship**: Ring buffer for recent run output events, send full snapshot + buffer on
connect before switching to live stream.

---

### 📜 Agent Transcript Logging

Write human-readable transcripts of agent sessions to disk — rendered prompts, tool
calls, results, formatted as markdown.

**Why**: Post-mortem debugging. Current stdout/stderr captures raw stream-json which
isn't human-readable.

**To ship**: Formatter for stream-json → markdown, write alongside log files. Config:
`logging.transcripts: true`.

---

### 📊 Metrics & Graphing

Time-series metrics: dispatch latency, run duration, success/failure rates, token usage.
Expose as Prometheus endpoint or built-in charts.

**Why**: Trend visibility for capacity planning and agent effectiveness measurement.

**To ship**: Metrics collector in orchestrator, Prometheus exporter (`/metrics`), or
built-in charting in web dashboard.

---

## Agent Capabilities

### 🛠️ Bundled Tracker CLI for Agents

Lightweight CLI injected into agent environments (`$MAESTRO_CLI`) for querying and
updating tracker state: get issue details, swap labels, post comments, create sub-issues.

**Why**: Agents currently rely on their own tools (MCP, Claude Code tools) for tracker
interaction. A dedicated CLI gives a guaranteed, consistent interface without MCP setup.

**To ship**: CLI with subcommands (`get-issue`, `update-state`, `add-label`,
`post-comment`), reads token from env/tmpfs, scoped to the agent's issue via env var.

---

### 🏷️ Agent-Generated Run Names

Inject an MCP tool on session start that lets the agent generate a descriptive run name
(e.g., "Fix pagination bug in user list API") instead of the default agent-type +
issue-identifier.

**Why**: Human-readable summaries improve dashboard readability.

---

### 🔐 MCP-Based Inline Approval

Inject a custom MCP server into Claude Code with approval tools (`RequestApproval`,
`AskUserQuestion`) that pause execution until human response — replacing the current
two-phase detection→approval→permissive execution model.

**Why**: The two-phase model loses agent context between detection and permissive runs.
MCP-based approval enables inline approval during a single continuous run.

**To ship**: MCP server binary exposing approval tools, injected via `--mcp-server`.
Server calls back to Maestro's API. Maestro routes to configured channel and responds
via MCP tool result.

---

### 🎚️ Runtime Plan/Build Mode Toggle

Per-run toggle between approval-gated "plan" mode and fully autonomous "build" mode,
changeable at runtime via API or dashboard.

**Why**: Trust calibration. Watch the first few runs in plan mode, then switch to build
mode once confident.

**To ship**: Runtime approval policy override (currently immutable after dispatch), API
endpoint, UI toggle.

---

## Workspace Management

### 📦 Per-Agent Workspace Init Scripts

Per-agent-type shell scripts that run after workspace creation (e.g., `npm install`,
`cp .env.example .env`, `pip install -r requirements.txt`).

**Why**: `hooks.after_create` is global. Different agent types on different repos need
different setup.

**To ship**: `workspace_init` field in `AgentTypeConfig` or pack `agent.yaml`. Executed
after `PrepareClone`, before `before_run` hook.

---

### 🧹 Automatic Workspace Cleanup

Periodic cleanup of workspaces for completed/failed runs with configurable retention.
Currently requires manual `maestro cleanup workspaces`.

**Why**: Disk accumulation, especially with Docker volumes.

**To ship**: Background goroutine, track workspace age via state, respect retention
config, run `before_remove` hook.

---

### 🌳 Git Worktree Strategy

Use `git worktree add` instead of full `git clone`. Share the base repository's object
store across all agent workspaces.

**Why**: Full clones per issue are expensive (disk, network, time). Worktrees share the
object database — creating a new workspace is instant after the first clone.

**To ship**: Base repo cache (one full clone per repo), `git worktree add -b <branch>
<path>`, cleanup via `git worktree remove`. New strategy: `workspace: git-worktree`.

---

### 🐾 Petname-Generated Workspace Names

Auto-generated memorable names (e.g., "brave-fox", "quiet-river") instead of sanitized
issue identifiers.

**Why**: UX touch for identifying workspaces in logs and dashboards.

---

## Communication & Approval Flow

### 📬 Message Queue with Auto-Dequeue

Queue incoming messages when an agent is busy (mid-execution) and automatically process
them sequentially when the current turn completes.

**Why**: Messages/approvals arriving mid-execution may be lost or require manual retry.

---

### 💬 Teams Channel Adapter

Microsoft Teams adapter for approval routing and agent-to-human messaging.

**Why**: Enterprise environments. The channel interface is designed for this —
implementation is the remaining work.

**To ship**: Teams Bot Framework SDK, adaptive card rendering for approvals, webhook
registration for responses.

---

### 💬 GitLab Issue Comments as Channel

Route approvals and messages as comments on the GitLab issue itself, with emoji
reactions or reply syntax for responses.

**Why**: Keeps all context in the tracker. No Slack/Teams dependency.

---

### 📧 Email Notifications

Approval requests via email with approve/reject links.

**Why**: Universal fallback channel.

---

## Security

### 🔒 Secrets via Tmpfs

Mount API tokens to `/run/secrets/<name>` on tmpfs (RAM-backed, never on disk) instead
of environment variables.

**Why**: Env vars are visible via `docker inspect`, process listings, and
`/proc/<pid>/environ`. Tmpfs secrets are invisible and auto-cleaned on container stop.

**To ship**: Tmpfs mount config in Docker harness, agents read from file path
(`GITLAB_TOKEN_FILE=/run/secrets/gitlab_token`).

---

### 🔒 Network Filtering Proxy

HTTP/HTTPS proxy outside the agent container allowing only configured domains and
blocking everything else.

**Why**: Prevents data exfiltration by prompt-injected agents. More flexible than
air-gapped `NetworkMode: "none"`.

**Config**: `sandbox.network.mode: filtered` + `allowed_domains` list.

---

### 🔒 Web Dashboard Authentication

Auth for the web API/dashboard. Currently localhost-only.

**Why**: Required for remote access. Options: bearer token, OAuth2 proxy, mTLS.

---

## Tracker Integrations

### 🔗 GitHub Issues / GitHub PRs

Tracker adapters for GitHub Issues and Pull Requests.

**Why**: GitHub is the most popular code hosting platform.

**To ship**: Implement `tracker.Tracker` using GitHub API. Filter by labels, assignees,
milestone. Lifecycle labels for state tracking.

---

### 🔗 Jira Adapter

Tracker adapter for Jira (Cloud and Server/Data Center).

**Why**: Most common enterprise issue tracker. Critical for adoption.

**To ship**: Implement `tracker.Tracker` using Jira REST API. Map statuses and labels to
Maestro's lifecycle model.

---

### ⏱️ Proactive Rate Limit Throttling

When tracker API remaining requests drop below a threshold (e.g., 10%), insert
proportional delays instead of waiting for 429 responses.

**Why**: Smoother degradation under load. Prevents poll failures.

**To ship**: Parse rate limit headers in tracker responses, expose quota in interface,
orchestrator inserts delays when low.

---

## Other Ideas

### Config Editing via Web UI

Edit `maestro.yaml` in the dashboard with syntax highlighting, validation preview, diff
view, and one-click apply. Extends the existing read-only viewer.

### Agent Pack Marketplace

Shareable, versioned agent packs via git repos or registry. `maestro install pack <url>`.

### Agent Router

A meta-agent that acts as a dispatch layer in front of other agents. Given a task, it
reads the issue, evaluates the available agent packs (descriptions, capabilities,
tooling), and routes the task to the best-fit agent — or rejects it if none qualify.
Replaces static label-based routing with LLM-driven classification.

**Why**: Static routing (source → agent_type via labels) breaks down when tasks are
ambiguous or span categories. An agent router can read the issue body, understand intent,
and make a judgment call — "this is a refactor, not a bug fix, send it to the
repo-maintainer agent" — without requiring the user to apply the right label. Also
enables a single catch-all source that intelligently fans out to specialized agents.

**To ship**: A built-in agent type (or agent pack) that receives the issue context and a
manifest of available agents (name, description, capabilities). Runs a lightweight LLM
call to classify and select. Returns the target agent type name. Orchestrator dispatches
to the selected agent instead of the statically-configured one. Config:
`agent_type: router` with `router.candidates: [code-pr, triage, repo-maintainer]`.
Fallback behavior when no agent matches (reject, default agent, or ask human).

### Distributed Workers

Agents on remote machines via SSH or Kubernetes Jobs. Significant architecture change.

### One-Click PR Creation

Dashboard button that instructs the agent to commit, push, and create a PR with
context-aware defaults (branch, target, diff summary).

### Sound / Desktop Notifications

Audio alerts and OS-level notifications when agents complete, fail, or need attention.

### Shared Tracker Client for Multi-Source

When multiple sources use the same tracker instance, share a single HTTP client to pool
connections and respect rate limits holistically.
