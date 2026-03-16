package orchestrator_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tjohnson/maestro/internal/config"
	claudeharness "github.com/tjohnson/maestro/internal/harness/claude"
	codexharness "github.com/tjohnson/maestro/internal/harness/codex"
	"github.com/tjohnson/maestro/internal/orchestrator"
	"github.com/tjohnson/maestro/internal/testutil"
	trackerbase "github.com/tjohnson/maestro/internal/tracker"
	gitlabtracker "github.com/tjohnson/maestro/internal/tracker/gitlab"
	lineartracker "github.com/tjohnson/maestro/internal/tracker/linear"
	"github.com/tjohnson/maestro/internal/workspace"
)

func TestServiceWithLiveClaudeHarness(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_CLAUDE")
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("skipping live test; claude binary not found: %v", err)
	}

	cfg := testConfig(t)
	repoURL := createGitRepo(t)
	promptPath := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(promptPath, []byte("Reply with exactly: MAESTRO_ORCHESTRATOR_SMOKE_OK"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	cfg.AgentTypes[0].Prompt = promptPath

	harness, err := claudeharness.NewAdapter()
	if err != nil {
		t.Fatalf("new harness: %v", err)
	}

	svc, err := orchestrator.NewServiceWithDeps(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), orchestrator.Dependencies{
		Tracker: &testutil.FakeTracker{
			Issues: singleIssue(cfg, repoURL, "gitlab:team/project#51", "team/project#51"),
		},
		Harness:   harness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 60*time.Second, func() bool {
		return snapshotHasEvent(svc.Snapshot(), "completed")
	})

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceWithLiveClaudeManualApproval(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_CLAUDE")
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("skipping live test; claude binary not found: %v", err)
	}

	cfg := testConfig(t)
	cfg.AgentTypes[0].ApprovalPolicy = "manual"
	repoURL := createGitRepo(t)
	promptPath := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(promptPath, []byte("Create a file named APPROVAL_OK.txt containing exactly MAESTRO_CLAUDE_APPROVAL_OK and then reply with exactly MAESTRO_CLAUDE_APPROVAL_OK."), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	cfg.AgentTypes[0].Prompt = promptPath

	harness, err := claudeharness.NewAdapter()
	if err != nil {
		t.Fatalf("new harness: %v", err)
	}

	issueID := "gitlab:team/project#54"
	identifier := "team/project#54"
	svc, err := orchestrator.NewServiceWithDeps(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), orchestrator.Dependencies{
		Tracker: &testutil.FakeTracker{
			Issues: singleIssue(cfg, repoURL, issueID, identifier),
		},
		Harness:   harness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	var requestID string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := svc.Snapshot()
		if len(snapshot.PendingApprovals) > 0 {
			requestID = snapshot.PendingApprovals[0].RequestID
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if requestID == "" {
		cancel()
		_ = <-errCh
		t.Fatal("timed out waiting for claude approval request")
	}

	if err := svc.ResolveApproval(requestID, "approve"); err != nil {
		t.Fatalf("approve request: %v", err)
	}

	waitFor(t, 60*time.Second, func() bool {
		return snapshotHasEvent(svc.Snapshot(), "completed")
	})

	workspacePath := filepath.Join(cfg.Workspace.Root, workspace.WorkspaceKey(identifier))
	content, err := os.ReadFile(filepath.Join(workspacePath, "APPROVAL_OK.txt"))
	if err != nil {
		t.Fatalf("read approval file: %v", err)
	}
	if strings.TrimSpace(string(content)) != "MAESTRO_CLAUDE_APPROVAL_OK" {
		t.Fatalf("approval file content = %q", string(content))
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceWithLiveCodexHarness(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_CODEX")
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skipf("skipping live test; codex binary not found: %v", err)
	}

	cfg := testConfig(t)
	cfg.AgentTypes[0].Harness = "codex"
	repoURL := createGitRepo(t)
	promptPath := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(promptPath, []byte("Reply with exactly: MAESTRO_ORCHESTRATOR_CODEX_SMOKE_OK"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	cfg.AgentTypes[0].Prompt = promptPath

	harness, err := codexharness.NewAdapter()
	if err != nil {
		t.Fatalf("new harness: %v", err)
	}

	svc, err := orchestrator.NewServiceWithDeps(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), orchestrator.Dependencies{
		Tracker: &testutil.FakeTracker{
			Issues: singleIssue(cfg, repoURL, "gitlab:team/project#52", "team/project#52"),
		},
		Harness:   harness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 60*time.Second, func() bool {
		return snapshotHasEvent(svc.Snapshot(), "completed")
	})

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceWithLiveCodexManualApproval(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_CODEX")
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skipf("skipping live test; codex binary not found: %v", err)
	}

	cfg := testConfig(t)
	cfg.AgentTypes[0].Harness = "codex"
	cfg.AgentTypes[0].ApprovalPolicy = "manual"
	repoURL := createGitRepo(t)
	promptPath := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(promptPath, []byte("Run the shell command `printf MAESTRO_CODEX_APPROVAL_OK > APPROVAL_OK.txt` and then reply with exactly MAESTRO_CODEX_APPROVAL_OK."), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	cfg.AgentTypes[0].Prompt = promptPath

	harness, err := codexharness.NewAdapter()
	if err != nil {
		t.Fatalf("new harness: %v", err)
	}

	issueID := "gitlab:team/project#53"
	identifier := "team/project#53"
	svc, err := orchestrator.NewServiceWithDeps(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), orchestrator.Dependencies{
		Tracker: &testutil.FakeTracker{
			Issues: singleIssue(cfg, repoURL, issueID, identifier),
		},
		Harness:   harness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	var requestID string
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := svc.Snapshot()
		if len(snapshot.PendingApprovals) > 0 {
			requestID = snapshot.PendingApprovals[0].RequestID
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if requestID == "" {
		cancel()
		_ = <-errCh
		t.Skip("codex app-server did not surface an approval request within 60s under on-request policy")
	}

	if err := svc.ResolveApproval(requestID, "approve"); err != nil {
		t.Fatalf("approve request: %v", err)
	}

	waitFor(t, 60*time.Second, func() bool {
		return snapshotHasEvent(svc.Snapshot(), "completed")
	})

	workspacePath := filepath.Join(cfg.Workspace.Root, workspace.WorkspaceKey(identifier))
	content, err := os.ReadFile(filepath.Join(workspacePath, "APPROVAL_OK.txt"))
	if err != nil {
		t.Fatalf("read approval file: %v", err)
	}
	if strings.TrimSpace(string(content)) != "MAESTRO_CODEX_APPROVAL_OK" {
		t.Fatalf("approval file content = %q", string(content))
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceWithLiveLinearSource(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_LINEAR")

	cfg := testConfig(t)
	cfg.User.LinearUsername = "operator@example.com"
	cfg.Sources[0] = config.SourceConfig{
		Name:      "live-linear",
		Tracker:   "linear",
		Repo:      createGitRepo(t),
		AgentType: "code-pr",
		Connection: config.SourceConnection{
			Token:   os.Getenv("MAESTRO_TEST_LINEAR_TOKEN"),
			Project: os.Getenv("MAESTRO_TEST_LINEAR_PROJECT"),
		},
		Filter: config.FilterConfig{
			States: []string{"Todo"},
		},
		PollInterval: config.Duration{Duration: 20 * time.Millisecond},
	}

	tracker, err := lineartracker.NewAdapter(cfg.Sources[0])
	if err != nil {
		t.Fatalf("new linear adapter: %v", err)
	}

	fakeHarness := &testutil.FakeHarness{}
	svc, err := orchestrator.NewServiceWithDeps(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), orchestrator.Dependencies{
		Tracker:   tracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 20*time.Second, func() bool {
		return snapshotHasEvent(svc.Snapshot(), "completed")
	})

	if got := len(fakeHarness.StartedRuns); got != 1 {
		t.Fatalf("started runs = %d, want 1", got)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceWithLiveGitLabReconciliationStopsRunWhenIssueCloses(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_GITLAB")
	env := testutil.RequireEnv(
		t,
		"MAESTRO_TEST_GITLAB_BASE_URL",
		"MAESTRO_TEST_GITLAB_TOKEN",
		"MAESTRO_TEST_GITLAB_PROJECT",
		"MAESTRO_TEST_GITLAB_LABEL",
	)

	cfg := testConfig(t)
	cfg.User.GitLabUsername = strings.TrimSpace(os.Getenv("MAESTRO_TEST_GITLAB_ASSIGNEE"))
	cfg.Sources[0] = config.SourceConfig{
		Name:      "live-gitlab",
		Tracker:   "gitlab",
		Repo:      createGitRepo(t),
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
		PollInterval: config.Duration{Duration: 50 * time.Millisecond},
	}

	tracker, err := gitlabtracker.NewAdapter(cfg.Sources[0])
	if err != nil {
		t.Fatalf("new gitlab adapter: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	issues, err := tracker.Poll(ctx)
	if err != nil {
		t.Fatalf("poll live gitlab: %v", err)
	}
	if len(issues) == 0 {
		t.Fatalf("expected at least one issue for project=%s label=%s", env["MAESTRO_TEST_GITLAB_PROJECT"], env["MAESTRO_TEST_GITLAB_LABEL"])
	}
	issue := issues[0]

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cleanupCancel()
	for _, label := range []string{
		trackerbase.LifecycleLabelActive,
		trackerbase.LifecycleLabelRetry,
		trackerbase.LifecycleLabelDone,
		trackerbase.LifecycleLabelFailed,
	} {
		_ = tracker.RemoveLifecycleLabel(cleanupCtx, issue.ID, label)
	}
	if err := liveGitLabUpdateIssueState(cleanupCtx, env["MAESTRO_TEST_GITLAB_BASE_URL"], env["MAESTRO_TEST_GITLAB_TOKEN"], env["MAESTRO_TEST_GITLAB_PROJECT"], issue.ID, "reopen"); err != nil {
		t.Fatalf("reopen live gitlab issue before test: %v", err)
	}
	t.Cleanup(func() {
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer restoreCancel()
		_ = liveGitLabUpdateIssueState(restoreCtx, env["MAESTRO_TEST_GITLAB_BASE_URL"], env["MAESTRO_TEST_GITLAB_TOKEN"], env["MAESTRO_TEST_GITLAB_PROJECT"], issue.ID, "reopen")
		for _, label := range []string{
			trackerbase.LifecycleLabelActive,
			trackerbase.LifecycleLabelRetry,
			trackerbase.LifecycleLabelDone,
			trackerbase.LifecycleLabelFailed,
		} {
			_ = tracker.RemoveLifecycleLabel(restoreCtx, issue.ID, label)
		}
	})

	fakeHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}
	svc, err := orchestrator.NewServiceWithDeps(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), orchestrator.Dependencies{
		Tracker:   tracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	runCtx, runCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer runCancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(runCtx)
	}()

	waitFor(t, 20*time.Second, func() bool {
		snapshot := svc.Snapshot()
		return snapshot.ActiveRun != nil && snapshot.ActiveRun.Issue.ID == issue.ID
	})

	if err := liveGitLabUpdateIssueState(context.Background(), env["MAESTRO_TEST_GITLAB_BASE_URL"], env["MAESTRO_TEST_GITLAB_TOKEN"], env["MAESTRO_TEST_GITLAB_PROJECT"], issue.ID, "close"); err != nil {
		t.Fatalf("close gitlab issue: %v", err)
	}

	waitFor(t, 20*time.Second, func() bool {
		snapshot := svc.Snapshot()
		return len(fakeHarness.StopCalls) == 1 && snapshot.ActiveRun == nil
	})

	runCancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceWithLiveGitLabEpicReconciliationStopsRunWhenEpicCloses(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_GITLAB_EPIC")
	env := testutil.RequireEnv(
		t,
		"MAESTRO_TEST_GITLAB_BASE_URL",
		"MAESTRO_TEST_GITLAB_TOKEN",
		"MAESTRO_TEST_GITLAB_EPIC_GROUP",
		"MAESTRO_TEST_GITLAB_EPIC_LABEL",
		"MAESTRO_TEST_GITLAB_EPIC_REPO",
	)

	cfg := testConfig(t)
	cfg.User.GitLabUsername = strings.TrimSpace(os.Getenv("MAESTRO_TEST_GITLAB_ASSIGNEE"))
	cfg.Sources[0] = config.SourceConfig{
		Name:      "live-gitlab-epic",
		Tracker:   "gitlab-epic",
		Repo:      env["MAESTRO_TEST_GITLAB_EPIC_REPO"],
		AgentType: "code-pr",
		Connection: config.GitLabConnection{
			BaseURL:  env["MAESTRO_TEST_GITLAB_BASE_URL"],
			Token:    env["MAESTRO_TEST_GITLAB_TOKEN"],
			Group:    env["MAESTRO_TEST_GITLAB_EPIC_GROUP"],
			TokenEnv: "MAESTRO_TEST_GITLAB_TOKEN",
		},
		Filter: config.FilterConfig{
			Labels: []string{env["MAESTRO_TEST_GITLAB_EPIC_LABEL"]},
		},
		PollInterval: config.Duration{Duration: 50 * time.Millisecond},
	}

	tracker, err := gitlabtracker.NewAdapter(cfg.Sources[0])
	if err != nil {
		t.Fatalf("new gitlab adapter: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	issues, err := tracker.Poll(ctx)
	if err != nil {
		t.Fatalf("poll live gitlab epics: %v", err)
	}
	if len(issues) == 0 {
		t.Fatalf("expected at least one epic for group=%s label=%s", env["MAESTRO_TEST_GITLAB_EPIC_GROUP"], env["MAESTRO_TEST_GITLAB_EPIC_LABEL"])
	}
	issue := issues[0]

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cleanupCancel()
	for _, label := range []string{
		trackerbase.LifecycleLabelActive,
		trackerbase.LifecycleLabelRetry,
		trackerbase.LifecycleLabelDone,
		trackerbase.LifecycleLabelFailed,
	} {
		_ = tracker.RemoveLifecycleLabel(cleanupCtx, issue.ID, label)
	}
	if err := liveGitLabUpdateEpicState(cleanupCtx, env["MAESTRO_TEST_GITLAB_BASE_URL"], env["MAESTRO_TEST_GITLAB_TOKEN"], issue.ID, "reopen"); err != nil {
		t.Fatalf("reopen live gitlab epic before test: %v", err)
	}
	t.Cleanup(func() {
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer restoreCancel()
		_ = liveGitLabUpdateEpicState(restoreCtx, env["MAESTRO_TEST_GITLAB_BASE_URL"], env["MAESTRO_TEST_GITLAB_TOKEN"], issue.ID, "reopen")
		for _, label := range []string{
			trackerbase.LifecycleLabelActive,
			trackerbase.LifecycleLabelRetry,
			trackerbase.LifecycleLabelDone,
			trackerbase.LifecycleLabelFailed,
		} {
			_ = tracker.RemoveLifecycleLabel(restoreCtx, issue.ID, label)
		}
	})

	fakeHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}
	svc, err := orchestrator.NewServiceWithDeps(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), orchestrator.Dependencies{
		Tracker:   tracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	runCtx, runCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer runCancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(runCtx)
	}()

	waitFor(t, 20*time.Second, func() bool {
		snapshot := svc.Snapshot()
		return snapshot.ActiveRun != nil && snapshot.ActiveRun.Issue.ID == issue.ID
	})

	if err := liveGitLabUpdateEpicState(context.Background(), env["MAESTRO_TEST_GITLAB_BASE_URL"], env["MAESTRO_TEST_GITLAB_TOKEN"], issue.ID, "close"); err != nil {
		t.Fatalf("close gitlab epic: %v", err)
	}

	waitFor(t, 20*time.Second, func() bool {
		snapshot := svc.Snapshot()
		return len(fakeHarness.StopCalls) == 1 && snapshot.ActiveRun == nil
	})

	runCancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceWithLiveLinearReconciliationStopsRunWhenIssueCompletes(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_LINEAR")
	env := testutil.RequireEnv(
		t,
		"MAESTRO_TEST_LINEAR_TOKEN",
		"MAESTRO_TEST_LINEAR_PROJECT",
	)

	cfg := testConfig(t)
	cfg.User.LinearUsername = "operator@example.com"
	cfg.Sources[0] = config.SourceConfig{
		Name:      "live-linear",
		Tracker:   "linear",
		Repo:      createGitRepo(t),
		AgentType: "code-pr",
		Connection: config.SourceConnection{
			Token:   env["MAESTRO_TEST_LINEAR_TOKEN"],
			Project: env["MAESTRO_TEST_LINEAR_PROJECT"],
		},
		Filter: config.FilterConfig{
			States: []string{"Todo"},
		},
		PollInterval: config.Duration{Duration: 50 * time.Millisecond},
	}

	tracker, err := lineartracker.NewAdapter(cfg.Sources[0])
	if err != nil {
		t.Fatalf("new linear adapter: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	issues, err := tracker.Poll(ctx)
	if err != nil {
		t.Fatalf("poll live linear: %v", err)
	}
	if len(issues) == 0 {
		t.Fatalf("expected at least one issue for project=%s", env["MAESTRO_TEST_LINEAR_PROJECT"])
	}
	issue := issues[0]

	originalStateID, err := liveLinearIssueStateID(ctx, env["MAESTRO_TEST_LINEAR_TOKEN"], issue.ExternalID)
	if err != nil {
		t.Fatalf("fetch original issue state: %v", err)
	}
	doneStateID, err := liveLinearWorkflowStateID(ctx, env["MAESTRO_TEST_LINEAR_TOKEN"], issue.Meta["team_id"], "completed")
	if err != nil {
		t.Fatalf("fetch completed workflow state: %v", err)
	}

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cleanupCancel()
	for _, label := range []string{
		trackerbase.LifecycleLabelActive,
		trackerbase.LifecycleLabelRetry,
		trackerbase.LifecycleLabelDone,
		trackerbase.LifecycleLabelFailed,
	} {
		_ = tracker.RemoveLifecycleLabel(cleanupCtx, issue.ID, label)
	}
	t.Cleanup(func() {
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer restoreCancel()
		_ = liveLinearUpdateIssueState(restoreCtx, env["MAESTRO_TEST_LINEAR_TOKEN"], issue.ExternalID, originalStateID)
		for _, label := range []string{
			trackerbase.LifecycleLabelActive,
			trackerbase.LifecycleLabelRetry,
			trackerbase.LifecycleLabelDone,
			trackerbase.LifecycleLabelFailed,
		} {
			_ = tracker.RemoveLifecycleLabel(restoreCtx, issue.ID, label)
		}
	})

	fakeHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}
	svc, err := orchestrator.NewServiceWithDeps(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), orchestrator.Dependencies{
		Tracker:   tracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	runCtx, runCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer runCancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(runCtx)
	}()

	waitFor(t, 20*time.Second, func() bool {
		snapshot := svc.Snapshot()
		return snapshot.ActiveRun != nil && snapshot.ActiveRun.Issue.ID == issue.ID
	})

	if err := liveLinearUpdateIssueState(context.Background(), env["MAESTRO_TEST_LINEAR_TOKEN"], issue.ExternalID, doneStateID); err != nil {
		t.Fatalf("move issue to completed state: %v", err)
	}

	waitFor(t, 20*time.Second, func() bool {
		snapshot := svc.Snapshot()
		return len(fakeHarness.StopCalls) == 1 && snapshot.ActiveRun == nil
	})

	runCancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestLiveMultiSourceTrackerTerminalStopDoesNotCascade(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_GITLAB")
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_GITLAB_EPIC")
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_LINEAR")

	env := testutil.RequireEnv(
		t,
		"MAESTRO_TEST_GITLAB_BASE_URL",
		"MAESTRO_TEST_GITLAB_TOKEN",
		"MAESTRO_TEST_GITLAB_PROJECT",
		"MAESTRO_TEST_GITLAB_LABEL",
		"MAESTRO_TEST_GITLAB_EPIC_GROUP",
		"MAESTRO_TEST_GITLAB_EPIC_LABEL",
		"MAESTRO_TEST_GITLAB_EPIC_REPO",
		"MAESTRO_TEST_LINEAR_TOKEN",
		"MAESTRO_TEST_LINEAR_PROJECT",
	)

	root := t.TempDir()
	projectRoot := filepath.Join(root, "project")
	epicRoot := filepath.Join(root, "epic")
	linearRoot := filepath.Join(root, "linear")
	for _, dir := range []string{projectRoot, epicRoot, linearRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	projectCfg := testConfigWithRoot(t, projectRoot)
	projectCfg.User.GitLabUsername = strings.TrimSpace(os.Getenv("MAESTRO_TEST_GITLAB_ASSIGNEE"))
	projectCfg.Sources[0] = config.SourceConfig{
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
		PollInterval: config.Duration{Duration: 50 * time.Millisecond},
	}
	projectTracker, err := gitlabtracker.NewAdapter(projectCfg.Sources[0])
	if err != nil {
		t.Fatalf("new gitlab tracker: %v", err)
	}

	epicCfg := testConfigWithRoot(t, epicRoot)
	epicCfg.User.GitLabUsername = strings.TrimSpace(os.Getenv("MAESTRO_TEST_GITLAB_ASSIGNEE"))
	epicCfg.Sources[0] = config.SourceConfig{
		Name:      "live-gitlab-epic",
		Tracker:   "gitlab-epic",
		Repo:      env["MAESTRO_TEST_GITLAB_EPIC_REPO"],
		AgentType: "code-pr",
		Connection: config.GitLabConnection{
			BaseURL:  env["MAESTRO_TEST_GITLAB_BASE_URL"],
			Token:    env["MAESTRO_TEST_GITLAB_TOKEN"],
			Group:    env["MAESTRO_TEST_GITLAB_EPIC_GROUP"],
			TokenEnv: "MAESTRO_TEST_GITLAB_TOKEN",
		},
		Filter: config.FilterConfig{
			Labels: []string{env["MAESTRO_TEST_GITLAB_EPIC_LABEL"]},
		},
		PollInterval: config.Duration{Duration: 50 * time.Millisecond},
	}
	epicTracker, err := gitlabtracker.NewAdapter(epicCfg.Sources[0])
	if err != nil {
		t.Fatalf("new gitlab epic tracker: %v", err)
	}

	linearCfg := testConfigWithRoot(t, linearRoot)
	linearCfg.User.LinearUsername = "operator@example.com"
	linearCfg.Sources[0] = config.SourceConfig{
		Name:      "live-linear",
		Tracker:   "linear",
		Repo:      createGitRepo(t),
		AgentType: "code-pr",
		Connection: config.SourceConnection{
			Token:   env["MAESTRO_TEST_LINEAR_TOKEN"],
			Project: env["MAESTRO_TEST_LINEAR_PROJECT"],
		},
		Filter: config.FilterConfig{
			States: []string{"Todo"},
		},
		PollInterval: config.Duration{Duration: 50 * time.Millisecond},
	}
	linearTracker, err := lineartracker.NewAdapter(linearCfg.Sources[0])
	if err != nil {
		t.Fatalf("new linear tracker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	projectIssues, err := projectTracker.Poll(ctx)
	if err != nil {
		t.Fatalf("poll gitlab project: %v", err)
	}
	if len(projectIssues) == 0 {
		t.Fatalf("expected at least one gitlab project issue")
	}
	projectIssue := projectIssues[0]

	epicIssues, err := epicTracker.Poll(ctx)
	if err != nil {
		t.Fatalf("poll gitlab epic: %v", err)
	}
	if len(epicIssues) == 0 {
		t.Fatalf("expected at least one gitlab epic child issue")
	}
	epicIssue := epicIssues[0]

	linearIssues, err := linearTracker.Poll(ctx)
	if err != nil {
		t.Fatalf("poll linear: %v", err)
	}
	if len(linearIssues) == 0 {
		t.Fatalf("expected at least one linear issue")
	}
	linearIssue := linearIssues[0]

	originalLinearStateID, err := liveLinearIssueStateID(ctx, env["MAESTRO_TEST_LINEAR_TOKEN"], linearIssue.ExternalID)
	if err != nil {
		t.Fatalf("fetch linear issue state: %v", err)
	}

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cleanupCancel()
	liveRemoveLifecycleLabels(cleanupCtx, projectTracker, projectIssue.ID)
	liveRemoveLifecycleLabels(cleanupCtx, epicTracker, epicIssue.ID)
	liveRemoveLifecycleLabels(cleanupCtx, linearTracker, linearIssue.ID)
	if err := liveGitLabUpdateIssueState(cleanupCtx, env["MAESTRO_TEST_GITLAB_BASE_URL"], env["MAESTRO_TEST_GITLAB_TOKEN"], env["MAESTRO_TEST_GITLAB_PROJECT"], projectIssue.ID, "reopen"); err != nil {
		t.Fatalf("reopen gitlab project issue: %v", err)
	}
	t.Cleanup(func() {
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer restoreCancel()
		_ = liveGitLabUpdateIssueState(restoreCtx, env["MAESTRO_TEST_GITLAB_BASE_URL"], env["MAESTRO_TEST_GITLAB_TOKEN"], env["MAESTRO_TEST_GITLAB_PROJECT"], projectIssue.ID, "reopen")
		_ = liveLinearUpdateIssueState(restoreCtx, env["MAESTRO_TEST_LINEAR_TOKEN"], linearIssue.ExternalID, originalLinearStateID)
		liveRemoveLifecycleLabels(restoreCtx, projectTracker, projectIssue.ID)
		liveRemoveLifecycleLabels(restoreCtx, epicTracker, epicIssue.ID)
		liveRemoveLifecycleLabels(restoreCtx, linearTracker, linearIssue.ID)
	})

	projectHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}
	epicHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}
	linearHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}

	projectSvc, err := orchestrator.NewServiceWithDeps(projectCfg, slog.New(slog.NewTextHandler(io.Discard, nil)), orchestrator.Dependencies{
		Tracker:   projectTracker,
		Harness:   projectHarness,
		Workspace: workspace.NewManager(projectCfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new project service: %v", err)
	}
	epicSvc, err := orchestrator.NewServiceWithDeps(epicCfg, slog.New(slog.NewTextHandler(io.Discard, nil)), orchestrator.Dependencies{
		Tracker:   epicTracker,
		Harness:   epicHarness,
		Workspace: workspace.NewManager(epicCfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new epic service: %v", err)
	}
	linearSvc, err := orchestrator.NewServiceWithDeps(linearCfg, slog.New(slog.NewTextHandler(io.Discard, nil)), orchestrator.Dependencies{
		Tracker:   linearTracker,
		Harness:   linearHarness,
		Workspace: workspace.NewManager(linearCfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new linear service: %v", err)
	}

	runCtx, runCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer runCancel()
	projectErrCh := make(chan error, 1)
	epicErrCh := make(chan error, 1)
	linearErrCh := make(chan error, 1)
	go func() { projectErrCh <- projectSvc.Run(runCtx) }()
	go func() { epicErrCh <- epicSvc.Run(runCtx) }()
	go func() { linearErrCh <- linearSvc.Run(runCtx) }()

	waitFor(t, 20*time.Second, func() bool {
		return projectSvc.Snapshot().ActiveRun != nil && epicSvc.Snapshot().ActiveRun != nil && linearSvc.Snapshot().ActiveRun != nil
	})

	if err := liveGitLabUpdateIssueState(context.Background(), env["MAESTRO_TEST_GITLAB_BASE_URL"], env["MAESTRO_TEST_GITLAB_TOKEN"], env["MAESTRO_TEST_GITLAB_PROJECT"], projectIssue.ID, "close"); err != nil {
		t.Fatalf("close gitlab project issue: %v", err)
	}

	waitFor(t, 20*time.Second, func() bool {
		return len(projectHarness.StopCalls) == 1 && projectSvc.Snapshot().ActiveRun == nil
	})
	if epicSvc.Snapshot().ActiveRun == nil {
		t.Fatal("expected gitlab epic source to remain active")
	}
	if linearSvc.Snapshot().ActiveRun == nil {
		t.Fatal("expected linear source to remain active")
	}

	close(epicHarness.WaitBlock)
	close(linearHarness.WaitBlock)
	waitFor(t, 20*time.Second, func() bool {
		return epicSvc.Snapshot().ActiveRun == nil && linearSvc.Snapshot().ActiveRun == nil
	})

	runCancel()
	if err := <-projectErrCh; err != nil {
		t.Fatalf("project service run: %v", err)
	}
	if err := <-epicErrCh; err != nil {
		t.Fatalf("epic service run: %v", err)
	}
	if err := <-linearErrCh; err != nil {
		t.Fatalf("linear service run: %v", err)
	}
}

func TestLiveMultiSourceRetryDoesNotBlockPeers(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_GITLAB")
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_GITLAB_EPIC")
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_LINEAR")

	env := testutil.RequireEnv(
		t,
		"MAESTRO_TEST_GITLAB_BASE_URL",
		"MAESTRO_TEST_GITLAB_TOKEN",
		"MAESTRO_TEST_GITLAB_PROJECT",
		"MAESTRO_TEST_GITLAB_LABEL",
		"MAESTRO_TEST_GITLAB_EPIC_GROUP",
		"MAESTRO_TEST_GITLAB_EPIC_LABEL",
		"MAESTRO_TEST_GITLAB_EPIC_REPO",
		"MAESTRO_TEST_LINEAR_TOKEN",
		"MAESTRO_TEST_LINEAR_PROJECT",
	)

	root := t.TempDir()
	projectRoot := filepath.Join(root, "project")
	epicRoot := filepath.Join(root, "epic")
	linearRoot := filepath.Join(root, "linear")
	for _, dir := range []string{projectRoot, epicRoot, linearRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	projectCfg := testConfigWithRoot(t, projectRoot)
	projectCfg.User.GitLabUsername = strings.TrimSpace(os.Getenv("MAESTRO_TEST_GITLAB_ASSIGNEE"))
	projectCfg.Sources[0] = config.SourceConfig{
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
		PollInterval: config.Duration{Duration: 50 * time.Millisecond},
	}
	projectTracker, err := gitlabtracker.NewAdapter(projectCfg.Sources[0])
	if err != nil {
		t.Fatalf("new gitlab tracker: %v", err)
	}

	epicCfg := testConfigWithRoot(t, epicRoot)
	epicCfg.User.GitLabUsername = strings.TrimSpace(os.Getenv("MAESTRO_TEST_GITLAB_ASSIGNEE"))
	epicCfg.Sources[0] = config.SourceConfig{
		Name:      "live-gitlab-epic",
		Tracker:   "gitlab-epic",
		Repo:      env["MAESTRO_TEST_GITLAB_EPIC_REPO"],
		AgentType: "code-pr",
		Connection: config.GitLabConnection{
			BaseURL:  env["MAESTRO_TEST_GITLAB_BASE_URL"],
			Token:    env["MAESTRO_TEST_GITLAB_TOKEN"],
			Group:    env["MAESTRO_TEST_GITLAB_EPIC_GROUP"],
			TokenEnv: "MAESTRO_TEST_GITLAB_TOKEN",
		},
		Filter: config.FilterConfig{
			Labels: []string{env["MAESTRO_TEST_GITLAB_EPIC_LABEL"]},
		},
		PollInterval: config.Duration{Duration: 50 * time.Millisecond},
	}
	epicTracker, err := gitlabtracker.NewAdapter(epicCfg.Sources[0])
	if err != nil {
		t.Fatalf("new gitlab epic tracker: %v", err)
	}

	linearCfg := testConfigWithRoot(t, linearRoot)
	linearCfg.User.LinearUsername = "operator@example.com"
	linearCfg.Sources[0] = config.SourceConfig{
		Name:      "live-linear",
		Tracker:   "linear",
		Repo:      createGitRepo(t),
		AgentType: "code-pr",
		Connection: config.SourceConnection{
			Token:   env["MAESTRO_TEST_LINEAR_TOKEN"],
			Project: env["MAESTRO_TEST_LINEAR_PROJECT"],
		},
		Filter: config.FilterConfig{
			States: []string{"Todo"},
		},
		PollInterval: config.Duration{Duration: 50 * time.Millisecond},
	}
	linearTracker, err := lineartracker.NewAdapter(linearCfg.Sources[0])
	if err != nil {
		t.Fatalf("new linear tracker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	projectIssues, err := projectTracker.Poll(ctx)
	if err != nil {
		t.Fatalf("poll gitlab project: %v", err)
	}
	if len(projectIssues) == 0 {
		t.Fatalf("expected at least one gitlab project issue")
	}
	projectIssue := projectIssues[0]

	epicIssues, err := epicTracker.Poll(ctx)
	if err != nil {
		t.Fatalf("poll gitlab epic: %v", err)
	}
	if len(epicIssues) == 0 {
		t.Fatalf("expected at least one gitlab epic child issue")
	}
	epicIssue := epicIssues[0]

	linearIssues, err := linearTracker.Poll(ctx)
	if err != nil {
		t.Fatalf("poll linear: %v", err)
	}
	if len(linearIssues) == 0 {
		t.Fatalf("expected at least one linear issue")
	}
	linearIssue := linearIssues[0]

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cleanupCancel()
	liveRemoveLifecycleLabels(cleanupCtx, projectTracker, projectIssue.ID)
	liveRemoveLifecycleLabels(cleanupCtx, epicTracker, epicIssue.ID)
	liveRemoveLifecycleLabels(cleanupCtx, linearTracker, linearIssue.ID)
	if err := liveGitLabUpdateIssueState(cleanupCtx, env["MAESTRO_TEST_GITLAB_BASE_URL"], env["MAESTRO_TEST_GITLAB_TOKEN"], env["MAESTRO_TEST_GITLAB_PROJECT"], projectIssue.ID, "reopen"); err != nil {
		t.Fatalf("reopen gitlab project issue: %v", err)
	}
	t.Cleanup(func() {
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer restoreCancel()
		_ = liveGitLabUpdateIssueState(restoreCtx, env["MAESTRO_TEST_GITLAB_BASE_URL"], env["MAESTRO_TEST_GITLAB_TOKEN"], env["MAESTRO_TEST_GITLAB_PROJECT"], projectIssue.ID, "reopen")
		liveRemoveLifecycleLabels(restoreCtx, projectTracker, projectIssue.ID)
		liveRemoveLifecycleLabels(restoreCtx, epicTracker, epicIssue.ID)
		liveRemoveLifecycleLabels(restoreCtx, linearTracker, linearIssue.ID)
	})

	projectHarness := &testutil.FakeHarness{WaitErrs: []error{errors.New("boom"), nil}}
	epicHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}
	linearHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}

	projectSvc, err := orchestrator.NewServiceWithDeps(projectCfg, slog.New(slog.NewTextHandler(io.Discard, nil)), orchestrator.Dependencies{
		Tracker:   projectTracker,
		Harness:   projectHarness,
		Workspace: workspace.NewManager(projectCfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new project service: %v", err)
	}
	epicSvc, err := orchestrator.NewServiceWithDeps(epicCfg, slog.New(slog.NewTextHandler(io.Discard, nil)), orchestrator.Dependencies{
		Tracker:   epicTracker,
		Harness:   epicHarness,
		Workspace: workspace.NewManager(epicCfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new epic service: %v", err)
	}
	linearSvc, err := orchestrator.NewServiceWithDeps(linearCfg, slog.New(slog.NewTextHandler(io.Discard, nil)), orchestrator.Dependencies{
		Tracker:   linearTracker,
		Harness:   linearHarness,
		Workspace: workspace.NewManager(linearCfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new linear service: %v", err)
	}

	runCtx, runCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer runCancel()
	projectErrCh := make(chan error, 1)
	epicErrCh := make(chan error, 1)
	linearErrCh := make(chan error, 1)
	go func() { projectErrCh <- projectSvc.Run(runCtx) }()
	go func() { epicErrCh <- epicSvc.Run(runCtx) }()
	go func() { linearErrCh <- linearSvc.Run(runCtx) }()

	waitFor(t, 20*time.Second, func() bool {
		return epicSvc.Snapshot().ActiveRun != nil && linearSvc.Snapshot().ActiveRun != nil
	})
	waitFor(t, 20*time.Second, func() bool {
		snapshot := projectSvc.Snapshot()
		return len(projectHarness.StartedRuns) >= 2 || snapshot.RetryCount > 0 || snapshotHasEvent(snapshot, "retry")
	})

	if epicSvc.Snapshot().ActiveRun == nil {
		t.Fatal("expected gitlab epic source to remain active during retry")
	}
	if linearSvc.Snapshot().ActiveRun == nil {
		t.Fatal("expected linear source to remain active during retry")
	}

	close(epicHarness.WaitBlock)
	close(linearHarness.WaitBlock)
	waitFor(t, 20*time.Second, func() bool {
		return epicSvc.Snapshot().ActiveRun == nil && linearSvc.Snapshot().ActiveRun == nil
	})

	runCancel()
	if err := <-projectErrCh; err != nil {
		t.Fatalf("project service run: %v", err)
	}
	if err := <-epicErrCh; err != nil {
		t.Fatalf("epic service run: %v", err)
	}
	if err := <-linearErrCh; err != nil {
		t.Fatalf("linear service run: %v", err)
	}
}

func snapshotHasEvent(snapshot orchestrator.Snapshot, want string) bool {
	for _, event := range snapshot.RecentEvents {
		if strings.Contains(event.Message, want) {
			return true
		}
	}
	return false
}

func liveRemoveLifecycleLabels(ctx context.Context, tracker trackerbase.Tracker, issueID string) {
	for _, label := range []string{
		trackerbase.LifecycleLabelActive,
		trackerbase.LifecycleLabelRetry,
		trackerbase.LifecycleLabelDone,
		trackerbase.LifecycleLabelFailed,
	} {
		_ = tracker.RemoveLifecycleLabel(ctx, issueID, label)
	}
}

func liveLinearIssueStateID(ctx context.Context, token string, issueID string) (string, error) {
	var resp struct {
		Issue struct {
			State struct {
				ID string `json:"id"`
			} `json:"state"`
		} `json:"issue"`
	}
	if err := liveLinearQuery(ctx, token, `
query($id: String!) {
  issue(id: $id) {
    state { id }
  }
}`, map[string]any{"id": issueID}, &resp); err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.Issue.State.ID) == "" {
		return "", fmt.Errorf("issue %s returned empty state id", issueID)
	}
	return resp.Issue.State.ID, nil
}

func liveLinearWorkflowStateID(ctx context.Context, token string, teamID string, stateType string) (string, error) {
	var resp struct {
		WorkflowStates struct {
			Nodes []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"nodes"`
		} `json:"workflowStates"`
	}
	if err := liveLinearQuery(ctx, token, `
query($teamId: ID!) {
  workflowStates(first: 20, filter: { team: { id: { eq: $teamId } } }) {
    nodes {
      id
      type
    }
  }
}`, map[string]any{"teamId": teamID}, &resp); err != nil {
		return "", err
	}
	for _, node := range resp.WorkflowStates.Nodes {
		if strings.EqualFold(node.Type, stateType) {
			return node.ID, nil
		}
	}
	return "", fmt.Errorf("workflow state with type %q not found for team %s", stateType, teamID)
}

func liveLinearUpdateIssueState(ctx context.Context, token string, issueID string, stateID string) error {
	return liveLinearQuery(ctx, token, `
mutation($id: String!, $stateId: String!) {
  issueUpdate(id: $id, input: { stateId: $stateId }) {
    success
  }
}`, map[string]any{
		"id":      issueID,
		"stateId": stateID,
	}, nil)
}

func liveLinearQuery(ctx context.Context, token string, query string, variables map[string]any, dst any) error {
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": variables,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.linear.app/graphql", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("linear graphql: %s", envelope.Errors[0].Message)
	}
	if dst != nil {
		return json.Unmarshal(envelope.Data, dst)
	}
	return nil
}

func liveGitLabUpdateIssueState(ctx context.Context, baseURL string, token string, project string, issueID string, event string) error {
	iid, err := gitlabIssueIID(issueID)
	if err != nil {
		return err
	}

	form := url.Values{}
	form.Set("state_event", event)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimRight(baseURL, "/")+"/api/v4/projects/"+url.PathEscape(project)+"/issues/"+strconv.Itoa(iid), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gitlab update issue state: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func liveGitLabUpdateEpicState(ctx context.Context, baseURL string, token string, issueID string, event string) error {
	group, iid, epicID, err := gitlabEpicRef(issueID)
	if err != nil {
		return err
	}

	command := "/" + event
	form := url.Values{}
	form.Set("body", command)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/api/v4/groups/"+url.PathEscape(group)+"/epics/"+strconv.Itoa(epicID)+"/notes", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)

	want := "opened"
	if event == "close" {
		want = "closed"
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		currentState, err := liveGitLabGetEpicState(ctx, baseURL, token, group, iid)
		if err == nil && strings.EqualFold(strings.TrimSpace(currentState), want) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("gitlab epic did not reach state %q after quick action %q", want, command)
}

func gitlabIssueIID(issueID string) (int, error) {
	hash := strings.LastIndex(issueID, "#")
	if hash == -1 || hash == len(issueID)-1 {
		return 0, fmt.Errorf("invalid gitlab issue id %q", issueID)
	}
	return strconv.Atoi(issueID[hash+1:])
}

func gitlabEpicRef(issueID string) (string, int, int, error) {
	trimmed := strings.TrimSpace(issueID)
	if !strings.HasPrefix(trimmed, "gitlab-epic:") {
		return "", 0, 0, fmt.Errorf("invalid gitlab epic id %q", issueID)
	}
	payload := strings.TrimPrefix(trimmed, "gitlab-epic:")
	parts := strings.SplitN(payload, "|", 2)
	if len(parts) != 2 {
		return "", 0, 0, fmt.Errorf("invalid gitlab epic id %q", issueID)
	}
	epicPart := parts[0]
	colon := strings.LastIndex(epicPart, ":")
	if colon == -1 || colon == len(epicPart)-1 {
		return "", 0, 0, fmt.Errorf("invalid gitlab epic id %q", issueID)
	}
	epicID, err := strconv.Atoi(epicPart[colon+1:])
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid gitlab epic id %q", issueID)
	}

	ref := epicPart[:colon]
	amp := strings.LastIndex(ref, "&")
	if amp == -1 || amp == len(ref)-1 {
		return "", 0, 0, fmt.Errorf("invalid gitlab epic id %q", issueID)
	}
	iid, err := strconv.Atoi(ref[amp+1:])
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid gitlab epic id %q", issueID)
	}
	return ref[:amp], iid, epicID, nil
}

func liveGitLabGetEpicState(ctx context.Context, baseURL string, token string, group string, iid int) (string, error) {
	var epic struct {
		State string `json:"state"`
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/v4/groups/"+url.PathEscape(group)+"/epics/"+strconv.Itoa(iid), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("PRIVATE-TOKEN", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gitlab get epic: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(&epic); err != nil {
		return "", err
	}
	return epic.State, nil
}
