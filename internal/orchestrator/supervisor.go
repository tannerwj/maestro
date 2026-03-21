package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/state"
	"github.com/tjohnson/maestro/internal/workspace"
)

type Runtime interface {
	Run(ctx context.Context) error
	Snapshot() Snapshot
	ResolveApproval(requestID string, decision string) error
	ResolveMessage(requestID string, reply string, resolvedVia string) error
	StopRun(runID string, reason string) error
}

type Supervisor struct {
	services []*Service
}

func NewRuntime(cfg *config.Config, logger *slog.Logger) (Runtime, error) {
	if len(cfg.Sources) == 1 {
		return NewService(cfg, logger)
	}
	return NewSupervisor(cfg, logger)
}

func NewSupervisor(cfg *config.Config, logger *slog.Logger) (*Supervisor, error) {
	agents := map[string]config.AgentTypeConfig{}
	for _, agent := range cfg.AgentTypes {
		agents[agent.Name] = agent
	}

	limiter := newSemaphoreLimiter(cfg.Defaults.MaxConcurrentGlobal)
	agentLimiters := map[string]dispatchLimiter{}
	for _, agent := range cfg.AgentTypes {
		agentLimiters[agent.Name] = newSemaphoreLimiter(agent.MaxConcurrent)
	}
	services := make([]*Service, 0, len(cfg.Sources))
	for _, source := range cfg.Sources {
		agent := agents[source.AgentType]
		scoped := scopedConfig(cfg, source, agent)
		tr, err := newTracker(source)
		if err != nil {
			return nil, err
		}
		hr, err := newHarness(agent.Harness)
		if err != nil {
			return nil, err
		}
		svc, err := NewServiceWithDeps(scoped, logger, Dependencies{
			Tracker:    tr,
			Harness:    hr,
			Workspace:  workspace.NewManager(cfg.Workspace.Root).WithGitLabAuth(source.Connection.BaseURL, source.Connection.Token),
			StateStore: state.NewStore(config.ScopedStateDir(cfg, source)),
			Limiter:    newCompositeLimiter(limiter, agentLimiters[agent.Name]),
		})
		if err != nil {
			return nil, err
		}
		services = append(services, svc)
	}
	return &Supervisor{services: services}, nil
}

func scopedConfig(cfg *config.Config, source config.SourceConfig, agent config.AgentTypeConfig) *config.Config {
	clone := *cfg
	clone.Sources = []config.SourceConfig{source}
	clone.AgentTypes = []config.AgentTypeConfig{agent}
	clone.State = cfg.State
	clone.State.Dir = config.ScopedStateDir(cfg, source)
	return &clone
}

func (s *Supervisor) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(s.services))
	var wg sync.WaitGroup
	for _, svc := range s.services {
		wg.Add(1)
		go func(svc *Service) {
			defer wg.Done()
			errCh <- svc.Run(runCtx)
		}(svc)
	}

	var firstErr error
	for i := 0; i < len(s.services); i++ {
		err := <-errCh
		if err != nil && firstErr == nil {
			firstErr = err
			cancel()
		}
	}
	wg.Wait()
	return firstErr
}

func (s *Supervisor) Snapshot() Snapshot {
	if len(s.services) == 0 {
		return Snapshot{}
	}

	snapshots := make([]Snapshot, 0, len(s.services))
	sourceNames := make([]string, 0, len(s.services))
	for _, svc := range s.services {
		snap := svc.Snapshot()
		snapshots = append(snapshots, snap)
		if snap.SourceName != "" {
			sourceNames = append(sourceNames, snap.SourceName)
		}
	}

	sort.Strings(sourceNames)
	merged := Snapshot{
		SourceName: strings.Join(sourceNames, ", "),
	}
	for _, snap := range snapshots {
		if snap.LastPollAt.After(merged.LastPollAt) {
			merged.LastPollAt = snap.LastPollAt
		}
		merged.LastPollCount += snap.LastPollCount
		merged.ClaimedCount += snap.ClaimedCount
		merged.RetryCount += snap.RetryCount
		merged.PendingApprovals = append(merged.PendingApprovals, snap.PendingApprovals...)
		merged.ApprovalHistory = append(merged.ApprovalHistory, snap.ApprovalHistory...)
		merged.RecentEvents = append(merged.RecentEvents, snap.RecentEvents...)
		merged.ActiveRuns = append(merged.ActiveRuns, snap.ActiveRuns...)
		merged.RunOutputs = append(merged.RunOutputs, snap.RunOutputs...)
		merged.SourceSummaries = append(merged.SourceSummaries, snap.SourceSummaries...)
		if merged.ActiveRun == nil && snap.ActiveRun != nil {
			merged.ActiveRun = snap.ActiveRun
		}
	}

	sort.Slice(merged.PendingApprovals, func(i, j int) bool {
		return merged.PendingApprovals[i].RequestedAt.Before(merged.PendingApprovals[j].RequestedAt)
	})
	sort.Slice(merged.ApprovalHistory, func(i, j int) bool {
		return merged.ApprovalHistory[i].DecidedAt.After(merged.ApprovalHistory[j].DecidedAt)
	})
	if len(merged.ApprovalHistory) > maxApprovalHistory {
		merged.ApprovalHistory = merged.ApprovalHistory[:maxApprovalHistory]
	}
	sort.Slice(merged.RecentEvents, func(i, j int) bool {
		return merged.RecentEvents[i].Time.After(merged.RecentEvents[j].Time)
	})
	if len(merged.RecentEvents) > maxRecentEvents {
		merged.RecentEvents = merged.RecentEvents[:maxRecentEvents]
	}
	sort.Slice(merged.ActiveRuns, func(i, j int) bool {
		return merged.ActiveRuns[i].StartedAt.Before(merged.ActiveRuns[j].StartedAt)
	})
	sort.Slice(merged.RunOutputs, func(i, j int) bool {
		if merged.RunOutputs[i].UpdatedAt.Equal(merged.RunOutputs[j].UpdatedAt) {
			return merged.RunOutputs[i].RunID < merged.RunOutputs[j].RunID
		}
		return merged.RunOutputs[i].UpdatedAt.After(merged.RunOutputs[j].UpdatedAt)
	})
	sort.Slice(merged.SourceSummaries, func(i, j int) bool {
		return merged.SourceSummaries[i].Name < merged.SourceSummaries[j].Name
	})
	return merged
}

func (s *Supervisor) ResolveApproval(requestID string, decision string) error {
	var errs []string
	for _, svc := range s.services {
		if err := svc.ResolveApproval(requestID, decision); err == nil {
			return nil
		} else if !strings.Contains(err.Error(), "not found") {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return fmt.Errorf("approval request %q not found", requestID)
}

func (s *Supervisor) ResolveMessage(requestID string, reply string, resolvedVia string) error {
	for _, svc := range s.services {
		if err := svc.ResolveMessage(requestID, reply, resolvedVia); err == nil {
			return nil
		}
	}
	return fmt.Errorf("message request %q not found", requestID)
}

func (s *Supervisor) StopRun(runID string, reason string) error {
	var errs []string
	for _, svc := range s.services {
		if err := svc.StopRun(runID, reason); err == nil {
			return nil
		} else if !strings.Contains(err.Error(), "not found") {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return fmt.Errorf("run %q not found", runID)
}

func (s *Supervisor) Services() []*Service {
	return slices.Clone(s.services)
}
