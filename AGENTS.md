# Maestro

Single-machine orchestration daemon: polls trackers (GitLab, Linear), dispatches AI agents (Claude Code, Codex), routes approvals (Slack, TUI, web).

## Architecture

```
maestro.yaml
    │
    ▼
NewRuntime()
├─ 1 source  → NewService()
└─ N sources → NewSupervisor() → N scoped Services
                  ├─ shared global limiter
                  └─ per-agent-type limiters

Service loop:
  tick() → Poll tracker → reconcile active runs → dispatch retries/new work until source capacity is full
  approval watcher → route to Slack/TUI/web → resolve → resume harness
  message watcher  → same pattern
```

Key boundary: Maestro is scheduler/runner + approval router. Agents own task progress (MR creation, state transitions, domain logic).

Important practical boundaries:

- Maestro shell hooks (`hooks.after_create`, `hooks.before_run`, `hooks.after_run`) are outer orchestration hooks, not harness-native hooks. They run on the host by default; `hooks.execution: container` runs them inside the same Docker environment as the harness.
- Pack `claude/` and `codex/` directories are copied into the workspace as `.claude/` and `.codex/`. Use those for harness-native config.
- Agent processes do not inherit the full parent shell environment. They get a curated runtime baseline plus explicit `agent_types[].env`.

## Current Runtime Behavior

- Workspaces are reused across retries. A healthy existing git workspace is fetched and rechecked-out instead of being re-cloned.
- Healthy reused workspaces are preserved on transient fetch/checkout failures; only corrupt repos are removed and re-cloned.
- Lifecycle routing is label-driven. `on_dispatch`, `on_complete`, and `on_failure` can add/remove labels and optionally set tracker state.
- Only the exact reserved lifecycle labels are intake blockers: `{prefix}:active`, `{prefix}:done`, `{prefix}:failed`, `{prefix}:retry`.
- Routing labels such as `{prefix}:coding` or `{prefix}:review` remain visible to source filters.
- Retry policy can be set globally and overridden per source (`retry_base`, `max_retry_backoff`, `max_attempts`).
- Each source can run more than one issue at a time via `sources[].max_active_runs`; effective concurrency is still capped by the per-agent and global limiters.
- State persistence includes rolling backups and corrupt-file fallback; unreadable `runs.json` is archived and recovery continues with empty state.
- Run stdout/stderr is persisted under `state.dir/runs/<run-id>/`.
- Loopback web/API binds do not require auth. Non-loopback binds use `server.api_key` or a generated ephemeral API key.
- Slack DM mode authorizes the configured DM user. Slack channel mode requires explicit `authorized_user_ids` / `authorized_user_ids_env`.
- `maestro --dry-run` polls once, evaluates eligibility, previews workspace choice/lifecycle actions, and renders prompts without launching harnesses or mutating state.
- `maestro doctor` includes route-collision diagnostics for overlapping or ambiguous source routing.
- TUI, web, and API support debounced force-poll requests that re-use the normal service loop instead of running an out-of-band poll path.

## Commands

```bash
make test                    # hermetic suite, no credentials needed
make build                   # builds bin/maestro with embedded web UI
make smoke-hermetic          # credential-free end-to-end smoke
go test ./...                # same as make test
go test -run TestLive ./...  # live integration tests (needs credentials)
maestro doctor --config ...  # config + binary preflight
```

## Code conventions

- Go 1.26.1+. Standard library preferred over external deps.
- `internal/` packages are not importable outside the module.
- Interfaces defined in the consumer package (e.g., `harness.Harness`, `tracker.Tracker`).
- Errors wrapped with `fmt.Errorf("context: %w", err)`. No bare `err` returns.
- Structured logging via `log/slog`. Every log line should carry enough context to identify the source/run/issue.
- No `panic()` in production code paths. Return errors.

## Test conventions

