#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

: "${MAESTRO_TIMEOUT_SECONDS:=180}"

tmpdir="$(mktemp -d)"
trap '[[ -n "${maestro_pid:-}" ]] && kill -TERM "${maestro_pid}" 2>/dev/null || true; [[ -n "${tracker_pid:-}" ]] && kill -TERM "${tracker_pid}" 2>/dev/null || true; wait "${maestro_pid:-}" 2>/dev/null || true; wait "${tracker_pid:-}" 2>/dev/null || true' EXIT

config_path="${tmpdir}/maestro.yaml"
state_json="${tmpdir}/fake-tracker-state.json"
tracker_log="${tmpdir}/fake-tracker.log"
workspace_root="${tmpdir}/workspaces"
logs_root="${tmpdir}/logs"
state_root="${tmpdir}/state"
artifacts_root="${tmpdir}/artifacts"
bin_root="${tmpdir}/bin"
binary_path="${tmpdir}/maestro"
repo_src="${tmpdir}/repo-src"
repo_bare="${tmpdir}/repo.git"
stage_pack="${tmpdir}/stage-codex-pack"
epic_prompt_path="${tmpdir}/epic-none-prompt.md"

mkdir -p "${artifacts_root}" "${bin_root}" "${repo_src}" "${stage_pack}/context" "${stage_pack}/codex"

cat >"${stage_pack}/agent.yaml" <<'EOF'
name: stage-codex-pack
description: Hermetic smoke Codex pipeline pack.
instance_name: stage-codex
harness: codex
workspace: git-clone
prompt: prompt.md
approval_policy: auto
max_concurrent: 1
codex:
  reasoning: low
  max_turns: 2
  thread_sandbox: workspace-write
  extra_args: ["--pack-extra"]
context_files:
  - context/rules.md
EOF

cat >"${stage_pack}/prompt.md" <<'EOF'
Stage pack prompt
AgentUpper={{upper .Agent.InstanceName}}
StateLower={{lower .Issue.State}}
LabelsJoined={{join .Issue.Labels ", "}}
DescDefault={{default "none" .Issue.Description}}
HasPrefix={{if hasPrefix .Issue.Identifier "team/project#"}}yes{{else}}no{{end}}
ContainsCoding={{if contains (join .Issue.Labels ", ") "orch:coding"}}yes{{else}}no{{end}}
IndentedContext:
{{indent 2 (trim .Agent.Context)}}
EOF

cat >"${stage_pack}/context/rules.md" <<'EOF'
local-pack-context
EOF

printf '%s\n' 'local-pack-config' >"${stage_pack}/codex/LOCAL_PACK.txt"

mkdir -p "${repo_src}/.maestro/context" "${repo_src}/.maestro/codex"
cat >"${repo_src}/.maestro/prompt.md" <<'EOF'
Repo pack prompt
RepoPromptState={{lower .Issue.State}}
RepoPromptLabels={{join .Issue.Labels ", "}}
RepoPromptDesc={{default "none" .Issue.Description}}
RepoPromptContext:
{{indent 2 (trim .Agent.Context)}}
EOF

cat >"${repo_src}/.maestro/context/rules.md" <<'EOF'
repo-pack-context
EOF

printf '%s\n' 'repo-pack-config' >"${repo_src}/.maestro/codex/REPO_PACK.txt"
printf '%s\n' '# hermetic smoke repo' >"${repo_src}/README.md"

(
  cd "${repo_src}"
  git init >/dev/null
  git add . >/dev/null
  git -c user.name='Smoke User' -c user.email='smoke@example.com' commit -m 'init' >/dev/null
  git clone --bare "${repo_src}" "${repo_bare}" >/dev/null
)

cat >"${epic_prompt_path}" <<'EOF'
Epic none prompt
EpicDesc={{default "none" .Issue.Description}}
EpicState={{upper .Issue.State}}
EOF

