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
go test ./internal/orchestrator -run TestServiceWithLiveCodexManualApproval -v
```

## Notes

- The default `go test ./...` suite now covers persisted `runs.json` state, approval history persistence, failed-run retries, restart recovery of an interrupted active run, tracker-label-based reconciliation stops, and the operator recovery helpers for run inspection, issue reset, and workspace cleanup.
- The live Codex manual-approval tests currently skip if the installed Codex app-server never emits an approval request under `on-request`. That behavior was observed in the current local environment on March 15, 2026.
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
