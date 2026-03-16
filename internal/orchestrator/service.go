package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/harness"
	claudeharness "github.com/tjohnson/maestro/internal/harness/claude"
	codexharness "github.com/tjohnson/maestro/internal/harness/codex"
	"github.com/tjohnson/maestro/internal/prompt"
	"github.com/tjohnson/maestro/internal/redact"
	"github.com/tjohnson/maestro/internal/state"
	"github.com/tjohnson/maestro/internal/tracker"
	gitlabtracker "github.com/tjohnson/maestro/internal/tracker/gitlab"
	lineartracker "github.com/tjohnson/maestro/internal/tracker/linear"
	"github.com/tjohnson/maestro/internal/workspace"
)

type Event struct {
	Time    time.Time
	Level   string
	Message string
}

type pendingStop struct {
	Status domain.RunStatus
	Reason string
	Retry  bool
}

type ApprovalView struct {
	RequestID       string
	RunID           string
	IssueID         string
	IssueIdentifier string
	AgentName       string
	ToolName        string
	ToolInput       string
	ApprovalPolicy  string
	RequestedAt     time.Time
	Resolvable      bool
}

type ApprovalHistoryEntry struct {
	RequestID       string
	RunID           string
	IssueID         string
	IssueIdentifier string
	AgentName       string
	ToolName        string
	ApprovalPolicy  string
	Decision        string
	Reason          string
	RequestedAt     time.Time
	DecidedAt       time.Time
	Outcome         string
}

type SourceSummary struct {
	Name             string
	DisplayGroup     string
	Tags             []string
	Tracker          string
	LastPollAt       time.Time
	LastPollCount    int
	ClaimedCount     int
	RetryCount       int
	ActiveRunCount   int
	PendingApprovals int
}

type Snapshot struct {
	SourceName       string
	SourceTracker    string
	LastPollAt       time.Time
	LastPollCount    int
	ClaimedCount     int
	RetryCount       int
	PendingApprovals []ApprovalView
	ApprovalHistory  []ApprovalHistoryEntry
	ActiveRun        *domain.AgentRun
	ActiveRuns       []domain.AgentRun
	SourceSummaries  []SourceSummary
	RecentEvents     []Event
}

type Dependencies struct {
	Tracker    tracker.Tracker
	Harness    harness.Harness
	Workspace  *workspace.Manager
	StateStore *state.Store
	Limiter    dispatchLimiter
}

type Service struct {
	cfg        *config.Config
	logger     *slog.Logger
	source     config.SourceConfig
	agent      config.AgentTypeConfig
	tracker    tracker.Tracker
	harness    harness.Harness
	workspace  *workspace.Manager
	stateStore *state.Store
	limiter    dispatchLimiter

	mu              sync.RWMutex
	claimed         map[string]struct{}
	finished        map[string]state.TerminalIssue
	retryQueue      map[string]state.RetryEntry
	activeRun       *domain.AgentRun
	lastPollAt      time.Time
	lastPollCount   int
	events          []Event
	runWG           sync.WaitGroup
	pendingStops    map[string]pendingStop
	approvals       map[string]ApprovalView
	approvalOrder   []string
	approvalHistory []ApprovalHistoryEntry
}

func NewService(cfg *config.Config, logger *slog.Logger) (*Service, error) {
	tr, err := newTracker(cfg.Sources[0])
	if err != nil {
		return nil, err
	}
	hr, err := newHarness(cfg.AgentTypes[0].Harness)
	if err != nil {
		return nil, err
	}

	return NewServiceWithDeps(cfg, logger, Dependencies{
		Tracker:    tr,
		Harness:    hr,
		Workspace:  workspace.NewManager(cfg.Workspace.Root).WithGitLabAuth(cfg.Sources[0].Connection.BaseURL, cfg.Sources[0].Connection.Token),
		StateStore: state.NewStore(cfg.State.Dir),
	})
}

func newTracker(source config.SourceConfig) (tracker.Tracker, error) {
	switch source.Tracker {
	case "gitlab", "gitlab-epic":
		return gitlabtracker.NewAdapter(source)
	case "linear":
		return lineartracker.NewAdapter(source)
	default:
		return nil, fmt.Errorf("unsupported tracker %q", source.Tracker)
	}
}

