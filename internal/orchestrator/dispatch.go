package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/harness"
	"github.com/tjohnson/maestro/internal/state"
	trackerbase "github.com/tjohnson/maestro/internal/tracker"
	"github.com/tjohnson/maestro/internal/workspace"
)

func (s *Service) dispatch(ctx context.Context, issue domain.Issue) error {
	if s.limiter != nil && !s.limiter.TryAcquire() {
		return nil
	}
	attempt := s.takeAttempt(issue)
	startedAt := time.Now()
	run := &domain.AgentRun{
		ID:             newRunID(startedAt),
		AgentName:      s.agent.InstanceName,
		AgentType:      s.agent.Name,
		Issue:          issue,
		SourceName:     s.source.Name,
		HarnessKind:    s.harness.Kind(),
		Status:         domain.RunStatusPending,
		Attempt:        attempt,
		ApprovalPolicy: s.agent.ApprovalPolicy,
		ApprovalState:  s.approvalState(),
		StartedAt:      startedAt,
	}
	if run.AgentName == "" {
		run.AgentName = s.agent.Name
	}

	s.mu.Lock()
	s.claimed[issue.ID] = struct{}{}
	s.activeRun = run
	s.mu.Unlock()
	_ = s.saveStateBestEffort()

	prefix := s.labelPrefix()
	dispatchTransition := config.ResolveDispatchTransition(s.cfg.Defaults.OnDispatch, s.source.OnDispatch)
	s.recordRunEvent(run, "info", "dispatching %s to %s", issue.Identifier, run.AgentName)
	s.applyTrackerLifecycle(ctx, issue.ID,
		[]string{trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixActive)},
		[]string{
			trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixRetry),
			trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixDone),
			trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixFailed),
		},
		fmt.Sprintf(
			"Maestro started workflow %s for %s with %s (attempt %d, run %s).",
			run.SourceName,
			run.Issue.Identifier,
			run.AgentName,
			run.Attempt+1,
			run.ID,
		),
	)
	if dispatchTransition != nil && strings.TrimSpace(dispatchTransition.State) != "" {
		if err := s.tracker.UpdateIssueState(ctx, issue.ID, dispatchTransition.State); err != nil {
			s.recordEvent("warn", "update issue state on dispatch for %s failed: %v", issue.ID, err)
		}
	}
	s.refreshActiveRunIssue(ctx, run.ID)

	s.runWG.Add(1)
	go s.executeRun(ctx, run)
	return nil
}

