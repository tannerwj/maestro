package ops

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/state"
)

func TestSummarizeConfig(t *testing.T) {
	cfg := &config.Config{
		ConfigPath: "/tmp/maestro.yaml",
		Workspace:  config.WorkspaceConfig{Root: "/tmp/workspaces"},
		State:      config.StateConfig{Dir: "/tmp/state"},
		Logging:    config.LoggingConfig{Dir: "/tmp/logs", MaxFiles: 7},
		Sources: []config.SourceConfig{
			{
				Name:      "source-1",
				Tracker:   "gitlab",
				Repo:      "https://gitlab.example.com/team/project.git",
				AgentType: "code-pr",
				Connection: config.SourceConnection{
					BaseURL:  "https://gitlab.example.com",
					Project:  "team/project",
					TokenEnv: "GITLAB_TOKEN",
				},
				Filter:       config.FilterConfig{Labels: []string{"agent:ready"}},
				PollInterval: config.Duration{Duration: time.Minute},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:           "code-pr",
				AgentPack:      "code-pr",
				Harness:        "claude-code",
				Workspace:      "git-clone",
				ApprovalPolicy: "auto",
				Prompt:         "/tmp/prompt.md",
				Env:            map[string]string{"FOO": "bar"},
			},
		},
	}

	summary := SummarizeConfig(cfg)
	if summary.LogMaxFiles != 7 {
		t.Fatalf("log max files = %d", summary.LogMaxFiles)
	}
	if len(summary.Sources) != 1 || summary.Sources[0].TokenEnv != "GITLAB_TOKEN" {
		t.Fatalf("source summaries = %+v", summary.Sources)
	}
	if len(summary.Agents) != 1 || len(summary.Agents[0].EnvKeys) != 1 || summary.Agents[0].EnvKeys[0] != "FOO" {
		t.Fatalf("agent summaries = %+v", summary.Agents)
	}
}

func TestSummarizeState(t *testing.T) {
	now := time.Now().UTC()
	summary := SummarizeState("source-a", filepath.Join("/tmp", "runs.json"), state.Snapshot{
		Version: 2,
		Finished: map[string]state.TerminalIssue{
			"issue-1": {IssueID: "issue-1", Identifier: "TAN-1", Status: domain.RunStatusDone, FinishedAt: now},
			"issue-4": {IssueID: "issue-4", Identifier: "TAN-4", Status: domain.RunStatusFailed, FinishedAt: now.Add(-time.Minute), Error: "boom"},
		},
		RetryQueue: map[string]state.RetryEntry{
			"issue-2": {IssueID: "issue-2", Identifier: "TAN-2", Attempt: 1, DueAt: now, Error: "retry boom"},
		},
		PendingApprovals: []state.PersistedApprovalRequest{
			{RequestID: "req-1", ToolName: "shell", IssueIdentifier: "TAN-3"},
		},
		ApprovalHistory: []state.PersistedApprovalDecision{
			{RequestID: "req-0", Decision: "approve", Outcome: "resolved"},
		},
	})

	if summary.FinishedCount != 2 || summary.RetryCount != 1 {
		t.Fatalf("unexpected state counts: %+v", summary)
	}
	if summary.PendingCount != 1 || summary.ApprovalHistCount != 1 {
		t.Fatalf("unexpected approval counts: %+v", summary)
	}
	if summary.Health != "retrying" || summary.LastError != "retry boom" {
		t.Fatalf("unexpected state rollup: %+v", summary)
	}
}

