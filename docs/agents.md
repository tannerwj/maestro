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
  model: opus-4.6
  reasoning: high
  max_turns: 1
  extra_args: ["--verbose"]

codex:
  model: gpt-5.4
  reasoning: high
  max_turns: 20
  thread_sandbox: workspace-write
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
  max_turns: 20
  thread_sandbox: workspace-write

claude_defaults:
  model: opus-4.6
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

Claude is currently single-turn only. The effective `claude.max_turns` must resolve to `1`.

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

## Prompt Template Data

Prompt files are Go text templates. The runtime passes:

- `.Issue`
- `.User`
- `.Agent`
- `.Source`
- `.Attempt`
- `.AgentName`

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
- [agents/dev-codex/agent.yaml](../agents/dev-codex/agent.yaml) — full Symphony-style Codex workflow
- [agents/repo-maintainer/agent.yaml](../agents/repo-maintainer/agent.yaml)
- [agents/triage/agent.yaml](../agents/triage/agent.yaml)

Example configs:

- [examples/gitlab-claude-auto.yaml](../examples/gitlab-claude-auto.yaml)
- [examples/gitlab-repo-maintainer.yaml](../examples/gitlab-repo-maintainer.yaml)
- [examples/linear-triage.yaml](../examples/linear-triage.yaml)

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
