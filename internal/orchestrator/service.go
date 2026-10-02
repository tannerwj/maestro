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
	Name                   string
	DisplayGroup           string
	Tags                   []string
	Tracker                string
	RateLimit              *domain.TrackerRateLimit
	ProjectURL             string
	FilterStates           []string
	FilterLabels           []string
	Execution              *ExecutionSummary
	LastPollAt             time.Time
	LastPollCount          int
	ClaimedCount           int
	RetryCount             int
	ActiveRunCount         int
	MaxActiveRuns          int
	AgentMaxConcurrent     int
	GlobalMaxConcurrent    int
	EffectiveMaxConcurrent int
	Metrics                domain.RunMetrics
	PendingApprovals       int
	PendingMessages        int
}

type MetricBreakdown struct {
	Name    string
	Metrics domain.RunMetrics
}

type Snapshot struct {
	SourceName       string
	SourceTracker    string
	LastPollAt       time.Time
	LastPollCount    int
	ClaimedCount     int
	RetryCount       int
	InstanceMetrics  domain.RunMetrics
	HarnessMetrics   []MetricBreakdown
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

type ExecutionSummary struct {
	Mode              string
	ReuseMode         string
	Reused            bool
	ContainerID       string
	ContainerName     string
	ProfileKey        string
	LineageKey        string
	Image             string
	Network           string
	NetworkPolicyMode string
	NetworkAllow      []string
	CPUs              float64
	Memory            string
	PIDsLimit         int
	AuthSource        string
	SecurityPreset    string
	EnvCount          int
	SecretMountCount  int
	ToolMountCount    int
}

type Dependencies struct {
	Tracker       tracker.Tracker
	Harness       harness.Harness
	ProcessRunner harness.ProcessRunner
	Workspace     *workspace.Manager
	StateStore    *state.Store
	Limiter       dispatchLimiter
	MetricsStore  *runtimeMetricsStore
}

type Service struct {
	cfg           *config.Config
	logger        *slog.Logger
	source        config.SourceConfig
	agent         config.AgentTypeConfig
	tracker       tracker.Tracker
	harness       harness.Harness
	processRunner harness.ProcessRunner
	workspace     *workspace.Manager
	stateStore    *state.Store
	limiter       dispatchLimiter
	stateMgr      *stateManager
	approvalMgr   *approvalRouter
	messageMgr    *messageRouter
	runMgr        *runManager

	mu                  sync.RWMutex
	claimed             map[string]struct{}
	finished            map[string]state.TerminalIssue
	retryQueue          map[string]state.RetryEntry
	activeRun           *domain.AgentRun
	activeRuns          map[string]*domain.AgentRun
	activeRunOrder      []string
	lastPollAt          time.Time
	lastPollCount       int
	events              []Event
	runWG               sync.WaitGroup
	pendingStops        map[string]pendingStop
	approvals           map[string]ApprovalView
	approvalOrder       []string
	approvalHistory     []ApprovalHistoryEntry
	messages            map[string]MessageView
	messageOrder        []string
	messageHistory      []MessageHistoryEntry
	messageWaiters      map[string]chan string
	runOutputs          map[string]*runOutputBuffer
	forcePollCh         chan struct{}
	forcePollPending    bool
	polling             bool
	lastPollAttemptAt   time.Time
	globalMaxConcurrent int
	draining            bool
	controlCh           chan struct{}
	cleanup             func() error
	metricsStore        *runtimeMetricsStore
}

func NewService(cfg *config.Config, logger *slog.Logger) (*Service, error) {
	shared := newRuntimeSharedDeps(cfg)
	service, err := buildScopedService(cfg, cfg.Sources[0], logger, shared)
	if err != nil {
		_ = shared.Close()
		return nil, err
	}
	service.cleanup = shared.Close
	return service, nil
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

func newHarness(agent config.AgentTypeConfig, runner harness.ProcessRunner) (harness.Harness, error) {
	switch agent.Harness {
	case "claude-code":
		return claudeharness.NewAdapter(claudeharness.WithProcessRunner(runner))
	case "codex":
		return codexharness.NewAdapter(codexharness.WithProcessRunner(runner))
	default:
		return nil, fmt.Errorf("unsupported harness %q", agent.Harness)
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
	if deps.ProcessRunner == nil {
		runner, err := harness.NewProcessRunner(cfg.AgentTypes[0].Docker)
		if err != nil {
			return nil, err
		}
		deps.ProcessRunner = runner
	}

	svc := &Service{
		cfg:                 cfg,
		logger:              logger,
		source:              cfg.Sources[0],
		agent:               cfg.AgentTypes[0],
		tracker:             deps.Tracker,
		harness:             deps.Harness,
		processRunner:       deps.ProcessRunner,
		workspace:           deps.Workspace,
		claimed:             map[string]struct{}{},
		finished:            map[string]state.TerminalIssue{},
		retryQueue:          map[string]state.RetryEntry{},
		activeRuns:          map[string]*domain.AgentRun{},
		stateStore:          deps.StateStore,
		limiter:             deps.Limiter,
		pendingStops:        map[string]pendingStop{},
		approvals:           map[string]ApprovalView{},
		messages:            map[string]MessageView{},
		messageWaiters:      map[string]chan string{},
		runOutputs:          map[string]*runOutputBuffer{},
		forcePollCh:         make(chan struct{}, 1),
		controlCh:           make(chan struct{}, 1),
		globalMaxConcurrent: cfg.Defaults.MaxConcurrentGlobal,
		metricsStore:        deps.MetricsStore,
	}
	if svc.metricsStore == nil {
		svc.metricsStore = newRuntimeMetricsStore()
	}
	svc.stateMgr = &stateManager{service: svc}
	svc.approvalMgr = &approvalRouter{service: svc}
	svc.messageMgr = &messageRouter{service: svc}
	svc.runMgr = &runManager{service: svc}
	if err := svc.stateMgr.restoreState(); err != nil {
		logger.Warn("restore state failed", "error", err)
	}
	return svc, nil
}

func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	activeRuns := s.activeRunSnapshotsLocked(time.Now())
	var activeRun *domain.AgentRun
	if len(activeRuns) > 0 {
		activeRun = &activeRuns[0]
	}

	events := filterRecentEvents(append([]Event(nil), s.events...))
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
			StdoutTail: sanitizeOutput(output.stdout.String()),
			StderrTail: sanitizeOutput(output.stderr.String()),
			UpdatedAt:  updatedAt,
		})
	}
	runIdentifiers := make(map[string]string, len(activeRuns))
	for _, run := range activeRuns {
		runIdentifiers[run.ID] = run.Issue.Identifier
	}
	for i := range runOutputs {
		if identifier, ok := runIdentifiers[runOutputs[i].RunID]; ok {
			runOutputs[i].IssueIdentifier = identifier
		}
	}
	sort.Slice(runOutputs, func(i, j int) bool {
		if runOutputs[i].UpdatedAt.Equal(runOutputs[j].UpdatedAt) {
			return runOutputs[i].RunID < runOutputs[j].RunID
		}
		return runOutputs[i].UpdatedAt.After(runOutputs[j].UpdatedAt)
	})
	sourceMetrics := aggregateMetricsForSource(s.metricsStore.snapshotForSource(s.source.Name), activeRuns)
	harnessRuns := activeRunsForHarness(activeRuns, s.agent.Harness)
	harnessMetrics := []MetricBreakdown{{
		Name:    s.agent.Harness,
		Metrics: aggregateMetricsForSource(s.metricsStore.snapshotForHarness(s.agent.Harness), harnessRuns),
	}}
	instanceMetrics := aggregateMetricsForSource(s.metricsStore.snapshotForSource(s.source.Name), activeRuns)
	return Snapshot{
		SourceName:       s.source.Name,
		SourceTracker:    s.source.Tracker,
		LastPollAt:       s.lastPollAt,
		LastPollCount:    s.lastPollCount,
		ClaimedCount:     len(s.claimed),
		RetryCount:       len(s.retryQueue),
		InstanceMetrics:  instanceMetrics,
		HarnessMetrics:   harnessMetrics,
		PendingApprovals: pendingApprovals,
		PendingMessages:  pendingMessages,
		Retries:          retries,
		ApprovalHistory:  history,
		MessageHistory:   messageHistory,
		ActiveRun:        activeRun,
		ActiveRuns:       activeRuns,
		RunOutputs:       runOutputs,
		SourceSummaries:  []SourceSummary{sourceSummaryForSnapshot(s.source, s.agent, s.globalMaxConcurrent, s.tracker, s.lastPollAt, s.lastPollCount, len(s.claimed), len(s.retryQueue), len(activeRuns), len(pendingApprovals), len(pendingMessages), sourceMetrics)},
		RecentEvents:     events,
	}
}

