package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/harness"
)

func (s *Service) startApprovalWatcher(ctx context.Context) {
	approvals := s.harness.Approvals()
	if approvals == nil {
		return
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case request, ok := <-approvals:
				if !ok {
					return
				}
				s.recordApprovalRequest(request)
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(approvalTimeoutPollInterval(s.agent.ApprovalTimeout.Duration))
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				expired := s.expireTimedOutApprovals(time.Now())
				if len(expired) == 0 {
					continue
				}
				for _, approval := range expired {
					s.recordRunEventByFields("warn", s.source.Name, approval.RunID, approval.IssueIdentifier, "approval timed out for %s (%s)", approval.RunID, approval.ToolName)
					s.stopRunForTimedOutApproval(approval)
				}
				_ = s.saveStateBestEffort()
			}
		}
	}()
}

func (s *Service) recordApprovalRequest(request harness.ApprovalRequest) {
	view := ApprovalView{
		RequestID:      request.RequestID,
		RunID:          request.RunID,
		ToolName:       request.ToolName,
		ToolInput:      request.ToolInput,
		ApprovalPolicy: request.ApprovalPolicy,
		RequestedAt:    request.RequestedAt,
		Resolvable:     true,
	}

	s.mu.Lock()
	if s.activeRun != nil && s.activeRun.ID == request.RunID {
		view.IssueID = s.activeRun.Issue.ID
		view.IssueIdentifier = s.activeRun.Issue.Identifier
		view.AgentName = s.activeRun.AgentName
		s.activeRun.LastActivityAt = time.Now()
	}
	s.approvals[request.RequestID] = view
	s.approvalOrder = append(s.approvalOrder, request.RequestID)
	if s.activeRun != nil && s.activeRun.ID == request.RunID {
		s.activeRun.Status = domain.RunStatusAwaiting
		s.activeRun.ApprovalState = domain.ApprovalStateAwaiting
	}
	s.mu.Unlock()

	s.recordRunEventByFields("warn", s.source.Name, request.RunID, view.IssueIdentifier, "approval requested for %s (%s)", request.RunID, request.ToolName)
	_ = s.saveStateBestEffort()
}

func (s *Service) ResolveApproval(requestID string, decision string) error {
	request, err := s.claimApprovalResolution(requestID)
	if err != nil {
		return err
	}

	if err := s.harness.Approve(context.Background(), harness.ApprovalDecision{
		RequestID: requestID,
		Decision:  decision,
	}); err != nil {
		s.restoreApprovalResolution(requestID)
		s.recordRunEventByFields("error", s.source.Name, request.RunID, request.IssueIdentifier, "approval %s for %s failed: %v", decision, request.RunID, err)
		return err
	}

	now := time.Now()
	history := ApprovalHistoryEntry{
		RequestID:       request.RequestID,
		RunID:           request.RunID,
		IssueID:         request.IssueID,
		IssueIdentifier: request.IssueIdentifier,
		AgentName:       request.AgentName,
		ToolName:        request.ToolName,
		ApprovalPolicy:  request.ApprovalPolicy,
		Decision:        decision,
		RequestedAt:     request.RequestedAt,
		DecidedAt:       now,
		Outcome:         "resolved",
	}

	s.mu.Lock()
	delete(s.approvals, requestID)
	s.approvalOrder = removeFromOrder(s.approvalOrder, requestID)
	if s.activeRun != nil && s.activeRun.ID == request.RunID {
		if decision == harness.DecisionApprove {
			s.activeRun.Status = domain.RunStatusActive
			s.activeRun.ApprovalState = domain.ApprovalStateApproved
		} else {
			s.activeRun.Status = domain.RunStatusAwaiting
			s.activeRun.ApprovalState = domain.ApprovalStateRejected
		}
		s.activeRun.LastActivityAt = now
	}
	s.appendApprovalHistory(history)
	s.mu.Unlock()

	s.recordRunEventByFields("info", s.source.Name, request.RunID, request.IssueIdentifier, "approval %s for %s (%s)", decision, request.RunID, request.ToolName)
	_ = s.saveStateBestEffort()
	return nil
}

