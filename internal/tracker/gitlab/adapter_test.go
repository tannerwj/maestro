package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tjohnson/maestro/internal/config"
)

func TestPollNormalizesProjectIssues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/projects/team/project":
			_, _ = w.Write([]byte(`{"id":1,"path_with_namespace":"team/project","http_url_to_repo":"https://gitlab.example.com/team/project.git"}`))
		case "/api/v4/projects/team/project/issues":
			_, _ = w.Write([]byte(`[{"id":101,"iid":42,"title":"Fix bug","description":"Details","state":"opened","web_url":"https://gitlab.example.com/team/project/-/issues/42","labels":["Agent:Ready"],"author":{"username":"tj"},"assignee":{"username":"tj"},"created_at":"2026-03-15T22:39:16Z","updated_at":"2026-03-15T22:40:16Z"}]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	adapter, err := NewAdapter(config.SourceConfig{
		Name:      "gitlab-project",
		Tracker:   "gitlab",
		AgentType: "code-pr",
		Connection: config.SourceConnection{
			BaseURL: server.URL,
			Token:   "secret",
			Project: "team/project",
		},
		Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	issues, err := adapter.Poll(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues len = %d", len(issues))
	}
	if got := issues[0].Meta["repo_url"]; got != "https://gitlab.example.com/team/project.git" {
		t.Fatalf("repo_url = %q", got)
	}
}

func TestPollNormalizesEpicChildIssues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/groups/team/platform/epics":
			if got := r.URL.Query().Get("labels"); got != "agent:ready" {
				t.Fatalf("labels query = %q", got)
			}
			_, _ = w.Write([]byte(`[{"id":29,"iid":7,"title":"Epic title","description":"Epic body","state":"opened","web_url":"https://gitlab.example.com/groups/team/platform/-/epics/7","labels":["agent:ready"],"author":{"username":"tj"},"created_at":"2026-03-15T22:39:16Z","updated_at":"2026-03-15T22:40:16Z"}]`))
		case "/api/v4/groups/team/platform/issues":
			if got := r.URL.Query().Get("assignee_username"); got != "tj" {
				t.Fatalf("assignee query = %q", got)
			}
			_, _ = w.Write([]byte(`[{"id":101,"iid":42,"project_id":1,"title":"Fix child issue","description":"Child details","state":"opened","web_url":"https://gitlab.example.com/team/platform/repo/-/issues/42","labels":["backend"],"author":{"username":"tj"},"assignee":{"username":"tj"},"created_at":"2026-03-15T22:39:16Z","updated_at":"2026-03-15T22:40:16Z","references":{"full":"team/platform/repo#42"},"epic":{"id":29,"iid":7}}]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	adapter, err := NewAdapter(config.SourceConfig{
		Name:      "gitlab-epic",
		Tracker:   "gitlab-epic",
		Repo:      "https://gitlab.example.com/team/platform/repo.git",
		AgentType: "repo-maintainer",
		Connection: config.SourceConnection{
			BaseURL: server.URL,
			Token:   "secret",
			Group:   "team/platform",
		},
		Filter: config.FilterConfig{
			Labels:   []string{"agent:ready"},
			Assignee: "tj",
		},
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	issues, err := adapter.Poll(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues len = %d", len(issues))
	}
	issue := issues[0]
	if issue.ID != "gitlab-epic:team/platform&7:29|team/platform/repo#42" {
		t.Fatalf("id = %q", issue.ID)
	}
	if issue.Identifier != "team/platform/repo#42" {
		t.Fatalf("identifier = %q", issue.Identifier)
	}
	if got := issue.Meta["repo_url"]; got != "https://gitlab.example.com/team/platform/repo.git" {
		t.Fatalf("repo_url = %q", got)
	}
	if got := issue.Meta["gitlab_scope"]; got != "epic-issue" {
		t.Fatalf("gitlab_scope = %q", got)
	}
	if !hasLabel(issue.Labels, "agent:ready") || !hasLabel(issue.Labels, "backend") {
		t.Fatalf("labels = %v", issue.Labels)
	}
}

func TestPollNormalizesEpicChildIssuesWithRepoPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/groups/team/platform/epics":
			_, _ = w.Write([]byte(`[{"id":29,"iid":7,"title":"Epic title","description":"Epic body","state":"opened","web_url":"https://gitlab.example.com/groups/team/platform/-/epics/7","labels":["agent:ready"],"author":{"username":"tj"},"created_at":"2026-03-15T22:39:16Z","updated_at":"2026-03-15T22:40:16Z"}]`))
		case "/api/v4/groups/team/platform/issues":
			_, _ = w.Write([]byte(`[{"id":101,"iid":42,"project_id":1,"title":"Fix child issue","description":"Child details","state":"opened","web_url":"https://gitlab.example.com/team/platform/repo/-/issues/42","labels":["backend"],"author":{"username":"tj"},"assignee":{"username":"tj"},"created_at":"2026-03-15T22:39:16Z","updated_at":"2026-03-15T22:40:16Z","references":{"full":"team/platform/repo#42"},"epic":{"id":29,"iid":7}}]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	adapter, err := NewAdapter(config.SourceConfig{
		Name:      "gitlab-epic",
		Tracker:   "gitlab-epic",
		Repo:      "team/platform/repo",
		AgentType: "repo-maintainer",
		Connection: config.SourceConnection{
			BaseURL: server.URL,
			Token:   "secret",
			Group:   "team/platform",
		},
		Filter: config.FilterConfig{
			Labels: []string{"agent:ready"},
		},
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	issues, err := adapter.Poll(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues len = %d", len(issues))
	}
	if got := issues[0].Meta["repo_url"]; got != server.URL+"/team/platform/repo.git" {
		t.Fatalf("repo_url = %q", got)
	}
}

func TestPollNormalizesEpicChildIssuesWithSeparateEpicAndIssueFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/groups/team/platform/epics":
			if got := r.URL.Query().Get("labels"); got != "bucket:ready" {
				t.Fatalf("epic labels query = %q", got)
			}
			_, _ = w.Write([]byte(`[{"id":29,"iid":7,"title":"Epic title","description":"Epic body","state":"opened","web_url":"https://gitlab.example.com/groups/team/platform/-/epics/7","labels":["bucket:ready"],"author":{"username":"tj"},"created_at":"2026-03-15T22:39:16Z","updated_at":"2026-03-15T22:40:16Z"}]`))
		case "/api/v4/groups/team/platform/issues":
			if got := r.URL.Query().Get("labels"); got != "agent:ready" {
				t.Fatalf("issue labels query = %q", got)
			}
			if got := r.URL.Query().Get("assignee_username"); got != "tj" {
				t.Fatalf("assignee query = %q", got)
			}
			_, _ = w.Write([]byte(`[{"id":101,"iid":42,"project_id":1,"title":"Fix child issue","description":"Child details","state":"opened","web_url":"https://gitlab.example.com/team/platform/repo/-/issues/42","labels":["agent:ready"],"author":{"username":"tj"},"assignee":{"username":"tj"},"created_at":"2026-03-15T22:39:16Z","updated_at":"2026-03-15T22:40:16Z","references":{"full":"team/platform/repo#42"},"epic":{"id":29,"iid":7}}]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	adapter, err := NewAdapter(config.SourceConfig{
		Name:      "gitlab-epic",
		Tracker:   "gitlab-epic",
		Repo:      "https://gitlab.example.com/team/platform/repo.git",
		AgentType: "repo-maintainer",
		Connection: config.SourceConnection{
			BaseURL: server.URL,
			Token:   "secret",
			Group:   "team/platform",
		},
		EpicFilter: config.FilterConfig{
			Labels: []string{"bucket:ready"},
		},
		IssueFilter: config.FilterConfig{
			Labels:   []string{"agent:ready"},
			Assignee: "tj",
		},
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	issues, err := adapter.Poll(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues len = %d", len(issues))
	}
	issue := issues[0]
	if !hasLabel(issue.Labels, "bucket:ready") || !hasLabel(issue.Labels, "agent:ready") {
		t.Fatalf("labels = %v", issue.Labels)
	}
}

func TestPollNormalizesEpicChildIssuesWithEpicIIDFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/groups/team/platform/epics":
			if got := r.URL.Query().Get("labels"); got != "" {
				t.Fatalf("epic labels query = %q", got)
			}
			_, _ = w.Write([]byte(`[
				{"id":29,"iid":7,"title":"Ignore me","description":"Epic body","state":"opened","web_url":"https://gitlab.example.com/groups/team/platform/-/epics/7","labels":["bucket:ready"],"author":{"username":"tj"},"created_at":"2026-03-15T22:39:16Z","updated_at":"2026-03-15T22:40:16Z"},
				{"id":31,"iid":11,"title":"Match me","description":"Epic body","state":"opened","web_url":"https://gitlab.example.com/groups/team/platform/-/epics/11","labels":["other:label"],"author":{"username":"tj"},"created_at":"2026-03-15T22:39:16Z","updated_at":"2026-03-15T22:40:16Z"}
			]`))
		case "/api/v4/groups/team/platform/issues":
			_, _ = w.Write([]byte(`[
				{"id":101,"iid":42,"project_id":1,"title":"Wrong epic child","description":"Child details","state":"opened","web_url":"https://gitlab.example.com/team/platform/repo/-/issues/42","labels":["agent:ready"],"author":{"username":"tj"},"assignee":{"username":"tj"},"created_at":"2026-03-15T22:39:16Z","updated_at":"2026-03-15T22:40:16Z","references":{"full":"team/platform/repo#42"},"epic":{"id":29,"iid":7}},
				{"id":102,"iid":43,"project_id":1,"title":"Target child","description":"Child details","state":"opened","web_url":"https://gitlab.example.com/team/platform/repo/-/issues/43","labels":["agent:ready"],"author":{"username":"tj"},"assignee":{"username":"tj"},"created_at":"2026-03-15T22:39:16Z","updated_at":"2026-03-15T22:40:16Z","references":{"full":"team/platform/repo#43"},"epic":{"id":31,"iid":11}}
			]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	adapter, err := NewAdapter(config.SourceConfig{
		Name:      "gitlab-epic",
		Tracker:   "gitlab-epic",
		Repo:      "https://gitlab.example.com/team/platform/repo.git",
		AgentType: "repo-maintainer",
		Connection: config.SourceConnection{
			BaseURL: server.URL,
			Token:   "secret",
			Group:   "team/platform",
		},
		EpicFilter: config.FilterConfig{
			IIDs: []int{11},
		},
		IssueFilter: config.FilterConfig{
			Labels:   []string{"agent:ready"},
			Assignee: "tj",
		},
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	issues, err := adapter.Poll(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues len = %d", len(issues))
	}
	if got := issues[0].ID; got != "gitlab-epic:team/platform&11:31|team/platform/repo#43" {
		t.Fatalf("id = %q", got)
	}
}

func TestEpicLifecycleOperationsUseIssueEndpoints(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)

		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/api/v4/projects/team/platform/repo/issues/42":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v4/projects/team/platform/repo/issues/42/notes":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/groups/team/platform/epics/7":
			_, _ = w.Write([]byte(`{"id":29,"iid":7,"title":"Epic title","description":"Epic body","state":"opened","web_url":"https://gitlab.example.com/groups/team/platform/-/epics/7","labels":["agent:ready"],"author":{"username":"tj"},"created_at":"2026-03-15T22:39:16Z","updated_at":"2026-03-15T22:40:16Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/projects/team/platform/repo/issues/42":
			_, _ = w.Write([]byte(`{"id":101,"iid":42,"project_id":1,"title":"Fix child issue","description":"Child details","state":"opened","web_url":"https://gitlab.example.com/team/platform/repo/-/issues/42","labels":["backend"],"author":{"username":"tj"},"assignee":{"username":"tj"},"created_at":"2026-03-15T22:39:16Z","updated_at":"2026-03-15T22:40:16Z","references":{"full":"team/platform/repo#42"}}`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	adapter, err := NewAdapter(config.SourceConfig{
		Name:    "gitlab-epic",
		Tracker: "gitlab-epic",
		Repo:    "https://gitlab.example.com/team/platform/repo.git",
		Connection: config.SourceConnection{
			BaseURL: server.URL,
			Token:   "secret",
			Group:   "team/platform",
		},
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	issueID := "gitlab-epic:team/platform&7:29|team/platform/repo#42"
	if err := adapter.AddLifecycleLabel(context.Background(), issueID, "maestro:active"); err != nil {
		t.Fatalf("add lifecycle label: %v", err)
	}
	if err := adapter.PostOperationalComment(context.Background(), issueID, "hello"); err != nil {
		t.Fatalf("post comment: %v", err)
	}
	if _, err := adapter.Get(context.Background(), issueID); err != nil {
		t.Fatalf("get issue: %v", err)
	}

	got := strings.Join(calls, "\n")
	if !strings.Contains(got, "PUT /api/v4/projects/team/platform/repo/issues/42") {
		t.Fatalf("missing issue update call in %s", got)
	}
	if !strings.Contains(got, "POST /api/v4/projects/team/platform/repo/issues/42/notes") {
		t.Fatalf("missing issue notes call in %s", got)
	}
	if !strings.Contains(got, "GET /api/v4/projects/team/platform/repo/issues/42") {
		t.Fatalf("missing issue get call in %s", got)
	}
	if !strings.Contains(got, "GET /api/v4/groups/team/platform/epics/7") {
		t.Fatalf("missing epic get call in %s", got)
	}
}

func TestParseGitLabEpicRef(t *testing.T) {
	group, iid, epicID, err := parseGitLabEpicRef("gitlab-epic:team/platform&7:29|team/platform/repo#42")
	if err != nil {
		t.Fatalf("parse epic ref: %v", err)
	}
	if group != "team/platform" || iid != 7 || epicID != 29 {
		t.Fatalf("unexpected parse result: %s %d %d", group, iid, epicID)
	}
}

func TestIssueAPIState(t *testing.T) {
	tests := []struct {
		name   string
		states []string
		want   string
	}{
		{name: "default", states: nil, want: "opened"},
		{name: "open", states: []string{"open"}, want: "opened"},
		{name: "closed", states: []string{"closed"}, want: "closed"},
		{name: "both", states: []string{"open", "closed"}, want: "all"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := issueAPIState(test.states); got != test.want {
				t.Fatalf("issueAPIState(%v) = %q, want %q", test.states, got, test.want)
			}
		})
	}
}

func hasLabel(labels []string, want string) bool {
	for _, label := range labels {
		if strings.EqualFold(label, want) {
			return true
		}
	}
	return false
}
