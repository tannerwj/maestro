# Maestro documentation

Start with the [project overview](../README.md). These guides describe the current runtime and configuration.

| Need | Read |
|---|---|
| Set up a first workflow | [Getting Started](getting-started.md) |
| Look up YAML fields | [Configuration reference](reference.md) |
| Configure agent packs and Docker | [Agents](agents.md) |
| Connect GitLab or Linear | [Trackers](trackers.md) |
| Run, inspect, recover, and use the dashboard | [Operator Guide](operator-guide.md) |
| Verify changes or run live checks | [Testing](../TESTING.md) |
| Review security and performance gaps | [Findings](../FINDINGS.md) |

## Examples

The YAML files in [`examples/`](../examples/) are starting points. Keep them there when running the commands in the guides: `agent_packs_dir` is resolved relative to each config file. If you copy an example elsewhere, adjust that path.

| Example | Tracker and agent |
|---|---|
| [GitLab + Claude](../examples/gitlab-claude-auto.yaml) | Smallest GitLab setup |
| [GitLab + Codex](../examples/gitlab-codex-auto.yaml) | Codex agent |
| [GitLab epic](../examples/gitlab-epic-claude-auto.yaml) | Epic and linked issues |
| [Linear + Codex](../examples/linear-codex-auto.yaml) | Smallest Linear setup |
| [Multi-source](../examples/multi-source-claude-auto.yaml) | Several trackers in one daemon |
| [Pipeline](../examples/gitlab-pipeline.yaml) | Implement, review, and merge routing |

More examples are in the same directory. The [archive](archive/README.md) holds historical design notes and older demo checklists; it is not the source of truth for current behavior.