func (s *Service) claimApprovalResolution(requestID string) (ApprovalView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	request, ok := s.approvals[requestID]
	if !ok {
		return ApprovalView{}, fmt.Errorf("approval request %q not found", requestID)
	}
	if !request.Resolvable {
		return ApprovalView{}, fmt.Errorf("approval request %q is already being resolved", requestID)
	}
	request.Resolvable = false
	s.approvals[requestID] = request
	return request, nil
}

func (s *Service) restoreApprovalResolution(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	request, ok := s.approvals[requestID]
	if !ok {
		return
	}
	request.Resolvable = true
	s.approvals[requestID] = request
}

func (s *Service) expireTimedOutApprovals(now time.Time) []ApprovalView {
	timeout := s.agent.ApprovalTimeout.Duration
	if timeout <= 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.approvalOrder) == 0 {
		return nil
	}

	expired := make([]ApprovalView, 0, len(s.approvalOrder))
	kept := make([]string, 0, len(s.approvalOrder))
	for _, requestID := range s.approvalOrder {
		approval, ok := s.approvals[requestID]
		if !ok {
			continue
		}
		if !approval.Resolvable {
			kept = append(kept, requestID)
			continue
		}
		if approval.RequestedAt.IsZero() || now.Before(approval.RequestedAt.Add(timeout)) {
			kept = append(kept, requestID)
			continue
		}

		expired = append(expired, approval)
		s.appendApprovalHistory(ApprovalHistoryEntry{
			RequestID:       approval.RequestID,
			RunID:           approval.RunID,
			IssueID:         approval.IssueID,
			IssueIdentifier: approval.IssueIdentifier,
			AgentName:       approval.AgentName,
			ToolName:        approval.ToolName,
			ApprovalPolicy:  approval.ApprovalPolicy,
			Decision:        harness.DecisionReject,
			Reason:          "approval timeout",
			RequestedAt:     approval.RequestedAt,
			DecidedAt:       now,
			Outcome:         "timed_out",
		})
		if s.activeRun != nil && s.activeRun.ID == approval.RunID {
			s.activeRun.ApprovalState = domain.ApprovalStateRejected
			s.activeRun.LastActivityAt = now
		}
		delete(s.approvals, requestID)
	}
	s.approvalOrder = kept
	return expired
}

func approvalTimeoutPollInterval(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return time.Second
	}

	interval := timeout / 4
	if interval < 50*time.Millisecond {
		return 50 * time.Millisecond
	}
	if interval > time.Second {
		return time.Second
	}
	return interval
}

func (s *Service) stopRunForTimedOutApproval(approval ApprovalView) {
	s.mu.RLock()
	active := s.activeRun != nil && s.activeRun.ID == approval.RunID
	s.mu.RUnlock()
	if !active {
		return
	}

	if err := s.StopRun(approval.RunID, approvalTimeoutFailureReason(approval)); err != nil {
		s.recordRunEventByFields("error", s.source.Name, approval.RunID, approval.IssueIdentifier, "stop run %s after approval timeout failed: %v", approval.RunID, err)
	}
}

func approvalTimeoutFailureReason(approval ApprovalView) string {
	if approval.ToolName == "" {
		return "approval timeout"
	}
	return fmt.Sprintf("approval timeout while waiting on %s", approval.ToolName)
}


func (s *Service) appendApprovalHistory(entry ApprovalHistoryEntry) {
	s.approvalHistory = append(s.approvalHistory, entry)
	if len(s.approvalHistory) > maxApprovalHistory {
		s.approvalHistory = s.approvalHistory[len(s.approvalHistory)-maxApprovalHistory:]
	}
}
