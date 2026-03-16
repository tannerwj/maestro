# Demo Walkthroughs

## GitLab + Claude

1. Set `MAESTRO_GITLAB_TOKEN`.
2. Copy [demo/gitlab-claude-auto/maestro.yaml](../demo/gitlab-claude-auto/maestro.yaml) and update the tracker fields.
3. Create one matching GitLab issue with the configured label.
4. Make sure that label only matches the one demo issue you want to run. A temporary demo-specific label is the safest option.
5. Start Maestro:

```bash
make run CONFIG=demo/gitlab-claude-auto/maestro.yaml
```

6. Watch the run in the TUI until the run is terminal. If you are using headless mode, wait for `maestro inspect runs` to show `Active: none`.
7. Inspect the result:

```bash
make inspect-runs CONFIG=demo/gitlab-claude-auto/maestro.yaml
make inspect-state CONFIG=demo/gitlab-claude-auto/maestro.yaml
```

8. If you want to reset the local demo state:

```bash
make reset-issue CONFIG=demo/gitlab-claude-auto/maestro.yaml ISSUE=group/project#123
make cleanup-workspaces CONFIG=demo/gitlab-claude-auto/maestro.yaml
```

## Linear + Claude

1. Set `MAESTRO_LINEAR_TOKEN`.
2. Copy [demo/linear-claude-auto/maestro.yaml](../demo/linear-claude-auto/maestro.yaml) and update the tracker fields.
3. Create one matching Linear issue in the configured project/state.
4. Make sure the filter only matches that one demo issue. A temporary demo-specific label is the safest option.
5. Start Maestro:

```bash
make run CONFIG=demo/linear-claude-auto/maestro.yaml
```

6. Watch the run in the TUI until the run is terminal. If you are using headless mode, wait for `maestro inspect runs` to show `Active: none`.
7. Inspect the result:

```bash
make inspect-runs CONFIG=demo/linear-claude-auto/maestro.yaml
make inspect-state CONFIG=demo/linear-claude-auto/maestro.yaml
```

8. If you want to reset the local demo state:

```bash
make reset-issue CONFIG=demo/linear-claude-auto/maestro.yaml ISSUE=TAN-123
make cleanup-workspaces CONFIG=demo/linear-claude-auto/maestro.yaml
```

## Multi-Source

1. Set both `MAESTRO_GITLAB_TOKEN` and `MAESTRO_LINEAR_TOKEN`.
2. Copy [demo/multi-source-claude-auto/maestro.yaml](../demo/multi-source-claude-auto/maestro.yaml) and update the tracker fields.
3. Prepare three isolated work items:
   - one GitLab project issue
   - one GitLab epic with one linked child issue
   - one Linear issue
4. Use dedicated labels so each source only sees the item you intend for that demo.
5. Start Maestro:

```bash
make run CONFIG=demo/multi-source-claude-auto/maestro.yaml
```

6. Inspect the source-scoped state:

```bash
make inspect-runs CONFIG=demo/multi-source-claude-auto/maestro.yaml
make inspect-state CONFIG=demo/multi-source-claude-auto/maestro.yaml
```

For a denser real-world starting point with shared defaults and six sources, use:

- [demo/many-sources-claude-auto/maestro.yaml](../demo/many-sources-claude-auto/maestro.yaml)

## Notes

- `reset issue` only clears local Maestro state and local workspaces. It does not reopen or relabel the tracker item.
- `reset issue` refuses to touch the currently active run. Wait until `maestro inspect runs` shows `Active: none`.
- `cleanup workspaces` preserves the currently active workspace from `runs.json`.
- For the simplest live demo, keep `approval_policy: auto`.
