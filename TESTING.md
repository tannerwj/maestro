# Testing

Maestro uses a layered test strategy:

- Fast local tests for config, orchestration, workspace handling, prompt rendering, adapter behavior, retry/backoff, and restart recovery.
- Opt-in live integration tests for external services and paid harnesses.

## Default test suite

Run the fast suite on every change:

```bash
make test
go test ./...
```

This suite must remain hermetic and safe to run without external credentials.

The hermetic suite now also covers:

- multi-source config validation
- explicit GitLab epic `epic_filter` vs `issue_filter` behavior
- global and per-agent dispatch limiter behavior

## Hermetic Maestro smoke

For a credential-free end-to-end smoke of the real Maestro binary, run:

```bash
make smoke-hermetic
```

This smoke uses:

- a local fake GitLab API
- a local fake Linear GraphQL API
- stub `claude` and `codex` binaries on `PATH`

Coverage includes:

- custom lifecycle label prefixes with routing labels in the same namespace
- global `on_complete` defaults plus per-source lifecycle overrides
- multi-turn Codex continuation
- prompt template FuncMap helpers
- local pack harness-config merge and explicit `extra_args: []` clearing
- built-in `dev-claude` and `dev-codex` packs
- repo-embedded pack prompt/context/harness-config directory population
- `workspace:none` on a GitLab epic source

## Web verification

Run the web checks before changing the browser UI or cutting a release with the embedded dashboard:

```bash
cd web
npm run lint
npm run build
npm run test:smoke
```

To verify the embedded frontend path rather than the dev filesystem path:

```bash
./scripts/sync_web_embed.sh
go build -o /tmp/maestro-web-smoke ./cmd/maestro
/tmp/maestro-web-smoke demo-web --host 127.0.0.1 --port 8761
```

Then, in a second shell:

```bash
cd web
PLAYWRIGHT_EXTERNAL_SERVER=1 PLAYWRIGHT_BASE_URL=http://127.0.0.1:8761 npm run test:smoke
```

## Slack validation

The Slack bridge currently has hermetic unit coverage for:

- thread creation
- approval message posting
- approval message resolution updates
- typed thread replies for pending Maestro control messages
- stop-from-Slack actions
- persisted Slack message references
- Socket Mode approval handling against a fake Slack HTTP + websocket server

Run:

```bash
go test ./internal/channel -v
```

There is no default live Slack integration test in the repo yet. Live validation requires a Socket Mode-enabled Slack app plus disposable DM or channel targets.

Slack app checklist for live validation:

- enable Socket Mode
- create an app token with `connections:write`
- add bot scopes:
  - `chat:write`
  - `im:write`
  - `im:history`
- enable Interactivity
- enable Event Subscriptions
- subscribe to bot event `message.im`
- enable the Messages tab setting that allows users to send messages to the app
- reinstall the app after changes

For an opt-in live Slack workspace check, set:

- `MAESTRO_TEST_LIVE_SLACK=1`
- `MAESTRO_TEST_SLACK_BOT_TOKEN`
- `MAESTRO_TEST_SLACK_APP_TOKEN`
- `MAESTRO_TEST_SLACK_USER_ID`

Run:

```bash
go test ./internal/channel -run TestLiveSlackClientDMThreadLifecycle -v
```

Typed-reply smoke checklist:

1. Configure `controls.before_work.enabled: true`.
2. Set `controls.before_work.mode: reply` if you want a plain question instead of approve/deny buttons.
3. Start Maestro against a disposable tracker issue.
4. Wait for the Slack DM thread and the separate `Before work question` message.
5. Reply in the thread as the user.
6. Verify:
   - the original question message stays intact
   - your user reply appears as its own thread message
   - Maestro posts a separate `Before-work question answered` follow-up
   - the run continues and completes

## Live GitLab validation

The GitLab live tests exercise both read and write paths against a configured project. They validate:

- authentication
- project resolution
- issue polling
- label filtering
- issue normalization
- lifecycle label writeback
- operational comments
- tracker-driven reconciliation of an active run when the issue becomes terminal

Required environment variables:

- `MAESTRO_TEST_LIVE_GITLAB=1`
- `MAESTRO_TEST_GITLAB_BASE_URL`
- `MAESTRO_TEST_GITLAB_TOKEN`
- `MAESTRO_TEST_GITLAB_PROJECT`
- `MAESTRO_TEST_GITLAB_LABEL`

Optional:

- `MAESTRO_TEST_GITLAB_ASSIGNEE`

Run:

