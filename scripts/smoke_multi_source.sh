#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

: "${MAESTRO_HARNESS:=claude-code}"
: "${MAESTRO_APPROVAL_POLICY:=auto}"
: "${MAESTRO_TIMEOUT_SECONDS:=420}"
: "${MAESTRO_GITLAB_BASE_URL:=https://gitlab.com}"
: "${MAESTRO_USER_NAME:=Smoke User}"
: "${MAESTRO_GITLAB_USERNAME:=}"
: "${MAESTRO_LINEAR_USERNAME:=}"

if [[ "${MAESTRO_APPROVAL_POLICY}" != "auto" ]]; then
  echo "scripts/smoke_multi_source.sh currently supports MAESTRO_APPROVAL_POLICY=auto only" >&2
  exit 1
fi

: "${MAESTRO_GITLAB_TOKEN:?set MAESTRO_GITLAB_TOKEN}"
: "${MAESTRO_GITLAB_PROJECT:?set MAESTRO_GITLAB_PROJECT}"
: "${MAESTRO_GITLAB_PROJECT_LABEL:?set MAESTRO_GITLAB_PROJECT_LABEL}"
: "${MAESTRO_GITLAB_EPIC_GROUP:?set MAESTRO_GITLAB_EPIC_GROUP}"
: "${MAESTRO_GITLAB_EPIC_LABEL:?set MAESTRO_GITLAB_EPIC_LABEL}"
: "${MAESTRO_GITLAB_EPIC_ISSUE_LABEL:=}"
: "${MAESTRO_GITLAB_EPIC_REPO:?set MAESTRO_GITLAB_EPIC_REPO}"
: "${MAESTRO_LINEAR_TOKEN:?set MAESTRO_LINEAR_TOKEN}"
: "${MAESTRO_LINEAR_PROJECT:?set MAESTRO_LINEAR_PROJECT}"
: "${MAESTRO_LINEAR_LABEL:?set MAESTRO_LINEAR_LABEL}"
: "${MAESTRO_LINEAR_REPO:?set MAESTRO_LINEAR_REPO}"

tmpdir="$(mktemp -d)"
config_path="${tmpdir}/maestro.yaml"
gitlab_project_prompt_path="${tmpdir}/gitlab-project-prompt.md"
gitlab_epic_prompt_path="${tmpdir}/gitlab-epic-prompt.md"
linear_prompt_path="${tmpdir}/linear-prompt.md"
workspace_root="${tmpdir}/workspaces"
logs_root="${tmpdir}/logs"
state_root="${tmpdir}/state"
binary_path="${tmpdir}/maestro"

gitlab_project_marker="MAESTRO_MULTI_GITLAB_PROJECT_OK"
gitlab_epic_marker="MAESTRO_MULTI_GITLAB_EPIC_OK"
linear_marker="MAESTRO_MULTI_LINEAR_OK"

cleanup() {
  if [[ -n "${maestro_pid:-}" ]] && kill -0 "${maestro_pid}" 2>/dev/null; then
    kill -TERM "${maestro_pid}" 2>/dev/null || true
    sleep 1
    kill -KILL "${maestro_pid}" 2>/dev/null || true
  fi
  wait "${maestro_pid:-}" 2>/dev/null || true
}
trap cleanup EXIT

cat >"${gitlab_project_prompt_path}" <<EOF
Create a file named MULTI_SOURCE_GITLAB_PROJECT.md in the repository root containing exactly ${gitlab_project_marker}.
Then reply with exactly ${gitlab_project_marker}.
EOF

cat >"${gitlab_epic_prompt_path}" <<EOF
Create a file named MULTI_SOURCE_GITLAB_EPIC.md in the repository root containing exactly ${gitlab_epic_marker}.
Then reply with exactly ${gitlab_epic_marker}.
EOF

cat >"${linear_prompt_path}" <<EOF
Create a file named MULTI_SOURCE_LINEAR.md in the repository root containing exactly ${linear_marker}.
Then reply with exactly ${linear_marker}.
EOF

cat >"${config_path}" <<EOF
defaults:
  poll_interval: 2s
  max_concurrent_global: 2
  stall_timeout: 10m

agent_packs_dir: ${ROOT}/agents

user:
  name: "${MAESTRO_USER_NAME}"
  gitlab_username: "${MAESTRO_GITLAB_USERNAME}"
  linear_username: "${MAESTRO_LINEAR_USERNAME}"

sources:
  - name: gitlab-project
    tracker: gitlab
    connection:
      base_url: ${MAESTRO_GITLAB_BASE_URL}
      token_env: MAESTRO_GITLAB_TOKEN
      project: ${MAESTRO_GITLAB_PROJECT}
    filter:
      labels: [${MAESTRO_GITLAB_PROJECT_LABEL}]