func newRunID(now time.Time) string {
	return fmt.Sprintf("run-%s-%06d", now.Format("20060102-150405"), now.Nanosecond()/1000)
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

	// Snapshot issue under lock to avoid data race with reconcileActiveRun
	// which may mutate s.activeRun.Issue concurrently on the tick goroutine.
	s.mu.RLock()
	issue := snapshotIssue(run.Issue)
	s.mu.RUnlock()

	prepared, err := s.prepareWorkspaceForIssue(ctx, issue, run.AgentName)
	if err != nil {
		return fmt.Errorf("prepare workspace: %w", err)
	}
	runtimeAgent, err := s.resolveRuntimeAgent(prepared.Path)
	if err != nil {
		return fmt.Errorf("resolve runtime agent: %w", err)
	}
	if err := s.workspace.PopulateHarnessConfig(prepared.Path, runtimeAgent.PackClaudeDir, runtimeAgent.PackCodexDir); err != nil {
		return fmt.Errorf("populate harness config: %w", err)
	}

	s.updateRun(run.ID, func(r *domain.AgentRun) {
		r.WorkspacePath = prepared.Path
	})
	s.runHookBestEffort(ctx, s.cfg.Hooks.AfterCreate, prepared.Path, run, "after_create")
	operatorInstruction, err := s.runBeforeWorkGate(ctx, run)
	if err != nil {
		return err
	}
	if err := s.runHook(ctx, s.cfg.Hooks.BeforeRun, prepared.Path, run, "before_run"); err != nil {
		return err
	}

	renderedPrompt, err := s.renderPrompt(runtimeAgent, issue, run.AgentName, run.Attempt, operatorInstruction)
	if err != nil {
		return fmt.Errorf("render prompt: %w", err)
	}

	// Resolve harness-specific configuration
	var model, reasoning, threadSandbox string
	var maxTurns int
	var turnSandboxPolicy map[string]any
	var extraArgs []string

	switch runtimeAgent.Harness {
	case "codex":
		resolved := config.ResolveCodexConfig(s.cfg.CodexDefaults, runtimeAgent.Codex)
		model = resolved.Model
		reasoning = resolved.Reasoning
		maxTurns = resolved.MaxTurns
		threadSandbox = resolved.ThreadSandbox
		turnSandboxPolicy = resolved.TurnSandboxPolicy
		extraArgs = resolved.ExtraArgs
	case "claude-code":
		resolved := config.ResolveClaudeConfig(s.cfg.ClaudeDefaults, runtimeAgent.Claude)
		model = resolved.Model
		reasoning = resolved.Reasoning
		maxTurns = resolved.MaxTurns
		extraArgs = resolved.ExtraArgs
	}

	// Build multi-turn continuation function
	var continuationFunc func(ctx context.Context, turnNumber int) (string, bool, error)
	if runtimeAgent.Harness == "codex" && maxTurns > 1 {
		issueID := issue.ID
		prefix := s.labelPrefix()
		activeLabel := trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixActive)
		sourceFilter := s.source.Filter
		continuationFunc = func(ctx context.Context, turnNumber int) (string, bool, error) {
			issue, err := s.tracker.Get(ctx, issueID)
			if err != nil {
				return "", false, err
			}
			if trackerbase.IsTerminal(issue) {
				return "", false, nil
			}
			if trackerbase.LifecycleLabelStateWithPrefix(issue.Labels, prefix) != activeLabel {
				return "", false, nil
			}
			if !trackerbase.MatchesFilterWithPrefix(issue, sourceFilter, prefix) {
				return "", false, nil
			}
			prompt := fmt.Sprintf(
				"Continuation turn %d of %d. Issue is still in active state %q.\nResume from current workspace state. Do not restate prior instructions.",
				turnNumber+1, maxTurns, issue.State,
			)
			return prompt, true, nil
		}
	}

	s.initRunOutput(run.ID)
	defer s.clearRunOutput(run.ID)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	stdoutWriter := &runOutputWriter{
		target:  &stdout,
		onWrite: func() { s.markRunActivity(run.ID) },
		append:  func(p []byte) { s.appendRunOutput(run.ID, "stdout", p) },
	}
	stderrWriter := &runOutputWriter{
		target:  &stderr,
		onWrite: func() { s.markRunActivity(run.ID) },
		append:  func(p []byte) { s.appendRunOutput(run.ID, "stderr", p) },
	}
	active, err := s.harness.Start(ctx, harness.RunConfig{
		RunID:             run.ID,
		Prompt:            renderedPrompt,
		Workdir:           prepared.Path,
		ApprovalPolicy:    run.ApprovalPolicy,
		Env:               runtimeAgent.Env,
		Stdout:            stdoutWriter,
		Stderr:            stderrWriter,
		Model:             model,
		Reasoning:         reasoning,
		MaxTurns:          maxTurns,
		ExtraArgs:         extraArgs,
		ThreadSandbox:     threadSandbox,
		TurnSandboxPolicy: turnSandboxPolicy,
		ContinuationFunc:  continuationFunc,
	})
	if err != nil {
		return fmt.Errorf("start harness: %w", sanitizeError(err))
	}

	s.updateRun(run.ID, func(r *domain.AgentRun) {
		r.Status = domain.RunStatusActive
		r.LastActivityAt = time.Now()
	})
	s.recordRunEvent(run, "info", "agent %s started for %s", run.AgentName, issue.Identifier)

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

