package gitlab

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/testutil"
	trackerbase "github.com/tjohnson/maestro/internal/tracker"
)

func TestLiveGitLabPollsConfiguredProject(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_GITLAB")
	env := testutil.RequireEnv(
		t,
		"MAESTRO_TEST_GITLAB_BASE_URL",
		"MAESTRO_TEST_GITLAB_TOKEN",
		"MAESTRO_TEST_GITLAB_PROJECT",
		"MAESTRO_TEST_GITLAB_LABEL",
	)

	adapter, err := NewAdapter(config.SourceConfig{
		Name:      "live-gitlab",
		Tracker:   "gitlab",
		AgentType: "code-pr",
		Connection: config.GitLabConnection{
			BaseURL:  env["MAESTRO_TEST_GITLAB_BASE_URL"],
			Token:    env["MAESTRO_TEST_GITLAB_TOKEN"],
			Project:  env["MAESTRO_TEST_GITLAB_PROJECT"],
			TokenEnv: "MAESTRO_TEST_GITLAB_TOKEN",
		},
		Filter: config.FilterConfig{
			Labels:   []string{env["MAESTRO_TEST_GITLAB_LABEL"]},
			Assignee: strings.TrimSpace(os.Getenv("MAESTRO_TEST_GITLAB_ASSIGNEE")),
		},
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	issues, err := adapter.Poll(ctx)
	if err != nil {
		t.Fatalf("poll live gitlab: %v", err)
	}
	if len(issues) == 0 {
		t.Fatalf("expected at least one issue for project=%s label=%s", env["MAESTRO_TEST_GITLAB_PROJECT"], env["MAESTRO_TEST_GITLAB_LABEL"])
	}

	issue := issues[0]
	if issue.TrackerKind != "gitlab" {
		t.Fatalf("tracker kind = %q", issue.TrackerKind)
	}
	if !strings.Contains(issue.Identifier, env["MAESTRO_TEST_GITLAB_PROJECT"]) {
		t.Fatalf("identifier = %q", issue.Identifier)
	}
	if issue.Meta["repo_url"] == "" {
		t.Fatal("expected repo_url metadata from live gitlab project")
	}
}

func TestLiveGitLabLifecycleWriteback(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_GITLAB")
	env := testutil.RequireEnv(
		t,
		"MAESTRO_TEST_GITLAB_BASE_URL",
		"MAESTRO_TEST_GITLAB_TOKEN",
		"MAESTRO_TEST_GITLAB_PROJECT",
		"MAESTRO_TEST_GITLAB_LABEL",
	)

	adapter, err := NewAdapter(config.SourceConfig{
		Name:      "live-gitlab",
		Tracker:   "gitlab",
		AgentType: "code-pr",
		Connection: config.GitLabConnection{
			BaseURL:  env["MAESTRO_TEST_GITLAB_BASE_URL"],
			Token:    env["MAESTRO_TEST_GITLAB_TOKEN"],
			Project:  env["MAESTRO_TEST_GITLAB_PROJECT"],
			TokenEnv: "MAESTRO_TEST_GITLAB_TOKEN",
		},
		Filter: config.FilterConfig{
			Labels: []string{env["MAESTRO_TEST_GITLAB_LABEL"]},
		},
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	issues, err := adapter.Poll(ctx)
	if err != nil {
		t.Fatalf("poll live gitlab: %v", err)
	}
	if len(issues) == 0 {
		t.Fatalf("expected at least one issue for project=%s label=%s", env["MAESTRO_TEST_GITLAB_PROJECT"], env["MAESTRO_TEST_GITLAB_LABEL"])
	}

	issue := issues[0]
	cleanupLabels := []string{
		trackerbase.LifecycleLabelActive,
		trackerbase.LifecycleLabelRetry,
		trackerbase.LifecycleLabelDone,
		trackerbase.LifecycleLabelFailed,
	}
	for _, label := range cleanupLabels {
		_ = adapter.RemoveLifecycleLabel(ctx, issue.ID, label)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		for _, label := range cleanupLabels {
			_ = adapter.RemoveLifecycleLabel(cleanupCtx, issue.ID, label)
		}
	})

	comment := "Maestro live GitLab writeback test."
	if err := adapter.PostOperationalComment(ctx, issue.ID, comment); err != nil {
		t.Fatalf("post operational comment: %v", err)
	}
	if !gitLabCommentExists(t, env, issue.ID, comment) {
		t.Fatalf("expected operational comment %q on %s", comment, issue.Identifier)
	}

	if err := adapter.AddLifecycleLabel(ctx, issue.ID, trackerbase.LifecycleLabelActive); err != nil {
		t.Fatalf("add lifecycle label: %v", err)
	}
	updated, err := adapter.Get(ctx, issue.ID)
	if err != nil {
		t.Fatalf("get issue after add: %v", err)
	}
	if !hasGitLabLabel(updated.Labels, trackerbase.LifecycleLabelActive) {
		t.Fatalf("expected %q on issue %s, labels=%v", trackerbase.LifecycleLabelActive, updated.Identifier, updated.Labels)
	}

	if err := adapter.RemoveLifecycleLabel(ctx, issue.ID, trackerbase.LifecycleLabelActive); err != nil {
		t.Fatalf("remove lifecycle label: %v", err)
	}
	updated, err = adapter.Get(ctx, issue.ID)
	if err != nil {
		t.Fatalf("get issue after remove: %v", err)
	}
	if hasGitLabLabel(updated.Labels, trackerbase.LifecycleLabelActive) {
		t.Fatalf("expected %q removed from issue %s, labels=%v", trackerbase.LifecycleLabelActive, updated.Identifier, updated.Labels)
	}
}

func TestLiveGitLabEpicPollsConfiguredGroup(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_GITLAB_EPIC")
	env := testutil.RequireEnv(
		t,
		"MAESTRO_TEST_GITLAB_BASE_URL",
		"MAESTRO_TEST_GITLAB_TOKEN",
		"MAESTRO_TEST_GITLAB_EPIC_GROUP",
		"MAESTRO_TEST_GITLAB_EPIC_LABEL",
		"MAESTRO_TEST_GITLAB_EPIC_REPO",
	)

	adapter, err := NewAdapter(config.SourceConfig{
		Name:      "live-gitlab-epic",
		Tracker:   "gitlab-epic",
		Repo:      env["MAESTRO_TEST_GITLAB_EPIC_REPO"],
		AgentType: "triage",
		Connection: config.GitLabConnection{
			BaseURL:  env["MAESTRO_TEST_GITLAB_BASE_URL"],
			Token:    env["MAESTRO_TEST_GITLAB_TOKEN"],
			Group:    env["MAESTRO_TEST_GITLAB_EPIC_GROUP"],
			TokenEnv: "MAESTRO_TEST_GITLAB_TOKEN",
		},
		Filter: config.FilterConfig{
			Labels:   []string{env["MAESTRO_TEST_GITLAB_EPIC_LABEL"]},
			Assignee: strings.TrimSpace(os.Getenv("MAESTRO_TEST_GITLAB_ASSIGNEE")),
		},
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	issues, err := adapter.Poll(ctx)
	if err != nil {
		t.Fatalf("poll live gitlab epics: %v", err)
	}
	if len(issues) == 0 {
		t.Fatalf("expected at least one epic child issue for group=%s label=%s", env["MAESTRO_TEST_GITLAB_EPIC_GROUP"], env["MAESTRO_TEST_GITLAB_EPIC_LABEL"])
	}

	issue := issues[0]
	if issue.Meta["gitlab_scope"] != "epic-issue" {
		t.Fatalf("gitlab_scope = %q", issue.Meta["gitlab_scope"])
	}
	if issue.Meta["repo_url"] == "" {
		t.Fatal("expected repo_url metadata from live gitlab epic source")
	}
}

func TestLiveGitLabEpicLifecycleWriteback(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_GITLAB_EPIC")
	env := testutil.RequireEnv(
		t,
		"MAESTRO_TEST_GITLAB_BASE_URL",
		"MAESTRO_TEST_GITLAB_TOKEN",
		"MAESTRO_TEST_GITLAB_EPIC_GROUP",
		"MAESTRO_TEST_GITLAB_EPIC_LABEL",
		"MAESTRO_TEST_GITLAB_EPIC_REPO",
	)

	adapter, err := NewAdapter(config.SourceConfig{
		Name:      "live-gitlab-epic",
		Tracker:   "gitlab-epic",
		Repo:      env["MAESTRO_TEST_GITLAB_EPIC_REPO"],
		AgentType: "triage",
		Connection: config.GitLabConnection{
			BaseURL:  env["MAESTRO_TEST_GITLAB_BASE_URL"],
			Token:    env["MAESTRO_TEST_GITLAB_TOKEN"],
			Group:    env["MAESTRO_TEST_GITLAB_EPIC_GROUP"],
			TokenEnv: "MAESTRO_TEST_GITLAB_TOKEN",
		},
		Filter: config.FilterConfig{
			Labels: []string{env["MAESTRO_TEST_GITLAB_EPIC_LABEL"]},
		},
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	issues, err := adapter.Poll(ctx)
	if err != nil {
		t.Fatalf("poll live gitlab epics: %v", err)
	}
	if len(issues) == 0 {
		t.Fatalf("expected at least one epic child issue for group=%s label=%s", env["MAESTRO_TEST_GITLAB_EPIC_GROUP"], env["MAESTRO_TEST_GITLAB_EPIC_LABEL"])
	}

	issue := issues[0]
	cleanupLabels := []string{
		trackerbase.LifecycleLabelActive,
		trackerbase.LifecycleLabelRetry,
		trackerbase.LifecycleLabelDone,
		trackerbase.LifecycleLabelFailed,
	}
	for _, label := range cleanupLabels {
		_ = adapter.RemoveLifecycleLabel(ctx, issue.ID, label)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		for _, label := range cleanupLabels {
			_ = adapter.RemoveLifecycleLabel(cleanupCtx, issue.ID, label)
		}
	})

	comment := "Maestro live GitLab epic writeback test."
	if err := adapter.PostOperationalComment(ctx, issue.ID, comment); err != nil {
		t.Fatalf("post operational comment: %v", err)
	}
	if !gitLabEpicIssueCommentExists(t, env, issue.ID, comment) {
		t.Fatalf("expected operational comment %q on %s", comment, issue.Identifier)
	}

	if err := adapter.AddLifecycleLabel(ctx, issue.ID, trackerbase.LifecycleLabelActive); err != nil {
		t.Fatalf("add lifecycle label: %v", err)
	}
	updated, err := adapter.Get(ctx, issue.ID)
	if err != nil {
		t.Fatalf("get issue after add: %v", err)
	}
	if !hasGitLabLabel(updated.Labels, trackerbase.LifecycleLabelActive) {
		t.Fatalf("expected %q on issue %s, labels=%v", trackerbase.LifecycleLabelActive, updated.Identifier, updated.Labels)
	}

	if err := adapter.RemoveLifecycleLabel(ctx, issue.ID, trackerbase.LifecycleLabelActive); err != nil {
		t.Fatalf("remove lifecycle label: %v", err)
	}
	updated, err = adapter.Get(ctx, issue.ID)
	if err != nil {
		t.Fatalf("get issue after remove: %v", err)
	}
	if hasGitLabLabel(updated.Labels, trackerbase.LifecycleLabelActive) {
		t.Fatalf("expected %q removed from issue %s, labels=%v", trackerbase.LifecycleLabelActive, updated.Identifier, updated.Labels)
	}
}

func gitLabCommentExists(t *testing.T, env map[string]string, issueID string, want string) bool {
	t.Helper()

	client, err := NewClient(env["MAESTRO_TEST_GITLAB_BASE_URL"], env["MAESTRO_TEST_GITLAB_TOKEN"])
	if err != nil {
		t.Fatalf("new gitlab client: %v", err)
	}
	iid, err := parseGitLabIssueIID(issueID)
	if err != nil {
		t.Fatalf("parse issue id: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var notes []struct {
		Body string `json:"body"`
	}
	if _, err := client.getJSON(ctx, "/api/v4/projects/"+url.PathEscape(env["MAESTRO_TEST_GITLAB_PROJECT"])+"/issues/"+url.PathEscape(strconv.Itoa(iid))+"/notes", nil, &notes); err != nil {
		t.Fatalf("get issue notes: %v", err)
	}
	for _, note := range notes {
		if strings.Contains(note.Body, want) {
			return true
		}
	}
	return false
}

func hasGitLabLabel(labels []string, want string) bool {
	for _, label := range labels {
		if strings.EqualFold(strings.TrimSpace(label), want) {
			return true
		}
	}
	return false
}

func gitLabEpicIssueCommentExists(t *testing.T, env map[string]string, issueID string, want string) bool {
	t.Helper()

	client, err := NewClient(env["MAESTRO_TEST_GITLAB_BASE_URL"], env["MAESTRO_TEST_GITLAB_TOKEN"])
	if err != nil {
		t.Fatalf("new gitlab client: %v", err)
	}
	ref, err := parseGitLabEpicIssueRef(issueID)
	if err != nil {
		t.Fatalf("parse epic issue id: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var notes []struct {
		Body string `json:"body"`
	}
	if _, err := client.getJSON(ctx, "/api/v4/projects/"+url.PathEscape(ref.Project)+"/issues/"+url.PathEscape(strconv.Itoa(ref.IssueIID))+"/notes", nil, &notes); err != nil {
		t.Fatalf("get issue notes: %v", err)
	}
	for _, note := range notes {
		if strings.Contains(note.Body, want) {
			return true
		}
	}
	return false
}
