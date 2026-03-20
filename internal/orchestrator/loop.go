package orchestrator

import (
	"context"
	"sort"
	"time"

	trackerbase "github.com/tjohnson/maestro/internal/tracker"
)

var pollRequestTimeout = 30 * time.Second

func (s *Service) Run(ctx context.Context) error {
	s.startApprovalWatcher(ctx)
	s.startMessageWatcher(ctx)
	if err := s.tick(ctx); err != nil {
		s.recordEvent("error", "initial poll failed: %v", err)
	}

	ticker := time.NewTicker(s.source.PollInterval.Duration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if err := s.shutdown(); err != nil {
				s.recordEvent("error", "shutdown failed: %v", err)
				return err
			}
			s.runWG.Wait()
			_ = s.saveStateBestEffort()
			s.recordEvent("info", "service shutting down")
			return nil
		case <-ticker.C:
			if err := s.tick(ctx); err != nil {
				s.recordEvent("error", "poll failed: %v", err)
			}
		}
	}
}

func (s *Service) tick(ctx context.Context) error {
	s.reconcileStalledRun(ctx)

	pollCtx, cancel := context.WithTimeout(ctx, pollRequestTimeout)
	defer cancel()

	issues, err := s.tracker.Poll(pollCtx)
	if err != nil {
		return err
	}

	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].CreatedAt.Equal(issues[j].CreatedAt) {
			return issues[i].Identifier < issues[j].Identifier
		}
		if issues[i].CreatedAt.IsZero() {
			return false
		}
		if issues[j].CreatedAt.IsZero() {
			return true
		}
		return issues[i].CreatedAt.Before(issues[j].CreatedAt)
	})

	s.mu.Lock()
	s.lastPollAt = time.Now()
	s.lastPollCount = len(issues)
	hasActive := s.activeRun != nil
	s.mu.Unlock()

	s.recordSourceEvent("info", s.source.Name, "polled %d candidate issues from %s", len(issues), s.source.Name)

	if hasActive {
		s.reconcileActiveRun(ctx, issues)
		return nil
	}

	if err := s.dispatchDueRetry(ctx); err != nil {
		return err
	}

	for _, issue := range issues {
		if s.isClaimed(issue.ID) || s.shouldSkipIssue(issue) {
			continue
		}
		return s.dispatch(ctx, issue)
	}

	return nil
}

func (s *Service) dispatchDueRetry(ctx context.Context) error {
	type retryCandidate struct {
		issueID string
		dueAt   time.Time
	}

	s.mu.RLock()
	candidates := make([]retryCandidate, 0, len(s.retryQueue))
	for issueID, retry := range s.retryQueue {
		if time.Now().Before(retry.DueAt) {
			continue
		}
		candidates = append(candidates, retryCandidate{
			issueID: issueID,
			dueAt:   retry.DueAt,
		})
	}
	s.mu.RUnlock()

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].dueAt.Equal(candidates[j].dueAt) {
			return candidates[i].issueID < candidates[j].issueID
		}
		return candidates[i].dueAt.Before(candidates[j].dueAt)
	})

	for _, candidate := range candidates {
		if s.isClaimed(candidate.issueID) {
			continue
		}
		issue, err := s.tracker.Get(ctx, candidate.issueID)
		if err != nil {
			s.recordSourceEvent("warn", s.source.Name, "retry lookup failed for %s: %v", candidate.issueID, err)
			continue
		}
		if trackerbase.IsTerminal(issue) {
			s.mu.Lock()
			delete(s.retryQueue, candidate.issueID)
			s.mu.Unlock()
			_ = s.saveStateBestEffort()
			s.recordSourceEvent("warn", s.source.Name, "discarded retry for terminal issue %s", issue.Identifier)
			continue
		}
		return s.dispatch(ctx, issue)
	}

	return nil
}

func (s *Service) shutdown() error {
	s.mu.RLock()
	activeRun := s.activeRun
	s.mu.RUnlock()

	if activeRun == nil {
		return nil
	}

	s.recordRunEvent(activeRun, "info", "stopping active run %s", activeRun.ID)
	return s.harness.Stop(context.Background(), activeRun.ID)
}
