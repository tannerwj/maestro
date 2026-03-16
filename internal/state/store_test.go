package state_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/state"
)

func TestStoreSaveAndLoad(t *testing.T) {
	store := state.NewStore(t.TempDir())
	now := time.Now().UTC().Round(time.Second)

	want := state.Snapshot{
		Finished: map[string]state.TerminalIssue{
			"issue-1": {
				IssueID:        "issue-1",
				Identifier:     "TAN-1",
				Status:         domain.RunStatusDone,
				Attempt:        1,
				IssueUpdatedAt: now,
				FinishedAt:     now,
			},
		},
		RetryQueue: map[string]state.RetryEntry{
			"issue-2": {
				IssueID:        "issue-2",
				Identifier:     "TAN-2",
				Attempt:        2,
				DueAt:          now.Add(time.Minute),
				Error:          "boom",
				IssueUpdatedAt: now,
			},
		},
		ActiveRun: &state.PersistedRun{
			RunID:          "run-1",
			IssueID:        "issue-3",
			Identifier:     "TAN-3",
			Status:         domain.RunStatusActive,
			Attempt:        1,
			WorkspacePath:  filepath.Join(t.TempDir(), "workspace"),
			StartedAt:      now,
			LastActivityAt: now,
			IssueUpdatedAt: now,
		},
		PendingApprovals: []state.PersistedApprovalRequest{
			{
				RequestID:       "req-1",
				RunID:           "run-1",
				IssueID:         "issue-3",
				IssueIdentifier: "TAN-3",
				AgentName:       "coder",
				ToolName:        "shell",
				ToolInput:       "rm -rf",
				ApprovalPolicy:  "manual",
				RequestedAt:     now,
				Resolvable:      true,
			},
		},
		ApprovalHistory: []state.PersistedApprovalDecision{
			{
				RequestID:       "req-0",
				RunID:           "run-0",
				IssueID:         "issue-0",
				IssueIdentifier: "TAN-0",
				AgentName:       "coder",
				ToolName:        "shell",
				ApprovalPolicy:  "manual",
				Decision:        "approve",
				RequestedAt:     now.Add(-time.Minute),
				DecidedAt:       now,
				Outcome:         "resolved",
			},
		},
	}

	if err := store.Save(want); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got.Version == 0 {
		t.Fatal("expected persisted version")
	}
	if got.Finished["issue-1"].Identifier != "TAN-1" {
		t.Fatalf("finished identifier = %q, want TAN-1", got.Finished["issue-1"].Identifier)
	}
	if got.RetryQueue["issue-2"].Attempt != 2 {
		t.Fatalf("retry attempt = %d, want 2", got.RetryQueue["issue-2"].Attempt)
	}
	if got.ActiveRun == nil || got.ActiveRun.RunID != "run-1" {
		t.Fatalf("active run = %+v, want run-1", got.ActiveRun)
	}
	if len(got.PendingApprovals) != 1 || got.PendingApprovals[0].RequestID != "req-1" {
		t.Fatalf("pending approvals = %+v, want req-1", got.PendingApprovals)
	}
	if len(got.ApprovalHistory) != 1 || got.ApprovalHistory[0].Outcome != "resolved" {
		t.Fatalf("approval history = %+v, want resolved entry", got.ApprovalHistory)
	}
}

func TestStoreLoadMissingFileReturnsEmptySnapshot(t *testing.T) {
	store := state.NewStore(t.TempDir())

	got, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Finished) != 0 {
		t.Fatalf("finished entries = %d, want 0", len(got.Finished))
	}
	if len(got.RetryQueue) != 0 {
		t.Fatalf("retry entries = %d, want 0", len(got.RetryQueue))
	}
}
