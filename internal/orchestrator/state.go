package orchestrator

import (
	"fmt"
	"time"

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/state"
)

func (s *Service) restoreState() error {
	snapshot, err := s.stateStore.Load()
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.finished = snapshot.Finished
	s.retryQueue = snapshot.RetryQueue
	s.approvals = make(map[string]ApprovalView, len(snapshot.PendingApprovals))
	s.approvalOrder = s.approvalOrder[:0]
	for _, approval := range snapshot.PendingApprovals {
		view := ApprovalView{
			RequestID:       approval.RequestID,
			RunID:           approval.RunID,
			IssueID:         approval.IssueID,
			IssueIdentifier: approval.IssueIdentifier,
			AgentName:       approval.AgentName,
			ToolName:        approval.ToolName,
			ToolInput:       approval.ToolInput,
			ApprovalPolicy:  approval.ApprovalPolicy,
			RequestedAt:     approval.RequestedAt,
			Resolvable:      approval.Resolvable,
		}
		s.approvals[view.RequestID] = view
		s.approvalOrder = append(s.approvalOrder, view.RequestID)
	}
	s.approvalHistory = s.approvalHistory[:0]
	for _, approval := range snapshot.ApprovalHistory {
		s.approvalHistory = append(s.approvalHistory, ApprovalHistoryEntry{
			RequestID:       approval.RequestID,
			RunID:           approval.RunID,
			IssueID:         approval.IssueID,
			IssueIdentifier: approval.IssueIdentifier,
			AgentName:       approval.AgentName,
			ToolName:        approval.ToolName,
			ApprovalPolicy:  approval.ApprovalPolicy,
			Decision:        approval.Decision,
			Reason:          approval.Reason,
			RequestedAt:     approval.RequestedAt,
			DecidedAt:       approval.DecidedAt,
			Outcome:         approval.Outcome,
		})
	}
	s.mu.Unlock()

	if snapshot.ActiveRun == nil {
		if s.expirePendingApprovals("restart without active run") {
			return s.saveStateBestEffort()
		}
		return nil
	}

	nextAttempt := snapshot.ActiveRun.Attempt + 1
	if nextAttempt >= s.cfg.State.MaxAttempts {
		s.mu.Lock()
		s.finished[snapshot.ActiveRun.IssueID] = state.TerminalIssue{
			IssueID:        snapshot.ActiveRun.IssueID,
			Identifier:     snapshot.ActiveRun.Identifier,
			Status:         domain.RunStatusFailed,
			Attempt:        snapshot.ActiveRun.Attempt,
			IssueUpdatedAt: snapshot.ActiveRun.IssueUpdatedAt,
			FinishedAt:     time.Now(),
			Error:          "run interrupted during shutdown or restart",
		}
		s.mu.Unlock()
		_ = s.expirePendingApprovals("restart after interrupted run")
		s.recordEvent("warn", "recovered active run %s but max attempts reached", snapshot.ActiveRun.RunID)
		return s.saveStateBestEffort()
	}

	s.mu.Lock()
	s.retryQueue[snapshot.ActiveRun.IssueID] = state.RetryEntry{
		IssueID:        snapshot.ActiveRun.IssueID,
		Identifier:     snapshot.ActiveRun.Identifier,
		Attempt:        nextAttempt,
		DueAt:          time.Now(),
		Error:          "recovered active run after restart",
		IssueUpdatedAt: snapshot.ActiveRun.IssueUpdatedAt,
	}
	s.mu.Unlock()

	_ = s.expirePendingApprovals("restart after interrupted run")
	s.recordEvent("warn", "recovered active run %s as retry attempt %d", snapshot.ActiveRun.RunID, nextAttempt)
	return s.saveStateBestEffort()
}

func (s *Service) saveStateBestEffort() error {
	if err := s.stateStore.Save(s.snapshotState()); err != nil {
		s.logger.Warn("persist state failed", "path", s.stateStore.Path(), "error", err)
		return err
	}
	return nil
}