$(if [[ -n "${MAESTRO_GITLAB_USERNAME}" ]]; then printf '      assignee: %s\n' "${MAESTRO_GITLAB_USERNAME}"; fi)
    agent_type: gitlab-project
    poll_interval: 2s

  - name: gitlab-epic
    tracker: gitlab-epic
    connection:
      base_url: ${MAESTRO_GITLAB_BASE_URL}
      token_env: MAESTRO_GITLAB_TOKEN
      group: ${MAESTRO_GITLAB_EPIC_GROUP}
    repo: ${MAESTRO_GITLAB_EPIC_REPO}
    epic_filter:
      labels: [${MAESTRO_GITLAB_EPIC_LABEL}]
    issue_filter:
$(if [[ -n "${MAESTRO_GITLAB_EPIC_ISSUE_LABEL}" ]]; then printf '      labels: [%s]\n' "${MAESTRO_GITLAB_EPIC_ISSUE_LABEL}"; fi)
$(if [[ -n "${MAESTRO_GITLAB_USERNAME}" ]]; then printf '      assignee: %s\n' "${MAESTRO_GITLAB_USERNAME}"; fi)
    agent_type: gitlab-epic
    poll_interval: 2s

  - name: linear
    tracker: linear
    connection:
      token_env: MAESTRO_LINEAR_TOKEN
      project: ${MAESTRO_LINEAR_PROJECT}
    repo: ${MAESTRO_LINEAR_REPO}
    filter:
      labels: [${MAESTRO_LINEAR_LABEL}]
$(if [[ -n "${MAESTRO_LINEAR_USERNAME}" ]]; then printf '      assignee: %s\n' "${MAESTRO_LINEAR_USERNAME}"; fi)
    agent_type: linear
    poll_interval: 2s

agent_types:
  - name: gitlab-project
    agent_pack: code-pr
    instance_name: gitlab-project-smoke
    harness: ${MAESTRO_HARNESS}
    workspace: git-clone
    prompt: ${gitlab_project_prompt_path}
    approval_policy: ${MAESTRO_APPROVAL_POLICY}
    max_concurrent: 1
    stall_timeout: 10m

  - name: gitlab-epic
    agent_pack: code-pr
    instance_name: gitlab-epic-smoke
    harness: ${MAESTRO_HARNESS}
    workspace: git-clone
    prompt: ${gitlab_epic_prompt_path}
    approval_policy: ${MAESTRO_APPROVAL_POLICY}
    max_concurrent: 1
    stall_timeout: 10m

  - name: linear
    agent_pack: code-pr
    instance_name: linear-smoke
    harness: ${MAESTRO_HARNESS}
    workspace: git-clone
    prompt: ${linear_prompt_path}
    approval_policy: ${MAESTRO_APPROVAL_POLICY}
    max_concurrent: 1
    stall_timeout: 10m

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
gitlab_project_result=""
gitlab_epic_result=""
linear_result=""
while (( $(date +%s) < deadline )); do
  if ! kill -0 "${maestro_pid}" 2>/dev/null; then
    echo "maestro exited before multi-source smoke completed" >&2
    cat "${tmpdir}/maestro.stdout" >&2 || true
    exit 1
  fi
  gitlab_project_result="$(find "${workspace_root}" -name MULTI_SOURCE_GITLAB_PROJECT.md -print -quit 2>/dev/null || true)"
  gitlab_epic_result="$(find "${workspace_root}" -name MULTI_SOURCE_GITLAB_EPIC.md -print -quit 2>/dev/null || true)"
  linear_result="$(find "${workspace_root}" -name MULTI_SOURCE_LINEAR.md -print -quit 2>/dev/null || true)"
  if [[ -n "${gitlab_project_result}" ]] && [[ -n "${gitlab_epic_result}" ]] && [[ -n "${linear_result}" ]] && \
     grep -qx "${gitlab_project_marker}" "${gitlab_project_result}" && \
     grep -qx "${gitlab_epic_marker}" "${gitlab_epic_result}" && \
     grep -qx "${linear_marker}" "${linear_result}"; then
    runs_output="$("${binary_path}" inspect runs --config "${config_path}")"
    if [[ "$(printf '%s' "${runs_output}" | grep -c '^Active: none$')" -eq 3 ]]; then
      break
    fi
  fi
  sleep 2
done

if [[ -z "${gitlab_project_result}" ]] || [[ -z "${gitlab_epic_result}" ]] || [[ -z "${linear_result}" ]]; then
  echo "timed out waiting for multi-source smoke result" >&2
  cat "${tmpdir}/maestro.stdout" >&2 || true
  exit 1
fi

cleanup
trap - EXIT

echo "Multi-source smoke passed."
echo "Config: ${config_path}"
echo "GitLab project prompt: ${gitlab_project_prompt_path}"
echo "GitLab epic prompt: ${gitlab_epic_prompt_path}"
echo "Linear prompt: ${linear_prompt_path}"
echo "Workspace root: ${workspace_root}"
echo "GitLab project result: ${gitlab_project_result}"
echo "GitLab epic result: ${gitlab_epic_result}"
echo "Linear result: ${linear_result}"
echo "Logs: ${logs_root}"
echo "Stdout: ${tmpdir}/maestro.stdout"
