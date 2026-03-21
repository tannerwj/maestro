#!/usr/bin/env python3
import argparse
import json
import threading
import time
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def now_rfc3339():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


class SmokeState:
    def __init__(self, path: str):
        self.path = path
        self.lock = threading.Lock()
        with open(path, "r", encoding="utf-8") as fh:
            self.data = json.load(fh)

    def save(self):
        with open(self.path, "w", encoding="utf-8") as fh:
            json.dump(self.data, fh, indent=2, sort_keys=True)

    def dump(self):
        with self.lock:
            return json.loads(json.dumps(self.data))

    def mutate(self, fn):
        with self.lock:
            result = fn(self.data)
            self.save()
            return result


def normalize_labels(labels):
    return [label.strip().lower() for label in labels if label and label.strip()]


def split_csv(value):
    if not value:
        return []
    return [item.strip().lower() for item in value.split(",") if item.strip()]


def issue_matches(issue, labels=None, state=None, assignee=None):
    if labels:
        label_set = set(normalize_labels(issue.get("labels", [])))
        if any(label not in label_set for label in labels):
            return False
    if state:
        issue_state = issue.get("state", "").strip().lower()
        if state == "opened" and issue_state not in ("opened", "open"):
            return False
        if state == "closed" and issue_state != "closed":
            return False
    if assignee:
        current = ""
        assignee_obj = issue.get("assignee")
        if assignee_obj:
            current = assignee_obj.get("username", "").strip().lower()
        if current != assignee.strip().lower():
            return False
    return True


def linear_issue_payload(issue, project, team):
    return {
        "id": issue["id"],
        "identifier": issue["identifier"],
        "title": issue["title"],
        "description": issue.get("description", ""),
        "url": issue["url"],
        "createdAt": issue["createdAt"],
        "updatedAt": issue["updatedAt"],
        "labels": {"nodes": [{"name": name} for name in issue.get("labels", [])]},
        "state": issue["state"],
        "assignee": issue.get("assignee"),
        "project": {"id": project["id"], "name": project["name"]},
        "team": {"id": team["id"], "key": team["key"], "name": team["name"]},
    }