func (s *Service) activeRunSnapshotsLocked(now time.Time) []domain.AgentRun {
	if len(s.activeRunOrder) == 0 {
		if s.activeRun == nil {
			return nil
		}
		copyRun := *s.activeRun
		copyRun.Metrics = domain.DeriveRunMetrics(copyRun.Metrics, copyRun.StartedAt, copyRun.CompletedAt, now)
		return []domain.AgentRun{copyRun}
	}
	runs := make([]domain.AgentRun, 0, len(s.activeRunOrder))
	for _, runID := range s.activeRunOrder {
		run := s.activeRuns[runID]
		if run == nil {
			continue
		}
		copyRun := *run
		copyRun.Metrics = domain.DeriveRunMetrics(copyRun.Metrics, copyRun.StartedAt, copyRun.CompletedAt, now)
		runs = append(runs, copyRun)
	}
	return runs
}

func (s *Service) activeRunCountLocked() int {
	if len(s.activeRunOrder) == 0 {
		if s.activeRun != nil {
			return 1
		}
		return 0
	}
	return len(s.activeRunOrder)
}

func activeRunsForHarness(runs []domain.AgentRun, harnessKind string) []domain.AgentRun {
	if len(runs) == 0 || harnessKind == "" {
		return nil
	}
	filtered := make([]domain.AgentRun, 0, len(runs))
	for _, run := range runs {
		if run.HarnessKind == harnessKind {
			filtered = append(filtered, run)
		}
	}
	return filtered
}