- Fast hermetic tests: no network, no credentials, no external state. Must pass in CI.
- Live tests gated by `TestLive*` prefix + env vars (see TESTING.md).
- Harness tests use stub shell scripts (see `internal/harness/claude/testdata/`, `internal/harness/codex/testdata/`).
- Orchestrator tests use in-memory tracker/harness/state stubs (see `internal/orchestrator/service_test.go` plus `service_approvals_test.go`, `service_messages_test.go`, and `service_recovery_test.go`).
- Hermetic smoke coverage lives in `scripts/smoke_hermetic.sh` and `scripts/smoke_fake_tracker.py`.
- Live smoke entrypoints are `scripts/smoke_gitlab.sh`, `scripts/smoke_linear.sh`, `scripts/smoke_multi_source.sh`, and `scripts/smoke_many_sources.sh`.
- Test names: `TestSubjectVerbExpectation` (e.g., `TestServiceRetriesFailedRunAndIncrementsAttempt`).

## Agent pack conventions

Packs live in `agents/<pack-name>/` and contain:

```
agents/<pack-name>/
├── agent.yaml       # required: pack config
├── prompt.md        # required: Go template with issue/agent/user context
├── context.md       # optional: operating context included in prompt
├── context/         # optional: additional context files
├── claude/          # optional: copied to workspace as .claude/ (CLAUDE.md, settings)
└── codex/           # optional: copied to workspace as .codex/ (skills, settings, hooks.json)
```

- `agent.yaml` fields: name, description, instance_name, harness, workspace, prompt, approval_policy, approval_timeout, communication, max_concurrent, stall_timeout, env, tools, skills, context_files, codex, claude
- `prompt.md` uses Go `text/template` syntax. Available data: `.Agent`, `.Issue`, `.User`, `.Source`, `.Attempt`, `.AgentName`, `.OperatorInstruction`
- Packs are referenced from `maestro.yaml` via `agent_types[].agent_pack` (name, path, or `repo:` prefix)
- Config YAML overrides pack defaults. tools/skills/context_files merge (not replace).
- `approval_policy`: `auto` or `manual`. Manual routes requests emitted by the harness; Claude manual mode is not a per-action security boundary after approval (see `FINDINGS.md`, S2).
- Codex-native hook config belongs in `agents/<pack>/codex/` and is copied into workspace `.codex/`; do not model Codex hooks as Maestro shell hooks.

## Config

- YAML config at path passed via `--config` flag.
- Sources define tracker + filters + agent type mapping.
- Agent types define harness + workspace + approval policy + prompt.
- Validation in `internal/config/validate.go` — `ValidateMVP()` enforces the config contract.
- Env vars referenced by name (e.g., `token_env: $GITLAB_TOKEN`), not inlined.
- `defaults.label_prefix` sets the lifecycle namespace globally; `sources[].label_prefix` can override it per source.
- `defaults.on_dispatch`, `defaults.on_complete`, and `defaults.on_failure` provide global lifecycle defaults and merge with per-source overrides.
- Slack channel mode requires explicit operator authorization via `authorized_user_ids` or `authorized_user_ids_env`. DM mode authorizes the configured DM user.

## Key files

| Area | Files |
|------|-------|
| Entry point | `cmd/maestro/main.go` |
| Orchestration | `internal/orchestrator/{service,loop,run_manager,run_finalize,supervisor}.go` |
| Orchestrator state/view conversion | `internal/orchestrator/state_convert.go`, `internal/api/view_convert.go` |
| Approval/message flow | `internal/orchestrator/{approvals,messages}.go` |
| Hooks | `internal/orchestrator/hooks.go` |
| Harness interface | `internal/harness/harness.go` |
| Claude adapter | `internal/harness/claude/adapter.go` |
| Codex adapter | `internal/harness/codex/adapter.go` |
| Harness env handling | `internal/harness/env.go` |
| Tracker interface | `internal/tracker/tracker.go` |
| GitLab adapter | `internal/tracker/gitlab/adapter.go` |
| Linear adapter | `internal/tracker/linear/adapter.go` |
| Config types | `internal/config/types.go` |
| Config validation | `internal/config/validate.go` |
| Pack resolution | `internal/config/agent_packs.go` |
| Prompt rendering | `internal/prompt/render.go` |
| Workspace | `internal/workspace/{manager,git}.go` |
| State persistence | `internal/state/store.go` |
| Slack bridge | `internal/channel/bridge.go` |
| Web API | `internal/api/server.go` |
| Web frontend | `web/` |
| TUI | `internal/tui/` |
| Logging | `internal/logging/` |
