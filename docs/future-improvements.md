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

Docker execution is already shipped. The remaining Docker work should focus on stronger isolation,
better secret handling, and shared-deployment policy.

### 🐳 Higher-Level Network Presets

Build on the existing allowlist mode with clearer presets like:

- `model-only`
- `tracker-and-model`
- `custom-allowlist`

**Why**: Network access is still the biggest remaining safety gap for containerized agents.

**To ship**: preset definitions, simpler operator-facing config, and `maestro doctor` guidance for
common egress shapes.

---

### 🐳 Tmpfs / File-Backed Secret Delivery

Move beyond env-var injection for sensitive material by supporting:

- tmpfs-backed secret files under `/run/secrets/...`
- optional `_FILE` helper env wiring for common tools
- a clearer split between plain env config and true secret material

**Why**: Env vars are still inspectable inside the container and are a weaker boundary than file- or
tmpfs-backed secret delivery.

**To ship**: tmpfs-backed secret mounts layered on top of the existing `docker.secrets` config.

---

### 🐳 Stronger Image Policy

Go beyond tag-vs-digest checks toward stronger shared-deployment policy, such as:

- required digests by environment
- trusted registry allowlists
- clearer provenance guidance for published images

**Why**: Reproducibility and supply-chain safety matter more as Docker-backed agents become normal.

**To ship**: stronger doctor/policy enforcement around image provenance, not just mutable-tag
warnings.

### 🐳 Tiered Isolation Modes

Configurable isolation strength beyond the current Docker hardening baseline:

| Tier | Technology | Security | Use case |
|------|-----------|----------|----------|
| 0 | No isolation (current) | None | Dev / trusted environments |
| 1 | Docker + hardening | Good | Default for production |
| 2 | Docker + gVisor runtime | Strong | Untrusted code |
| 3 | Firecracker microVMs | Strongest | Enterprise / multi-tenant |

Docker's `HostConfig.Runtime` field swaps runtimes without changing the harness
interface. Tier 0 → 1 is the big lift; 1 → 2/3 is a config change.

---

### 🐳 Broader Warm Pools and Reuse Policy

Current reuse modes cover trusted stateless reuse and lineage reuse. Remaining work includes:

- profile-wide pre-created containers held ready for dispatch
- stronger reset semantics between shared stateless runs
- explicit pool sizing and eviction policy
- better status/debug visibility for reused containers

**Why**: Broader pooling could reduce cold-start latency further than the current reuse modes,
especially for high-frequency investigative workloads.

**When**: After the current Docker path is stable in broader use and dispatch latency becomes a
bottleneck.

---

### 🐳 Cache Policy and Lifecycle Controls

Current cache mounts are shipped. Remaining work is mostly around stronger policy and lifecycle
management, for example:

- clearer cache retention and cleanup controls
- per-hook or per-step cache scoping
- better debugging/status visibility for active cache mounts

---

## Configuration & Developer Experience

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

### ⚡ Webhook-Driven Dispatch

Accept inbound webhooks from trackers (GitLab, Linear) to trigger immediate dispatch
instead of waiting for the next poll cycle.

**Why**: Reduces dispatch latency from poll_interval (30-60s) to near-instant.

**To ship**: HTTP endpoint, webhook signature verification, event filtering, coexist with
polling. Config: `webhooks.enabled`, `webhooks.secret_env`.

---

### 🔄 Claude approval-session resume cleanup

Claude multi-turn continuation now resumes persisted sessions between completed turns.
One remaining refinement is to resume a permission-blocked turn directly instead of
rerunning that prompt from the last completed session after approval.

---

### 🔁 True Harness Resume After Maestro Restart

Maestro already preserves workspace state and converts interrupted active runs into
immediate retries after restart. What it does not do yet is reattach to the same live
harness session.

**Why**: Restart recovery currently behaves like "start a fresh run in the same
checkout." That is useful, but it is weaker than a true `/resume` of the original
Claude or Codex session.

**To ship**: Persist enough harness session identity to reconnect to the original
Claude/Codex session or thread after Maestro restarts, and fall back to the current
workspace-based retry path only if reattachment fails. This would also need a clear
policy for restoring pending approvals/messages versus invalidating them when the
underlying session cannot be resumed safely.

---

## Observability & Dashboard

### 🎨 Rich Run Status Indicators

Granular status taxonomy: streaming, waiting_input, needs_attention, ready, idle, failed
— each with distinct visual treatment (colors, pulses, badges).

**Why**: Current statuses (pending, preparing, active, awaiting_approval, done, failed)
don't distinguish "agent is thinking" from "agent needs human input" at a glance.

**To ship**: Extend `RunStatus` enum, harness adapters emit finer-grained status from
stream events, frontend renders distinct indicators.

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

### 💻 Operator-Executed Command Requests

Allow agents to ask Maestro for an operator-executed local command when they are blocked on
interactive or operator-owned tooling, for example:

- browser/device login flows
- MFA-gated cloud CLIs
- local-only credentials or hardware-backed auth
- commands that require a real TTY or stdin interaction

Instead of repeatedly failing a non-interactive command, the agent would emit a structured
request containing:

- the exact command to run
- working directory
- why it is needed
- what output or result should be pasted back

Maestro would surface that request through the existing operator channels (TUI/web/Slack)
as a pending control item. The operator could then approve it, run it locally in their own
shell, and reply with the result for the agent to continue.

**Why**: Some valuable workflows require operator-owned local context that should not be
proxied through the agent runtime. A structured handoff is safer and more reliable than
trying to force every interactive CLI through the harness.

**To ship**: New operator-command message kind, a structured prompt contract for agents,
UI rendering for copy/run/reply flow, and reply handling that feeds command results back
into the active run.

**Follow-up**: Capture and surface the last interactive/local-only command that failed so
stderr-only errors like `write_stdin failed: stdin is closed for this session` are
actionable in the TUI/web/API even when the command never became a formal approval
request.

**Related improvement**: detect long quiet single-turn runs that have likely already
finished the meaningful work. For example, if a review agent has already merged the PR
or the workspace is clean on the target branch but the harness remains active with no new
output or metrics, surface that as a likely-lingering run so the operator can stop it
confidently.

---

### 💬 Teams Channel Adapter

Microsoft Teams adapter for approval routing and agent-to-human messaging.

**Why**: Enterprise environments. The channel interface is designed for this —
implementation is the remaining work.

**To ship**: Teams Bot Framework SDK, adaptive card rendering for approvals, webhook
registration for responses.

---

### 🔌 Generic Channel Provider Abstraction

Extract the Slack-first bridge into a reusable provider interface once there is a second
real production channel with similar requirements.

**Why deferred**: Slack is still the only fully exercised interactive channel. A generic
abstraction now would be speculative and likely weaker than the concrete behavior we
already know we need.

**To ship**: Introduce a narrow provider boundary around thread persistence, outbound
approval/message rendering, inbound action handling, and channel/user resolution after
Slack and one more production channel have both proven those responsibilities stable.

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

### 🔒 Enforced Container Egress Policy

Docker `network_policy.mode: allowlist` currently sets proxy variables for HTTP/HTTPS
clients on bridge networking. Direct sockets can bypass the proxy.

**Why**: A network boundary must cover direct connections before the allowlist can be
treated as an isolation control. See [finding S3](../FINDINGS.md).

**To ship**: Enforce egress at the namespace or daemon layer and verify it with a
Docker-backed bypass test. Until then, use `network_policy.mode: none` or external
egress controls when isolation is required.

---

### 🔒 Stronger Remote Dashboard Authentication

Non-loopback binds already require a bearer API key. The remote deployment path still
needs TLS, key lifecycle controls, and narrower permissions for read-only users.

**Why**: The current key grants config and run-control access along with status reads.
See [finding S7](../FINDINGS.md).

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

### 🚨 Sentry Source Adapter

Treat Sentry as a filtered operational work source for high-signal issue groups, not as a
raw event firehose.

**Why**: Sentry already has the right primitives for “something actionable is broken now”:
issue groups, assignees, comments/activity, unresolved/resolved/ignored states, and rich
stack trace context. It is a good fit for investigation, regression response, and
auto-fix workflows when the intake is tightly scoped.

**Recommended scope**:
- Poll Sentry issue groups, not individual events
- Support high-signal filters only:
  - project
  - environment
  - status (`unresolved`, optionally `ignored` / `resolved`)
  - regression-only or inbox/new issue style queries
  - minimum event/user count thresholds
  - assignee / owner filters where available
- Keep lifecycle light:
  - unresolved
  - resolved
  - ignored
- Optionally add comments/activity back to Sentry, but do not force a heavyweight workflow
  model onto it

**Why this shape**: Sentry's own docs and ecosystem integrations mostly treat it as the
source of operational signals that then either trigger action directly or sync into a
planning tracker. Their issue APIs are centered around filtered issue lists and light
state mutation, and their Linear/Jira integrations are geared around creating and syncing
external work rather than replacing a dedicated project tracker.

**To ship**:
- Implement `tracker.Tracker` using Sentry's issue APIs
- Map a Sentry issue/group into `domain.Issue`
- Support polling via organization issue listing plus query/project/environment filters
- Support minimal state writeback via `resolved` / `unresolved` / `ignored`
- Parse rate-limit headers and keep polling conservative
- Add Sentry-specific filter config instead of overloading generic label semantics

**Likely config**:
`tracker: sentry` with `connection.base_url`, `connection.org`, `connection.project`,
`token_env`, and filter fields such as `environment`, `query`, `regression_only`,
`min_events`, `min_users`, and `assigned_to`.

**Non-goals for v1**:
- raw event ingestion
- incident/on-call escalation replacement
- trying to mirror every Sentry alert rule in Maestro config
- forcing full planning-tracker semantics (milestones, elaborate workflow states, etc.)

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
