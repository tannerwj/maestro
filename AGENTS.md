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
  tick() → Poll tracker → reconcile active run OR dispatch new
  approval watcher → route to Slack/TUI/web → resolve → resume harness
  message watcher  → same pattern
```

Key boundary: Maestro is scheduler/runner + approval router. Agents own task progress (MR creation, state transitions, domain logic).

## Commands

```bash
make test                    # hermetic suite, no credentials needed
make build                   # builds bin/maestro with embedded web UI
go test ./...                # same as make test
go test -run TestLive ./...  # live integration tests (needs credentials)
```

## Code conventions

- Go 1.26+. Standard library preferred over external deps.
- `internal/` packages are not importable outside the module.
- Interfaces defined in the consumer package (e.g., `harness.Harness`, `tracker.Tracker`).
- Errors wrapped with `fmt.Errorf("context: %w", err)`. No bare `err` returns.
- Structured logging via `log/slog`. Every log line should carry enough context to identify the source/run/issue.
- No `panic()` in production code paths. Return errors.

## Test conventions

- Fast hermetic tests: no network, no credentials, no external state. Must pass in CI.
- Live tests gated by `TestLive*` prefix + env vars (see TESTING.md).
- Harness tests use stub shell scripts (see `internal/harness/claude/testdata/`, `internal/harness/codex/testdata/`).
- Orchestrator tests use in-memory tracker/harness/state stubs (see `internal/orchestrator/service_test.go`).
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
└── codex/           # optional: copied to workspace as .codex/ (skills, settings)
```

- `agent.yaml` fields: name, description, instance_name, harness, workspace, prompt, approval_policy, approval_timeout, communication, max_concurrent, stall_timeout, env, tools, skills, context_files, codex, claude
- `prompt.md` uses Go `text/template` syntax. Available data: `.Agent`, `.Issue`, `.User`, `.Source`, `.Attempt`, `.AgentName`, `.OperatorInstruction`
- Packs are referenced from `maestro.yaml` via `agent_types[].agent_pack` (name, path, or `repo:` prefix)
- Config YAML overrides pack defaults. tools/skills/context_files merge (not replace).
- `approval_policy`: `auto` (no approval) or `manual` (all actions require approval)

## Config

- YAML config at path passed via `--config` flag.
- Sources define tracker + filters + agent type mapping.
- Agent types define harness + workspace + approval policy + prompt.
- Validation in `internal/config/validate.go` — `ValidateMVP()` enforces the config contract.
- Env vars referenced by name (e.g., `token_env: $GITLAB_TOKEN`), not inlined.

## Key files

| Area | Files |
|------|-------|
| Entry point | `cmd/maestro/main.go` |
| Orchestration | `internal/orchestrator/{service,loop,dispatch,supervisor}.go` |
| Approval/message flow | `internal/orchestrator/{approvals,messages}.go` |
| Hooks | `internal/orchestrator/hooks.go` |
| Harness interface | `internal/harness/harness.go` |
| Claude adapter | `internal/harness/claude/adapter.go` |
| Codex adapter | `internal/harness/codex/adapter.go` |
| Tracker interface | `internal/tracker/tracker.go` |
| GitLab adapter | `internal/tracker/gitlab/adapter.go` |
| Linear adapter | `internal/tracker/linear/adapter.go` |
| Config types | `internal/config/types.go` |
| Config validation | `internal/config/validate.go` |
| Pack resolution | `internal/config/agent_packs.go` |
| Prompt rendering | `internal/prompt/render.go` |
| Workspace | `internal/workspace/{manager,git}.go` |
| State persistence | `internal/state/store.go` |
| Slack channel | `internal/channel/slack/` |
| Web API | `internal/api/server.go` |
| Web frontend | `web/` |
| TUI | `internal/tui/` |
| Logging | `internal/logging/` |
