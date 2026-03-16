package orchestrator

import (
	"context"
	"sort"
	"time"
)

func (s *Service) Run(ctx context.Context) error {
	s.startApprovalWatcher(ctx)
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

	issues, err := s.tracker.Poll(ctx)
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

	s.recordEvent("info", "polled %d candidate issues from %s", len(issues), s.source.Name)

	if hasActive {
		s.reconcileActiveRun(ctx, issues)
		return nil
	}

	for _, issue := range issues {
		if s.isClaimed(issue.ID) || s.shouldSkipIssue(issue) {
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

	s.recordEvent("info", "stopping active run %s", activeRun.ID)
	return s.harness.Stop(context.Background(), activeRun.ID)
}