```bash
go test ./internal/tracker/gitlab -run TestLiveGitLabPollsConfiguredProject -v
go test ./internal/tracker/gitlab -run TestLiveGitLabLifecycleWriteback -v
go test ./internal/orchestrator -run TestServiceWithLiveGitLabReconciliationStopsRunWhenIssueCloses -v
```

For a full end-to-end GitLab smoke using the real CLI/harness path:

```bash
export MAESTRO_GITLAB_BASE_URL=https://gitlab.com
export MAESTRO_GITLAB_PROJECT=<namespace/project>
./scripts/smoke_gitlab.sh
```

Optional overrides:

- `MAESTRO_CLAUDE_MODEL` to pin a different Claude model alias for the live smoke. Default: `sonnet`.
- `MAESTRO_WORKSPACE=none` to exercise the real tracker/orchestrator path without cloning a private repo.

By default, `scripts/smoke_gitlab.sh` now provisions its own disposable GitLab issue with a unique
label and closes it during cleanup. If you want to point the smoke at an existing issue pool
instead, set:

```bash
export MAESTRO_GITLAB_SMOKE_PROVISION_FIXTURE=0
export MAESTRO_GITLAB_LABEL=agent:ready
./scripts/smoke_gitlab.sh
```

GitLab epic support currently has hermetic unit coverage but no default live test command in this repo, because live validation requires access to a GitLab group with epics enabled.

If you do have an epic-capable GitLab group, you can enable the epic live suite with:

- `MAESTRO_TEST_LIVE_GITLAB_EPIC=1`
- `MAESTRO_TEST_GITLAB_BASE_URL`
- `MAESTRO_TEST_GITLAB_TOKEN`
- `MAESTRO_TEST_GITLAB_EPIC_GROUP`
- `MAESTRO_TEST_GITLAB_EPIC_LABEL`
- `MAESTRO_TEST_GITLAB_EPIC_REPO`

`MAESTRO_TEST_GITLAB_EPIC_REPO` should be a plain repo URL. Do not embed credentials in it.

Run:

```bash
go test ./internal/tracker/gitlab -run TestLiveGitLabEpicPollsConfiguredGroup -v
go test ./internal/tracker/gitlab -run TestLiveGitLabEpicLifecycleWriteback -v
go test ./internal/orchestrator -run TestServiceWithLiveGitLabEpicReconciliationStopsRunWhenEpicCloses -v
```

## Live Linear validation

The Linear live tests exercise both read and write paths against a configured project. They validate:

- authentication
- project-scoped issue polling
- state filtering
- issue normalization
- lifecycle label writeback
- tracker-driven reconciliation of an active run when the issue becomes terminal

Required environment variables:

- `MAESTRO_TEST_LIVE_LINEAR=1`
- `MAESTRO_TEST_LINEAR_TOKEN`
- `MAESTRO_TEST_LINEAR_PROJECT`

Run:

```bash
go test ./internal/tracker/linear -run TestLiveLinearPollsConfiguredProject -v
go test ./internal/tracker/linear -run TestLiveLinearLifecycleWriteback -v
go test ./internal/orchestrator -run TestServiceWithLiveLinearSource -v
go test ./internal/orchestrator -run TestServiceWithLiveLinearReconciliationStopsRunWhenIssueCompletes -v
```

For a full end-to-end Linear smoke using the real CLI/harness path:

```bash
export MAESTRO_LINEAR_PROJECT="Maestro Testbed"
./scripts/smoke_linear.sh
```

Optional overrides:

- `MAESTRO_CLAUDE_MODEL` to pin a different Claude model alias for the live smoke. Default: `sonnet`.
- `MAESTRO_WORKSPACE=none` to skip local workspace cloning and use an empty per-run workspace instead.

By default, `scripts/smoke_linear.sh` now provisions its own disposable labeled issue, moves it into
the configured workflow state, and marks it completed during cleanup. If you want to point the smoke
at an existing issue pool instead, set:

```bash
export MAESTRO_LINEAR_SMOKE_PROVISION_FIXTURE=0
./scripts/smoke_linear.sh
```

## Live Claude validation

The Claude live test validates the CLI invocation path against your installed subscription. It runs
in a temporary directory and now covers both the basic non-interactive path and the manual-approval
rerun path.

Required environment variables:

- `MAESTRO_TEST_LIVE_CLAUDE=1`

Run:

```bash
go test ./internal/harness/claude -run TestLiveClaudeHarness -v
go test ./internal/harness/claude -run TestLiveClaudeManualApproval -v
```

## Service-level validation with a real Claude session