func (s *Service) activeRunByIDLocked(runID string) *domain.AgentRun {
	if len(s.activeRunOrder) == 0 && s.activeRun != nil && s.activeRun.ID == runID {
		return s.activeRun
	}
	return s.activeRuns[runID]
}

func (s *Service) setActiveRunLocked(run *domain.AgentRun) {
	if run == nil {
		return
	}
	if s.activeRuns == nil {
		s.activeRuns = map[string]*domain.AgentRun{}
	}
	if _, exists := s.activeRuns[run.ID]; !exists {
		s.activeRunOrder = append(s.activeRunOrder, run.ID)
	}
	s.activeRuns[run.ID] = run
	s.syncPrimaryActiveRunLocked()
}

func (s *Service) removeActiveRunLocked(runID string) *domain.AgentRun {
	run := s.activeRunByIDLocked(runID)
	if run == nil {
		return nil
	}
	if len(s.activeRunOrder) == 0 {
		s.activeRun = nil
		return run
	}
	delete(s.activeRuns, runID)
	s.activeRunOrder = removeFromOrder(s.activeRunOrder, runID)
	s.syncPrimaryActiveRunLocked()
	return run
}

func (s *Service) activeRunsAtCapacityLocked() bool {
	return s.activeRunCountLocked() >= s.source.EffectiveMaxActiveRuns()
}

func (s *Service) concurrencyCapsLocked() (int, int, int, int) {
	sourceCap := s.source.EffectiveMaxActiveRuns()
	agentCap := effectivePositiveCap(s.agent.MaxConcurrent)
	globalCap := effectivePositiveCap(s.globalMaxConcurrent)
	return sourceCap, agentCap, globalCap, effectivePositiveCap(sourceCap, agentCap, globalCap)
}

