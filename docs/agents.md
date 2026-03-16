# Agents

## Current Model

Maestro now supports multiple configured agent types in one config, mapped across multiple sources.

The supported model is:

- define one or more `agent_types`
- optionally point it at an `agent_pack`
- let the pack provide the default prompt, context, tools, skills, harness, and approval policy
- map each source to the agent type it should use
- override only the fields you want to change in the config

## Pack Layout

A pack is a directory with an `agent.yaml` plus any referenced files:

```text
agents/
  repo-maintainer/
    agent.yaml
    prompt.md
    context.md
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
```

## Config Usage

Point the config at a pack root, then reference a pack by name:

```yaml
agent_packs_dir: ../agents

sources:
  - name: project-a
    tracker: gitlab
    agent_type: repo-maintainer

agent_types:
  - name: repo-maintainer
    agent_pack: repo-maintainer
    instance_name: maintainer
    approval_policy: manual
```

Resolution rules:

- if `agent_pack` is a bare name, Maestro resolves it under `agent_packs_dir`
- if `agent_pack` looks like a path, Maestro resolves it relative to the config file
- pack-relative `prompt` and `context_files` paths are resolved from the pack directory

## Merge Rules

Pack defaults fill in missing agent fields.

Config values win over pack defaults for:

- `instance_name`
- `harness`
- `workspace`
- `prompt`
- `approval_policy`
- `max_concurrent`
- `stall_timeout`
- `env`

Pack and config values are combined for:

- `tools`
- `skills`
- `context_files`

Loaded context file contents are concatenated into `.Agent.Context` for prompt templates.

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

## Practical Recommendation

For a good first custom pack:

1. start from `agents/code-pr`
2. rename it for the job you actually want
3. move durable repo/process rules into `context.md`
4. keep the prompt focused on the task loop
5. map each source to that pack via `agent_type`
6. only change harness or approval policy when you have a concrete reason