func newHarness(kind string) (harness.Harness, error) {
	switch kind {
	case "claude-code":
		return claudeharness.NewAdapter()
	case "codex":
		return codexharness.NewAdapter()
	default:
		return nil, fmt.Errorf("unsupported harness %q", kind)
	}
}

func NewServiceWithDeps(cfg *config.Config, logger *slog.Logger, deps Dependencies) (*Service, error) {
	if deps.Tracker == nil {
		return nil, fmt.Errorf("tracker dependency is required")
	}
	if deps.Harness == nil {
		return nil, fmt.Errorf("harness dependency is required")
	}
	if deps.Workspace == nil {
		return nil, fmt.Errorf("workspace dependency is required")
	}
	if deps.StateStore == nil {
		deps.StateStore = state.NewStore(cfg.State.Dir)
	}

	svc := &Service{
		cfg:          cfg,
		logger:       logger,
		source:       cfg.Sources[0],
		agent:        cfg.AgentTypes[0],
		tracker:      deps.Tracker,
		harness:      deps.Harness,
		workspace:    deps.Workspace,
		claimed:      map[string]struct{}{},
		finished:     map[string]state.TerminalIssue{},
		retryQueue:   map[string]state.RetryEntry{},
		stateStore:   deps.StateStore,
		limiter:      deps.Limiter,
		pendingStops: map[string]pendingStop{},
		approvals:    map[string]ApprovalView{},
	}
	if err := svc.restoreState(); err != nil {
		logger.Warn("restore state failed", "error", err)
	}
	return svc, nil
}

func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var activeRun *domain.AgentRun
	if s.activeRun != nil {
		copyRun := *s.activeRun
		activeRun = &copyRun
	}

	events := append([]Event(nil), s.events...)
	pendingApprovals := make([]ApprovalView, 0, len(s.approvalOrder))
	for _, requestID := range s.approvalOrder {
		if request, ok := s.approvals[requestID]; ok {
			pendingApprovals = append(pendingApprovals, request)
		}
	}
	history := append([]ApprovalHistoryEntry(nil), s.approvalHistory...)
	return Snapshot{
		SourceName:       s.source.Name,
		SourceTracker:    s.source.Tracker,
		LastPollAt:       s.lastPollAt,
		LastPollCount:    s.lastPollCount,
		ClaimedCount:     len(s.claimed),
		RetryCount:       len(s.retryQueue),
		PendingApprovals: pendingApprovals,
		ApprovalHistory:  history,
		ActiveRun:        activeRun,
		ActiveRuns:       activeRuns(activeRun),
		SourceSummaries:  []SourceSummary{sourceSummaryForSnapshot(s.source, s.lastPollAt, s.lastPollCount, len(s.claimed), len(s.retryQueue), len(activeRuns(activeRun)), len(pendingApprovals))},
		RecentEvents:     events,
	}
}

func activeRuns(run *domain.AgentRun) []domain.AgentRun {
	if run == nil {
		return nil
	}
	return []domain.AgentRun{*run}
}

func sourceSummaryForSnapshot(source config.SourceConfig, lastPollAt time.Time, lastPollCount int, claimedCount int, retryCount int, activeRunCount int, pendingApprovals int) SourceSummary {
	return SourceSummary{
		Name:             source.Name,
		DisplayGroup:     source.DisplayGroup,
		Tags:             append([]string(nil), source.Tags...),
		Tracker:          source.Tracker,
		LastPollAt:       lastPollAt,
		LastPollCount:    lastPollCount,
		ClaimedCount:     claimedCount,
		RetryCount:       retryCount,
		ActiveRunCount:   activeRunCount,
		PendingApprovals: pendingApprovals,
	}
}

func (s *Service) recordEvent(level string, message string, args ...any) {
	msg := redact.String(fmt.Sprintf(message, args...))
	s.logger.Log(context.Background(), parseLevel(level), msg)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.events = append(s.events, Event{
		Time:    time.Now(),
		Level:   strings.ToUpper(level),
		Message: msg,
	})
	if len(s.events) > 20 {
		s.events = s.events[len(s.events)-20:]
	}
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func (s *Service) renderPrompt(issue domain.Issue, agentName string, attempt int) (string, error) {
	return prompt.RenderFile(s.agent.Prompt, prompt.Data{
		Issue:     issue,
		User:      s.cfg.User,
		Agent:     s.agent,
		Source:    s.source,
		Attempt:   attempt,
		AgentName: agentName,
	})
}