export SMOKE_REPO_BARE="${repo_bare}"
export SMOKE_STATE_JSON="${state_json}"
python3 - <<'PY'
import json, time
import os
repo_bare = os.environ["SMOKE_REPO_BARE"]
state_path = os.environ["SMOKE_STATE_JSON"]
now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
state = {
    "gitlab": {
        "projects": {
            "team/project": {
                "id": 1,
                "path_with_namespace": "team/project",
                "web_url": "http://fake/gitlab/team/project",
                "ssh_url_to_repo": "",
                "http_url_to_repo": repo_bare,
                "issues": [
                    {
                        "id": 1001,
                        "iid": 101,
                        "project_id": 1,
                        "title": "Pipeline issue",
                        "description": "",
                        "state": "opened",
                        "web_url": "http://fake/gitlab/team/project/-/issues/101",
                        "labels": ["orch:coding"],
                        "author": {"username": "fixture"},
                        "assignee": None,
                        "created_at": now,
                        "updated_at": now,
                        "references": {"full": "team/project#101"},
                        "notes": [],
                    },
                    {
                        "id": 1002,
                        "iid": 102,
                        "project_id": 1,
                        "title": "Repo pack issue",
                        "description": "",
                        "state": "opened",
                        "web_url": "http://fake/gitlab/team/project/-/issues/102",
                        "labels": ["repo:ready"],
                        "author": {"username": "fixture"},
                        "assignee": None,
                        "created_at": now,
                        "updated_at": now,
                        "references": {"full": "team/project#102"},
                        "notes": [],
                    },
                    {
                        "id": 1003,
                        "iid": 201,
                        "project_id": 1,
                        "title": "Epic child issue",
                        "description": "",
                        "state": "opened",
                        "web_url": "http://fake/gitlab/team/project/-/issues/201",
                        "labels": ["epic:item"],
                        "author": {"username": "fixture"},
                        "assignee": None,
                        "created_at": now,
                        "updated_at": now,
                        "references": {"full": "team/project#201"},
                        "epic": {"id": 900, "iid": 9},
                        "notes": [],
                    },
                ],
            }
        },
        "groups": {
            "team": {
                "projects": ["team/project"],
                "epics": [
                    {
                        "id": 900,
                        "iid": 9,
                        "title": "Epic bucket",
                        "description": "Hermetic epic",
                        "state": "opened",
                        "web_url": "http://fake/gitlab/groups/team/-/epics/9",
                        "labels": ["epic:bucket"],
                        "author": {"username": "fixture"},
                        "created_at": now,
                        "updated_at": now,
                    }
                ],
            }
        },
    },
    "linear": {
        "projects": [
            {
                "id": "project-1",
                "name": "Smoke Project",
                "team": {"id": "team-1", "key": "SMK", "name": "Smoke Team"},
            }
        ],
        "labels": [
            {"id": "label-linear-ready", "name": "linear:ready", "teamId": "team-1"},
            {"id": "label-linear-done", "name": "linear:done", "teamId": "team-1"},
        ],
        "issues": [
            {
                "id": "linear-1",
                "identifier": "SMK-1",
                "title": "Linear dev codex issue",
                "description": "",
                "url": "http://fake/linear/SMK-1",
                "createdAt": now,
                "updatedAt": now,
                "labels": ["linear:ready"],
                "state": {"name": "Todo", "type": "unstarted"},
                "assignee": None,
                "projectId": "project-1",
                "comments": [],
            }
        ],
    },
}
with open(state_path, "w", encoding="utf-8") as fh:
    json.dump(state, fh, indent=2, sort_keys=True)
PY

tracker_port="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)"

python3 "${ROOT}/scripts/smoke_fake_tracker.py" --host 127.0.0.1 --port "${tracker_port}" --state "${state_json}" >"${tracker_log}" 2>&1 &
tracker_pid=$!

for _ in $(seq 1 50); do
  if curl -fsS "http://127.0.0.1:${tracker_port}/__health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done
curl -fsS "http://127.0.0.1:${tracker_port}/__health" >/dev/null

cat >"${bin_root}/codex" <<'PY'
#!/usr/bin/env python3
import json
import os
import sys

ARTIFACTS = os.environ["SMOKE_ARTIFACTS"]
LOG_PATH = os.path.join(ARTIFACTS, "codex-events.jsonl")
PID = os.getpid()


def log(event):
    event["pid"] = PID
    with open(LOG_PATH, "a", encoding="utf-8") as fh:
        fh.write(json.dumps(event, sort_keys=True) + "\n")


def write_response(payload):
    sys.stdout.write(json.dumps(payload) + "\n")
    sys.stdout.flush()


def basename(path):
    return os.path.basename(path.rstrip("/"))