class Handler(BaseHTTPRequestHandler):
    server_version = "SmokeFakeTracker/1.0"

    def do_GET(self):
        parsed = urllib.parse.urlparse(self.path)
        if parsed.path == "/__health":
            self.send_json(200, {"ok": True})
            return
        if parsed.path == "/__dump":
            self.send_json(200, self.server.state.dump())
            return
        if parsed.path.startswith("/gitlab/api/v4/"):
            self.handle_gitlab_get(parsed)
            return
        self.send_json(404, {"error": "not found"})

    def do_POST(self):
        parsed = urllib.parse.urlparse(self.path)
        if parsed.path in ("/linear", "/linear/graphql"):
            self.handle_linear_graphql()
            return
        if parsed.path.startswith("/gitlab/api/v4/"):
            self.handle_gitlab_post(parsed)
            return
        self.send_json(404, {"error": "not found"})

    def do_PUT(self):
        parsed = urllib.parse.urlparse(self.path)
        if parsed.path.startswith("/gitlab/api/v4/"):
            self.handle_gitlab_put(parsed)
            return
        self.send_json(404, {"error": "not found"})

    def log_message(self, fmt, *args):
        return

    def read_form(self):
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length).decode("utf-8")
        return urllib.parse.parse_qs(raw, keep_blank_values=True)

    def read_json(self):
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length).decode("utf-8")
        return json.loads(raw or "{}")

    def send_json(self, status, payload, headers=None):
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        if headers:
            for key, value in headers.items():
                self.send_header(key, value)
        self.end_headers()
        self.wfile.write(body)

    def handle_gitlab_get(self, parsed):
        path = parsed.path[len("/gitlab/api/v4/"):]
        query = urllib.parse.parse_qs(parsed.query)

        if path.startswith("projects/") and "/issues" not in path:
            project = urllib.parse.unquote(path[len("projects/"):])
            payload = self.server.state.mutate(lambda data: data["gitlab"]["projects"].get(project))
            if payload is None:
                self.send_json(404, {"error": "project not found"})
                return
            self.send_json(200, payload)
            return

        if path.startswith("projects/") and path.endswith("/issues"):
            project = urllib.parse.unquote(path[len("projects/"): -len("/issues")])

            def load(data):
                project_data = data["gitlab"]["projects"].get(project)
                if project_data is None:
                    return None
                issues = [
                    issue for issue in project_data["issues"]
                    if issue_matches(
                        issue,
                        labels=split_csv(query.get("labels", [""])[0]),
                        state=query.get("state", ["opened"])[0],
                        assignee=query.get("assignee_username", [""])[0],
                    )
                ]
                return json.loads(json.dumps(issues))

            payload = self.server.state.mutate(load)
            if payload is None:
                self.send_json(404, {"error": "project not found"})
                return
            self.send_json(200, payload, headers={"X-Next-Page": ""})
            return

        if path.startswith("projects/") and "/issues/" in path:
            project_part, iid_part = path[len("projects/"):].split("/issues/", 1)
            project = urllib.parse.unquote(project_part)
            iid = int(iid_part)

            def load(data):
                project_data = data["gitlab"]["projects"].get(project)
                if project_data is None:
                    return None
                for issue in project_data["issues"]:
                    if issue["iid"] == iid:
                        return json.loads(json.dumps(issue))
                return "missing"

            payload = self.server.state.mutate(load)
            if payload is None or payload == "missing":
                self.send_json(404, {"error": "issue not found"})
                return
            self.send_json(200, payload)
            return

        if path.startswith("groups/") and path.endswith("/epics"):
            group = urllib.parse.unquote(path[len("groups/"): -len("/epics")])

            def load(data):
                group_data = data["gitlab"]["groups"].get(group)
                if group_data is None:
                    return None
                epics = [
                    epic for epic in group_data["epics"]
                    if issue_matches(
                        {"labels": epic.get("labels", []), "state": epic.get("state", "")},
                        labels=split_csv(query.get("labels", [""])[0]),
                        state=query.get("state", ["opened"])[0],
                    )
                ]
                return json.loads(json.dumps(epics))

            payload = self.server.state.mutate(load)
            if payload is None:
                self.send_json(404, {"error": "group not found"})
                return
            self.send_json(200, payload, headers={"X-Next-Page": ""})
            return

        if path.startswith("groups/") and "/epics/" in path:
            group_part, iid_part = path[len("groups/"):].split("/epics/", 1)
            group = urllib.parse.unquote(group_part)
            iid = int(iid_part)

            def load(data):
                group_data = data["gitlab"]["groups"].get(group)
                if group_data is None:
                    return None
                for epic in group_data["epics"]:
                    if epic["iid"] == iid:
                        return json.loads(json.dumps(epic))
                return "missing"

            payload = self.server.state.mutate(load)
            if payload is None or payload == "missing":
                self.send_json(404, {"error": "epic not found"})
                return
            self.send_json(200, payload)
            return

        if path.startswith("groups/") and path.endswith("/issues"):
            group = urllib.parse.unquote(path[len("groups/"): -len("/issues")])

            def load(data):
                group_data = data["gitlab"]["groups"].get(group)
                if group_data is None:
                    return None
                out = []
                for project_name in group_data["projects"]:
                    project_data = data["gitlab"]["projects"][project_name]
                    for issue in project_data["issues"]:
                        if issue_matches(
                            issue,
                            labels=split_csv(query.get("labels", [""])[0]),
                            state=query.get("state", ["opened"])[0],
                            assignee=query.get("assignee_username", [""])[0],
                        ):
                            out.append(json.loads(json.dumps(issue)))
                return out

            payload = self.server.state.mutate(load)
            if payload is None:
                self.send_json(404, {"error": "group not found"})
                return
            self.send_json(200, payload, headers={"X-Next-Page": ""})
            return

        self.send_json(404, {"error": "unknown gitlab path"})

    def handle_gitlab_post(self, parsed):
        path = parsed.path[len("/gitlab/api/v4/"):]
        if path.startswith("projects/") and path.endswith("/notes") and "/issues/" in path:
            project_part, iid_part = path[len("projects/"):].split("/issues/", 1)
            project = urllib.parse.unquote(project_part)
            iid = int(iid_part[:-len("/notes")])
            form = self.read_form()
            body = form.get("body", [""])[0]

            def mutate(data):
                issue = find_gitlab_issue(data, project, iid)
                if issue is None:
                    return None
                issue.setdefault("notes", []).append({"body": body, "created_at": now_rfc3339()})
                issue["updated_at"] = now_rfc3339()
                return {"ok": True}

            payload = self.server.state.mutate(mutate)
            if payload is None:
                self.send_json(404, {"error": "issue not found"})
                return
            self.send_json(200, payload)
            return

        self.send_json(404, {"error": "unknown gitlab path"})

    def handle_gitlab_put(self, parsed):
        path = parsed.path[len("/gitlab/api/v4/"):]
        if path.startswith("projects/") and "/issues/" in path:
            project_part, iid_part = path[len("projects/"):].split("/issues/", 1)
            project = urllib.parse.unquote(project_part)
            iid = int(iid_part)
            form = self.read_form()

            def mutate(data):
                issue = find_gitlab_issue(data, project, iid)
                if issue is None:
                    return None
                labels = normalize_labels(issue.get("labels", []))
                for label in split_csv(form.get("add_labels", [""])[0]):
                    if label not in labels:
                        labels.append(label)
                for label in split_csv(form.get("remove_labels", [""])[0]):
                    labels = [current for current in labels if current != label]
                issue["labels"] = labels
                state_event = form.get("state_event", [""])[0].strip().lower()
                if state_event == "close":
                    issue["state"] = "closed"
                elif state_event == "reopen":
                    issue["state"] = "opened"
                issue["updated_at"] = now_rfc3339()
                return {"ok": True}

            payload = self.server.state.mutate(mutate)
            if payload is None:
                self.send_json(404, {"error": "issue not found"})
                return
            self.send_json(200, payload)
            return

        self.send_json(404, {"error": "unknown gitlab path"})

    def handle_linear_graphql(self):
        body = self.read_json()
        query = body.get("query", "")
        variables = body.get("variables", {}) or {}

        def reply(data):
            linear = data["linear"]
            projects = linear["projects"]
            labels = linear["labels"]
            issues = linear["issues"]

            if "projects(first:" in query:
                name = variables.get("name", "")
                nodes = [
                    {
                        "id": project["id"],
                        "name": project["name"],
                        "teams": {"nodes": [project["team"]]},
                    }
                    for project in projects
                    if project["name"] == name
                ]
                return {"projects": {"nodes": nodes}}

            if "issues(first: 50" in query:
                project_id = variables.get("projectId")
                nodes = []
                for issue in issues:
                    if issue["projectId"] != project_id:
                        continue
                    project = next(project for project in projects if project["id"] == issue["projectId"])
                    nodes.append(linear_issue_payload(issue, project, project["team"]))
                return {"issues": {"nodes": nodes, "pageInfo": {"hasNextPage": False, "endCursor": ""}}}

            if "issue(id:" in query:
                issue_id = variables.get("id")
                for issue in issues:
                    if issue["id"] == issue_id:
                        project = next(project for project in projects if project["id"] == issue["projectId"])
                        return {"issue": linear_issue_payload(issue, project, project["team"])}
                return {"issue": None}

            if "issueLabels(first: 50" in query:
                team_id = variables.get("teamId")
                nodes = [
                    {"id": label["id"], "name": label["name"]}
                    for label in labels
                    if label["teamId"] == team_id
                ]
                return {"issueLabels": {"nodes": nodes, "pageInfo": {"hasNextPage": False, "endCursor": ""}}}

            if "commentCreate" in query:
                issue_id = variables.get("issueId")
                comment = variables.get("body", "")
                for issue in issues:
                    if issue["id"] == issue_id:
                        issue.setdefault("comments", []).append({"body": comment, "createdAt": now_rfc3339()})
                        issue["updatedAt"] = now_rfc3339()
                        return {"commentCreate": {"success": True}}
                return {"commentCreate": {"success": False}}

            if "issueLabelCreate" in query:
                name = variables.get("name")
                team_id = variables.get("teamId")
                for label in labels:
                    if label["teamId"] == team_id and label["name"].lower() == name.lower():
                        return {"issueLabelCreate": {"success": True, "issueLabel": {"id": label["id"], "name": label["name"]}}}
                new_id = f"label-{len(labels) + 1}"
                label = {"id": new_id, "name": name, "teamId": team_id}
                labels.append(label)
                return {"issueLabelCreate": {"success": True, "issueLabel": {"id": new_id, "name": name}}}

            if "issueAddLabel" in query:
                issue_id = variables.get("issueId")
                label_id = variables.get("labelId")
                label = next((label for label in labels if label["id"] == label_id), None)
                issue = next((issue for issue in issues if issue["id"] == issue_id), None)
                if label is None or issue is None:
                    return {"issueAddLabel": {"success": False}}
                lowered = label["name"].lower()
                if lowered not in normalize_labels(issue.get("labels", [])):
                    issue.setdefault("labels", []).append(label["name"])
                issue["updatedAt"] = now_rfc3339()
                return {"issueAddLabel": {"success": True}}

            if "issueRemoveLabel" in query:
                issue_id = variables.get("issueId")
                label_id = variables.get("labelId")
                label = next((label for label in labels if label["id"] == label_id), None)
                issue = next((issue for issue in issues if issue["id"] == issue_id), None)
                if label is None or issue is None:
                    return {"issueRemoveLabel": {"success": False}}
                lowered = label["name"].lower()
                issue["labels"] = [name for name in issue.get("labels", []) if name.lower() != lowered]
                issue["updatedAt"] = now_rfc3339()
                return {"issueRemoveLabel": {"success": True}}

            return {}

        payload = self.server.state.mutate(reply)
        self.send_json(200, {"data": payload})


def find_gitlab_issue(data, project_name, iid):
    project = data["gitlab"]["projects"].get(project_name)
    if project is None:
        return None
    for issue in project["issues"]:
        if issue["iid"] == iid:
            return issue
    return None


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--state", required=True)
    args = parser.parse_args()

    server = ThreadingHTTPServer((args.host, args.port), Handler)
    server.state = SmokeState(args.state)
    print(f"listening on {args.host}:{args.port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
