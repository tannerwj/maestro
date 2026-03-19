#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

: "${MAESTRO_HARNESS:=claude-code}"
: "${MAESTRO_APPROVAL_POLICY:=auto}"
: "${MAESTRO_TIMEOUT_SECONDS:=180}"
: "${MAESTRO_LINEAR_STATE:=Todo}"
: "${MAESTRO_LINEAR_SMOKE_PROVISION_FIXTURE:=1}"
: "${MAESTRO_USER_NAME:=Smoke User}"
: "${MAESTRO_LINEAR_USERNAME:=}"

if [[ "${MAESTRO_APPROVAL_POLICY}" != "auto" ]]; then
  echo "scripts/smoke_linear.sh currently supports MAESTRO_APPROVAL_POLICY=auto only" >&2
  exit 1
fi

: "${MAESTRO_LINEAR_TOKEN:?set MAESTRO_LINEAR_TOKEN}"
: "${MAESTRO_LINEAR_PROJECT:?set MAESTRO_LINEAR_PROJECT}"

tmpdir="$(mktemp -d)"
config_path="${tmpdir}/maestro.yaml"
prompt_path="${tmpdir}/prompt.md"
workspace_root="${tmpdir}/workspaces"
logs_root="${tmpdir}/logs"
state_root="${tmpdir}/state"
repo_src="${tmpdir}/repo-src"
repo_bare="${tmpdir}/repo.git"
marker="MAESTRO_LINEAR_SMOKE_OK"
binary_path="${tmpdir}/maestro"
issue_identifier=""
issue_id=""
issue_label_name=""
done_state_id=""
provisioned_issue=0

run_is_idle() {
  local state_file="${state_root}/runs.json"
  [[ -f "${state_file}" ]] || return 1
  if grep -q '"active_run"' "${state_file}"; then
    return 1
  fi
  grep -q '"finished"' "${state_file}"
}

cleanup() {
  if [[ -n "${maestro_pid:-}" ]] && kill -0 "${maestro_pid}" 2>/dev/null; then
    kill -TERM "${maestro_pid}" 2>/dev/null || true
    sleep 1
    kill -KILL "${maestro_pid}" 2>/dev/null || true
  fi
  if [[ "${provisioned_issue}" == "1" ]] && [[ -n "${issue_id}" ]] && [[ -n "${done_state_id}" ]]; then
    linear_query '
mutation($id: String!, $stateId: String!) {
  issueUpdate(id: $id, input: { stateId: $stateId }) {
    success
  }
}' "$(jq -cn --arg id "${issue_id}" --arg stateId "${done_state_id}" '{id: $id, stateId: $stateId}')" >/dev/null 2>&1 || true
  fi
  wait "${maestro_pid:-}" 2>/dev/null || true
}
trap cleanup EXIT

