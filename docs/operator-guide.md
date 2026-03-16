# Operator Guide

## Run Modes

TUI mode:

```bash
go run ./cmd/maestro run --config /path/to/maestro.yaml
```

Headless mode:

```bash
go run ./cmd/maestro run --config /path/to/maestro.yaml --no-tui
```

Installed binary:

```bash
maestro run --config /path/to/maestro.yaml
```

## TUI Controls

- `j` or down arrow selects the next pending approval
- `k` or up arrow selects the previous pending approval
- `a` approves the selected pending approval
- `r` rejects the selected pending approval
- `q` quit

## State And Logs

Important directories:

- `workspace.root`: checked out workspaces
- `state.dir`: persisted `runs.json`
- `logging.dir`: log files
- `logging.max_files`: local log retention cap; older `*.log` files are pruned on startup

If the config has multiple sources, Maestro stores state per source under:

- `state.dir/<source-name>/runs.json`

The process keeps enough state to:

- retry failed runs
- retry runs stopped by stall detection
- suppress already-finished issues until the tracker item changes
- recover an interrupted active run as a retry after restart
- preserve recent approval history across restart

The current runtime allows multiple sources in one config. Concurrency is bounded by:

- `defaults.max_concurrent_global`
- `agent_types[].max_concurrent`

Stall detection uses the configured inactivity timeout:

- `defaults.stall_timeout` for the shared default
- `agent_types[].stall_timeout` for a per-agent override

If a run stops producing observable output for longer than that window, Maestro stops it and schedules a retry.

Supported hook phases:

- `hooks.after_create`
- `hooks.before_run`
- `hooks.after_run`

All hooks run through the local shell and share `hooks.timeout`.

## Normal Demo Flow

1. Create or label one tracker issue so it matches the source filter.
2. Start Maestro.
3. Watch the active run in the TUI or logs.
4. If using manual approval, approve the request.
5. Inspect the workspace branch and tracker comments/labels.

If Maestro restarts while an approval is pending, the request is preserved in local state as stale history and the interrupted run is recovered as a retry. That gives the operator context without pretending the old approval can still be resolved.

## Shutdown

Use `Ctrl-C` or stop the process cleanly. Maestro will:

- stop the active harness
- persist state
- pick the run back up as a retry on the next start if needed

## Troubleshooting

No issues found:

- verify the tracker token env var is set
- verify the filter matches a real open issue
- verify the tracker project ID or path is correct

Run never starts:

- verify the harness binary is installed and authenticated
- verify the prompt path or `agent_pack` path is valid
- verify the source repo metadata is present

Run restarts unexpectedly:

- inspect `state.dir/runs.json`
- inspect the latest log file in `logging.dir`

Run stalls and gets retried:

- inspect recent log events for the last observed activity and stop reason
- increase `stall_timeout` if the agent regularly spends long periods without output
- move long local setup into `hooks.before_run` only if it is expected and bounded by `hooks.timeout`

Useful inspection commands:

```bash
maestro inspect config --config /path/to/maestro.yaml
maestro inspect state --config /path/to/maestro.yaml
maestro inspect state --state-dir /path/to/state-dir
maestro inspect runs --config /path/to/maestro.yaml
```

`inspect runs` summarizes active, retry, finished, done, and failed counts per source, along with the latest sanitized error and a health status such as `active`, `retrying`, or `degraded`.

`inspect state` gives the same per-source health rollup over persisted `runs.json`, including pending approvals and approval history counts.

Useful recovery commands:

```bash
maestro reset issue --config /path/to/maestro.yaml group/project#123
maestro cleanup workspaces --config /path/to/maestro.yaml --dry-run
maestro cleanup workspaces --config /path/to/maestro.yaml
```

`reset issue` only touches local state. It does not change the tracker item. It refuses to reset the currently active run.

`cleanup workspaces` removes workspace directories under `workspace.root` except for the currently active workspace recorded in `runs.json`.

Active run should have stopped but kept going:

- check whether the tracker item was marked `maestro:done` or `maestro:failed`
- check whether the issue or epic bucket became terminal
- inspect recent events in the TUI; reconciliation stops are logged there explicitly

Large multi-source config is hard to scan in the TUI:

- press `/` to search by source name, tracker, group, or tag
- press `f` to cycle source groups
- press `c` to clear all source filters

Hooks behave unexpectedly:

- check the shell command under `hooks.after_create`, `hooks.before_run`, or `hooks.after_run`
- check `hooks.timeout`
- inspect logs for the sanitized hook stderr/stdout
- remember that `hooks.before_remove` is not wired yet

Codex manual approval does not appear:

- this is a known limitation of the currently tested app-server behavior
- use Claude manual approval if you need a live approval demo today
