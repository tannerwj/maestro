#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
METADATA_PATH="${ROOT}/demo/taxhawk-demo-app-testbed.json"
SPEC_PATH="${ROOT}/demo/taxhawk-demo-app-spec.md"

: "${MAESTRO_TAXHAWK_GITLAB_BASE_URL:?set MAESTRO_TAXHAWK_GITLAB_BASE_URL}"
: "${MAESTRO_TAXHAWK_GITLAB_TOKEN:?set MAESTRO_TAXHAWK_GITLAB_TOKEN}"

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

require_cmd jq
require_cmd curl

if [[ ! -f "${METADATA_PATH}" ]]; then
  echo "missing metadata file: ${METADATA_PATH}" >&2
  exit 1
fi

if [[ ! -f "${SPEC_PATH}" ]]; then
  echo "missing spec file: ${SPEC_PATH}" >&2
  exit 1
fi

api_base="${MAESTRO_TAXHAWK_GITLAB_BASE_URL%/}"
project_id="$(jq -r '.project.id' "${METADATA_PATH}")"
group_path="$(jq -r '.group' "${METADATA_PATH}")"
epic_iid="$(jq -r '.epic.iid' "${METADATA_PATH}")"
spec_url="${api_base}/$(jq -r '.project.path_with_namespace' "${METADATA_PATH}")/-/blob/master/README.md"

gitlab_api() {
  local method="$1"
  local path="$2"
  shift 2
  curl -fsS -H "PRIVATE-TOKEN: ${MAESTRO_TAXHAWK_GITLAB_TOKEN}" -X "${method}" "$@" "${api_base}${path}"
}

update_issue() {
  local iid="$1"
  local title="$2"
  local labels="$3"
  local description="$4"
  gitlab_api PUT "/api/v4/projects/${project_id}/issues/${iid}" \
    --data-urlencode "title=${title}" \
    --data-urlencode "labels=${labels}" \
    --data-urlencode "description=${description}" >/dev/null
}

epic_description="$(cat <<EOF
Reusable Maestro autonomy testbed epic for the demo app.

Use this epic to evaluate:

- issue quality
- workflow routing
- agent pack quality
- restart recovery
- autonomous execution with minimal operator input

Canonical product spec:

