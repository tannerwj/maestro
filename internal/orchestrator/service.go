package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
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

const (
	maxRecentEvents    = 20
	maxApprovalHistory = 10
	maxMessageHistory  = 10
)

func removeFromOrder(order []string, id string) []string {
	out := order[:0:0]
	for _, candidate := range order {
		if candidate != id {
			out = append(out, candidate)
		}
	}
	return out
}

type Event struct {
	Time    time.Time
	Level   string
	Source  string
	RunID   string
	Issue   string
	Message string
}

type eventContext struct {
	SourceName      string
	RunID           string
	IssueIdentifier string
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

type MessageView struct {
	RequestID       string
	RunID           string
	IssueID         string
	IssueIdentifier string
	SourceName      string
	AgentName       string
	Kind            string
	Summary         string
	Body            string
	RequestedAt     time.Time
	Resolvable      bool
}

type MessageHistoryEntry struct {
	RequestID       string
	RunID           string
	IssueID         string
	IssueIdentifier string
	SourceName      string
	AgentName       string
	Kind            string
	Summary         string
	Body            string
	Reply           string
	ResolvedVia     string
	RequestedAt     time.Time
	RepliedAt       time.Time
	Outcome         string
}

type RetryView struct {
	IssueID         string
	IssueIdentifier string
	SourceName      string
	Attempt         int
	DueAt           time.Time
	Error           string
}

type RunOutputView struct {
	RunID           string
	SourceName      string
	IssueIdentifier string
	StdoutTail      string
	StderrTail      string
	UpdatedAt       time.Time
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
	PendingMessages  int
}

type Snapshot struct {
	SourceName       string
	SourceTracker    string
	LastPollAt       time.Time
	LastPollCount    int
	ClaimedCount     int
	RetryCount       int
	PendingApprovals []ApprovalView
	PendingMessages  []MessageView
	Retries          []RetryView
	ApprovalHistory  []ApprovalHistoryEntry
	MessageHistory   []MessageHistoryEntry
	ActiveRun        *domain.AgentRun
	ActiveRuns       []domain.AgentRun
	RunOutputs       []RunOutputView
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
	messages        map[string]MessageView
	messageOrder    []string
	messageHistory  []MessageHistoryEntry
	messageWaiters  map[string]chan string
	runOutputs      map[string]*runOutputBuffer
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
		cfg:            cfg,
		logger:         logger,
		source:         cfg.Sources[0],
		agent:          cfg.AgentTypes[0],
		tracker:        deps.Tracker,
		harness:        deps.Harness,
		workspace:      deps.Workspace,
		claimed:        map[string]struct{}{},
		finished:       map[string]state.TerminalIssue{},
		retryQueue:     map[string]state.RetryEntry{},
		stateStore:     deps.StateStore,
		limiter:        deps.Limiter,
		pendingStops:   map[string]pendingStop{},
		approvals:      map[string]ApprovalView{},
		messages:       map[string]MessageView{},
		messageWaiters: map[string]chan string{},
		runOutputs:     map[string]*runOutputBuffer{},
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
	pendingMessages := make([]MessageView, 0, len(s.messageOrder))
	for _, requestID := range s.messageOrder {
		if request, ok := s.messages[requestID]; ok {
			pendingMessages = append(pendingMessages, request)
		}
	}
	messageHistory := append([]MessageHistoryEntry(nil), s.messageHistory...)
	retries := make([]RetryView, 0, len(s.retryQueue))
	for _, retry := range s.retryQueue {
		retries = append(retries, RetryView{
			IssueID:         retry.IssueID,
			IssueIdentifier: retry.Identifier,
			SourceName:      s.source.Name,
			Attempt:         retry.Attempt,
			DueAt:           retry.DueAt,
			Error:           retry.Error,
		})
	}
	sort.Slice(retries, func(i, j int) bool {
		if retries[i].DueAt.Equal(retries[j].DueAt) {
			return retries[i].IssueIdentifier < retries[j].IssueIdentifier
		}
		return retries[i].DueAt.Before(retries[j].DueAt)
	})
	runOutputs := make([]RunOutputView, 0, len(s.runOutputs))
	for runID, output := range s.runOutputs {
		output.mu.RLock()
		updatedAt := output.updatedAt
		output.mu.RUnlock()
		runOutputs = append(runOutputs, RunOutputView{
			RunID:      runID,
			SourceName: s.source.Name,
			StdoutTail: output.stdout.String(),
			StderrTail: output.stderr.String(),
			UpdatedAt:  updatedAt,
		})
	}
	if activeRun != nil {
		for i := range runOutputs {
			if runOutputs[i].RunID == activeRun.ID {
				runOutputs[i].IssueIdentifier = activeRun.Issue.Identifier
				break
			}
		}
	}
	sort.Slice(runOutputs, func(i, j int) bool {
		if runOutputs[i].UpdatedAt.Equal(runOutputs[j].UpdatedAt) {
			return runOutputs[i].RunID < runOutputs[j].RunID
		}
		return runOutputs[i].UpdatedAt.After(runOutputs[j].UpdatedAt)
	})
	return Snapshot{
		SourceName:       s.source.Name,
		SourceTracker:    s.source.Tracker,
		LastPollAt:       s.lastPollAt,
		LastPollCount:    s.lastPollCount,
		ClaimedCount:     len(s.claimed),
		RetryCount:       len(s.retryQueue),
		PendingApprovals: pendingApprovals,
		PendingMessages:  pendingMessages,
		Retries:          retries,
		ApprovalHistory:  history,
		MessageHistory:   messageHistory,
		ActiveRun:        activeRun,
		ActiveRuns:       activeRuns(activeRun),
		RunOutputs:       runOutputs,
		SourceSummaries:  []SourceSummary{sourceSummaryForSnapshot(s.source, s.lastPollAt, s.lastPollCount, len(s.claimed), len(s.retryQueue), len(activeRuns(activeRun)), len(pendingApprovals), len(pendingMessages))},
		RecentEvents:     events,
	}
}

func activeRuns(run *domain.AgentRun) []domain.AgentRun {
	if run == nil {
		return nil
	}
	return []domain.AgentRun{*run}
}

func sourceSummaryForSnapshot(source config.SourceConfig, lastPollAt time.Time, lastPollCount int, claimedCount int, retryCount int, activeRunCount int, pendingApprovals int, pendingMessages int) SourceSummary {
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
		PendingMessages:  pendingMessages,
	}
}