func TestSummarizeRuns(t *testing.T) {
	now := time.Now().UTC()
	summary := SummarizeRuns("source-a", state.Snapshot{
		ActiveRun: &state.PersistedRun{
			RunID:          "run-1",
			IssueID:        "issue-1",
			Identifier:     "TAN-1",
			Status:         domain.RunStatusActive,
			Attempt:        1,
			WorkspacePath:  "/tmp/workspaces/TAN-1",
			StartedAt:      now.Add(-time.Minute),
			LastActivityAt: now,
		},
		RetryQueue: map[string]state.RetryEntry{
			"issue-2": {IssueID: "issue-2", Identifier: "TAN-2", Attempt: 2, DueAt: now.Add(time.Minute), Error: "retry err"},
		},
		Finished: map[string]state.TerminalIssue{
			"issue-3": {IssueID: "issue-3", Identifier: "TAN-3", Status: domain.RunStatusDone, Attempt: 0, FinishedAt: now},
			"issue-4": {IssueID: "issue-4", Identifier: "TAN-4", Status: domain.RunStatusFailed, Attempt: 1, FinishedAt: now.Add(-time.Minute), Error: "failed err"},
		},
	})

	if summary.ActiveRun == nil || summary.ActiveRun.RunID != "run-1" {
		t.Fatalf("active run summary = %+v", summary.ActiveRun)
	}
	if len(summary.Retries) != 1 || summary.Retries[0].Status != "retry_queued" {
		t.Fatalf("retries = %+v", summary.Retries)
	}
	if len(summary.Finished) != 2 {
		t.Fatalf("finished = %+v", summary.Finished)
	}
	if summary.SourceName != "source-a" || summary.Health != "retrying" || summary.LastError != "retry err" {
		t.Fatalf("unexpected run rollup: %+v", summary)
	}
}

func TestResetIssueRemovesStateAndWorkspace(t *testing.T) {
	root := t.TempDir()
	store := state.NewStore(filepath.Join(root, "state"))
	workspacePath := filepath.Join(root, "workspaces", "TAN-9")
	if err := os.MkdirAll(workspacePath, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	err := store.Save(state.Snapshot{
		Finished: map[string]state.TerminalIssue{
			"issue-9": {IssueID: "issue-9", Identifier: "TAN-9", Status: domain.RunStatusFailed, FinishedAt: time.Now().UTC()},
		},
		RetryQueue: map[string]state.RetryEntry{
			"issue-9": {IssueID: "issue-9", Identifier: "TAN-9", Attempt: 1, DueAt: time.Now().UTC()},
		},
		PendingApprovals: []state.PersistedApprovalRequest{
			{RequestID: "req-1", IssueID: "issue-9", IssueIdentifier: "TAN-9"},
		},
		ApprovalHistory: []state.PersistedApprovalDecision{
			{RequestID: "req-0", IssueID: "issue-9", IssueIdentifier: "TAN-9"},
		},
	})
	if err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	result, err := ResetIssue(store, "TAN-9", filepath.Join(root, "workspaces"), true)
	if err != nil {
		t.Fatalf("reset issue: %v", err)
	}
	if !result.RemovedFinished || !result.RemovedRetry || result.RemovedPendingApprovals != 1 || result.RemovedApprovalHistory != 1 {
		t.Fatalf("unexpected reset result: %+v", result)
	}
	if len(result.RemovedWorkspacePaths) != 1 {
		t.Fatalf("removed workspace paths = %+v", result.RemovedWorkspacePaths)
	}

	snapshot, err := store.Load()
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if len(snapshot.Finished) != 0 || len(snapshot.RetryQueue) != 0 || len(snapshot.PendingApprovals) != 0 || len(snapshot.ApprovalHistory) != 0 {
		t.Fatalf("snapshot not cleared: %+v", snapshot)
	}
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists, stat err=%v", err)
	}
}

func TestResetIssueRejectsActiveRun(t *testing.T) {
	root := t.TempDir()
	store := state.NewStore(filepath.Join(root, "state"))
	err := store.Save(state.Snapshot{
		ActiveRun: &state.PersistedRun{
			RunID:      "run-1",
			IssueID:    "issue-9",
			Identifier: "TAN-9",
			Status:     domain.RunStatusActive,
		},
	})
	if err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	if _, err := ResetIssue(store, "TAN-9", "", false); err == nil {
		t.Fatal("expected active run reset to fail")
	}
}

func TestCleanupWorkspacesKeepsActiveRun(t *testing.T) {
	root := t.TempDir()
	active := filepath.Join(root, "TAN-1")
	stale := filepath.Join(root, "TAN-2")
	for _, path := range []string{active, stale} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}

	result, err := CleanupWorkspaces(root, state.Snapshot{
		ActiveRun: &state.PersistedRun{WorkspacePath: active},
	}, false)
	if err != nil {
		t.Fatalf("cleanup workspaces: %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0] != stale {
		t.Fatalf("removed = %+v", result.Removed)
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != active {
		t.Fatalf("skipped = %+v", result.Skipped)
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("active workspace missing: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale workspace still exists, stat err=%v", err)
	}
}