This test uses the real Claude CLI with a fake tracker and a temporary local git repository. It
validates the orchestrator path through workspace preparation, prompt rendering, harness startup,
and run completion without touching a live tracker.

Required environment variables:

- `MAESTRO_TEST_LIVE_CLAUDE=1`

Run:

```bash
go test ./internal/orchestrator -run TestServiceWithLiveClaudeHarness -v
go test ./internal/orchestrator -run TestServiceWithLiveClaudeManualApproval -v
```

## Live Codex validation

The Codex live test validates the app-server harness path against your installed Codex login. It
starts a thread and turn over stdio, streams response deltas, and waits for `turn/completed`.

Required environment variables:

- `MAESTRO_TEST_LIVE_CODEX=1`

Run:

```bash
go test ./internal/harness/codex -run TestLiveCodexHarness -v
go test ./internal/orchestrator -run TestServiceWithLiveCodexHarness -v
go test ./internal/harness/codex -run TestLiveCodexManualApproval -v
go test ./internal/harness/codex -run TestLiveCodexMessageRequest -v
go test ./internal/orchestrator -run TestServiceWithLiveCodexManualApproval -v
```

## Notes

- The default `go test ./...` suite now covers persisted `runs.json` state, approval history persistence, failed-run retries, restart recovery of an interrupted active run, tracker-label-based reconciliation stops, and the operator recovery helpers for run inspection, issue reset, and workspace cleanup.
- The live Codex manual-approval tests currently skip if the installed Codex app-server never emits an approval request under `on-request`. That behavior was observed in the current local environment on March 15, 2026.
- The live Codex message-request test currently skips if the installed Codex session never emits a native `request_user_input` call for the prompt within 60 seconds.
- Real binary smoke runs remain manual because they require a configured tracker issue plus an authenticated local CLI session.
- For a real three-source smoke, use [scripts/smoke_multi_source.sh](scripts/smoke_multi_source.sh) with one GitLab project issue, one GitLab epic-linked child issue, and one Linear issue isolated by dedicated labels.
- For a fixture-provisioning many-sources smoke, use [scripts/smoke_many_sources.sh](scripts/smoke_many_sources.sh). It creates fresh GitLab project issues, fresh GitLab epics plus linked child issues, and a fresh Linear issue before starting Maestro.

## Live Multi-Source Smoke

The multi-source smoke validates one Maestro process handling:

- one GitLab project issue source
- one GitLab epic bucket source dispatching a linked child issue
- one Linear project issue source

Required environment variables:

- `MAESTRO_GITLAB_TOKEN`
- `MAESTRO_GITLAB_PROJECT`
- `MAESTRO_GITLAB_PROJECT_LABEL`
- `MAESTRO_GITLAB_EPIC_GROUP`
- `MAESTRO_GITLAB_EPIC_LABEL`
- `MAESTRO_GITLAB_EPIC_REPO`
- `MAESTRO_LINEAR_TOKEN`
- `MAESTRO_LINEAR_PROJECT`
- `MAESTRO_LINEAR_LABEL`
- `MAESTRO_LINEAR_REPO`

Optional:

- `MAESTRO_GITLAB_BASE_URL`
- `MAESTRO_GITLAB_EPIC_ISSUE_LABEL`
- `MAESTRO_GITLAB_USERNAME`
- `MAESTRO_LINEAR_USERNAME`
- `MAESTRO_HARNESS`
- `MAESTRO_TIMEOUT_SECONDS`

Run:

```bash
make smoke-multi-source
```

## Live Many-Sources Smoke

The many-sources smoke validates one Maestro process handling six sources in one run:

- 3 GitLab epic sources
- 2 GitLab project sources
- 1 Linear source

It provisions fresh fixtures automatically, including:

- 2 GitLab project issues
- 3 GitLab epics
- 3 GitLab epic-linked child issues
- 1 Linear issue

Required environment variables:

- `MAESTRO_GITLAB_TOKEN`
- `MAESTRO_GITLAB_PROJECT`
- `MAESTRO_GITLAB_EPIC_GROUP`
- `MAESTRO_GITLAB_EPIC_REPO`
- `MAESTRO_LINEAR_TOKEN`
- `MAESTRO_LINEAR_PROJECT`

Optional:

- `MAESTRO_GITLAB_BASE_URL`
- `MAESTRO_GITLAB_USERNAME`
- `MAESTRO_LINEAR_USERNAME`
- `MAESTRO_HARNESS`
- `MAESTRO_TIMEOUT_SECONDS`
- `MAESTRO_USER_NAME`

Run:

```bash
make smoke-many-sources
```
