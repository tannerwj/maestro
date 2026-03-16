package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/harness"
	"github.com/tjohnson/maestro/internal/state"
	trackerbase "github.com/tjohnson/maestro/internal/tracker"
)

func (s *Service) dispatch(ctx context.Context, issue domain.Issue) error {
	if s.limiter != nil && !s.limiter.TryAcquire() {
		return nil
	}
	attempt := s.takeAttempt(issue)
	run := &domain.AgentRun{
		ID:             fmt.Sprintf("%d", time.Now().UnixNano()),
		AgentName:      s.agent.InstanceName,
		AgentType:      s.agent.Name,
		Issue:          issue,
		SourceName:     s.source.Name,
		HarnessKind:    s.harness.Kind(),
		Status:         domain.RunStatusPending,
		Attempt:        attempt,
		ApprovalPolicy: s.agent.ApprovalPolicy,
		ApprovalState:  s.approvalState(),
		StartedAt:      time.Now(),
	}
	if run.AgentName == "" {
		run.AgentName = s.agent.Name
	}

	s.mu.Lock()
	s.claimed[issue.ID] = struct{}{}
	s.activeRun = run
	s.mu.Unlock()
	_ = s.saveStateBestEffort()

	s.recordEvent("info", "dispatching %s to %s", issue.Identifier, run.AgentName)
	s.applyTrackerLifecycle(ctx, issue.ID,
		[]string{trackerbase.LifecycleLabelActive},
		[]string{
			trackerbase.LifecycleLabelRetry,
			trackerbase.LifecycleLabelDone,
			trackerbase.LifecycleLabelFailed,
		},
		fmt.Sprintf("Maestro started run %s (attempt %d) with agent %s.", run.ID, run.Attempt, run.AgentName),
	)
	s.refreshActiveRunIssue(ctx, run.ID)

	s.runWG.Add(1)
	go s.executeRun(ctx, run)
	return nil
}

func (s *Service) executeRun(ctx context.Context, run *domain.AgentRun) {
	defer s.runWG.Done()
	if err := s.prepareAndStart(ctx, run); err != nil {
		s.failRun(run.ID, sanitizeError(err))
	}
}

