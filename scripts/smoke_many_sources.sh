#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

: "${MAESTRO_HARNESS:=claude-code}"
: "${MAESTRO_CLAUDE_MODEL:=sonnet}"
: "${MAESTRO_WORKSPACE:=git-clone}"
: "${MAESTRO_APPROVAL_POLICY:=auto}"
: "${MAESTRO_TIMEOUT_SECONDS:=900}"
: "${MAESTRO_GITLAB_BASE_URL:=https://gitlab.com}"
: "${MAESTRO_USER_NAME:=Smoke User}"
: "${MAESTRO_GITLAB_USERNAME:=}"
: "${MAESTRO_LINEAR_USERNAME:=}"

if [[ "${MAESTRO_APPROVAL_POLICY}" != "auto" ]]; then
  echo "scripts/smoke_many_sources.sh currently supports MAESTRO_APPROVAL_POLICY=auto only" >&2
  exit 1
fi

: "${MAESTRO_GITLAB_TOKEN:?set MAESTRO_GITLAB_TOKEN}"
: "${MAESTRO_GITLAB_PROJECT:?set MAESTRO_GITLAB_PROJECT}"
: "${MAESTRO_GITLAB_EPIC_GROUP:?set MAESTRO_GITLAB_EPIC_GROUP}"
: "${MAESTRO_GITLAB_EPIC_REPO:?set MAESTRO_GITLAB_EPIC_REPO}"
: "${MAESTRO_LINEAR_TOKEN:?set MAESTRO_LINEAR_TOKEN}"
: "${MAESTRO_LINEAR_PROJECT:?set MAESTRO_LINEAR_PROJECT}"

tmpdir="$(mktemp -d)"
config_path="${tmpdir}/maestro.yaml"
workspace_root="${tmpdir}/workspaces"
logs_root="${tmpdir}/logs"
state_root="${tmpdir}/state"
binary_path="${tmpdir}/maestro"
repo_src="${tmpdir}/repo-src"
repo_bare="${tmpdir}/repo.git"
tag="many$(date +%s)"

uri() {
  jq -nr --arg x "$1" '$x|@uri'
}

gitlab_api() {
  local method="$1"
  local path="$2"
  shift 2
  curl -fsS -H "PRIVATE-TOKEN: ${MAESTRO_GITLAB_TOKEN}" -X "${method}" "$@" "${MAESTRO_GITLAB_BASE_URL%/}${path}"
}

gitlab_create_label() {
  local scope_kind="$1"
  local scope="$2"
  local name="$3"
  local color="$4"
  local path
  if [[ "${scope_kind}" == "group" ]]; then
    path="/api/v4/groups/$(uri "${scope}")/labels"
  else
    path="/api/v4/projects/$(uri "${scope}")/labels"
  fi
  gitlab_api POST "${path}" --data-urlencode "name=${name}" --data-urlencode "color=${color}" >/dev/null || true
}

gitlab_user_id() {
  local username="$1"
  if [[ -z "${username}" ]]; then
    return 0
  fi
  gitlab_api GET "/api/v4/users?username=$(uri "${username}")" | jq -r '.[0].id // empty'
}

gitlab_create_issue() {
  local project="$1"
  local title="$2"
  local description="$3"
  local labels="$4"
  local assignee_id="${5:-}"
  local args=(
    --data-urlencode "title=${title}"
    --data-urlencode "description=${description}"
    --data-urlencode "labels=${labels}"
  )
  if [[ -n "${assignee_id}" ]]; then
    args+=(--data-urlencode "assignee_ids[]=${assignee_id}")
  fi
  gitlab_api POST "/api/v4/projects/$(uri "${project}")/issues" "${args[@]}"
}

gitlab_create_epic() {
  local group="$1"
  local title="$2"
  local description="$3"
  local labels="$4"
  gitlab_api POST "/api/v4/groups/$(uri "${group}")/epics" \
    --data-urlencode "title=${title}" \
    --data-urlencode "description=${description}" \
    --data-urlencode "labels=${labels}"
}

gitlab_link_issue_to_epic() {
  local project="$1"
  local issue_iid="$2"
  local epic_id="$3"
  gitlab_api PUT "/api/v4/projects/$(uri "${project}")/issues/${issue_iid}" --data-urlencode "epic_id=${epic_id}" >/dev/null
}

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

cleanup() {
  if [[ -n "${maestro_pid:-}" ]] && kill -0 "${maestro_pid}" 2>/dev/null; then
    kill -TERM "${maestro_pid}" 2>/dev/null || true
    sleep 1
    kill -KILL "${maestro_pid}" 2>/dev/null || true
  fi
  wait "${maestro_pid:-}" 2>/dev/null || true
}
trap cleanup EXIT

