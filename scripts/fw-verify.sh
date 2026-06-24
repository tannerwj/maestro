#!/usr/bin/env bash
# fw-verify.sh — Hourly cron script that checks fw:deployed tickets for user feedback.
# Sends a Slack DM when a user comments, with context on whether they confirmed or denied.
#
# Cron: 0 * * * * source ~/.zshrc && /Users/btaylor/Desktop/maestro/scripts/fw-verify.sh
#
# Requires: MAESTRO_TAXHAWK_GITLAB_TOKEN, MAESTRO_SLACK_BOT_TOKEN, MAESTRO_SLACK_USER_ID

set -euo pipefail

GITLAB_HOST="https://git.taxhawk.com"
# GitLab project ID (numeric) or URL-encoded path. Override via env; defaults to
# the firewall-access project (427) so existing cron entries keep working.
PROJECT_ID="${MAESTRO_FW_PROJECT_ID:-427}"
GITLAB_TOKEN="${MAESTRO_TAXHAWK_GITLAB_TOKEN:?missing MAESTRO_TAXHAWK_GITLAB_TOKEN}"
SLACK_TOKEN="${MAESTRO_SLACK_BOT_TOKEN:?missing MAESTRO_SLACK_BOT_TOKEN}"
SLACK_USER="${MAESTRO_SLACK_USER_ID:?missing MAESTRO_SLACK_USER_ID}"
STATE_FILE="/Users/btaylor/.local/state/maestro/fw-verify-seen.json"

mkdir -p "$(dirname "$STATE_FILE")"
[ -f "$STATE_FILE" ] || echo '{}' > "$STATE_FILE"

# Fetch all open issues with fw:deployed label assigned to btaylor.
issues=$(curl -sf -H "PRIVATE-TOKEN: $GITLAB_TOKEN" \
  "${GITLAB_HOST}/api/v4/projects/${PROJECT_ID}/issues?state=opened&labels=fw:deployed&assignee_username=btaylor&per_page=100")

count=$(echo "$issues" | python3 -c "import json,sys; print(len(json.load(sys.stdin)))" 2>/dev/null || echo 0)
if [ "$count" = "0" ]; then
  exit 0
fi

# Process each issue.
echo "$issues" | python3 -c "
import json, sys, os, subprocess, urllib.request, urllib.parse

GITLAB_HOST = '${GITLAB_HOST}'
PROJECT_ID = ${PROJECT_ID}
GITLAB_TOKEN = os.environ['MAESTRO_TAXHAWK_GITLAB_TOKEN']
SLACK_TOKEN = os.environ['MAESTRO_SLACK_BOT_TOKEN']
SLACK_USER = os.environ['MAESTRO_SLACK_USER_ID']
STATE_FILE = '${STATE_FILE}'

# Bot/system usernames to ignore.
IGNORE_USERS = {'btaylor', 'fw-research-agent', 'fw-execute-agent', 'ghost', ''}

# Keywords.
WORKING_KEYWORDS = ['working', 'confirmed', 'looks good', 'all set', 'thanks', 'verified', 'good to go', 'all good']
NOT_WORKING_KEYWORDS = ['not working', 'still can.t connect', 'no access', 'broken', 'denied', 'can.t reach', 'unable to', 'doesn.t work', 'does not work']

def load_state():
    try:
        with open(STATE_FILE) as f:
            return json.load(f)
    except (json.JSONDecodeError, FileNotFoundError):
        return {}

def save_state(state):
    with open(STATE_FILE, 'w') as f:
        json.dump(state, f, indent=2)

def gitlab_get(path):
    req = urllib.request.Request(f'{GITLAB_HOST}/api/v4/{path}')
    req.add_header('PRIVATE-TOKEN', GITLAB_TOKEN)
    with urllib.request.urlopen(req) as resp:
        return json.load(resp)

def slack_dm(text):
    data = json.dumps({'channel': SLACK_USER, 'text': text}).encode()
    req = urllib.request.Request('https://slack.com/api/chat.postMessage', data=data)
    req.add_header('Authorization', f'Bearer {SLACK_TOKEN}')
    req.add_header('Content-Type', 'application/json')
    with urllib.request.urlopen(req) as resp:
        result = json.load(resp)
        if not result.get('ok'):
            print(f'  Slack error: {result.get(\"error\", \"unknown\")}', file=sys.stderr)

def classify(text):
    lower = text.lower()
    import re
    for kw in NOT_WORKING_KEYWORDS:
        if re.search(kw, lower):
            return 'not_working'
    for kw in WORKING_KEYWORDS:
        if kw in lower:
            return 'working'
    return 'unknown'

issues = json.load(sys.stdin)
state = load_state()

for issue in issues:
    iid = issue['iid']
    title = issue['title']
    url = issue['web_url']
    key = str(iid)

    last_seen_id = state.get(key, 0)

    # Fetch comments.
    notes = gitlab_get(f'projects/{PROJECT_ID}/issues/{iid}/notes?per_page=100&sort=asc')

    # Find implementation comment index.
    impl_idx = -1
    for i, note in enumerate(notes):
        body = note.get('body', '')
        if 'Firewall Change Implemented' in body or 'committed and pushed' in body.lower() or 'access should be active' in body.lower():
            impl_idx = i

    if impl_idx < 0:
        continue

    # Check for new user comments after implementation.
    new_max = last_seen_id
    for note in notes[impl_idx + 1:]:
        note_id = note['id']
        if note_id <= last_seen_id:
            continue
        if note.get('system', False):
            continue
        author = note.get('author', {}).get('username', '')
        if author in IGNORE_USERS:
            continue

        body = note.get('body', '').strip()
        if not body:
            continue

        verdict = classify(body)
        new_max = max(new_max, note_id)

        if verdict == 'working':
            slack_dm(f':white_check_mark: *#{iid}* — User confirmed access is working.\n>{body[:200]}\n<{url}|View issue>\n\nClose this ticket?')
            print(f'  #{iid}: user confirmed working')
        elif verdict == 'not_working':
            slack_dm(f':x: *#{iid}* — User reports access is NOT working.\n>{body[:200]}\n<{url}|View issue>\n\nInvestigate firewall logs?')
            print(f'  #{iid}: user reports NOT working')
        else:
            slack_dm(f':speech_balloon: *#{iid}* — New comment from @{author}:\n>{body[:200]}\n<{url}|View issue>')
            print(f'  #{iid}: new comment (unclassified)')

    if new_max > last_seen_id:
        state[key] = new_max

save_state(state)
"
