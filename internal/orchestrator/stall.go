package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/tjohnson/maestro/internal/domain"
)

func (s *Service) reconcileStalledRun(ctx context.Context) {
	s.mu.RLock()
	runs := s.activeRunSnapshotsLocked(time.Now())
	s.mu.RUnlock()

	for _, run := range runs {
		if run.Status != domain.RunStatusActive && run.Status != domain.RunStatusAwaiting && run.Status != domain.RunStatusPreparing {
			continue
		}
		if run.LastActivityAt.IsZero() {
			continue
		}
		if time.Since(run.LastActivityAt) < s.agent.StallTimeout.Duration {
			continue
		}
		s.stopRunAsFailed(ctx, run.ID, fmt.Sprintf("run stalled after %s without observable activity", s.agent.StallTimeout.Duration))
	}
}

func (s *Service) stopRunAsFailed(ctx context.Context, runID string, reason string) {
	s.mu.Lock()
	if s.activeRunByIDLocked(runID) == nil {
		s.mu.Unlock()
		return
	}
	if _, exists := s.pendingStops[runID]; exists {
		s.mu.Unlock()
		return
	}
	s.pendingStops[runID] = pendingStop{Status: domain.RunStatusFailed, Reason: reason, Retry: true}
	s.mu.Unlock()

	s.recordRunEventByFields("warn", s.source.Name, runID, "", "stopping run %s: %s", runID, reason)
	if err := s.harness.Stop(ctx, runID); err != nil {
		s.recordRunEventByFields("error", s.source.Name, runID, "", "stop run %s failed: %v", runID, err)
	}
}
