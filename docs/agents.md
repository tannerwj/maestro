# Agents

## Current Model

Maestro now supports multiple configured agent types in one config, mapped across multiple sources.

The supported model is:

- define one or more `agent_types`
- optionally point it at an `agent_pack`
- let the pack provide the default prompt, context, tools, skills, harness config, and optional defaults
- map each source to the agent type it should use
- override only the fields you want to change in the config

## Pack Layout

A local pack is a directory with an `agent.yaml` plus any referenced files:

```text
agents/
  repo-maintainer/
    agent.yaml
    prompt.md
    context.md
    claude/
      CLAUDE.md
    codex/
      skills/
```

Example pack file:

```yaml
name: repo-maintainer
description: Repository maintenance agent
instance_name: repo-maintainer
harness: claude-code
workspace: git-clone
prompt: prompt.md
approval_policy: manual
approval_timeout: 24h
max_concurrent: 1
tools:
  - formatters
  - linters
skills:
  - dependency hygiene
context_files:
  - context.md
env:
  GOFLAGS: -mod=mod

# Harness-specific config (only the matching harness block is used)
claude:
  model: claude-opus-4-6
  reasoning: high
  max_turns: 1
  extra_args: ["--verbose"]

codex:
  model: gpt-5.4
  reasoning: high
  max_turns: 1
  # thread_sandbox: workspaceWrite  # optional: overrides approval_policy-derived sandbox

docker:
  image: ghcr.io/acme/maestro-agent:latest
  workspace_mount_path: /workspace
  network: bridge                   # bridge (default), none, or host
  cpus: 2
  memory: 4g
  pids_limit: 256
  env_passthrough:
    - ANTHROPIC_API_KEY
  mounts:
    - source: ~/.config/claude
      target: /home/agent/.config/claude
      read_only: true
```

Optional pack directories:

- `claude/` is copied into the prepared workspace as `.claude/`
- `codex/` is copied into the prepared workspace as `.codex/`
- these directories are where harness-native instructions, skills, and settings live

Repo-embedded packs are also supported for `workspace: git-clone` agents. Instead of resolving from
`agent_packs_dir`, Maestro can resolve agent environment files from the cloned repository itself.

## Config Usage

Point the config at a pack root, then reference a local pack by name:

```yaml
agent_packs_dir: ../agents

# Harness defaults apply to all agents using the respective harness
codex_defaults:
  model: gpt-5.4
  reasoning: high
  max_turns: 1

claude_defaults:
  model: claude-opus-4-6
  reasoning: high
  max_turns: 1

sources:
  - name: project-a
    tracker: gitlab
    agent_type: repo-maintainer

agent_types:
  - name: repo-maintainer
    agent_pack: repo-maintainer
    instance_name: maintainer
    approval_policy: manual
    claude:                          # per-agent override wins over claude_defaults
      model: sonnet-4.5
```

Harness config resolution order:

1. Per-agent `codex:` or `claude:` block (highest priority)
2. Pack `codex:` or `claude:` block
3. Top-level `codex_defaults` or `claude_defaults`
4. Built-in defaults

Docker config resolution order:

1. Per-agent `docker:` block (highest priority)
2. Pack `docker:` block
3. Built-in Docker defaults for omitted fields (`workspace_mount_path: /workspace`, `network: bridge`)

Resolution rules:

- if `agent_pack` is a bare name, Maestro resolves it under `agent_packs_dir`
- if `agent_pack` looks like a path, Maestro resolves it relative to the config file
- pack-relative `prompt` and `context_files` paths are resolved from the pack directory
- if `agent_pack` starts with `repo:`, Maestro resolves the pack after clone from that repo-relative path
- `agent_pack: "repo:"` and `agent_pack: "repo:.maestro"` both use `.maestro/` in the cloned repo

Example repo-embedded pack:

```yaml
sources:
  - name: project-a
    tracker: linear
    repo: https://gitlab.example.com/team/project.git
    agent_type: code-pr

agent_types:
  - name: code-pr
    agent_pack: "repo:.maestro"
    harness: codex
    workspace: git-clone
    approval_policy: auto
    max_concurrent: 1
```

