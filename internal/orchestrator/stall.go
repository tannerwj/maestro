package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/tjohnson/maestro/internal/domain"
)

func (s *Service) reconcileStalledRun(ctx context.Context) {
	s.mu.RLock()
	if s.activeRun == nil {
		s.mu.RUnlock()
		return
	}
	run := *s.activeRun
	s.mu.RUnlock()

	if run.Status != domain.RunStatusActive && run.Status != domain.RunStatusAwaiting {
		return
	}
	if run.LastActivityAt.IsZero() {
		return
	}
	if time.Since(run.LastActivityAt) < s.agent.StallTimeout.Duration {
		return
	}

	s.stopRunAsFailed(ctx, run.ID, fmt.Sprintf("run stalled after %s without observable activity", s.agent.StallTimeout.Duration))
}

func (s *Service) stopRunAsFailed(ctx context.Context, runID string, reason string) {
	s.mu.Lock()
	if s.activeRun == nil || s.activeRun.ID != runID {
		s.mu.Unlock()
		return
	}
	if _, exists := s.pendingStops[runID]; exists {
		s.mu.Unlock()
		return
	}
	s.pendingStops[runID] = pendingStop{Status: domain.RunStatusFailed, Reason: reason, Retry: true}
	s.mu.Unlock()

	s.recordEvent("warn", "stopping run %s: %s", runID, reason)
	if err := s.harness.Stop(ctx, runID); err != nil {
		s.recordEvent("error", "stop run %s failed: %v", runID, err)
	}
}
