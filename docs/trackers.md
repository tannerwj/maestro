# Trackers

## Supported Today

| Tracker | Scope | Status |
| --- | --- | --- |
| GitLab | project issues | supported |
| GitLab | epics | supported |
| Linear | project issues | supported |

## GitLab Project Issues

Maestro’s current GitLab adapter polls:

```text
/api/v4/projects/:project/issues
```

That means the supported unit of work is a project issue, filtered by labels, assignee, or state.

Use GitLab when:

- the code lives in the same GitLab project you want to clone
- you want issue labels for ready/retry/done lifecycle markers
- you want the simplest end-to-end tracker-to-repo story

Sample config: [examples/gitlab-claude-auto.yaml](../examples/gitlab-claude-auto.yaml)

## GitLab Epics

GitLab epics are now supported as a separate tracker mode:

```yaml
sources:
  - tracker: gitlab-epic
    connection:
      base_url: https://gitlab.com
      group: your-group
    repo: https://gitlab.com/your-group/your-project.git
    epic_filter:
      # Optional exact epic targeting inside the group.
      # iids: [1, 7]
      labels: [bucket:ready]
    issue_filter:
      labels: [agent:ready]
      assignee: $MAESTRO_USER
```

Important differences from project issues:

- epics are polled at the GitLab group or subgroup level
- the source must provide `connection.group`
- the source must also provide `repo`, because an epic is not tied to one project clone target
- `epic_filter` defines the bucket of eligible epics
- `epic_filter.iids` can pin the source to one or more exact epic IIDs within the configured group
- `issue_filter` defines which linked child issues are actually eligible work
- Maestro dispatches the open project issues linked to those matching epics
- lifecycle labels and operational comments are written back to the linked issue, not the epic
- if the epic closes while a linked issue is running, reconciliation stops the run because the bucket became terminal
- `issue_filter.assignee` and `issue_filter.states` apply to the linked issue
- if you set both `epic_filter.iids` and `epic_filter.labels`, an epic must satisfy both
- if you use the legacy `filter` field with `gitlab-epic`, Maestro treats it as an epic bucket filter plus child-issue assignee/state fallback for backward compatibility
- `repo` must be a plain repo URL without embedded credentials; use `connection.token_env` for auth

Live validation status:

- the repo ships unit coverage and sample config for epic mode
- live epic polling, writeback, and reconciliation are validated against an epic-capable GitLab group

Sample config: [examples/gitlab-epic-claude-auto.yaml](../examples/gitlab-epic-claude-auto.yaml)
## Linear Project Issues

The current Linear adapter polls project issues and normalizes them into the same internal issue shape used by GitLab.

Because Linear issues are not inherently tied to a repo, the config must provide:

```yaml
sources:
  - tracker: linear
    repo: /path/or/url/to/repo.git
```

Project identification supports two forms. You can use `project_url` with a full Linear project URL:

```yaml
sources:
  - tracker: linear
    project_url: https://linear.app/myteam/project/my-project-abc123/issues
    repo: /path/or/url/to/repo.git
```

Or use `connection.project` with a project name or GraphQL project ID:

```yaml
sources:
  - tracker: linear
    connection:
      project: My Project
    repo: /path/or/url/to/repo.git
```

When `project_url` is set, Maestro extracts the project slug from the URL and resolves the project ID via GraphQL. If both are set, `project_url` takes priority.

### Linear State Transitions

`UpdateIssueState` is fully implemented. When lifecycle hooks include a `state` field, Maestro resolves the target workflow state ID per-issue by querying the issue's team workflow states via GraphQL. State name matching is case-insensitive. Resolved state IDs are cached per team.

Use Linear when:

- planning lives in Linear but code may live elsewhere
- you want to test the same orchestration loop against a non-GitLab tracker

Sample config: [examples/linear-codex-auto.yaml](../examples/linear-codex-auto.yaml)

## Dispatch Guard

Before dispatching an issue, Maestro re-fetches it from the tracker to verify it is still eligible. If the
issue has become terminal, no longer matches the source filter, or has gained a lifecycle label since the
last poll, dispatch is silently skipped. This prevents races between the poll interval and external tracker
changes (e.g., a human closing the issue between poll and dispatch).

## Tracker Writeback

Both supported trackers now have live-validated writeback for:

- operational comments
- active/retry/done/failed lifecycle labels
- reconciliation stop when the tracker issue becomes terminal
- reconciliation stop when an active item is explicitly marked `maestro:done` or `maestro:failed`

`maestro:done` and `maestro:failed` are also treated as intake blockers now. In practice, that means a completed or failed issue will not be picked up again by a fresh Maestro process unless you remove the lifecycle label or otherwise change your tracker workflow to make it eligible again.