With a matching repository layout:

```text
.maestro/
  prompt.md
  context/
    rules.md
  claude/
    CLAUDE.md
  codex/
    skills/
```

## Merge Rules

Pack defaults fill in missing agent fields.

Config values win over pack defaults for:

- `instance_name`
- `harness`
- `workspace`
- `prompt`
- `approval_policy`
- `approval_timeout`
- `max_concurrent`
- `stall_timeout`
- `env`

Pack and config values are combined for:

- `tools`
- `skills`
- `context_files`

`codex:` and `claude:` blocks from packs provide harness-specific defaults. Per-agent type overrides
in `maestro.yaml` win over pack values for the same harness block. Top-level `codex_defaults` and
`claude_defaults` fill any remaining gaps.

`docker:` blocks from packs provide execution defaults for containerized harness runs. Per-agent
`docker:` values in `maestro.yaml` override pack values field-by-field.

Claude multi-turn runs reuse the saved Claude session via `--resume` between turns. Set
`claude.max_turns` above `1` when you want continuation behavior.

Loaded context file contents are concatenated into `.Agent.Context` for prompt templates.

For repo-embedded packs, resolution happens in two phases:

1. Maestro reads orchestration fields from `maestro.yaml` before clone.
2. After clone, Maestro reads `prompt.md`, `context/`, `claude/`, and `codex/` from the repo pack.

That means these fields must stay in `maestro.yaml` for repo packs:

- `harness`
- `workspace`
- `approval_policy`
- `approval_timeout`
- `max_concurrent`

## Docker Execution

Docker mode keeps Maestro orchestration on the host and containerizes only the harness process.

- tracker polling, lifecycle/state updates, workspace preparation/reuse, prompt rendering, approval routing, message routing, and state persistence all stay on the host
- the prepared host workspace is bind-mounted into the container and used as the harness working directory
- git changes stay visible on the host immediately; there is no copy-back step
- Maestro runs the container as the current host uid/gid where supported so workspace files keep usable ownership

Docker config:

- `docker.image`: required image that already contains `claude` or `codex`
- `docker.image_pin_mode`: `allow` (default) or `require`; `maestro doctor` still warns on mutable tag-based references either way
- `docker.workspace_mount_path`: optional container path for the workspace bind mount
- `docker.pull_policy`: `missing` (default), `always`, or `never`
- `docker.network`: legacy coarse mode fallback: `bridge`, `none`, or `host`
- `docker.network_policy`: optional phase-2 policy layer:
  - `mode: none`: force `--network none`
  - `mode: bridge`: force Docker bridge networking
  - `mode: allowlist`: keep bridge networking and inject a Maestro-managed HTTP/HTTPS proxy with an explicit host/domain allowlist
  - `allow`: required for `allowlist`; accepts exact hosts/domains or `*.suffix.example` patterns
- `docker.cpus`, `docker.memory`, `docker.pids_limit`: basic resource limits
- `docker.secrets`: preferred structured allowlist for extra host env vars or read-only secret/config mounts
  - `env[]`: explicit env allowlist entries via `preset` or `source`/`target`
  - `mounts[]`: explicit read-only secret/config mounts via `source`/`target` or a target `preset`
- `docker.tools`: preferred structured allowlist for extra read-only tool/config mounts
  - `mounts[]`: explicit read-only mounts via `source`/`target` or a target `preset`
- `docker.env_passthrough`: legacy raw env injection path, still supported for compatibility
- `docker.mounts`: legacy explicit read-only bind mounts, still supported for compatibility
- `docker.auth`: presets for common auth flows
- `docker.security`: named presets plus raw hardening overrides
- `docker.cache`: optional writable cache mounts and common cache presets
- `docker.reuse`: explicit container reuse policy
  - `mode: none`: default; fresh container per run
  - `mode: stateless`: reuse a trusted shared container for matching Docker profiles; Maestro resets `/tmp/maestro-runs` between runs and stops the container after each run so stray processes are cleared
  - `mode: lineage`: reuse only within the same issue/workspace lineage so retries and continuations can keep mutable workspace/home/cache state without crossing tickets