func (s *Service) availableRunCapacityLocked() int {
	remaining := s.source.EffectiveMaxActiveRuns() - s.activeRunCountLocked()
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (s *Service) syncPrimaryActiveRunLocked() {
	s.activeRun = nil
	for _, runID := range s.activeRunOrder {
		if run := s.activeRuns[runID]; run != nil {
			s.activeRun = run
			return
		}
	}
}

func sourceSummaryForSnapshot(source config.SourceConfig, agent config.AgentTypeConfig, globalMaxConcurrent int, sourceTracker tracker.Tracker, lastPollAt time.Time, lastPollCount int, claimedCount int, retryCount int, activeRunCount int, pendingApprovals int, pendingMessages int, metrics domain.RunMetrics) SourceSummary {
	var rateLimit *domain.TrackerRateLimit
	if reporter, ok := sourceTracker.(tracker.RateLimitReporter); ok {
		rateLimit = domain.CloneTrackerRateLimit(reporter.RateLimit())
	}
	sourceMax := source.EffectiveMaxActiveRuns()
	agentMax := effectivePositiveCap(agent.MaxConcurrent)
	globalMax := effectivePositiveCap(globalMaxConcurrent)
	return SourceSummary{
		Name:                   source.Name,
		DisplayGroup:           source.DisplayGroup,
		Tags:                   append([]string(nil), source.Tags...),
		Tracker:                source.Tracker,
		RateLimit:              rateLimit,
		ProjectURL:             source.ProjectURL,
		FilterStates:           append([]string(nil), source.Filter.States...),
		FilterLabels:           append([]string(nil), source.Filter.Labels...),
		Execution:              summarizeExecution(agent),
		LastPollAt:             lastPollAt,
		LastPollCount:          lastPollCount,
		ClaimedCount:           claimedCount,
		RetryCount:             retryCount,
		ActiveRunCount:         activeRunCount,
		MaxActiveRuns:          sourceMax,
		AgentMaxConcurrent:     agentMax,
		GlobalMaxConcurrent:    globalMax,
		EffectiveMaxConcurrent: effectivePositiveCap(sourceMax, agentMax, globalMax),
		Metrics:                metrics,
		PendingApprovals:       pendingApprovals,
		PendingMessages:        pendingMessages,
	}
}

func effectivePositiveCap(values ...int) int {
	best := 0
	for _, value := range values {
		if value < 1 {
			continue
		}
		if best == 0 || value < best {
			best = value
		}
	}
	if best == 0 {
		return 1
	}
	return best
}

func (s *Service) ApplyLiveConcurrency(source config.SourceConfig, agent config.AgentTypeConfig, globalMaxConcurrent int) {
	s.mu.Lock()
	s.source.MaxActiveRuns = source.MaxActiveRuns
	s.agent.MaxConcurrent = agent.MaxConcurrent
	s.globalMaxConcurrent = globalMaxConcurrent
	s.mu.Unlock()

	s.recordSourceEvent(
		"info",
		s.source.Name,
		"applied live concurrency update: source %d, agent %d, global %d, effective %d",
		source.EffectiveMaxActiveRuns(),
		effectivePositiveCap(agent.MaxConcurrent),
		effectivePositiveCap(globalMaxConcurrent),
		effectivePositiveCap(source.EffectiveMaxActiveRuns(), agent.MaxConcurrent, globalMaxConcurrent),
	)
}

func summarizeExecution(agent config.AgentTypeConfig) *ExecutionSummary {
	summary := &ExecutionSummary{
		Mode:       "host",
		AuthSource: "host",
	}
	if agent.Docker == nil {
		return summary
	}
	docker := config.ResolveDockerConfig(nil, agent.Docker)
	summary.Mode = "docker"
	if docker.Reuse != nil {
		summary.ReuseMode = config.NormalizeDockerReuseMode(docker.Reuse.Mode)
	}
	summary.Image = docker.Image
	summary.Network = config.EffectiveDockerNetwork(&docker)
	if docker.NetworkPolicy != nil {
		summary.NetworkPolicyMode = config.NormalizeDockerNetworkPolicyMode(docker.NetworkPolicy.Mode)
		summary.NetworkAllow = append([]string(nil), docker.NetworkPolicy.Allow...)
	}
	summary.CPUs = docker.CPUs
	summary.Memory = strings.TrimSpace(docker.Memory)
	summary.PIDsLimit = docker.PIDsLimit
	summary.AuthSource = summarizeDockerAuthSource(agent)
	if docker.Security != nil {
		summary.SecurityPreset = strings.TrimSpace(docker.Security.Preset)
	}
	homeTarget := config.DockerHomeDefault
	if value := strings.TrimSpace(agent.Env["HOME"]); value != "" {
		homeTarget = value
	}
	access := config.ResolveDockerAccess(agent.Docker, homeTarget)
	summary.EnvCount = len(access.Env)
	summary.SecretMountCount = len(access.SecretMounts)
	summary.ToolMountCount = len(access.ToolMounts)
	return summary
}

func summarizeDockerAuthSource(agent config.AgentTypeConfig) string {
	if agent.Docker == nil {
		return "host"
	}

	homeTarget := config.DockerHomeDefault
	if value := strings.TrimSpace(agent.Env["HOME"]); value != "" {
		homeTarget = value
	}
	access := config.ResolveDockerAccess(agent.Docker, homeTarget)
	envAuth := false
	for _, grant := range access.Env {
		if isDockerAuthEnv(grant.Target) {
			envAuth = true
			break
		}
	}

	mountAuth := false
	for _, grant := range access.SecretMounts {
		if config.DockerMountLooksSecret(grant.Source, grant.Target) {
			mountAuth = true
			break
		}
	}

	switch {
	case envAuth && mountAuth:
		return "env + mounted config"
	case envAuth:
		return "env"
	case mountAuth:
		return "mounted config"
	default:
		if agent.Docker.Auth == nil {
			return "none"
		}
		return "preset"
	}
}

func isDockerAuthEnv(key string) bool {
	switch strings.ToUpper(strings.TrimSpace(key)) {
	case "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "OPENAI_API_KEY":
		return true
	default:
		return false
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