linear_query() {
  local query="$1"
  local variables_json='{}'
  if [[ $# -ge 2 ]]; then
    variables_json="$2"
  fi
  local payload
  payload="$(jq -cn --arg query "${query}" --argjson variables "${variables_json}" '{query: $query, variables: $variables}')"
  curl -fsS https://api.linear.app/graphql \
    -H "Authorization: ${MAESTRO_LINEAR_TOKEN}" \
    -H "Content-Type: application/json" \
    --data "${payload}"
}

linear_find_project() {
  linear_query '
query($name: String!) {
  projects(first: 50, filter: { name: { eq: $name } }) {
    nodes {
      id
      name
      teams {
        nodes { id name }
      }
    }
  }
}' "$(jq -cn --arg name "${MAESTRO_LINEAR_PROJECT}" '{name: $name}')" | jq -c '.data.projects.nodes[0]'
}

linear_find_user_id() {
  local query_value="$1"
  if [[ -z "${query_value}" ]]; then
    return 0
  fi
  local result
  result="$(linear_query '
query($value: String!) {
  users(first: 10, filter: { email: { eq: $value } }) {
    nodes { id }
  }
}' "$(jq -cn --arg value "${query_value}" '{value: $value}')" | jq -r '.data.users.nodes[0].id // empty')"
  if [[ -n "${result}" ]]; then
    printf '%s' "${result}"
    return 0
  fi
  result="$(linear_query '
query($value: String!) {
  users(first: 10, filter: { displayName: { eq: $value } }) {
    nodes { id }
  }
}' "$(jq -cn --arg value "${query_value}" '{value: $value}')" | jq -r '.data.users.nodes[0].id // empty')"
  printf '%s' "${result}"
}

linear_create_label() {
  local name="$1"
  linear_query '
mutation($name: String!, $color: String!) {
  issueLabelCreate(input: { name: $name, color: $color }) {
    issueLabel { id name }
  }
}' "$(jq -cn --arg name "${name}" --arg color "#8b5cf6" '{name: $name, color: $color}')" | jq -r '.data.issueLabelCreate.issueLabel.id'
}

linear_create_issue() {
  local team_id="$1"
  local project_id="$2"
  local label_id="$3"
  local assignee_id="$4"
  local title="$5"
  local description="$6"
  local variables
  if [[ -n "${assignee_id}" ]]; then
    variables="$(jq -cn --arg teamId "${team_id}" --arg projectId "${project_id}" --arg labelId "${label_id}" --arg assigneeId "${assignee_id}" --arg title "${title}" --arg description "${description}" '{teamId: $teamId, projectId: $projectId, labelId: $labelId, assigneeId: $assigneeId, title: $title, description: $description}')"
    linear_query '
mutation($teamId: String!, $projectId: String!, $labelId: String!, $assigneeId: String!, $title: String!, $description: String!) {
  issueCreate(input: { teamId: $teamId, projectId: $projectId, labelIds: [$labelId], assigneeId: $assigneeId, title: $title, description: $description }) {
    issue { id identifier url }
  }
}' "${variables}"
  else
    variables="$(jq -cn --arg teamId "${team_id}" --arg projectId "${project_id}" --arg labelId "${label_id}" --arg title "${title}" --arg description "${description}" '{teamId: $teamId, projectId: $projectId, labelId: $labelId, title: $title, description: $description}')"
    linear_query '
mutation($teamId: String!, $projectId: String!, $labelId: String!, $title: String!, $description: String!) {
  issueCreate(input: { teamId: $teamId, projectId: $projectId, labelIds: [$labelId], title: $title, description: $description }) {
    issue { id identifier url }
  }
}' "${variables}"
  fi
}

linear_workflow_state_id() {
  local team_id="$1"
  local state_type="$2"
  linear_query '
query($teamId: ID!) {
  workflowStates(first: 20, filter: { team: { id: { eq: $teamId } } }) {
    nodes { id type }
  }
}' "$(jq -cn --arg teamId "${team_id}" '{teamId: $teamId}')" | jq -r --arg stateType "${state_type}" '.data.workflowStates.nodes[] | select((.type | ascii_downcase) == ($stateType | ascii_downcase)) | .id' | head -n1
}

linear_workflow_state_id_by_name() {
  local team_id="$1"
  local state_name="$2"
  linear_query '
query($teamId: ID!) {
  workflowStates(first: 20, filter: { team: { id: { eq: $teamId } } }) {
    nodes { id name type }
  }
}' "$(jq -cn --arg teamId "${team_id}" '{teamId: $teamId}')" | jq -r --arg stateName "${state_name}" '.data.workflowStates.nodes[] | select((.name | ascii_downcase) == ($stateName | ascii_downcase)) | .id' | head -n1
}

mkdir -p "${repo_src}"
(
  cd "${repo_src}"
  git init >/dev/null
  printf '%s\n' '# smoke repo' > README.md
  git add README.md >/dev/null
  git -c user.name='Smoke User' -c user.email='smoke@example.com' commit -m 'init' >/dev/null
  git clone --bare "${repo_src}" "${repo_bare}" >/dev/null
)

if [[ "${MAESTRO_LINEAR_SMOKE_PROVISION_FIXTURE}" == "1" ]]; then
  project_json="$(linear_find_project)"
  if [[ "${project_json}" == "null" || -z "${project_json}" ]]; then
    echo "Linear project ${MAESTRO_LINEAR_PROJECT} not found" >&2
    exit 1
  fi
  linear_project_id="$(printf '%s' "${project_json}" | jq -r '.id')"
  linear_team_id="$(printf '%s' "${project_json}" | jq -r '.teams.nodes[0].id // empty')"
  if [[ -z "${linear_team_id}" ]]; then
    echo "Linear project ${MAESTRO_LINEAR_PROJECT} has no team" >&2
    exit 1
  fi
  done_state_id="$(linear_workflow_state_id "${linear_team_id}" "completed")"
  issue_label_name="smoke-$(date +%s)"
  issue_label_id="$(linear_create_label "${issue_label_name}")"
  linear_assignee_id="$(linear_find_user_id "${MAESTRO_LINEAR_USERNAME}")"
  issue_json="$(linear_create_issue "${linear_team_id}" "${linear_project_id}" "${issue_label_id}" "${linear_assignee_id}" "Linear smoke fixture $(date +%s)" "Disposable fixture for Maestro Linear smoke.")"
  issue_id="$(printf '%s' "${issue_json}" | jq -r '.data.issueCreate.issue.id')"
  issue_identifier="$(printf '%s' "${issue_json}" | jq -r '.data.issueCreate.issue.identifier')"
  target_state_id="$(linear_workflow_state_id_by_name "${linear_team_id}" "${MAESTRO_LINEAR_STATE}")"
  if [[ -n "${target_state_id}" ]]; then
    linear_query '
mutation($id: String!, $stateId: String!) {
  issueUpdate(id: $id, input: { stateId: $stateId }) {
    success
  }
}' "$(jq -cn --arg id "${issue_id}" --arg stateId "${target_state_id}" '{id: $id, stateId: $stateId}')" >/dev/null
  fi
  provisioned_issue=1
fi

cat >"${prompt_path}" <<EOF
Create a file named SMOKE_RESULT.md in the repository root containing exactly ${marker}.
Then reply with exactly ${marker}.
EOF

cat >"${config_path}" <<EOF
defaults:
  poll_interval: 2s
  max_concurrent_global: 1

user:
  name: "${MAESTRO_USER_NAME}"
  linear_username: "${MAESTRO_LINEAR_USERNAME}"

sources:
  - name: linear-smoke
    tracker: linear
    connection:
      token_env: MAESTRO_LINEAR_TOKEN
      project: ${MAESTRO_LINEAR_PROJECT}
    repo: ${repo_bare}
    filter:
$(if [[ -n "${issue_label_name}" ]]; then printf '      labels: [%s]\n' "${issue_label_name}"; fi)
      states: [${MAESTRO_LINEAR_STATE}]
    agent_type: code-pr
    poll_interval: 2s

agent_types:
  - name: code-pr
    instance_name: smoke-agent
    harness: ${MAESTRO_HARNESS}
    workspace: git-clone
    prompt: ${prompt_path}
    approval_policy: ${MAESTRO_APPROVAL_POLICY}
    max_concurrent: 1

workspace:
  root: ${workspace_root}

state:
  dir: ${state_root}
  retry_base: 2s
  max_retry_backoff: 10s
  max_attempts: 2

logging:
  level: info
  dir: ${logs_root}
EOF

(
  cd "${ROOT}"
  go build -o "${binary_path}" ./cmd/maestro
  "${binary_path}" --config "${config_path}" --no-tui
) >"${tmpdir}/maestro.stdout" 2>&1 &
maestro_pid=$!

deadline=$(( $(date +%s) + MAESTRO_TIMEOUT_SECONDS ))
result_file=""
while (( $(date +%s) < deadline )); do
  if ! kill -0 "${maestro_pid}" 2>/dev/null; then
    echo "maestro exited before smoke completed" >&2
    cat "${tmpdir}/maestro.stdout" >&2 || true
    exit 1
  fi
  result_file="$(find "${workspace_root}" -name SMOKE_RESULT.md -print -quit 2>/dev/null || true)"
  if [[ -n "${result_file}" ]] && grep -qx "${marker}" "${result_file}"; then
    break
  fi
  sleep 2
done

if [[ -z "${result_file}" ]]; then
  echo "timed out waiting for Linear smoke result" >&2
  cat "${tmpdir}/maestro.stdout" >&2 || true
  exit 1
fi

idle_deadline=$(( $(date +%s) + 30 ))
while (( $(date +%s) < idle_deadline )); do
  if run_is_idle; then
    break
  fi
  sleep 1
done

cleanup
trap - EXIT

echo "Linear smoke passed."
echo "Config: ${config_path}"
echo "Prompt: ${prompt_path}"
echo "Workspace root: ${workspace_root}"
echo "Result file: ${result_file}"
echo "Logs: ${logs_root}"
echo "Stdout: ${tmpdir}/maestro.stdout"
if [[ "${provisioned_issue}" == "1" ]]; then
  echo "Provisioned issue: ${issue_identifier}"
  echo "Provisioned label: ${issue_label_name}"
fi