- local spec in this repo: \`demo/taxhawk-demo-app-spec.md\`
- project: ${spec_url}

Reset workflow:

1. reset the repo to the baseline commit
2. reopen the linked child issues
3. remove Maestro lifecycle state from the issues
4. rerun the workflows

The linked issues are intentionally specific and should be used to iterate on what makes a good autonomous backlog item.
EOF
)"

gitlab_api PUT "/api/v4/groups/${group_path}/epics/${epic_iid}" \
  --data-urlencode "title=Build reusable demo app" \
  --data-urlencode "labels=bucket:demo-app" \
  --data-urlencode "description=${epic_description}" >/dev/null

issue_1_description="$(cat <<'EOF'
## Goal

Bootstrap the repository into a runnable demo app shell using a fixed initial stack:

- React + Vite frontend
- Node + Express backend

## Deliverables

- runnable application shell checked into the repo
- root README that replaces any generated template text and explains:
  - the fixed stack
  - why it is appropriate for this demo app
  - install, run, and verification commands
- initial project structure that clearly separates frontend and backend concerns
- baseline developer scripts for install, run, and verification entrypoints
- a verification path that exits cleanly from a fresh checkout

## Acceptance criteria

- A clean checkout can be started by following the README.
- The fixed stack is documented with a short rationale and stays intentionally simple.
- The repo structure is ready for backend and frontend follow-up issues.
- The app shell renders or responds successfully even if the core task flow is not complete yet.
- Verification commands complete without hanging on a long-running process.
- The repository does not include committed install artifacts such as `node_modules`.

## Verification

- From a clean checkout, follow the README and run the documented verification command.
- Run the documented start command and confirm the shell app responds successfully.

## Constraints

- Favor simplicity over novelty.
- Use exactly a React + Vite frontend and a small Node + Express backend for this issue.
- Do not add authentication, external services, or deployment infrastructure.
- Do not implement full task persistence in this issue.
- Do not add large framework layers or infrastructure that are not required by the issue.
- Do not spend time evaluating alternate stacks for this bootstrap.
- If information required to finish safely is missing, ask a focused Maestro question or document the blocker and stop instead of guessing.

## Dependencies

- none
EOF
)"

issue_2_description="$(cat <<'EOF'
## Goal

Add a task API backed by an in-memory store so the frontend can create and update tasks.

## Deliverables

- API endpoints for listing, creating, updating, and completing tasks
- a simple task data model
- request/response behavior documented in the repo
- basic validation and error handling

## Acceptance criteria

- The API supports:
  - list tasks
  - create task
  - update task title and description
  - mark task complete
- The app runs locally without external infrastructure.
- The API is easy for the frontend issue to consume.

## Verification

- Run the documented backend or app test command.
- Manually exercise the task API locally or add a small automated check.

## Constraints

- Keep persistence in-memory for this issue.
- Do not introduce a database yet.
- Do not redesign the repo structure created by issue 1 unless required.
- If information required to finish safely is missing, ask a focused Maestro question or document the blocker and stop instead of guessing.

## Dependencies

- depends on issue 1
EOF
)"

issue_3_description="$(cat <<'EOF'
## Goal

Build the main task-management UI on top of the task API.

## Deliverables

- task list view
- task creation flow
- task editing flow
- task completion flow
- responsive layout that remains usable on mobile-width screens

## Acceptance criteria

- A user can see all current tasks.
- A user can create a new task.
- A user can edit task title and description.
- A user can mark a task complete.
- The UI is clear, functional, and not visually broken on desktop or mobile.

## Verification

- Run the documented frontend or app test command.
- Manually verify the CRUD flow in the browser against the local app.

## Constraints

- Focus on clarity and reliability over visual polish.
- Do not add auth or advanced filtering.
- Use the existing API instead of inventing a second task flow.
- If information required to finish safely is missing, ask a focused Maestro question or document the blocker and stop instead of guessing.

## Dependencies

- depends on issues 1 and 2
EOF
)"

issue_4_description="$(cat <<'EOF'
## Goal

Replace the temporary in-memory task store with SQLite so tasks survive restarts.

## Deliverables

- SQLite-backed task storage
- automatic local initialization or migration path
- updated documentation for any setup or reset steps

## Acceptance criteria

- Existing task operations continue to work after the persistence change.
- Data survives application restart.
- A clean local environment can initialize storage without manual database setup.
- The implementation is still suitable for a single-developer local app.

## Verification

- Start the app, create or update tasks, restart the app, and confirm the data remains.
- Run the documented verification command.

## Constraints

- Keep the storage local and simple.
- Do not add hosted databases or cloud infrastructure.
- Minimize disruption to the API and UI already built.
- If information required to finish safely is missing, ask a focused Maestro question or document the blocker and stop instead of guessing.

## Dependencies

- depends on issues 1, 2, and ideally 3
EOF
)"

issue_5_description="$(cat <<'EOF'
## Goal

Add automated verification so Maestro runs are easy to validate and regressions are easy to spot.

## Deliverables

- backend and/or integration tests for the key task flows
- frontend verification for the primary task interactions
- CI configuration that runs the important checks on the repo
- documented local verification command

## Acceptance criteria

- The repo has a clear command a reviewer can run to verify the app.
- CI runs the important checks automatically.
- The core task flow has enough automated coverage to catch obvious regressions.
- Failures are understandable and actionable.

## Verification

- Run the local verification command from the README.
- Confirm CI is configured and references the same checks.

## Constraints

- Prefer a smaller stable suite over broad but flaky coverage.
- Do not block on perfect coverage.
- If information required to finish safely is missing, ask a focused Maestro question or document the blocker and stop instead of guessing.

## Dependencies

- depends on the main app path existing, especially issues 2 and 3
EOF
)"

update_issue 1 "Bootstrap the demo app shell and stack" "agent:ready,workflow:repo" "${issue_1_description}"
update_issue 2 "Add task API with in-memory persistence" "agent:ready,workflow:backend" "${issue_2_description}"
update_issue 3 "Build the task list and editing UI" "agent:ready,workflow:frontend" "${issue_3_description}"
update_issue 4 "Replace in-memory task storage with SQLite" "agent:ready,workflow:backend" "${issue_4_description}"
update_issue 5 "Add tests and CI checks for the demo app" "agent:ready,workflow:qa" "${issue_5_description}"

echo "TaxHawk demo app backlog synced."
echo "Project: $(jq -r '.project.path_with_namespace' "${METADATA_PATH}")"
echo "Epic: $(jq -r '.epic.title' "${METADATA_PATH}")"