Authentication patterns:

- subscription-backed CLI auth: use `docker.auth.mode: claude-config-mount` or `codex-config-mount` with a minimal read-only host config mount
- Claude API-key auth: use `docker.auth.mode: claude-api-key`
- Claude proxy/gateway auth: use `docker.auth.mode: claude-proxy` and set `ANTHROPIC_BASE_URL` when the gateway is not `https://api.anthropic.com`
- Codex API-key auth: use `docker.auth.mode: codex-api-key`
- prefer `docker.secrets` and `docker.tools` for additional access beyond `docker.auth`; reserve raw `docker.env_passthrough` and `docker.mounts` for compatibility with older configs
- Maestro keeps the container home writable by default and does not mount the operator's full home directory
- if `docker.security` is omitted, Maestro applies the `default` preset: `no_new_privileges: true`, `read_only_root_fs: true`, `drop_capabilities: [ALL]`, `tmpfs: [/tmp]`
- `docker.security.preset: locked-down` keeps the default baseline and adds `/var/tmp` as tmpfs
- `docker.security.preset: compat` keeps `no_new_privileges: true` but drops the read-only rootfs, cap-drop, and tmpfs baseline so you can opt back into individual controls as needed
- raw `docker.security.*` fields apply on top of the selected preset
- if `HOME` is not provided explicitly, Maestro gives the container a writable local `HOME` automatically
- `docker.network_policy.mode: allowlist` is intentionally scoped to HTTP/HTTPS egress through a Maestro-managed proxy; it is not a general-purpose firewall product
- Direct sockets can bypass that proxy on bridge networking. Use `network_policy.mode: none` or external egress controls when network isolation is required; see [finding S3](../FINDINGS.md).
- allowlist mode rejects conflicting proxy env configuration (`HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY`, `NO_PROXY`, including lowercase variants) because Maestro owns those env vars in that mode
- allowlist mode requires bridge networking; `maestro doctor` verifies support for the required Docker host-gateway resolution where possible
- `maestro doctor` also shows the effective Docker env injections and read-only secret/tool mounts each agent will receive
- `maestro doctor` warns when `docker.reuse.mode: stateless` is combined with risky settings such as mutable repo workspaces, broad env passthrough, broad network access, or permissive security profiles
- cache presets are available for common language/tool caches:
  - `claude-cache`
  - `codex-cache`
  - `npm-cache`
  - `go-cache`
  - `pip-cache`
  - `cargo-cache`

Example:

```yaml
agent_types:
  - name: dev-claude-docker
    agent_pack: dev-claude
    harness: claude-code
    workspace: git-clone
    approval_policy: manual
    docker:
      image: ghcr.io/acme/maestro-claude@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
      image_pin_mode: require
      workspace_mount_path: /workspace
      pull_policy: missing
      network: none
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
        preset: default
        read_only_root_fs: true
        tmpfs: [/tmp]
      cache:
        profiles: [claude-cache]
      reuse:
        mode: none
```

Concrete examples:

Claude in Docker using bearer-token proxy auth:

```yaml
agent_types:
  - name: dev-claude-docker
    agent_pack: dev-claude
    harness: claude-code
    workspace: git-clone
    approval_policy: manual
    claude:
      model: claude-opus-4-6
      reasoning: high
    docker:
      image: ghcr.io/acme/maestro-claude:latest
      workspace_mount_path: /workspace
      network: bridge
      cpus: 2
      memory: 4g
      pids_limit: 256
      auth:
        mode: claude-api-key

Fast investigative Codex agent using stateless reuse:

```yaml
agent_types:
  - name: ops-codex-stateless
    harness: codex
    workspace: none
    approval_policy: auto
    docker:
      image: ghcr.io/acme/maestro-codex@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
      network_policy:
        mode: allowlist
        allow: [api.openai.com, api.linear.app, gitlab.example.com]
      security:
        preset: default
      auth:
        mode: codex-api-key
      tools:
        mounts:
          - preset: git-config
            source: ~/.gitconfig
      reuse:
        mode: stateless