run_failed() {
  local state_file="${state_root}/runs.json"
  [[ -f "${state_file}" ]] || return 1
  grep -q '"status": "failed"' "${state_file}"
}

mkdir -p "${repo_src}"
(
  cd "${repo_src}"
  git init >/dev/null
  printf '%s\n' '# many sources smoke repo' > README.md
  git add README.md >/dev/null
  git -c user.name='Smoke User' -c user.email='smoke@example.com' commit -m 'init' >/dev/null
  git clone --bare "${repo_src}" "${repo_bare}" >/dev/null
)

gitlab_assignee_id="$(gitlab_user_id "${MAESTRO_GITLAB_USERNAME}")"

project_app_label="${tag}-project-app"
project_ops_label="${tag}-project-ops"
epic_platform_label="${tag}-epic-platform"
epic_infra_label="${tag}-epic-infra"
epic_growth_label="${tag}-epic-growth"
epic_platform_issue_label="${tag}-epic-platform-issue"
epic_infra_issue_label="${tag}-epic-infra-issue"
epic_growth_issue_label="${tag}-epic-growth-issue"

for label in "${project_app_label}" "${project_ops_label}" "${epic_platform_issue_label}" "${epic_infra_issue_label}" "${epic_growth_issue_label}"; do
  gitlab_create_label project "${MAESTRO_GITLAB_PROJECT}" "${label}" "#347d39"
done
for label in "${epic_platform_label}" "${epic_infra_label}" "${epic_growth_label}"; do
  gitlab_create_label group "${MAESTRO_GITLAB_EPIC_GROUP}" "${label}" "#8b5cf6"
done

project_app_issue="$(gitlab_create_issue "${MAESTRO_GITLAB_PROJECT}" "Many sources project app ${tag}" "Fixture for many-sources project app." "${project_app_label}" "${gitlab_assignee_id}")"
project_ops_issue="$(gitlab_create_issue "${MAESTRO_GITLAB_PROJECT}" "Many sources project ops ${tag}" "Fixture for many-sources project ops." "${project_ops_label}" "${gitlab_assignee_id}")"

epic_platform="$(gitlab_create_epic "${MAESTRO_GITLAB_EPIC_GROUP}" "Many sources epic platform ${tag}" "Fixture for exact epic source." "${epic_platform_label}")"
epic_infra="$(gitlab_create_epic "${MAESTRO_GITLAB_EPIC_GROUP}" "Many sources epic infra ${tag}" "Fixture for infra epic source." "${epic_infra_label}")"
epic_growth="$(gitlab_create_epic "${MAESTRO_GITLAB_EPIC_GROUP}" "Many sources epic growth ${tag}" "Fixture for growth epic source." "${epic_growth_label}")"

epic_platform_id="$(printf '%s' "${epic_platform}" | jq -r '.id')"
epic_platform_iid="$(printf '%s' "${epic_platform}" | jq -r '.iid')"
epic_infra_id="$(printf '%s' "${epic_infra}" | jq -r '.id')"
epic_growth_id="$(printf '%s' "${epic_growth}" | jq -r '.id')"

epic_platform_issue="$(gitlab_create_issue "${MAESTRO_GITLAB_PROJECT}" "Many sources epic platform child ${tag}" "Fixture for platform epic child." "${epic_platform_issue_label}" "${gitlab_assignee_id}")"
epic_infra_issue="$(gitlab_create_issue "${MAESTRO_GITLAB_PROJECT}" "Many sources epic infra child ${tag}" "Fixture for infra epic child." "${epic_infra_issue_label}" "${gitlab_assignee_id}")"
epic_growth_issue="$(gitlab_create_issue "${MAESTRO_GITLAB_PROJECT}" "Many sources epic growth child ${tag}" "Fixture for growth epic child." "${epic_growth_issue_label}" "${gitlab_assignee_id}")"

gitlab_link_issue_to_epic "${MAESTRO_GITLAB_PROJECT}" "$(printf '%s' "${epic_platform_issue}" | jq -r '.iid')" "${epic_platform_id}"
gitlab_link_issue_to_epic "${MAESTRO_GITLAB_PROJECT}" "$(printf '%s' "${epic_infra_issue}" | jq -r '.iid')" "${epic_infra_id}"
gitlab_link_issue_to_epic "${MAESTRO_GITLAB_PROJECT}" "$(printf '%s' "${epic_growth_issue}" | jq -r '.iid')" "${epic_growth_id}"

linear_project="$(linear_find_project)"
linear_project_id="$(printf '%s' "${linear_project}" | jq -r '.id')"
linear_team_id="$(printf '%s' "${linear_project}" | jq -r '.teams.nodes[0].id')"
if [[ -z "${linear_project_id}" || -z "${linear_team_id}" || "${linear_project_id}" == "null" || "${linear_team_id}" == "null" ]]; then
  echo "failed to resolve Linear project/team for ${MAESTRO_LINEAR_PROJECT}" >&2
  exit 1