func (s *Service) snapshotState() state.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := state.Snapshot{
		Finished:   make(map[string]state.TerminalIssue, len(s.finished)),
		RetryQueue: make(map[string]state.RetryEntry, len(s.retryQueue)),
	}
	for issueID, finished := range s.finished {
		snapshot.Finished[issueID] = finished
	}
	for issueID, retry := range s.retryQueue {
		snapshot.RetryQueue[issueID] = retry
	}
	for _, requestID := range s.approvalOrder {
		if approval, ok := s.approvals[requestID]; ok {
			snapshot.PendingApprovals = append(snapshot.PendingApprovals, state.PersistedApprovalRequest{
				RequestID:       approval.RequestID,
				RunID:           approval.RunID,
				IssueID:         approval.IssueID,
				IssueIdentifier: approval.IssueIdentifier,
				AgentName:       approval.AgentName,
				ToolName:        approval.ToolName,
				ToolInput:       approval.ToolInput,
				ApprovalPolicy:  approval.ApprovalPolicy,
				RequestedAt:     approval.RequestedAt,
				Resolvable:      approval.Resolvable,
			})
		}
	}
	for _, entry := range s.approvalHistory {
		snapshot.ApprovalHistory = append(snapshot.ApprovalHistory, state.PersistedApprovalDecision{
			RequestID:       entry.RequestID,
			RunID:           entry.RunID,
			IssueID:         entry.IssueID,
			IssueIdentifier: entry.IssueIdentifier,
			AgentName:       entry.AgentName,
			ToolName:        entry.ToolName,
			ApprovalPolicy:  entry.ApprovalPolicy,
			Decision:        entry.Decision,
			Reason:          entry.Reason,
			RequestedAt:     entry.RequestedAt,
			DecidedAt:       entry.DecidedAt,
			Outcome:         entry.Outcome,
		})
	}
	if s.activeRun != nil {
		snapshot.ActiveRun = &state.PersistedRun{
			RunID:          s.activeRun.ID,
			IssueID:        s.activeRun.Issue.ID,
			Identifier:     s.activeRun.Issue.Identifier,
			Status:         s.activeRun.Status,
			Attempt:        s.activeRun.Attempt,
			WorkspacePath:  s.activeRun.WorkspacePath,
			StartedAt:      s.activeRun.StartedAt,
			LastActivityAt: s.activeRun.LastActivityAt,
			IssueUpdatedAt: s.activeRun.Issue.UpdatedAt,
		}
	}
	return snapshot
}

func (s *Service) expirePendingApprovals(reason string) bool {
	s.mu.Lock()
	if len(s.approvalOrder) == 0 {
		s.mu.Unlock()
		return false
	}
	for _, requestID := range s.approvalOrder {
		approval, ok := s.approvals[requestID]
		if !ok {
			continue
		}
		s.appendApprovalHistory(ApprovalHistoryEntry{
			RequestID:       approval.RequestID,
			RunID:           approval.RunID,
			IssueID:         approval.IssueID,
			IssueIdentifier: approval.IssueIdentifier,
			AgentName:       approval.AgentName,
			ToolName:        approval.ToolName,
			ApprovalPolicy:  approval.ApprovalPolicy,
			Decision:        "stale",
			Reason:          reason,
			RequestedAt:     approval.RequestedAt,
			DecidedAt:       time.Now(),
			Outcome:         "stale_restart",
		})
	}
	s.approvals = map[string]ApprovalView{}
	s.approvalOrder = nil
	s.mu.Unlock()
	return true
}

func (s *Service) shouldSkipIssue(issue domain.Issue) bool {
	changed := false
	skip := false

	s.mu.Lock()
	if finished, ok := s.finished[issue.ID]; ok {
		if issueRecordStale(issue.UpdatedAt, finished.IssueUpdatedAt) {
			delete(s.finished, issue.ID)
			changed = true
		} else {
			skip = true
		}
	}
	if !skip {
		if retry, ok := s.retryQueue[issue.ID]; ok {
			if issueRecordStale(issue.UpdatedAt, retry.IssueUpdatedAt) {
				delete(s.retryQueue, issue.ID)
				changed = true
			} else if time.Now().Before(retry.DueAt) {
				skip = true
			}
		}
	}
	s.mu.Unlock()

	if changed {
		_ = s.saveStateBestEffort()
	}
	return skip
}

func (s *Service) takeAttempt(issue domain.Issue) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	retry, ok := s.retryQueue[issue.ID]
	if !ok {
		return 0
	}
	delete(s.retryQueue, issue.ID)
	return retry.Attempt
}

func (s *Service) scheduleRetry(run *domain.AgentRun, err error) bool {
	nextAttempt := run.Attempt + 1
	if nextAttempt >= s.cfg.State.MaxAttempts {
		return false
	}

	s.retryQueue[run.Issue.ID] = state.RetryEntry{
		IssueID:        run.Issue.ID,
		Identifier:     run.Issue.Identifier,
		Attempt:        nextAttempt,
		DueAt:          time.Now().Add(s.retryBackoff(nextAttempt)),
		Error:          sanitizeOutput(err.Error()),
		IssueUpdatedAt: run.Issue.UpdatedAt,
	}
	return true
}

func (s *Service) retryBackoff(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}

	backoff := s.cfg.State.RetryBase.Duration
	for i := 1; i < attempt; i++ {
		backoff *= 2
		if backoff >= s.cfg.State.MaxRetryBackoff.Duration {
			return s.cfg.State.MaxRetryBackoff.Duration
		}
	}
	return backoff
}

func (s *Service) approvalState() domain.ApprovalState {
	switch s.agent.ApprovalPolicy {
	case "auto":
		return domain.ApprovalStateApproved
	default:
		return domain.ApprovalStateAwaiting
	}
}

func issueRecordStale(issueUpdatedAt time.Time, recordUpdatedAt time.Time) bool {
	return !issueUpdatedAt.IsZero() && !recordUpdatedAt.IsZero() && issueUpdatedAt.After(recordUpdatedAt)
}

func (s *Service) statusSummary() string {
	snapshot := s.Snapshot()
	return fmt.Sprintf("claimed=%d retry=%d", snapshot.ClaimedCount, snapshot.RetryCount)
}