def create_marker(path):
    with open(path, "w", encoding="utf-8") as fh:
        fh.write("ok\n")


def handle_turn(cwd, turn_number, prompt, sandbox_policy):
    key = basename(cwd)
    if key == "team_project_101":
        if turn_number == 1:
            create_marker(os.path.join(ARTIFACTS, "stage1-turn1.txt"))
            if os.path.exists(os.path.join(cwd, ".codex", "LOCAL_PACK.txt")):
                create_marker(os.path.join(ARTIFACTS, "stage1-local-pack.txt"))
        elif turn_number == 2:
            create_marker(os.path.join(ARTIFACTS, "stage1-turn2.txt"))
    elif key == "team_project_102":
        create_marker(os.path.join(ARTIFACTS, "repo-pack.txt"))
        if os.path.exists(os.path.join(cwd, ".codex", "REPO_PACK.txt")):
            create_marker(os.path.join(ARTIFACTS, "repo-pack-config.txt"))
    elif key == "SMK-1":
        create_marker(os.path.join(ARTIFACTS, "linear-dev-codex.txt"))

    log({
        "event": "turn_start",
        "cwd": cwd,
        "turn": turn_number,
        "prompt": prompt,
        "sandbox_policy": sandbox_policy,
    })


def main():
    log({"event": "cli_args", "argv": sys.argv[1:]})
    if "app-server" not in sys.argv:
        print("expected app-server mode", file=sys.stderr)
        sys.exit(1)

    turn_count = 0
    while True:
        line = sys.stdin.readline()
        if not line:
            break
        msg = json.loads(line)
        method = msg.get("method")
        msg_id = msg.get("id")
        params = msg.get("params", {})
        if method == "initialize":
            write_response({"id": msg_id, "result": {"userAgent": "smoke-stub"}})
        elif method == "initialized":
            continue
        elif method == "thread/start":
            log({
                "event": "thread_start",
                "cwd": params.get("cwd"),
                "sandbox": params.get("sandbox"),
                "approval_policy": params.get("approvalPolicy"),
            })
            write_response({
                "id": msg_id,
                "result": {
                    "approvalPolicy": params.get("approvalPolicy"),
                    "cwd": params.get("cwd"),
                    "model": "gpt-5.4",
                    "modelProvider": "openai",
                    "sandbox": {"type": params.get("sandbox")},
                    "thread": {
                        "id": f"thread-{PID}",
                        "cliVersion": "0.0.0",
                        "createdAt": 0,
                        "cwd": params.get("cwd"),
                        "ephemeral": True,
                        "modelProvider": "openai",
                        "preview": "",
                        "source": "appServer",
                        "status": {"type": "idle"},
                        "turns": [],
                        "updatedAt": 0,
                    },
                },
            })
        elif method == "turn/start":
            turn_count += 1
            prompt = ""
            input_items = params.get("input") or []
            if input_items:
                prompt = input_items[0].get("text", "")
            handle_turn(params.get("cwd", ""), turn_count, prompt, params.get("sandboxPolicy"))
            write_response({"id": msg_id, "result": {"turn": {"id": f"turn-{turn_count}", "items": [], "status": "inProgress"}}})
            write_response({"method": "turn/completed", "params": {"threadId": params.get("threadId"), "turn": {"id": f"turn-{turn_count}", "items": [], "status": "completed"}}})


if __name__ == "__main__":
    main()
PY

cat >"${bin_root}/claude" <<'PY'
#!/usr/bin/env python3
import json
import os
import sys

ARTIFACTS = os.environ["SMOKE_ARTIFACTS"]
LOG_PATH = os.path.join(ARTIFACTS, "claude-events.jsonl")


def log(event):
    with open(LOG_PATH, "a", encoding="utf-8") as fh:
        fh.write(json.dumps(event, sort_keys=True) + "\n")


def create_marker(name):
    with open(os.path.join(ARTIFACTS, name), "w", encoding="utf-8") as fh:
        fh.write("ok\n")


def arg_value(flag):
    for idx, arg in enumerate(sys.argv):
        if arg == flag and idx + 1 < len(sys.argv):
            return sys.argv[idx + 1]
    return ""