fi
linear_assignee_id="$(linear_find_user_id "${MAESTRO_LINEAR_USERNAME}")"
linear_label_name="${tag}-linear"
linear_label_id="$(linear_create_label "${linear_label_name}")"
linear_issue="$(linear_create_issue "${linear_team_id}" "${linear_project_id}" "${linear_label_id}" "${linear_assignee_id}" "Many sources linear ${tag}" "Fixture for many-sources linear source.")"
linear_issue_identifier="$(printf '%s' "${linear_issue}" | jq -r '.data.issueCreate.issue.identifier')"

markers=(
  "many_epic_platform:MAESTRO_MANY_EPIC_PLATFORM_OK:MANY_EPIC_PLATFORM.md"
  "many_epic_infra:MAESTRO_MANY_EPIC_INFRA_OK:MANY_EPIC_INFRA.md"
  "many_epic_growth:MAESTRO_MANY_EPIC_GROWTH_OK:MANY_EPIC_GROWTH.md"
  "many_project_app:MAESTRO_MANY_PROJECT_APP_OK:MANY_PROJECT_APP.md"
  "many_project_ops:MAESTRO_MANY_PROJECT_OPS_OK:MANY_PROJECT_OPS.md"
  "many_linear:MAESTRO_MANY_LINEAR_OK:MANY_LINEAR.md"
)

for spec in "${markers[@]}"; do
  IFS=: read -r agent_name marker filename <<<"${spec}"
  cat >"${tmpdir}/${agent_name}.md" <<EOF
Create a file named ${filename} in the repository root containing exactly ${marker}.
Then reply with exactly ${marker}.
EOF
done

cat >"${config_path}" <<EOF
defaults:
  poll_interval: 2s
  max_concurrent_global: 3
  stall_timeout: 10m

agent_packs_dir: ${ROOT}/agents

source_defaults:
  gitlab:
    connection:
      base_url: ${MAESTRO_GITLAB_BASE_URL}
      token_env: MAESTRO_GITLAB_TOKEN
      project: ${MAESTRO_GITLAB_PROJECT}
    poll_interval: 2s
  gitlab_epic:
    connection:
      base_url: ${MAESTRO_GITLAB_BASE_URL}
      token_env: MAESTRO_GITLAB_TOKEN
      group: ${MAESTRO_GITLAB_EPIC_GROUP}
    repo: ${MAESTRO_GITLAB_EPIC_REPO}
    poll_interval: 2s
  linear:
    connection:
      token_env: MAESTRO_LINEAR_TOKEN
      project: ${MAESTRO_LINEAR_PROJECT}
    repo: ${repo_bare}
    poll_interval: 2s

agent_defaults:
  harness: ${MAESTRO_HARNESS}
  workspace: ${MAESTRO_WORKSPACE}
  approval_policy: ${MAESTRO_APPROVAL_POLICY}
  max_concurrent: 2
  stall_timeout: 10m
$(if [[ "${MAESTRO_HARNESS}" == "claude-code" ]]; then printf '  claude:\n    model: %s\n' "${MAESTRO_CLAUDE_MODEL}"; fi)

user:
  name: "${MAESTRO_USER_NAME}"
  gitlab_username: "${MAESTRO_GITLAB_USERNAME}"
  linear_username: "${MAESTRO_LINEAR_USERNAME}"

sources:
  - name: epic-platform
    display_group: Planning
    tags: [platform, exact-epic]
    tracker: gitlab-epic
    epic_filter:
      iids: [${epic_platform_iid}]
    issue_filter:
      labels: [${epic_platform_issue_label}]
$(if [[ -n "${MAESTRO_GITLAB_USERNAME}" ]]; then printf '      assignee: %s\n' "${MAESTRO_GITLAB_USERNAME}"; fi)
    agent_type: many_epic_platform

  - name: epic-infra
    display_group: Planning
    tags: [infra]
    tracker: gitlab-epic
    epic_filter:
      labels: [${epic_infra_label}]
    issue_filter:
      labels: [${epic_infra_issue_label}]
$(if [[ -n "${MAESTRO_GITLAB_USERNAME}" ]]; then printf '      assignee: %s\n' "${MAESTRO_GITLAB_USERNAME}"; fi)
    agent_type: many_epic_infra

  - name: epic-growth
    display_group: Planning
    tags: [growth]
    tracker: gitlab-epic
    epic_filter:
      labels: [${epic_growth_label}]
    issue_filter:
      labels: [${epic_growth_issue_label}]
