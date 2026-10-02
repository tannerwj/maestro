package orchestrator

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/harness"
	trackerbase "github.com/tjohnson/maestro/internal/tracker"
	"github.com/tjohnson/maestro/internal/workspace"
)

type runManager struct {
	service *Service
}

func (r *runManager) dispatch(ctx context.Context, issue domain.Issue) (bool, string, error) {
	return r.dispatchWithAttempt(ctx, issue, nil)
}

func (r *runManager) dispatchRetry(ctx context.Context, issue domain.Issue, attempt int) (bool, string, error) {
	return r.dispatchWithAttempt(ctx, issue, &attempt)
}

func (r *runManager) dispatchWithAttempt(ctx context.Context, issue domain.Issue, attemptOverride *int) (bool, string, error) {
	s := r.service
	if s.limiter != nil {
		acquired, blockedBy := s.limiter.TryAcquire()
		if !acquired {
			return false, blockedBy, nil
		}
	}

	attempt := s.stateMgr.takeAttempt(issue)
	if attemptOverride != nil {
		attempt = s.stateMgr.takeAttemptOverride(issue.ID, *attemptOverride)
	}
	startedAt := time.Now()
	run := &domain.AgentRun{
		ID:             newRunID(startedAt),
		AgentName:      s.agent.InstanceName,
		AgentType:      s.agent.Name,
		Issue:          issue,
		SourceName:     s.source.Name,
		HarnessKind:    s.harness.Kind(),
		Status:         domain.RunStatusPending,
		CurrentTurn:    1,
		Attempt:        attempt,
		ApprovalPolicy: s.agent.ApprovalPolicy,
		ApprovalState:  s.stateMgr.approvalState(),
		StartedAt:      startedAt,
	}
	if run.AgentName == "" {
		run.AgentName = s.agent.Name
	}

	s.mu.Lock()
	s.claimed[issue.ID] = struct{}{}
	s.setActiveRunLocked(run)
	s.mu.Unlock()
	_ = s.stateMgr.saveStateBestEffort()

	prefix := s.labelPrefix()
	dispatchTransition := config.ResolveDispatchTransition(s.cfg.Defaults.OnDispatch, s.source.OnDispatch)
	s.recordRunEvent(run, "info", "dispatching %s to %s", issue.Identifier, run.AgentName)
	s.applyDispatchLifecycle(ctx, issue.ID, dispatchTransition, prefix, run)
	s.refreshActiveRunIssue(ctx, run.ID)

	s.runWG.Add(1)
	go r.executeRun(ctx, run)
	return true, "", nil
}

func newRunID(now time.Time) string {
	return fmt.Sprintf("run-%s-%06d", now.Format("20060102-150405"), now.Nanosecond()/1000)
}

func (r *runManager) executeRun(ctx context.Context, run *domain.AgentRun) {
	s := r.service
	defer s.runWG.Done()
	if err := r.prepareAndStart(ctx, run); err != nil {
		r.failRun(run.ID, sanitizeError(err))
	}
}