func (s *Service) prepareAndStart(ctx context.Context, run *domain.AgentRun) error {
	s.updateRun(run.ID, func(r *domain.AgentRun) {
		r.Status = domain.RunStatusPreparing
		r.LastActivityAt = time.Now()
	})

	prepared, err := s.workspace.Prepare(ctx, run.Issue, run.AgentName)
	if err != nil {
		return fmt.Errorf("prepare workspace: %w", err)
	}

	s.updateRun(run.ID, func(r *domain.AgentRun) {
		r.WorkspacePath = prepared.Path
	})
	s.runHookBestEffort(ctx, s.cfg.Hooks.AfterCreate, prepared.Path, run, "after_create")
	if err := s.runHook(ctx, s.cfg.Hooks.BeforeRun, prepared.Path, run, "before_run"); err != nil {
		return err
	}

	renderedPrompt, err := s.renderPrompt(run.Issue, run.AgentName, run.Attempt)
	if err != nil {
		return fmt.Errorf("render prompt: %w", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	stdoutWriter := &activityWriter{target: &stdout, onWrite: func() { s.markRunActivity(run.ID) }}
	stderrWriter := &activityWriter{target: &stderr, onWrite: func() { s.markRunActivity(run.ID) }}
	active, err := s.harness.Start(ctx, harness.RunConfig{
		RunID:          run.ID,
		Prompt:         renderedPrompt,
		Workdir:        prepared.Path,
		ApprovalPolicy: run.ApprovalPolicy,
		Env:            s.agent.Env,
		Stdout:         stdoutWriter,
		Stderr:         stderrWriter,
	})
	if err != nil {
		return fmt.Errorf("start harness: %w", sanitizeError(err))
	}

	s.updateRun(run.ID, func(r *domain.AgentRun) {
		r.Status = domain.RunStatusActive
		r.LastActivityAt = time.Now()
	})
	s.recordEvent("info", "agent %s started for %s", run.AgentName, run.Issue.Identifier)

	if err := active.Wait(); err != nil {
		s.runHookBestEffort(context.Background(), s.cfg.Hooks.AfterRun, prepared.Path, run, "after_run")
		return fmt.Errorf(
			"agent exited with error: %w stderr=%s stdout=%s",
			sanitizeError(err),
			sanitizeOutput(stderr.String()),
			sanitizeOutput(stdout.String()),
		)
	}

	s.runHookBestEffort(context.Background(), s.cfg.Hooks.AfterRun, prepared.Path, run, "after_run")
	s.completeRun(run.ID)
	return nil
}

func (s *Service) completeRun(runID string) {
	var issueID string
	var issueIdentifier string
	status := domain.RunStatusDone
	comment := "Maestro completed the run successfully."
	var retry state.RetryEntry
	scheduledRetry := false
	s.mu.Lock()
	if s.activeRun != nil && s.activeRun.ID == runID {
		if stop, ok := s.pendingStops[runID]; ok {
			if stop.Retry {
				scheduledRetry = s.scheduleRetry(s.activeRun, fmt.Errorf("%s", stop.Reason))
				if scheduledRetry {
					retry = s.retryQueue[s.activeRun.Issue.ID]
				} else {
					status = stop.Status
					comment = stop.Reason
				}
			} else {
				status = stop.Status
				comment = stop.Reason
			}
			delete(s.pendingStops, runID)
		}
		s.activeRun.Status = status
		s.activeRun.CompletedAt = time.Now()
		issueID = s.activeRun.Issue.ID
		issueIdentifier = s.activeRun.Issue.Identifier
		if scheduledRetry {
			delete(s.finished, issueID)
		} else {
			s.finished[issueID] = state.TerminalIssue{
				IssueID:        s.activeRun.Issue.ID,
				Identifier:     s.activeRun.Issue.Identifier,
				Status:         status,
				Attempt:        s.activeRun.Attempt,
				IssueUpdatedAt: s.activeRun.Issue.UpdatedAt,
				FinishedAt:     s.activeRun.CompletedAt,
				Error:          comment,
			}
		}
		if !scheduledRetry {
			delete(s.retryQueue, issueID)
		}
		s.activeRun = nil
	}
	s.mu.Unlock()

	if scheduledRetry {
		s.recordEvent("warn", "run %s stopped: %s; retry %d scheduled for %s", runID, comment, retry.Attempt, retry.DueAt.Format(time.RFC3339))
		if issueID != "" {
			s.applyTrackerLifecycle(context.Background(), issueID, []string{trackerbase.LifecycleLabelRetry}, []string{
				trackerbase.LifecycleLabelActive,
				trackerbase.LifecycleLabelDone,
				trackerbase.LifecycleLabelFailed,
			}, fmt.Sprintf("Maestro run %s stopped and retry %d is scheduled: %s", runID, retry.Attempt, comment))
			s.refreshStoredIssueTimestamp(context.Background(), issueID)
		}
		s.releaseClaim(issueID)
		_ = s.saveStateBestEffort()
		return
	}

	s.recordEvent("info", "run %s completed", runID)
	if issueID != "" {
		add := []string{trackerbase.LifecycleLabelDone}
		if status == domain.RunStatusFailed {
			add = []string{trackerbase.LifecycleLabelFailed}
		}
		s.applyTrackerLifecycle(context.Background(), issueID, add, []string{
			trackerbase.LifecycleLabelActive,
			trackerbase.LifecycleLabelRetry,
		}, comment)
		s.refreshStoredIssueTimestamp(context.Background(), issueID)
		s.recordEvent("info", "tracker state updated for %s", issueIdentifier)
	}
	s.releaseClaim(issueID)
	if s.limiter != nil {
		s.limiter.Release()
	}
	_ = s.saveStateBestEffort()
}

func (s *Service) failRun(runID string, err error) {
	var issueID string
	var issueIdentifier string
	var retry state.RetryEntry
	scheduledRetry := false
	var stop pendingStop
	plannedStop := false
	s.mu.Lock()
	if s.activeRun != nil && s.activeRun.ID == runID {
		s.activeRun.Status = domain.RunStatusFailed
		s.activeRun.CompletedAt = time.Now()
		s.activeRun.Error = err.Error()
		issueID = s.activeRun.Issue.ID
		issueIdentifier = s.activeRun.Issue.Identifier
		stop, plannedStop = s.pendingStops[runID]
		delete(s.pendingStops, runID)
		if plannedStop {
			if stop.Retry {
				scheduledRetry = s.scheduleRetry(s.activeRun, fmt.Errorf("%s: %w", stop.Reason, err))
				if scheduledRetry {
					retry = s.retryQueue[issueID]
				}
			} else {
				s.finished[issueID] = state.TerminalIssue{
					IssueID:        s.activeRun.Issue.ID,
					Identifier:     s.activeRun.Issue.Identifier,
					Status:         stop.Status,
					Attempt:        s.activeRun.Attempt,
					IssueUpdatedAt: s.activeRun.Issue.UpdatedAt,
					FinishedAt:     s.activeRun.CompletedAt,
					Error:          stop.Reason,
				}
			}
		} else {
			scheduledRetry = s.scheduleRetry(s.activeRun, err)
			if scheduledRetry {
				retry = s.retryQueue[issueID]
			} else {
				s.finished[issueID] = state.TerminalIssue{
					IssueID:        s.activeRun.Issue.ID,
					Identifier:     s.activeRun.Issue.Identifier,
					Status:         domain.RunStatusFailed,
					Attempt:        s.activeRun.Attempt,
					IssueUpdatedAt: s.activeRun.Issue.UpdatedAt,
					FinishedAt:     s.activeRun.CompletedAt,
					Error:          err.Error(),
				}
			}
		}
		s.activeRun = nil
	}
	s.mu.Unlock()

	if plannedStop {
		if stop.Retry {
			s.recordEvent("warn", "run %s stopped: %s; retry %d scheduled for %s", runID, stop.Reason, retry.Attempt, retry.DueAt.Format(time.RFC3339))
			s.applyTrackerLifecycle(context.Background(), issueID, []string{trackerbase.LifecycleLabelRetry}, []string{
				trackerbase.LifecycleLabelActive,
				trackerbase.LifecycleLabelDone,
				trackerbase.LifecycleLabelFailed,
			}, fmt.Sprintf("Maestro run %s stopped and retry %d is scheduled: %s", runID, retry.Attempt, stop.Reason))
			s.refreshStoredIssueTimestamp(context.Background(), issueID)
			s.releaseClaim(issueID)
			if s.limiter != nil {
				s.limiter.Release()
			}
			_ = s.saveStateBestEffort()
			return
		}
		s.recordEvent("warn", "run %s stopped: %s", runID, stop.Reason)
		add := []string{trackerbase.LifecycleLabelDone}
		if stop.Status == domain.RunStatusFailed {
			add = []string{trackerbase.LifecycleLabelFailed}
		}
		s.applyTrackerLifecycle(context.Background(), issueID, add, []string{
			trackerbase.LifecycleLabelActive,
			trackerbase.LifecycleLabelRetry,
		}, stop.Reason)
		s.refreshStoredIssueTimestamp(context.Background(), issueID)
		s.releaseClaim(issueID)
		if s.limiter != nil {
			s.limiter.Release()
		}
		_ = s.saveStateBestEffort()
		return
	}
	if scheduledRetry {
		s.recordEvent("warn", "run %s failed: %v; retry %d scheduled for %s", runID, err, retry.Attempt, retry.DueAt.Format(time.RFC3339))
		s.applyTrackerLifecycle(context.Background(), issueID, []string{trackerbase.LifecycleLabelRetry}, []string{
			trackerbase.LifecycleLabelActive,
			trackerbase.LifecycleLabelDone,
			trackerbase.LifecycleLabelFailed,
		}, fmt.Sprintf("Maestro run %s failed and retry %d is scheduled: %v", runID, retry.Attempt, err))
		s.refreshStoredIssueTimestamp(context.Background(), issueID)
		s.releaseClaim(issueID)
		if s.limiter != nil {
			s.limiter.Release()
		}
		_ = s.saveStateBestEffort()
		return
	}
	s.recordEvent("error", "run %s failed: %v", runID, err)
	if issueID != "" {
		s.applyTrackerLifecycle(context.Background(), issueID, []string{trackerbase.LifecycleLabelFailed}, []string{
			trackerbase.LifecycleLabelActive,
			trackerbase.LifecycleLabelRetry,
			trackerbase.LifecycleLabelDone,
		}, fmt.Sprintf("Maestro run %s failed for %s: %v", runID, issueIdentifier, err))
		s.refreshStoredIssueTimestamp(context.Background(), issueID)
	}
	s.releaseClaim(issueID)
	if s.limiter != nil {
		s.limiter.Release()
	}
	_ = s.saveStateBestEffort()
}

func (s *Service) isClaimed(issueID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.claimed[issueID]
	return ok
}

func (s *Service) updateRun(runID string, mutate func(*domain.AgentRun)) {
	s.mu.Lock()
	if s.activeRun == nil || s.activeRun.ID != runID {
		s.mu.Unlock()
		return
	}
	mutate(s.activeRun)
	s.mu.Unlock()
	_ = s.saveStateBestEffort()
}

func (s *Service) releaseClaim(issueID string) {
	if issueID == "" {
		return
	}

	s.mu.Lock()
	delete(s.claimed, issueID)
	s.mu.Unlock()
}
