package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/harness"
)

func (s *Service) startMessageWatcher(ctx context.Context) {
	messages := s.harness.Messages()
	if messages == nil {
		return
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case request, ok := <-messages:
				if !ok {
					return
				}
				s.recordMessageRequest(request)
			}
		}
	}()
}

func (s *Service) recordMessageRequest(request harness.MessageRequest) {
	kind := strings.TrimSpace(request.Kind)
	if kind == "" {
		kind = "agent_message"
	}
	s.recordMessageView(MessageView{
		RequestID:   request.RequestID,
		RunID:       request.RunID,
		Kind:        kind,
		Summary:     request.Summary,
		Body:        request.Body,
		RequestedAt: request.RequestedAt,
		Resolvable:  true,
	})
}

func (s *Service) recordMessageView(input MessageView) {
	view := input

	s.mu.Lock()
	view.SourceName = s.source.Name
	if s.activeRun != nil && s.activeRun.ID == view.RunID {
		view.IssueID = s.activeRun.Issue.ID
		view.IssueIdentifier = s.activeRun.Issue.Identifier
		view.AgentName = s.activeRun.AgentName
		s.activeRun.Status = domain.RunStatusAwaiting
		s.activeRun.LastActivityAt = time.Now()
	}
	s.messages[view.RequestID] = view
	s.messageOrder = append(s.messageOrder, view.RequestID)
	s.mu.Unlock()

	s.recordRunEventByFields("warn", s.source.Name, view.RunID, view.IssueIdentifier, "%s requested for %s", messageKindLabel(view.Kind), view.RunID)
	_ = s.saveStateBestEffort()
}

func (s *Service) ResolveMessage(requestID string, reply string, resolvedVia string) error {
	s.mu.RLock()
	request, ok := s.messages[requestID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("message request %q not found", requestID)
	}

	now := time.Now()
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return fmt.Errorf("message reply cannot be empty")
	}
	resolvedVia = strings.TrimSpace(resolvedVia)
	if resolvedVia == "" {
		resolvedVia = "operator"
	}

	s.mu.RLock()
	_, hasWaiter := s.messageWaiters[requestID]
	s.mu.RUnlock()

	if !hasWaiter {
		if err := s.harness.Reply(context.Background(), harness.MessageReply{
			RequestID: requestID,
			Kind:      request.Kind,
			Reply:     reply,
			RepliedAt: now,
		}); err != nil {
			s.recordRunEventByFields("error", s.source.Name, request.RunID, request.IssueIdentifier, "%s reply for %s failed: %v", messageKindLabel(request.Kind), request.RunID, err)
			return err
		}
	}

	history := MessageHistoryEntry{
		RequestID:       request.RequestID,
		RunID:           request.RunID,
		IssueID:         request.IssueID,
		IssueIdentifier: request.IssueIdentifier,
		SourceName:      request.SourceName,
		AgentName:       request.AgentName,
		Kind:            request.Kind,
		Summary:         request.Summary,
		Body:            request.Body,
		Reply:           reply,
		ResolvedVia:     resolvedVia,
		RequestedAt:     request.RequestedAt,
		RepliedAt:       now,
		Outcome:         "resolved",
	}

	s.mu.Lock()
	delete(s.messages, requestID)
	s.messageOrder = removeFromOrder(s.messageOrder, requestID)
	waiter, hasWaiter := s.messageWaiters[requestID]
	if hasWaiter {
		delete(s.messageWaiters, requestID)
	}
	if s.activeRun != nil && s.activeRun.ID == request.RunID {
		s.activeRun.Status = domain.RunStatusActive
		s.activeRun.LastActivityAt = now
	}
	s.appendMessageHistory(history)
	s.mu.Unlock()

	if hasWaiter {
		waiter <- reply
		close(waiter)
	}

	s.recordRunEventByFields("info", s.source.Name, request.RunID, request.IssueIdentifier, "%s reply received for %s via %s", messageKindLabel(request.Kind), request.RunID, resolvedVia)
	_ = s.saveStateBestEffort()
	return nil
}

func (s *Service) createControlMessage(run *domain.AgentRun, kind string, summary string, body string) (MessageView, <-chan string) {
	now := time.Now()
	requestID := "control-before-work:" + run.ID
	if strings.TrimSpace(kind) == "" {
		kind = "before_work_review"
	}
	view := MessageView{
		RequestID:   requestID,
		RunID:       run.ID,
		Kind:        kind,
		Summary:     summary,
		Body:        body,
		RequestedAt: now,
		Resolvable:  true,
	}
	replyCh := make(chan string, 1)

	s.mu.Lock()
	s.messageWaiters[requestID] = replyCh
	s.mu.Unlock()
	s.recordMessageView(view)
	return view, replyCh
}

func (s *Service) cancelControlMessage(requestID string, outcome string) {
	s.mu.Lock()
	request, ok := s.messages[requestID]
	if !ok {
		delete(s.messageWaiters, requestID)
		s.mu.Unlock()
		return
	}
	delete(s.messages, requestID)
	s.messageOrder = removeFromOrder(s.messageOrder, requestID)
	delete(s.messageWaiters, requestID)
	s.appendMessageHistory(MessageHistoryEntry{
		RequestID:       request.RequestID,
		RunID:           request.RunID,
		IssueID:         request.IssueID,
		IssueIdentifier: request.IssueIdentifier,
		SourceName:      request.SourceName,
		AgentName:       request.AgentName,
		Kind:            request.Kind,
		Summary:         request.Summary,
		Body:            request.Body,
		Reply:           "",
		ResolvedVia:     "maestro",
		RequestedAt:     request.RequestedAt,
		RepliedAt:       time.Now(),
		Outcome:         outcome,
	})
	s.mu.Unlock()
	_ = s.saveStateBestEffort()
}


func (s *Service) appendMessageHistory(entry MessageHistoryEntry) {
	s.messageHistory = append(s.messageHistory, entry)
	if len(s.messageHistory) > maxMessageHistory {
		s.messageHistory = s.messageHistory[len(s.messageHistory)-maxMessageHistory:]
	}
}

func messageKindLabel(kind string) string {
	switch strings.TrimSpace(kind) {
	case "before_work", "before_work_review":
		return "before_work gate"
	case "before_work_reply":
		return "before_work question"
	case "", "agent_message":
		return "agent message"
	default:
		return kind
	}
}