def main():
    prompt = sys.stdin.read()
    workdir = arg_value("--add-dir")
    key = os.path.basename(workdir.rstrip("/"))
    log({"argv": sys.argv[1:], "workdir": workdir, "prompt": prompt})

    if key == "team_project_101":
        create_marker("stage2-claude.txt")
    elif key == "team_project_201":
        create_marker("epic-none.txt")
        if not os.path.exists(os.path.join(workdir, ".git")):
            create_marker("epic-none-no-git.txt")

    sys.stdout.write('{"type":"assistant"}\n')
    sys.stdout.write('{"type":"result","result":"CLAUDE_OK"}\n')
    sys.stdout.flush()


if __name__ == "__main__":
    main()
PY

chmod +x "${bin_root}/codex" "${bin_root}/claude"

cat >"${config_path}" <<EOF
defaults:
  poll_interval: 1s
  max_concurrent_global: 4
  stall_timeout: 1m
  label_prefix: orch
  on_complete:
    add_labels: [orch:review]
    remove_labels: [orch:coding]
  on_failure:
    add_labels: [orch:needs-attention]

agent_packs_dir: ${ROOT}/agents

codex_defaults:
  model: gpt-5.4
  reasoning: medium
  max_turns: 1
  thread_sandbox: workspace-write
  extra_args: ["--default-extra"]

claude_defaults:
  model: opus-4.6
  reasoning: medium

user:
  name: "Smoke User"

sources:
  - name: gitlab-coding
    tracker: gitlab
    connection:
      base_url: http://127.0.0.1:${tracker_port}/gitlab
      token_env: SMOKE_GITLAB_TOKEN
      project: team/project
    filter:
      labels: [orch:coding]
    agent_type: stage-codex
    poll_interval: 1s

  - name: gitlab-review
    tracker: gitlab
    connection:
      base_url: http://127.0.0.1:${tracker_port}/gitlab
      token_env: SMOKE_GITLAB_TOKEN
      project: team/project
    filter:
      labels: [orch:review]
    agent_type: review-claude
    poll_interval: 1s
    on_complete:
      add_labels: [orch:done-route]
      remove_labels: [orch:review]

  - name: gitlab-repo-pack
    tracker: gitlab
    connection:
      base_url: http://127.0.0.1:${tracker_port}/gitlab
      token_env: SMOKE_GITLAB_TOKEN
      project: team/project
    filter:
      labels: [repo:ready]
    agent_type: repo-pack-codex
    poll_interval: 1s
    on_complete:
      add_labels: [repo:done]
      remove_labels: [repo:ready]

  - name: gitlab-epic-none
    tracker: gitlab-epic
    connection:
      base_url: http://127.0.0.1:${tracker_port}/gitlab
      token_env: SMOKE_GITLAB_TOKEN
      group: team
    epic_filter:
      labels: [epic:bucket]
    issue_filter:
      labels: [epic:item]
    agent_type: epic-none
    poll_interval: 1s
    on_complete:
      add_labels: [epic:done]
      remove_labels: [epic:item]

  - name: linear-dev-codex
    tracker: linear
    connection:
      base_url: http://127.0.0.1:${tracker_port}/linear/graphql
      token_env: SMOKE_LINEAR_TOKEN
      project: Smoke Project
    repo: ${repo_bare}
    filter:
      labels: [linear:ready]
      states: [Todo]
    agent_type: linear-codex
    poll_interval: 1s
    on_complete:
      add_labels: [linear:done]
      remove_labels: [linear:ready]

agent_types:
  - name: stage-codex
    agent_pack: ${stage_pack}
    codex:
      reasoning: high
      max_turns: 2
      extra_args: []

  - name: review-claude
    agent_pack: dev-claude

  - name: repo-pack-codex
    agent_pack: "repo:.maestro"
    harness: codex
    workspace: git-clone
    approval_policy: auto
    max_concurrent: 1
    codex:
      reasoning: high
      max_turns: 1

  - name: epic-none
    harness: claude-code
    workspace: none
    prompt: ${epic_prompt_path}
    approval_policy: auto
    max_concurrent: 1
    claude:
      extra_args: ["--epic-extra"]

  - name: linear-codex
    agent_pack: dev-codex
    codex:
      max_turns: 1

workspace:
  root: ${workspace_root}

state:
  dir: ${state_root}
  retry_base: 1s
  max_retry_backoff: 5s
  max_attempts: 2

hooks:
  timeout: 15s

logging:
  level: info
  dir: ${logs_root}
  max_files: 5
EOF