func snapshotIssue(issue domain.Issue) domain.Issue {
	issue.Labels = append([]string(nil), issue.Labels...)
	if issue.Meta != nil {
		meta := make(map[string]string, len(issue.Meta))
		for k, v := range issue.Meta {
			meta[k] = v
		}
		issue.Meta = meta
	}
	return issue
}

func (s *Service) prepareWorkspaceForIssue(ctx context.Context, issue domain.Issue, agentName string) (workspace.Prepared, error) {
	switch s.agent.Workspace {
	case "git-clone":
		return s.workspace.PrepareClone(ctx, issue, agentName)
	case "none":
		return s.workspace.PrepareEmpty(issue)
	default:
		return workspace.Prepared{}, fmt.Errorf("unsupported workspace strategy %q", s.agent.Workspace)
	}
}

func (s *Service) resolveRuntimeAgent(workspacePath string) (config.AgentTypeConfig, error) {
	agent := s.agent
	if strings.TrimSpace(agent.RepoPackPath) == "" {
		if repoPackPath, ok := config.ParseRepoPackRef(agent.AgentPack); ok {
			agent.RepoPackPath = repoPackPath
		}
	}
	if strings.TrimSpace(agent.RepoPackPath) == "" {
		return agent, nil
	}
	pack, err := config.ResolveRepoPack(workspacePath, agent.RepoPackPath)
	if err != nil {
		return config.AgentTypeConfig{}, err
	}
	agent.Prompt = pack.Prompt
	agent.ContextFiles = append([]string(nil), pack.ContextFiles...)
	agent.Context = pack.Context
	agent.PackClaudeDir = pack.ClaudeDir
	agent.PackCodexDir = pack.CodexDir
	return agent, nil
}

