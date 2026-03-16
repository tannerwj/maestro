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

	s.recordEvent("warn", "approval requested for %s (%s)", request.RunID, request.ToolName)
	_ = s.saveStateBestEffort()
}

func (s *Service) ResolveApproval(requestID string, decision string) error {
	s.mu.RLock()
	request, ok := s.approvals[requestID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("approval request %q not found", requestID)
	}

	if err := s.harness.Approve(context.Background(), harness.ApprovalDecision{
		RequestID: requestID,
		Decision:  decision,
	}); err != nil {
		s.recordEvent("error", "approval %s for %s failed: %v", decision, request.RunID, err)
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
	s.approvalOrder = removeApprovalID(s.approvalOrder, requestID)
	if s.activeRun != nil && s.activeRun.ID == request.RunID {
		if decision == "approve" {
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

	s.recordEvent("info", "approval %s for %s (%s)", decision, request.RunID, request.ToolName)
	_ = s.saveStateBestEffort()
	return nil
}

func removeApprovalID(order []string, requestID string) []string {
	out := order[:0]
	for _, candidate := range order {
		if candidate != requestID {
			out = append(out, candidate)
		}
	}
	return out
}

func (s *Service) appendApprovalHistory(entry ApprovalHistoryEntry) {
	s.approvalHistory = append(s.approvalHistory, entry)
	if len(s.approvalHistory) > 10 {
		s.approvalHistory = s.approvalHistory[len(s.approvalHistory)-10:]
	}
}