func (s *Service) recordEvent(level string, message string, args ...any) {
	s.recordEventWithContext(level, eventContext{}, message, args...)
}

func (s *Service) recordSourceEvent(level string, sourceName string, message string, args ...any) {
	s.recordEventWithContext(level, eventContext{SourceName: sourceName}, message, args...)
}

func (s *Service) recordRunEvent(run *domain.AgentRun, level string, message string, args ...any) {
	if run == nil {
		s.recordEvent(level, message, args...)
		return
	}
	s.recordEventWithContext(level, eventContext{
		SourceName:      run.SourceName,
		RunID:           run.ID,
		IssueIdentifier: run.Issue.Identifier,
	}, message, args...)
}

func (s *Service) recordRunEventByFields(level string, sourceName string, runID string, issueIdentifier string, message string, args ...any) {
	s.recordEventWithContext(level, eventContext{
		SourceName:      sourceName,
		RunID:           runID,
		IssueIdentifier: issueIdentifier,
	}, message, args...)
}

func (s *Service) recordEventWithContext(level string, ctx eventContext, message string, args ...any) {
	msg := redact.String(fmt.Sprintf(message, args...))
	s.logger.Log(context.Background(), parseLevel(level), msg)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.events = append(s.events, Event{
		Time:    time.Now(),
		Level:   strings.ToUpper(level),
		Source:  ctx.SourceName,
		RunID:   ctx.RunID,
		Issue:   ctx.IssueIdentifier,
		Message: msg,
	})
	if len(s.events) > maxRecentEvents {
		s.events = s.events[len(s.events)-maxRecentEvents:]
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

func (s *Service) labelPrefix() string {
	if p := strings.TrimSpace(s.source.LabelPrefix); p != "" {
		return p
	}
	if p := strings.TrimSpace(s.cfg.Defaults.LabelPrefix); p != "" {
		return p
	}
	return "maestro"
}

func (s *Service) renderPrompt(agent config.AgentTypeConfig, issue domain.Issue, agentName string, attempt int, operatorInstruction string) (string, error) {
	return prompt.RenderFile(agent.Prompt, prompt.Data{
		Issue:               issue,
		User:                s.cfg.User,
		Agent:               agent,
		Source:              s.source,
		Attempt:             attempt,
		AgentName:           agentName,
		OperatorInstruction: operatorInstruction,
	})
}