func (s *Service) runBeforeWorkGate(ctx context.Context, run *domain.AgentRun) (string, error) {
	if !s.cfg.Controls.BeforeWork.Enabled {
		return "", nil
	}

	body := strings.TrimSpace(s.cfg.Controls.BeforeWork.Prompt)
	if body == "" {
		body = fmt.Sprintf("Review %s before work begins. Reply with any operator instructions or simply say start.", run.Issue.Identifier)
	}
	summary := fmt.Sprintf("Before work: %s", run.Issue.Identifier)
	kind := "before_work_review"
	if strings.EqualFold(strings.TrimSpace(s.cfg.Controls.BeforeWork.Mode), "reply") {
		kind = "before_work_reply"
	}
	view, replyCh := s.createControlMessage(run, kind, summary, body)
	defer s.cancelControlMessage(view.RequestID, "cancelled")

	s.recordRunEvent(run, "info", "waiting for before_work confirmation for %s", run.Issue.Identifier)

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case reply, ok := <-replyCh:
			if !ok {
				return "", fmt.Errorf("before_work gate for %s was closed", run.ID)
			}
			return strings.TrimSpace(reply), nil
		case <-ticker.C:
			s.mu.RLock()
			_, stopped := s.pendingStops[run.ID]
			s.mu.RUnlock()
			if stopped {
				return "", fmt.Errorf("run stopped before work began")
			}
		}
	}
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

	prefix := s.labelPrefix()
	onComplete := config.ResolveLifecycleTransition(s.cfg.Defaults.OnComplete, s.source.OnComplete)
	onFailure := config.ResolveLifecycleTransition(s.cfg.Defaults.OnFailure, s.source.OnFailure)
	activeLabel := trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixActive)
	retryLabel := trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixRetry)
	doneLabel := trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixDone)
	failedLabel := trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixFailed)

	if scheduledRetry {
		s.recordRunEventByFields("warn", s.source.Name, runID, issueIdentifier, "run %s stopped: %s; retry %d scheduled for %s", runID, comment, retry.Attempt, retry.DueAt.Format(time.RFC3339))
		if issueID != "" {
			s.applyTrackerLifecycle(context.Background(), issueID, []string{retryLabel}, []string{
				activeLabel, doneLabel, failedLabel,
			}, fmt.Sprintf("Maestro run %s stopped and retry %d is scheduled: %s", runID, retry.Attempt, comment))
			s.refreshStoredIssueTimestamp(context.Background(), issueID)
		}
		s.finalizeRun(issueID)
		return
	}

	s.recordRunEventByFields("info", s.source.Name, runID, issueIdentifier, "run %s completed", runID)
	if issueID != "" {
		if status == domain.RunStatusFailed {
			s.applyTerminalLifecycle(context.Background(), issueID, onFailure, prefix, failedLabel, comment)
		} else {
			s.applyTerminalLifecycle(context.Background(), issueID, onComplete, prefix, doneLabel, comment)
		}
		s.refreshStoredIssueTimestamp(context.Background(), issueID)
		s.recordRunEventByFields("info", s.source.Name, runID, issueIdentifier, "tracker state updated for %s", issueIdentifier)
	}
	s.finalizeRun(issueID)
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

	prefix := s.labelPrefix()
	onComplete := config.ResolveLifecycleTransition(s.cfg.Defaults.OnComplete, s.source.OnComplete)
	onFailure := config.ResolveLifecycleTransition(s.cfg.Defaults.OnFailure, s.source.OnFailure)
	activeLabel := trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixActive)
	retryLabel := trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixRetry)
	doneLabel := trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixDone)
	failedLabel := trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixFailed)

	if plannedStop {
		if stop.Retry {
			s.recordRunEventByFields("warn", s.source.Name, runID, issueIdentifier, "run %s stopped: %s; retry %d scheduled for %s", runID, stop.Reason, retry.Attempt, retry.DueAt.Format(time.RFC3339))
			s.applyTrackerLifecycle(context.Background(), issueID, []string{retryLabel}, []string{
				activeLabel, doneLabel, failedLabel,
			}, fmt.Sprintf("Maestro run %s stopped and retry %d is scheduled: %s", runID, retry.Attempt, stop.Reason))
			s.refreshStoredIssueTimestamp(context.Background(), issueID)
			s.finalizeRun(issueID)
			return
		}
		s.recordRunEventByFields("warn", s.source.Name, runID, issueIdentifier, "run %s stopped: %s", runID, stop.Reason)
		if stop.Status == domain.RunStatusFailed {
			s.applyTerminalLifecycle(context.Background(), issueID, onFailure, prefix, failedLabel, stop.Reason)
		} else {
			s.applyTerminalLifecycle(context.Background(), issueID, onComplete, prefix, doneLabel, stop.Reason)
		}
		s.refreshStoredIssueTimestamp(context.Background(), issueID)
		s.finalizeRun(issueID)
		return
	}
	if scheduledRetry {
		s.recordRunEventByFields("warn", s.source.Name, runID, issueIdentifier, "run %s failed: %v; retry %d scheduled for %s", runID, err, retry.Attempt, retry.DueAt.Format(time.RFC3339))
		s.applyTrackerLifecycle(context.Background(), issueID, []string{retryLabel}, []string{
			activeLabel, doneLabel, failedLabel,
		}, fmt.Sprintf("Maestro run %s failed and retry %d is scheduled: %v", runID, retry.Attempt, err))
		s.refreshStoredIssueTimestamp(context.Background(), issueID)
		s.finalizeRun(issueID)
		return
	}
	s.recordRunEventByFields("error", s.source.Name, runID, issueIdentifier, "run %s failed: %v", runID, err)
	if issueID != "" {
		s.applyTerminalLifecycle(context.Background(), issueID, onFailure, prefix, failedLabel, fmt.Sprintf("Maestro run %s failed for %s: %v", runID, issueIdentifier, err))
		s.refreshStoredIssueTimestamp(context.Background(), issueID)
	}
	s.finalizeRun(issueID)
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

func (s *Service) finalizeRun(issueID string) {
	s.releaseClaim(issueID)
	if s.limiter != nil {
		s.limiter.Release()
	}
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