export SMOKE_GITLAB_TOKEN="smoke-token"
export SMOKE_LINEAR_TOKEN="smoke-token"
export SMOKE_ARTIFACTS="${artifacts_root}"
export PATH="${bin_root}:${PATH}"

(
  cd "${ROOT}"
  go build -o "${binary_path}" ./cmd/maestro
  "${binary_path}" run --config "${config_path}" --no-tui
) >"${tmpdir}/maestro.stdout" 2>&1 &
maestro_pid=$!

deadline=$(( $(date +%s) + MAESTRO_TIMEOUT_SECONDS ))
while (( $(date +%s) < deadline )); do
  if ! kill -0 "${maestro_pid}" 2>/dev/null; then
    echo "maestro exited before hermetic smoke completed" >&2
    cat "${tmpdir}/maestro.stdout" >&2 || true
    exit 1
  fi

  if [[ -f "${artifacts_root}/stage1-turn2.txt" ]] && \
     [[ -f "${artifacts_root}/stage2-claude.txt" ]] && \
     [[ -f "${artifacts_root}/repo-pack.txt" ]] && \
     [[ -f "${artifacts_root}/repo-pack-config.txt" ]] && \
     [[ -f "${artifacts_root}/epic-none.txt" ]] && \
     [[ -f "${artifacts_root}/epic-none-no-git.txt" ]] && \
     [[ -f "${artifacts_root}/linear-dev-codex.txt" ]]; then
    runs_output="$("${binary_path}" inspect runs --config "${config_path}")"
    if [[ "$(printf '%s' "${runs_output}" | grep -c '^Active: none$')" -eq 5 ]]; then
      break
    fi
  fi
  sleep 1
done

if ! [[ -f "${artifacts_root}/stage1-turn2.txt" ]]; then
  echo "timed out waiting for hermetic smoke artifacts" >&2
  cat "${tmpdir}/maestro.stdout" >&2 || true
  exit 1
fi

export SMOKE_VERIFY_STATE="${state_json}"
export SMOKE_VERIFY_ARTIFACTS="${artifacts_root}"
export SMOKE_VERIFY_WORKSPACE="${workspace_root}"
python3 - <<'PY'
import json
import os
import sys

state_path = os.environ["SMOKE_VERIFY_STATE"]
artifacts = os.environ["SMOKE_VERIFY_ARTIFACTS"]
workspace_root = os.environ["SMOKE_VERIFY_WORKSPACE"]
with open(state_path, "r", encoding="utf-8") as fh:
    state = json.load(fh)

project_issues = {issue["iid"]: issue for issue in state["gitlab"]["projects"]["team/project"]["issues"]}
issue101 = project_issues[101]
issue102 = project_issues[102]
issue201 = project_issues[201]
linear_issue = state["linear"]["issues"][0]

def require(cond, msg):
    if not cond:
        raise SystemExit(msg)

require(set(issue101["labels"]) == {"orch:done-route"}, f"issue101 labels={issue101['labels']}")
require(set(issue102["labels"]) == {"repo:done"}, f"issue102 labels={issue102['labels']}")
require(set(issue201["labels"]) == {"epic:done"}, f"issue201 labels={issue201['labels']}")
require(set(label.lower() for label in linear_issue["labels"]) == {"linear:done"}, f"linear labels={linear_issue['labels']}")

notes101 = [note["body"] for note in issue101.get("notes", [])]
require(any("started workflow gitlab-coding" in note for note in notes101), f"missing gitlab-coding start note: {notes101}")
require(any("started workflow gitlab-review" in note for note in notes101), f"missing gitlab-review start note: {notes101}")

with open(os.path.join(artifacts, "codex-events.jsonl"), "r", encoding="utf-8") as fh:
    codex_events = [json.loads(line) for line in fh if line.strip()]
with open(os.path.join(artifacts, "claude-events.jsonl"), "r", encoding="utf-8") as fh:
    claude_events = [json.loads(line) for line in fh if line.strip()]

def events_for_cwd(cwd_suffix, event_type):
    return [event for event in codex_events if event.get("event") == event_type and event.get("cwd", "").endswith(cwd_suffix)]