$(if [[ -n "${MAESTRO_GITLAB_USERNAME}" ]]; then printf '      assignee: %s\n' "${MAESTRO_GITLAB_USERNAME}"; fi)
    agent_type: many_epic_growth

  - name: gitlab-app
    display_group: Delivery
    tags: [app, backend]
    tracker: gitlab
    filter:
      labels: [${project_app_label}]
$(if [[ -n "${MAESTRO_GITLAB_USERNAME}" ]]; then printf '      assignee: %s\n' "${MAESTRO_GITLAB_USERNAME}"; fi)
    agent_type: many_project_app

  - name: gitlab-ops
    display_group: Delivery
    tags: [ops]
    tracker: gitlab
    filter:
      labels: [${project_ops_label}]
$(if [[ -n "${MAESTRO_GITLAB_USERNAME}" ]]; then printf '      assignee: %s\n' "${MAESTRO_GITLAB_USERNAME}"; fi)
    agent_type: many_project_ops

  - name: linear-product
    display_group: Planning
    tags: [product, triage]
    tracker: linear
    filter:
      labels: [${linear_label_name}]
$(if [[ -n "${MAESTRO_LINEAR_USERNAME}" ]]; then printf '      assignee: %s\n' "${MAESTRO_LINEAR_USERNAME}"; fi)
    agent_type: many_linear

agent_types:
  - name: many_epic_platform
    agent_pack: code-pr
    instance_name: many-epic-platform
    prompt: ${tmpdir}/many_epic_platform.md

  - name: many_epic_infra
    agent_pack: repo-maintainer
    instance_name: many-epic-infra
    prompt: ${tmpdir}/many_epic_infra.md

  - name: many_epic_growth
    agent_pack: repo-maintainer
    instance_name: many-epic-growth
    prompt: ${tmpdir}/many_epic_growth.md

  - name: many_project_app
    agent_pack: code-pr
    instance_name: many-project-app
    prompt: ${tmpdir}/many_project_app.md

  - name: many_project_ops
    agent_pack: repo-maintainer
    instance_name: many-project-ops
    prompt: ${tmpdir}/many_project_ops.md

  - name: many_linear
    agent_pack: triage
    instance_name: many-linear
    prompt: ${tmpdir}/many_linear.md

workspace:
  root: ${workspace_root}

state:
  dir: ${state_root}
  retry_base: 2s
  max_retry_backoff: 10s
  max_attempts: 2

hooks:
  timeout: 30s

logging:
  level: info
  dir: ${logs_root}
  max_files: 5
EOF

(
  cd "${ROOT}"
  go build -o "${binary_path}" ./cmd/maestro
  "${binary_path}" run --config "${config_path}" --no-tui
) >"${tmpdir}/maestro.stdout" 2>&1 &
maestro_pid=$!

deadline=$(( $(date +%s) + MAESTRO_TIMEOUT_SECONDS ))
while (( $(date +%s) < deadline )); do
  if ! kill -0 "${maestro_pid}" 2>/dev/null; then
    echo "maestro exited before many-sources smoke completed" >&2
    cat "${tmpdir}/maestro.stdout" >&2 || true
    exit 1
  fi
  if run_failed; then
    echo "Many-sources smoke run failed" >&2
    cat "${tmpdir}/maestro.stdout" >&2 || true
    exit 1
  fi

  all_done=1
  for spec in "${markers[@]}"; do
    IFS=: read -r _ marker filename <<<"${spec}"
    result_file="$(find "${workspace_root}" -name "${filename}" -print -quit 2>/dev/null || true)"
    if [[ -z "${result_file}" ]] || ! grep -qx "${marker}" "${result_file}"; then
      all_done=0
      break
    fi
  done

  if [[ "${all_done}" -eq 1 ]]; then
    runs_output="$("${binary_path}" inspect runs --config "${config_path}")"
    if [[ "$(printf '%s' "${runs_output}" | grep -c '^Active: none$')" -eq 6 ]]; then
      break
    fi
  fi
  sleep 2
done

if [[ "${all_done:-0}" -ne 1 ]]; then
  echo "timed out waiting for many-sources smoke result" >&2
  cat "${tmpdir}/maestro.stdout" >&2 || true
  exit 1
fi

cleanup
trap - EXIT

echo "Many-sources smoke passed."
echo "Config: ${config_path}"
echo "Workspace root: ${workspace_root}"
echo "Linear issue: ${linear_issue_identifier}"
echo "GitLab project label: ${project_app_label}"
echo "GitLab ops label: ${project_ops_label}"
echo "GitLab exact epic iid: ${epic_platform_iid}"
echo "Logs: ${logs_root}"
echo "Stdout: ${tmpdir}/maestro.stdout"
