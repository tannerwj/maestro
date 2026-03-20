package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
	trackerbase "github.com/tjohnson/maestro/internal/tracker"
)

func (s *Service) reconcileActiveRun(ctx context.Context, polled []domain.Issue) {
	s.mu.RLock()
	if s.activeRun == nil {
		s.mu.RUnlock()
		return
	}
	run := *s.activeRun
	s.mu.RUnlock()

	current := findIssue(polled, run.Issue.ID)
	if current == nil {
		refreshed, err := s.tracker.Get(ctx, run.Issue.ID)
		if err != nil {
			s.recordRunEvent(&run, "warn", "reconcile get failed for %s: %v", run.Issue.Identifier, err)
			return
		}
		current = &refreshed
	}

	s.updateRun(run.ID, func(r *domain.AgentRun) {
		r.Issue = *current
	})

	prefix := s.labelPrefix()
	doneLabel := trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixDone)
	failedLabel := trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixFailed)

	switch {
	case trackerbase.LifecycleLabelStateWithPrefix(current.Labels, prefix) == doneLabel:
		s.stopActiveRunFromTracker(ctx, run.ID, domain.RunStatusDone, fmt.Sprintf("issue %s marked %s in tracker", current.Identifier, doneLabel))
	case trackerbase.LifecycleLabelStateWithPrefix(current.Labels, prefix) == failedLabel:
		s.stopActiveRunFromTracker(ctx, run.ID, domain.RunStatusFailed, fmt.Sprintf("issue %s marked %s in tracker", current.Identifier, failedLabel))
	case trackerbase.IsTerminal(*current):
		s.stopActiveRunFromTracker(ctx, run.ID, domain.RunStatusDone, fmt.Sprintf("issue %s became terminal in tracker", current.Identifier))
	case !trackerbase.MatchesFilterWithPrefix(*current, s.source.Filter, prefix):
		s.stopActiveRunFromTracker(ctx, run.ID, domain.RunStatusFailed, fmt.Sprintf("issue %s no longer matches source filter", current.Identifier))
	}
}

func (s *Service) stopActiveRunFromTracker(ctx context.Context, runID string, status domain.RunStatus, reason string) {
	s.mu.Lock()
	if s.activeRun == nil || s.activeRun.ID != runID {
		s.mu.Unlock()
		return
	}
	if _, exists := s.pendingStops[runID]; exists {
		s.mu.Unlock()
		return
	}
	s.pendingStops[runID] = pendingStop{Status: status, Reason: reason}
	s.mu.Unlock()

	s.recordRunEventByFields("warn", s.source.Name, runID, "", "stopping run %s: %s", runID, reason)
	if err := s.harness.Stop(ctx, runID); err != nil {
		s.recordRunEventByFields("error", s.source.Name, runID, "", "stop run %s failed: %v", runID, err)
	}
}

func (s *Service) applyTrackerLifecycle(ctx context.Context, issueID string, add []string, remove []string, comment string) {
	for _, label := range remove {
		if err := s.tracker.RemoveLifecycleLabel(ctx, issueID, label); err != nil {
			s.recordEvent("warn", "remove lifecycle label %s on %s failed: %v", label, issueID, err)
		}
	}
	for _, label := range add {
		if err := s.tracker.AddLifecycleLabel(ctx, issueID, label); err != nil {
			s.recordEvent("warn", "add lifecycle label %s on %s failed: %v", label, issueID, err)
		}
	}
	if comment != "" {
		if err := s.tracker.PostOperationalComment(ctx, issueID, comment); err != nil {
			s.recordEvent("warn", "post operational comment on %s failed: %v", issueID, err)
		}
	}
}

// applyTerminalLifecycle applies a configurable lifecycle transition on completion or failure.
// When transition is nil, it falls back to the default behavior: remove {prefix}:active and
// add the provided defaultAdd label (typically {prefix}:done or {prefix}:failed).
func (s *Service) applyTerminalLifecycle(ctx context.Context, issueID string, transition *config.LifecycleTransition, prefix string, defaultAdd string, comment string) {
	activeLabel := trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixActive)
	retryLabel := trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixRetry)

	if transition != nil {
		remove := []string{activeLabel}
		remove = append(remove, transition.RemoveLabels...)
		s.applyTrackerLifecycle(ctx, issueID, transition.AddLabels, remove, comment)
		if stateName := strings.TrimSpace(transition.State); stateName != "" {
			if err := s.tracker.UpdateIssueState(ctx, issueID, stateName); err != nil {
				s.recordEvent("warn", "update issue state for %s failed: %v", issueID, err)
			}
		}
		return
	}

	// Default behavior
	s.applyTrackerLifecycle(ctx, issueID, []string{defaultAdd}, []string{activeLabel, retryLabel}, comment)
}

func (s *Service) refreshStoredIssueTimestamp(ctx context.Context, issueID string) {
	refreshed, err := s.tracker.Get(ctx, issueID)
	if err != nil {
		s.recordEvent("warn", "refresh tracker timestamp for %s failed: %v", issueID, err)
		return
	}

	changed := false

	s.mu.Lock()
	if finished, ok := s.finished[issueID]; ok {
		finished.IssueUpdatedAt = refreshed.UpdatedAt
		s.finished[issueID] = finished
		changed = true
	}
	if retry, ok := s.retryQueue[issueID]; ok {
		retry.IssueUpdatedAt = refreshed.UpdatedAt
		s.retryQueue[issueID] = retry
		changed = true
	}
	s.mu.Unlock()

	if changed {
		_ = s.saveStateBestEffort()
	}
}

func (s *Service) refreshActiveRunIssue(ctx context.Context, runID string) {
	refreshed, err := s.refreshIssue(ctx, func(issue domain.Issue) bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.activeRun != nil && s.activeRun.ID == runID && s.activeRun.Issue.ID == issue.ID
	})
	if err != nil {
		s.recordRunEventByFields("warn", s.source.Name, runID, "", "refresh active run %s failed: %v", runID, err)
		return
	}

	s.updateRun(runID, func(r *domain.AgentRun) {
		r.Issue = refreshed
	})
}

func (s *Service) refreshIssue(ctx context.Context, accept func(domain.Issue) bool) (domain.Issue, error) {
	s.mu.RLock()
	if s.activeRun == nil {
		s.mu.RUnlock()
		return domain.Issue{}, fmt.Errorf("no active run")
	}
	issueID := s.activeRun.Issue.ID
	s.mu.RUnlock()

	refreshed, err := s.tracker.Get(ctx, issueID)
	if err != nil {
		return domain.Issue{}, err
	}
	if !accept(refreshed) {
		return domain.Issue{}, fmt.Errorf("active run changed while refreshing %s", issueID)
	}
	return refreshed, nil
}

func findIssue(issues []domain.Issue, issueID string) *domain.Issue {
	for i := range issues {
		if issues[i].ID == issueID {
			return &issues[i]
		}
	}
	return nil
}