func (r *runManager) prepareAndStart(ctx context.Context, run *domain.AgentRun) error {
	s := r.service
	r.updateRun(run.ID, func(r *domain.AgentRun) {
		r.Status = domain.RunStatusPreparing
		r.LastActivityAt = time.Now()
	})

	s.mu.RLock()
	issue := snapshotIssue(run.Issue)
	s.mu.RUnlock()

	prepared, err := r.prepareWorkspaceForIssue(ctx, issue, run.AgentName)
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

	r.updateRun(run.ID, func(r *domain.AgentRun) {
		r.WorkspacePath = prepared.Path
	})
	s.runHookBestEffort(ctx, s.cfg.Hooks.AfterCreate, prepared.Path, run, "after_create")
	operatorInstruction, err := r.runBeforeWorkGate(ctx, run)
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
	if maxTurns < 1 {
		maxTurns = 1
	}
	run.MaxTurns = maxTurns

	var continuationFunc func(ctx context.Context, turnNumber int) (string, bool, error)
	if maxTurns > 1 && (runtimeAgent.Harness == "codex" || runtimeAgent.Harness == "claude-code") {
		issueID := issue.ID
		prefix := s.labelPrefix()
		activeLabel := trackerbase.LifecycleLabel(prefix, trackerbase.LifecycleSuffixActive)
		sourceFilter := s.source.EffectiveIssueFilter()
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
	lineageKey := runLineageKey(s.source.Name, issue, prepared.Path)

	stdoutLog, err := s.openRunLog(run.ID, "stdout.log")
	if err != nil {
		return fmt.Errorf("open run stdout log: %w", err)
	}
	defer stdoutLog.Close()
	stderrLog, err := s.openRunLog(run.ID, "stderr.log")
	if err != nil {
		return fmt.Errorf("open run stderr log: %w", err)
	}
	defer stderrLog.Close()
	stdoutWriter := &runOutputWriter{
		target:  stdoutLog,
		onWrite: func() { s.markRunActivity(run.ID) },
		append:  func(p []byte) { s.appendRunOutput(run.ID, "stdout", p) },
	}
	stderrWriter := &runOutputWriter{
		target:  stderrLog,
		onWrite: func() { s.markRunActivity(run.ID) },
		append:  func(p []byte) { s.appendRunOutput(run.ID, "stderr", p) },
	}
	active, err := s.harness.Start(ctx, harness.RunConfig{
		RunID:          run.ID,
		Prompt:         renderedPrompt,
		Workdir:        prepared.Path,
		LineageKey:     lineageKey,
		ApprovalPolicy: run.ApprovalPolicy,
		Env:            runtimeAgent.Env,
		Stdout:         stdoutWriter,
		Stderr:         stderrWriter,
		MetricsCallback: func(metrics domain.RunMetrics) {
			r.updateRun(run.ID, func(activeRun *domain.AgentRun) {
				activeRun.Metrics = domain.MergeRunMetrics(activeRun.Metrics, metrics)
				activeRun.Metrics = domain.DeriveRunMetrics(activeRun.Metrics, activeRun.StartedAt, activeRun.CompletedAt, time.Now())
			})
		},
		TurnCallback: func(currentTurn int, maxTurns int) {
			r.updateRun(run.ID, func(activeRun *domain.AgentRun) {
				if currentTurn > 0 {
					activeRun.CurrentTurn = currentTurn
				}
				if maxTurns > 0 {
					activeRun.MaxTurns = maxTurns
				}
			})
		},
		ExecutionMetadataCallback: func(metadata harness.ExecutionMetadata) {
			r.updateRun(run.ID, func(activeRun *domain.AgentRun) {
				activeRun.Execution = executionMetadataFromHarness(metadata)
			})
		},
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

	r.updateRun(run.ID, func(r *domain.AgentRun) {
		r.Status = domain.RunStatusActive
		r.LastActivityAt = time.Now()
	})
	s.recordRunEvent(run, "info", "agent %s started for %s", run.AgentName, issue.Identifier)

	if err := active.Wait(); err != nil {
		s.runHookBestEffort(context.Background(), s.cfg.Hooks.AfterRun, prepared.Path, run, "after_run")
		stdoutTail, stderrTail := s.runOutputTails(run.ID)
		return fmt.Errorf(
			"agent exited with error: %w stderr=%s stdout=%s",
			sanitizeError(err),
			sanitizeOutput(stderrTail),
			sanitizeOutput(stdoutTail),
		)
	}

	s.runHookBestEffort(context.Background(), s.cfg.Hooks.AfterRun, prepared.Path, run, "after_run")
	r.completeRun(run.ID)
	return nil
}

func runLineageKey(sourceName string, issue domain.Issue, workspacePath string) string {
	parts := []string{
		"source=" + strings.TrimSpace(sourceName),
		"issue=" + strings.TrimSpace(issue.ID),
	}
	if strings.TrimSpace(issue.Identifier) != "" {
		parts = append(parts, "identifier="+strings.TrimSpace(issue.Identifier))
	}
	if strings.TrimSpace(workspacePath) != "" {
		parts = append(parts, "workspace="+filepath.Clean(workspacePath))
	}
	return strings.Join(parts, "|")
}

func executionMetadataFromHarness(metadata harness.ExecutionMetadata) *domain.RunExecutionMetadata {
	if strings.TrimSpace(metadata.Mode) == "" && metadata.ContainerReuse == nil {
		return nil
	}
	out := &domain.RunExecutionMetadata{
		Mode: strings.TrimSpace(metadata.Mode),
	}
	if metadata.ContainerReuse != nil {
		out.ContainerReuse = &domain.ContainerReuseMetadata{
			Mode:          strings.TrimSpace(metadata.ContainerReuse.Mode),
			Reused:        metadata.ContainerReuse.Reused,
			ContainerID:   strings.TrimSpace(metadata.ContainerReuse.ContainerID),
			ContainerName: strings.TrimSpace(metadata.ContainerReuse.ContainerName),
			ProfileKey:    strings.TrimSpace(metadata.ContainerReuse.ProfileKey),
			LineageKey:    strings.TrimSpace(metadata.ContainerReuse.LineageKey),
		}
	}
	return out
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
	if len(issue.Blockers) > 0 {
		blockers := make([]domain.Issue, 0, len(issue.Blockers))
		for _, blocker := range issue.Blockers {
			blockers = append(blockers, snapshotIssue(blocker))
		}
		issue.Blockers = blockers
	}
	return issue
}

func (s *Service) resolveRuntimeAgent(workspacePath string) (config.AgentTypeConfig, error) {
	return resolveRuntimeAgent(s.agent, workspacePath)
}

func resolveRuntimeAgent(agent config.AgentTypeConfig, workspacePath string) (config.AgentTypeConfig, error) {
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

func (r *runManager) prepareWorkspaceForIssue(ctx context.Context, issue domain.Issue, agentName string) (workspace.Prepared, error) {
	s := r.service
	switch s.agent.Workspace {
	case "git-clone":
		return s.workspace.PrepareClone(ctx, issue, agentName)
	case "none":
		return s.workspace.PrepareEmpty(issue)
	default:
		return workspace.Prepared{}, fmt.Errorf("unsupported workspace strategy %q", s.agent.Workspace)
	}
}

func (r *runManager) runBeforeWorkGate(ctx context.Context, run *domain.AgentRun) (string, error) {
	s := r.service
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
