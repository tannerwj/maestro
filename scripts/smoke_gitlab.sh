#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

: "${MAESTRO_GITLAB_BASE_URL:=https://gitlab.com}"
: "${MAESTRO_HARNESS:=claude-code}"
: "${MAESTRO_APPROVAL_POLICY:=auto}"
: "${MAESTRO_TIMEOUT_SECONDS:=180}"
: "${MAESTRO_GITLAB_LABEL:=agent:ready}"
: "${MAESTRO_USER_NAME:=Smoke User}"
: "${MAESTRO_GITLAB_USERNAME:=}"

if [[ "${MAESTRO_APPROVAL_POLICY}" != "auto" ]]; then
  echo "scripts/smoke_gitlab.sh currently supports MAESTRO_APPROVAL_POLICY=auto only" >&2
  exit 1
fi

: "${MAESTRO_GITLAB_TOKEN:?set MAESTRO_GITLAB_TOKEN}"
: "${MAESTRO_GITLAB_PROJECT:?set MAESTRO_GITLAB_PROJECT}"

tmpdir="$(mktemp -d)"
config_path="${tmpdir}/maestro.yaml"
prompt_path="${tmpdir}/prompt.md"
workspace_root="${tmpdir}/workspaces"
logs_root="${tmpdir}/logs"
state_root="${tmpdir}/state"
marker="MAESTRO_GITLAB_SMOKE_OK"
binary_path="${tmpdir}/maestro"

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
  wait "${maestro_pid:-}" 2>/dev/null || true
}
trap cleanup EXIT

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
  gitlab_username: "${MAESTRO_GITLAB_USERNAME}"

sources:
  - name: gitlab-smoke
    tracker: gitlab
    connection:
      base_url: ${MAESTRO_GITLAB_BASE_URL}
      token_env: MAESTRO_GITLAB_TOKEN
      project: ${MAESTRO_GITLAB_PROJECT}
    filter:
      labels: [${MAESTRO_GITLAB_LABEL}]
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
  echo "timed out waiting for GitLab smoke result" >&2
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

echo "GitLab smoke passed."
echo "Config: ${config_path}"
echo "Prompt: ${prompt_path}"
echo "Workspace root: ${workspace_root}"
echo "Result file: ${result_file}"
echo "Logs: ${logs_root}"
echo "Stdout: ${tmpdir}/maestro.stdout"