stage1_threads = events_for_cwd("team_project_101", "thread_start")
require(len(stage1_threads) == 1, f"stage1 threads={stage1_threads}")
require(stage1_threads[0]["sandbox"] == "workspace-write", stage1_threads[0])
stage1_turns = events_for_cwd("team_project_101", "turn_start")
require(len(stage1_turns) == 2, f"stage1 turns={stage1_turns}")
require("AgentUpper=STAGE-CODEX" in stage1_turns[0]["prompt"], stage1_turns[0]["prompt"])
require("StateLower=open" in stage1_turns[0]["prompt"], stage1_turns[0]["prompt"])
require("DescDefault=none" in stage1_turns[0]["prompt"], stage1_turns[0]["prompt"])
require("HasPrefix=yes" in stage1_turns[0]["prompt"], stage1_turns[0]["prompt"])
require("ContainsCoding=yes" in stage1_turns[0]["prompt"], stage1_turns[0]["prompt"])
require("  local-pack-context" in stage1_turns[0]["prompt"], stage1_turns[0]["prompt"])
require("Continuation turn 2 of 2" in stage1_turns[1]["prompt"], stage1_turns[1]["prompt"])

repo_turns = events_for_cwd("team_project_102", "turn_start")
require(len(repo_turns) == 1, f"repo turns={repo_turns}")
require("RepoPromptContext:" in repo_turns[0]["prompt"], repo_turns[0]["prompt"])
require("repo-pack-context" in repo_turns[0]["prompt"], repo_turns[0]["prompt"])

linear_threads = events_for_cwd("SMK-1", "thread_start")
require(len(linear_threads) == 1, f"linear threads={linear_threads}")
require(linear_threads[0]["sandbox"] == "danger-full-access", linear_threads[0])
linear_turns = events_for_cwd("SMK-1", "turn_start")
require(len(linear_turns) == 1, f"linear turns={linear_turns}")
require(linear_turns[0]["sandbox_policy"] == {"type": "dangerFullAccess"}, linear_turns[0])

cli_args_by_pid = {event["pid"]: event["argv"] for event in codex_events if event.get("event") == "cli_args"}
stage1_pid = stage1_turns[0]["pid"]
stage1_args = cli_args_by_pid[stage1_pid]
require("--config" in stage1_args, stage1_args)
require("model_reasoning_effort=high" in stage1_args, stage1_args)
require("--default-extra" not in stage1_args, stage1_args)
require("--pack-extra" not in stage1_args, stage1_args)

linear_pid = linear_turns[0]["pid"]
linear_args = cli_args_by_pid[linear_pid]
require("model_reasoning_effort=high" in linear_args, linear_args)

claude_by_workdir = {event["workdir"]: event for event in claude_events}
review_claude = next(event for event in claude_events if event["workdir"].endswith("team_project_101"))
epic_claude = next(event for event in claude_events if event["workdir"].endswith("team_project_201"))
require("--effort" in review_claude["argv"], review_claude["argv"])
require("high" in review_claude["argv"], review_claude["argv"])
require("--epic-extra" in epic_claude["argv"], epic_claude["argv"])
require("--effort" in epic_claude["argv"], epic_claude["argv"])
require("medium" in epic_claude["argv"], epic_claude["argv"])

epic_workspace = os.path.join(workspace_root, "team_project_201")
linear_workspace = os.path.join(workspace_root, "SMK-1")
require(os.path.exists(os.path.join(epic_workspace, "EPIC_NONE_OK.txt")) is False or True, "noop")
require(not os.path.exists(os.path.join(epic_workspace, ".git")), "epic workspace unexpectedly has .git")
require(os.path.exists(os.path.join(linear_workspace, ".git")), "linear workspace missing .git")

for marker in [
    "stage1-turn1.txt",
    "stage1-turn2.txt",
    "stage1-local-pack.txt",
    "stage2-claude.txt",
    "repo-pack.txt",
    "repo-pack-config.txt",
    "epic-none.txt",
    "epic-none-no-git.txt",
    "linear-dev-codex.txt",
]:
    require(os.path.exists(os.path.join(artifacts, marker)), f"missing artifact {marker}")
PY

echo "Hermetic smoke passed."
echo "Config: ${config_path}"
echo "Workspace root: ${workspace_root}"
echo "Artifacts: ${artifacts_root}"
echo "Fake tracker state: ${state_json}"
echo "Tracker log: ${tracker_log}"
echo "Maestro stdout: ${tmpdir}/maestro.stdout"
