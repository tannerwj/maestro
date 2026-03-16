package ops

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/state"
	"github.com/tjohnson/maestro/internal/workspace"
)

type RunsSummary struct {
	SourceName    string      `json:"source_name"`
	Health        string      `json:"health"`
	ActiveCount   int         `json:"active_count"`
	RetryCount    int         `json:"retry_count"`
	FinishedCount int         `json:"finished_count"`
	DoneCount     int         `json:"done_count"`
	FailedCount   int         `json:"failed_count"`
	LastError     string      `json:"last_error,omitempty"`
	ActiveRun     *RunRecord  `json:"active_run,omitempty"`
	Retries       []RunRecord `json:"retries,omitempty"`
	Finished      []RunRecord `json:"finished,omitempty"`
}

type RunRecord struct {
	IssueID        string    `json:"issue_id"`
	Identifier     string    `json:"identifier"`
	RunID          string    `json:"run_id,omitempty"`
	Status         string    `json:"status"`
	Attempt        int       `json:"attempt"`
	WorkspacePath  string    `json:"workspace_path,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	LastActivityAt time.Time `json:"last_activity_at,omitempty"`
	FinishedAt     time.Time `json:"finished_at,omitempty"`
	DueAt          time.Time `json:"due_at,omitempty"`
	Error          string    `json:"error,omitempty"`
	IssueUpdatedAt time.Time `json:"issue_updated_at,omitempty"`
}

type ResetIssueResult struct {
	IssueRef                string   `json:"issue_ref"`
	MatchedIssueID          string   `json:"matched_issue_id,omitempty"`
	MatchedIdentifier       string   `json:"matched_identifier,omitempty"`
	RemovedFinished         bool     `json:"removed_finished"`
	RemovedRetry            bool     `json:"removed_retry"`
	RemovedPendingApprovals int      `json:"removed_pending_approvals"`
	RemovedApprovalHistory  int      `json:"removed_approval_history"`
	RemovedWorkspacePaths   []string `json:"removed_workspace_paths,omitempty"`
}

type CleanupWorkspacesResult struct {
	Root      string   `json:"root"`
	DryRun    bool     `json:"dry_run"`
	Protected []string `json:"protected,omitempty"`
	Removed   []string `json:"removed,omitempty"`
	Skipped   []string `json:"skipped,omitempty"`
}

func SummarizeRuns(sourceName string, snapshot state.Snapshot) RunsSummary {
	summary := RunsSummary{SourceName: sourceName}
	if snapshot.ActiveRun != nil {
		summary.ActiveCount = 1
		summary.ActiveRun = &RunRecord{
			IssueID:        snapshot.ActiveRun.IssueID,
			Identifier:     snapshot.ActiveRun.Identifier,
			RunID:          snapshot.ActiveRun.RunID,
			Status:         string(snapshot.ActiveRun.Status),
			Attempt:        snapshot.ActiveRun.Attempt,
			WorkspacePath:  snapshot.ActiveRun.WorkspacePath,
			StartedAt:      snapshot.ActiveRun.StartedAt,
			LastActivityAt: snapshot.ActiveRun.LastActivityAt,
			IssueUpdatedAt: snapshot.ActiveRun.IssueUpdatedAt,
		}
	}
	for _, retry := range snapshot.RetryQueue {
		summary.RetryCount++
		summary.Retries = append(summary.Retries, RunRecord{
			IssueID:        retry.IssueID,
			Identifier:     retry.Identifier,
			Status:         "retry_queued",
			Attempt:        retry.Attempt,
			DueAt:          retry.DueAt,
			Error:          retry.Error,
			IssueUpdatedAt: retry.IssueUpdatedAt,
		})
	}
	for _, finished := range snapshot.Finished {
		summary.FinishedCount++
		switch finished.Status {
		case domain.RunStatusDone:
			summary.DoneCount++
		case domain.RunStatusFailed:
			summary.FailedCount++
		}
		summary.Finished = append(summary.Finished, RunRecord{
			IssueID:        finished.IssueID,
			Identifier:     finished.Identifier,
			Status:         string(finished.Status),
			Attempt:        finished.Attempt,
			FinishedAt:     finished.FinishedAt,
			Error:          finished.Error,
			IssueUpdatedAt: finished.IssueUpdatedAt,
		})
	}
	sort.Slice(summary.Retries, func(i, j int) bool {
		if summary.Retries[i].DueAt.Equal(summary.Retries[j].DueAt) {
			return summary.Retries[i].Identifier < summary.Retries[j].Identifier
		}
		return summary.Retries[i].DueAt.Before(summary.Retries[j].DueAt)
	})
	sort.Slice(summary.Finished, func(i, j int) bool {
		if summary.Finished[i].FinishedAt.Equal(summary.Finished[j].FinishedAt) {
			return summary.Finished[i].Identifier < summary.Finished[j].Identifier
		}
		return summary.Finished[i].FinishedAt.After(summary.Finished[j].FinishedAt)
	})
	summary.LastError = latestRunError(summary)
	summary.Health = summarizeRunHealth(summary)
	return summary
}

func latestRunError(summary RunsSummary) string {
	for _, retry := range summary.Retries {
		if strings.TrimSpace(retry.Error) != "" {
			return retry.Error
		}
	}
	for _, finished := range summary.Finished {
		if strings.TrimSpace(finished.Error) != "" {
			return finished.Error
		}
	}
	return ""
}

func summarizeRunHealth(summary RunsSummary) string {
	switch {
	case summary.RetryCount > 0:
		return "retrying"
	case summary.ActiveCount > 0 && summary.FailedCount > 0:
		return "active+degraded"
	case summary.ActiveCount > 0:
		return "active"
	case summary.FailedCount > 0:
		return "degraded"
	case summary.FinishedCount > 0:
		return "idle"
	default:
		return "empty"
	}
}

func ResetIssue(store *state.Store, issueRef string, workspaceRoot string, removeWorkspace bool) (ResetIssueResult, error) {
	snapshot, err := store.Load()
	if err != nil {
		return ResetIssueResult{}, err
	}

	result := ResetIssueResult{IssueRef: issueRef}
	matchID, matchIdentifier, err := locateIssue(snapshot, issueRef)
	if err != nil {
		return result, err
	}
	result.MatchedIssueID = matchID
	result.MatchedIdentifier = matchIdentifier

	if snapshot.ActiveRun != nil && matchesIssueRef(snapshot.ActiveRun.IssueID, snapshot.ActiveRun.Identifier, issueRef) {
		return result, fmt.Errorf("issue %q is the active run; stop Maestro before resetting it", issueRef)
	}

	if _, ok := snapshot.Finished[matchID]; ok {
		delete(snapshot.Finished, matchID)
		result.RemovedFinished = true
	}
	if _, ok := snapshot.RetryQueue[matchID]; ok {
		delete(snapshot.RetryQueue, matchID)
		result.RemovedRetry = true
	}

	pending := snapshot.PendingApprovals[:0]
	for _, approval := range snapshot.PendingApprovals {
		if matchesIssueRef(approval.IssueID, approval.IssueIdentifier, issueRef) {
			result.RemovedPendingApprovals++
			continue
		}
		pending = append(pending, approval)
	}
	snapshot.PendingApprovals = pending

	history := snapshot.ApprovalHistory[:0]
	for _, decision := range snapshot.ApprovalHistory {
		if matchesIssueRef(decision.IssueID, decision.IssueIdentifier, issueRef) {
			result.RemovedApprovalHistory++
			continue
		}
		history = append(history, decision)
	}
	snapshot.ApprovalHistory = history

	if removeWorkspace && strings.TrimSpace(workspaceRoot) != "" {
		for _, candidate := range workspaceCandidates(workspaceRoot, matchIdentifier) {
			if _, err := os.Stat(candidate); err == nil {
				if err := os.RemoveAll(candidate); err != nil {
					return result, fmt.Errorf("remove workspace %s: %w", candidate, err)
				}
				result.RemovedWorkspacePaths = append(result.RemovedWorkspacePaths, candidate)
			}
		}
	}

	if err := store.Save(snapshot); err != nil {
		return result, err
	}
	return result, nil
}

func CleanupWorkspaces(root string, snapshot state.Snapshot, dryRun bool) (CleanupWorkspacesResult, error) {
	protected := []string{}
	if snapshot.ActiveRun != nil && strings.TrimSpace(snapshot.ActiveRun.WorkspacePath) != "" {
		protected = append(protected, snapshot.ActiveRun.WorkspacePath)
	}
	return CleanupWorkspacesWithProtected(root, protected, dryRun)
}

func CleanupWorkspacesWithProtected(root string, protectedPaths []string, dryRun bool) (CleanupWorkspacesResult, error) {
	result := CleanupWorkspacesResult{Root: root, DryRun: dryRun}

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}

	protected := map[string]struct{}{}
	for _, path := range protectedPaths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		protected[filepath.Clean(path)] = struct{}{}
	}
	for path := range protected {
		result.Protected = append(result.Protected, path)
	}
	sort.Strings(result.Protected)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(root, entry.Name())
		cleaned := filepath.Clean(candidate)
		if _, ok := protected[cleaned]; ok {
			result.Skipped = append(result.Skipped, cleaned)
			continue
		}
		if !dryRun {
			if err := os.RemoveAll(cleaned); err != nil {
				return result, fmt.Errorf("remove workspace %s: %w", cleaned, err)
			}
		}
		result.Removed = append(result.Removed, cleaned)
	}

	sort.Strings(result.Removed)
	sort.Strings(result.Skipped)
	return result, nil
}

func locateIssue(snapshot state.Snapshot, issueRef string) (string, string, error) {
	for issueID, finished := range snapshot.Finished {
		if matchesIssueRef(issueID, finished.Identifier, issueRef) {
			return issueID, finished.Identifier, nil
		}
	}
	for issueID, retry := range snapshot.RetryQueue {
		if matchesIssueRef(issueID, retry.Identifier, issueRef) {
			return issueID, retry.Identifier, nil
		}
	}
	if snapshot.ActiveRun != nil && matchesIssueRef(snapshot.ActiveRun.IssueID, snapshot.ActiveRun.Identifier, issueRef) {
		return snapshot.ActiveRun.IssueID, snapshot.ActiveRun.Identifier, nil
	}
	for _, approval := range snapshot.PendingApprovals {
		if matchesIssueRef(approval.IssueID, approval.IssueIdentifier, issueRef) {
			return approval.IssueID, approval.IssueIdentifier, nil
		}
	}
	for _, decision := range snapshot.ApprovalHistory {
		if matchesIssueRef(decision.IssueID, decision.IssueIdentifier, issueRef) {
			return decision.IssueID, decision.IssueIdentifier, nil
		}
	}
	return "", "", fmt.Errorf("issue %q not found in local state", issueRef)
}

func matchesIssueRef(issueID string, identifier string, issueRef string) bool {
	trimmed := strings.TrimSpace(issueRef)
	return trimmed != "" && (issueID == trimmed || identifier == trimmed)
}

func workspaceCandidates(root string, identifier string) []string {
	if strings.TrimSpace(identifier) == "" {
		return nil
	}
	key := workspace.WorkspaceKey(identifier)
	return []string{filepath.Join(root, key)}
}