```

Coding agent using lineage reuse:

```yaml
agent_types:
  - name: repo-coder-lineage
    harness: claude-code
    workspace: git-clone
    approval_policy: manual
    docker:
      image: ghcr.io/acme/maestro-claude@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
      workspace_mount_path: /workspace
      network: none
      security:
        preset: default
      cache:
        profiles: [claude-cache, go-cache]
      auth:
        mode: claude-config-mount
        source: ~/.claude
      reuse:
        mode: lineage
```

Reuse guidance:

- `stateless` is for trusted, low-mutation investigative or reporting workflows; it is not the recommended default for code-modifying agents
- `lineage` keeps reuse scoped to the same issue/workspace lineage and is the safer choice when retries or continuations benefit from preserved workspace/home/cache state
- when no compatible reusable container is available, Maestro falls back to the normal cold `docker run` path instead of waiting indefinitely
        source: ANTHROPIC_API_KEY
      secrets:
        env:
          - preset: anthropic-base-url
      cache:
        profiles: [claude-cache]
```

Codex in Docker using an OpenAI-compatible proxy:

```yaml
agent_types:
  - name: dev-codex-docker
    agent_pack: dev-codex
    harness: codex
    workspace: git-clone
    approval_policy: manual
    codex:
      model: openai/gpt-5-mini
      reasoning: high
      extra_args:
        - --config
        - forced_login_method="api"
        - --config
        - openai_base_url="https://llm-proxy.example.com"
    docker:
      image: ghcr.io/acme/maestro-codex:latest
      workspace_mount_path: /workspace
      network: bridge
      cpus: 2
      memory: 4g
      pids_limit: 256
      auth:
        mode: codex-api-key
        source: OPENAI_API_KEY
      cache:
        profiles: [codex-cache]
```

Docker with allowlisted egress:

```yaml
agent_types:
  - name: dev-codex-allowlist
    agent_pack: dev-codex
    harness: codex
    workspace: git-clone
    approval_policy: manual
    codex:
      model: gpt-5.4
      reasoning: high
    docker:
      image: ghcr.io/acme/maestro-codex@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
      network_policy:
        mode: allowlist
        allow:
          - api.openai.com
          - "*.openai.com"
      auth:
        mode: codex-api-key
        source: OPENAI_API_KEY
      cache:
        profiles: [codex-cache]
```

For Codex proxy mode:

- use a model name that your proxy actually exposes
- pass `forced_login_method="api"` so the CLI does not prefer a stored ChatGPT login
- pass `openai_base_url` via `codex.extra_args`; the bare `OPENAI_BASE_URL` env path is deprecated in Codex CLI
- Maestro will synthesize container-local Codex API-key auth state when `docker.auth.mode: codex-api-key` is set

Current phase-1 limits:

- only the harness process is containerized by default; set `hooks.execution: container` if outer Maestro hooks should run in the same Docker environment
- additional `docker.mounts` entries must be `read_only: true`
- raw `docker.env_passthrough` and `docker.mounts` remain broad compatibility paths; prefer `docker.secrets` and `docker.tools` for explicit allowlists
- Docker availability is checked when the harness is constructed; if `docker` is missing, startup fails with a direct error

## Prompt Template Data

Prompt files are Go text templates. The runtime passes:

- `.Issue`
- `.User`
- `.Agent`
- `.Source`
- `.Attempt`
- `.AgentName`
- `.OperatorInstruction`

Useful `.Agent` fields now include:

- `.Agent.Name`
- `.Agent.Description`
- `.Agent.Tools`
- `.Agent.Skills`
- `.Agent.Context`
- `.Agent.ApprovalPolicy`
- `.Agent.ApprovalTimeout`

Template FuncMap helpers:

