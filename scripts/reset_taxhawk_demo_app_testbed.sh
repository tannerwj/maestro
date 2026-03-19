#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
METADATA_PATH="${ROOT}/demo/taxhawk-demo-app-testbed.json"
DEMO_VAR_DIR="${ROOT}/demo/taxhawk-demo-app/var"

: "${MAESTRO_TAXHAWK_GITLAB_BASE_URL:?set MAESTRO_TAXHAWK_GITLAB_BASE_URL}"
: "${MAESTRO_TAXHAWK_GITLAB_TOKEN:?set MAESTRO_TAXHAWK_GITLAB_TOKEN}"

if [[ ! -f "${METADATA_PATH}" ]]; then
  echo "missing metadata file: ${METADATA_PATH}" >&2
  exit 1
fi

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

require_cmd jq
require_cmd git
require_cmd python3
require_cmd curl

api_base="${MAESTRO_TAXHAWK_GITLAB_BASE_URL%/}"
project_id="$(jq -r '.project.id' "${METADATA_PATH}")"
project_path="$(jq -r '.project.path_with_namespace' "${METADATA_PATH}")"
default_branch="$(jq -r '.project.default_branch' "${METADATA_PATH}")"
initial_commit_sha="$(jq -r '.project.initial_commit_sha' "${METADATA_PATH}")"
epic_id="$(jq -r '.epic.id' "${METADATA_PATH}")"

gitlab_api() {
  local method="$1"
  local path="$2"
  shift 2
  curl -fsS -H "PRIVATE-TOKEN: ${MAESTRO_TAXHAWK_GITLAB_TOKEN}" -X "${method}" "$@" "${api_base}${path}"
}

basic_auth_header() {
  python3 - <<'PY' "${MAESTRO_TAXHAWK_GITLAB_TOKEN}"
import base64
import sys

raw = f"oauth2:{sys.argv[1]}".encode()
print("Authorization: Basic " + base64.b64encode(raw).decode())
PY
}

reset_issue() {
  local iid="$1"
  local labels_csv="$2"
  gitlab_api PUT "/api/v4/projects/${project_id}/issues/${iid}" \
    --data-urlencode "state_event=reopen" \
    --data-urlencode "labels=${labels_csv}" \
    --data-urlencode "epic_id=${epic_id}" >/dev/null
}

tmpdir="$(mktemp -d)"
cleanup() {
  rm -rf "${tmpdir}"
}
trap cleanup EXIT

auth_header="$(basic_auth_header)"
repo_url="${api_base}/${project_path}.git"
repo_dir="${tmpdir}/repo"

git -c "http.extraHeader=${auth_header}" clone "${repo_url}" "${repo_dir}" >/dev/null 2>&1
(
  cd "${repo_dir}"
  git reset --hard "${initial_commit_sha}" >/dev/null
  git clean -fd >/dev/null
  git -c "http.extraHeader=${auth_header}" push --force origin "HEAD:${default_branch}" >/dev/null
)

while IFS=$'\t' read -r iid labels_csv; do
  reset_issue "${iid}" "${labels_csv}"
done < <(jq -r '.issues[] | [.iid, (.labels | join(","))] | @tsv' "${METADATA_PATH}")

rm -rf "${DEMO_VAR_DIR}/state" "${DEMO_VAR_DIR}/logs" "${DEMO_VAR_DIR}/workspaces"
mkdir -p "${DEMO_VAR_DIR}/state" "${DEMO_VAR_DIR}/logs" "${DEMO_VAR_DIR}/workspaces"

echo "TaxHawk demo app testbed reset."
echo "Project: ${project_path}"
echo "Branch: ${default_branch}"
echo "Reset commit: ${initial_commit_sha}"
echo "Epic: $(jq -r '.epic.title' "${METADATA_PATH}")"
echo "Local var reset: ${DEMO_VAR_DIR}"