| Helper | Usage | Description |
|---|---|---|
| `default` | `{{default "none" .Issue.Description}}` | Returns first arg if second is empty/nil |
| `join` | `{{join .Issue.Labels ", "}}` | Join string slice with separator |
| `lower` | `{{lower .Issue.State}}` | Lowercase |
| `upper` | `{{upper .Issue.State}}` | Uppercase |
| `trim` | `{{trim .Issue.Title}}` | Trim whitespace |
| `contains` | `{{if contains .Issue.Title "bug"}}` | String contains check |
| `hasPrefix` | `{{if hasPrefix .Issue.Identifier "ENG-"}}` | String prefix check |
| `indent` | `{{indent 4 .Issue.Description}}` | Indent each line by N spaces |

## Approval Timeout

`approval_timeout` is configurable per agent type and defaults to `24h`.

- when an approval request stays unresolved past `requested_at + approval_timeout`, Maestro marks it as timed out
- the timed-out approval is recorded in approval history with outcome `timed_out`
- the active run is stopped and finishes as failed instead of waiting indefinitely
- on restart, Maestro applies the same timeout check to persisted pending approvals before deciding whether to retry a recovered run

## Tools And Skills

In the current build, `tools` and `skills` are declarative metadata, not runtime capability gates.

That means:

- the harness still determines what is actually executable
- approval policy still determines what needs review
- `tools`, `skills`, and `context` help standardize prompts and operator expectations

This is still valuable because it gives you one place to encode:

- repo conventions
- preferred commands
- review rules
- domain-specific reminders

## Built-In Packs

The repo now ships with:

- [agents/code-pr/agent.yaml](../agents/code-pr/agent.yaml)
- [agents/dev-claude/agent.yaml](../agents/dev-claude/agent.yaml) — general-purpose Claude Code implementation agent
- [agents/dev-codex/agent.yaml](../agents/dev-codex/agent.yaml) — general-purpose Codex implementation agent
- [agents/review-claude/agent.yaml](../agents/review-claude/agent.yaml) — automated code review + squash-merge agent
- [agents/repo-maintainer/agent.yaml](../agents/repo-maintainer/agent.yaml)
- [agents/triage/agent.yaml](../agents/triage/agent.yaml)
- [agents/access-reviewer/agent.yaml](../agents/access-reviewer/agent.yaml) — IAM access review and compliance
- [agents/query-optimizer/agent.yaml](../agents/query-optimizer/agent.yaml) — multi-engine query optimization
- [agents/vuln-triage/agent.yaml](../agents/vuln-triage/agent.yaml) — vulnerability triage and remediation
- [agents/demo-app-bootstrap/agent.yaml](../agents/demo-app-bootstrap/agent.yaml) — new app scaffolding

`dev-claude` and `dev-codex` both use a 6-phase prompt structure: Orient, Plan, Implement, Validate, Publish, Complete. Each phase has explicit gates and guardrails for unattended execution. Retry attempts resume from the existing workspace state rather than starting fresh.

`review-claude` is designed for workflow chaining. A coding source dispatches `dev-codex` or `dev-claude` to implement the work, and on completion the lifecycle labels route the issue to a review source that dispatches `review-claude`. The review agent verifies tests pass, reviews the diff, and squash-merges passing work. Failing work gets routed back with actionable review comments.

Example configs:

- [examples/gitlab-claude-auto.yaml](../examples/gitlab-claude-auto.yaml)

## Making Your Own Pack

1. Create a new directory under your pack root.
2. Add `agent.yaml`.
3. Add `prompt.md`.
4. Add one or more `context_files` if the agent needs durable repo or domain guidance.
5. Point `agent_packs_dir` at that root and set `agent_pack` in the config.
6. Override only the fields that should differ for a specific deployment.

If you want agent behavior versioned with application code, put the pack in the repo under
`.maestro/` and set `agent_pack: "repo:.maestro"` instead.

## Practical Recommendation

For a good first custom pack:

1. start from `agents/code-pr`
2. rename it for the job you actually want
3. move durable repo/process rules into `context.md`
4. keep the prompt focused on the task loop
5. map each source to that pack via `agent_type`
6. only change harness or approval policy when you have a concrete reason
