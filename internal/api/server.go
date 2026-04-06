package api

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/harness"
	"github.com/tjohnson/maestro/internal/ops"
	"github.com/tjohnson/maestro/internal/orchestrator"
	"gopkg.in/yaml.v3"
)

const shutdownTimeout = 5 * time.Second
const streamTickInterval = time.Second

//go:embed static/index.html static/app
var embeddedFrontend embed.FS

type runtimeView interface {
	Snapshot() orchestrator.Snapshot
	ResolveApproval(requestID string, decision string) error
	ResolveMessage(requestID string, reply string, resolvedVia string) error
	StopRun(runID string, reason string) error
	RequestForcePoll(sourceName string) (orchestrator.ForcePollResult, error)
}

type Server struct {
	addr          string
	apiKey        string
	logger        *slog.Logger
	runtime       runtimeView
	configSummary ops.ConfigSummary
	configMu      sync.RWMutex
	httpServer    *http.Server
	frontendDir   string
	frontendFS    fs.FS
}

func (s *Server) getConfigSummary() ops.ConfigSummary {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.configSummary
}

func (s *Server) setConfigSummary(summary ops.ConfigSummary) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.configSummary = summary
}

type statusResponse struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Config      ops.ConfigSummary `json:"config"`
	Snapshot    snapshotJSON      `json:"snapshot"`
}

type collectionResponse struct {
	GeneratedAt time.Time `json:"generated_at"`
	Count       int       `json:"count"`
}

type configRawResponse struct {
	GeneratedAt time.Time `json:"generated_at"`
	ConfigPath  string    `json:"config_path,omitempty"`
	Editable    bool      `json:"editable"`
	YAML        string    `json:"yaml,omitempty"`
}

type configValidateRequest struct {
	YAML string `json:"yaml"`
}

type packSaveRequest struct {
	OriginalName   string   `json:"original_name"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	InstanceName   string   `json:"instance_name"`
	Harness        string   `json:"harness"`
	Workspace      string   `json:"workspace"`
	ApprovalPolicy string   `json:"approval_policy"`
	MaxConcurrent  int      `json:"max_concurrent"`
	Tools          []string `json:"tools"`
	Skills         []string `json:"skills"`
	EnvKeys        []string `json:"env_keys"`
	PromptBody     string   `json:"prompt_body"`
	ContextBody    string   `json:"context_body"`
}

type packSaveResponse struct {
	OK            bool               `json:"ok"`
	GeneratedAt   time.Time          `json:"generated_at"`
	RestartNeeded bool               `json:"restart_needed"`
	ValidationErr string             `json:"validation_error,omitempty"`
	Config        *ops.ConfigSummary `json:"config,omitempty"`
}

type configValidateResponse struct {
	OK            bool               `json:"ok"`
	GeneratedAt   time.Time          `json:"generated_at"`
	ConfigPath    string             `json:"config_path,omitempty"`
	Editable      bool               `json:"editable"`
	RestartNeeded bool               `json:"restart_needed"`
	ValidationErr string             `json:"validation_error,omitempty"`
	Diff          string             `json:"diff,omitempty"`
	Config        *ops.ConfigSummary `json:"config,omitempty"`
}

type configBackupSummary struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"created_at"`
}

type configBackupResponse struct {
	GeneratedAt time.Time             `json:"generated_at"`
	ConfigPath  string                `json:"config_path,omitempty"`
	Count       int                   `json:"count"`
	Items       []configBackupSummary `json:"items"`
}

type configBackupDetailResponse struct {
	GeneratedAt time.Time            `json:"generated_at"`
	ConfigPath  string               `json:"config_path,omitempty"`
	Backup      *configBackupSummary `json:"backup,omitempty"`
	YAML        string               `json:"yaml,omitempty"`
	Diff        string               `json:"diff,omitempty"`
}

type snapshotJSON struct {
	SourceName       string                `json:"source_name,omitempty"`
	SourceTracker    string                `json:"source_tracker,omitempty"`
	LastPollAt       time.Time             `json:"last_poll_at,omitempty"`
	LastPollCount    int                   `json:"last_poll_count"`
	ClaimedCount     int                   `json:"claimed_count"`
	RetryCount       int                   `json:"retry_count"`
	InstanceMetrics  *runMetricsJSON       `json:"instance_metrics,omitempty"`
	HarnessMetrics   []metricBreakdownJSON `json:"harness_metrics,omitempty"`
	PendingApprovals []approvalJSON        `json:"pending_approvals,omitempty"`
	PendingMessages  []messageJSON         `json:"pending_messages,omitempty"`
	Retries          []retryJSON           `json:"retries,omitempty"`
	ApprovalHistory  []approvalHistoryJSON `json:"approval_history,omitempty"`
	MessageHistory   []messageHistoryJSON  `json:"message_history,omitempty"`
	ActiveRun        *runJSON              `json:"active_run,omitempty"`
	ActiveRuns       []runJSON             `json:"active_runs,omitempty"`
	RunOutputs       []runOutputJSON       `json:"run_outputs,omitempty"`
	SourceSummaries  []sourceSummaryJSON   `json:"source_summaries,omitempty"`
	RecentEvents     []eventJSON           `json:"recent_events,omitempty"`
}

type sourceSummaryJSON struct {
	Name                   string                `json:"name"`
	DisplayGroup           string                `json:"display_group,omitempty"`
	Tags                   []string              `json:"tags,omitempty"`
	Tracker                string                `json:"tracker"`
	RateLimit              *trackerRateLimitJSON `json:"rate_limit,omitempty"`
	Execution              *executionJSON        `json:"execution,omitempty"`
	LastPollAt             time.Time             `json:"last_poll_at,omitempty"`
	LastPollCount          int                   `json:"last_poll_count"`
	ClaimedCount           int                   `json:"claimed_count"`
	RetryCount             int                   `json:"retry_count"`
	ActiveRunCount         int                   `json:"active_run_count"`
	MaxActiveRuns          int                   `json:"max_active_runs"`
	AgentMaxConcurrent     int                   `json:"agent_max_concurrent"`
	GlobalMaxConcurrent    int                   `json:"global_max_concurrent"`
	EffectiveMaxConcurrent int                   `json:"effective_max_concurrent"`
	Metrics                *runMetricsJSON       `json:"metrics,omitempty"`
	PendingApprovals       int                   `json:"pending_approvals"`
	PendingMessages        int                   `json:"pending_messages"`
}

type metricBreakdownJSON struct {
	Name    string          `json:"name"`
	Metrics *runMetricsJSON `json:"metrics,omitempty"`
}

type issueJSON struct {
	ID          string    `json:"id,omitempty"`
	Identifier  string    `json:"identifier,omitempty"`
	Title       string    `json:"title,omitempty"`
	Description string    `json:"description,omitempty"`
	URL         string    `json:"url,omitempty"`
	State       string    `json:"state,omitempty"`
	Labels      []string  `json:"labels,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

type runJSON struct {
	ID             string          `json:"id"`
	AgentName      string          `json:"agent_name"`
	AgentType      string          `json:"agent_type,omitempty"`
	Issue          issueJSON       `json:"issue"`
	SourceName     string          `json:"source_name"`
	HarnessKind    string          `json:"harness_kind,omitempty"`
	Execution      *executionJSON  `json:"execution,omitempty"`
	WorkspacePath  string          `json:"workspace_path,omitempty"`
	Status         string          `json:"status"`
	CurrentTurn    int             `json:"current_turn,omitempty"`
	MaxTurns       int             `json:"max_turns,omitempty"`
	Attempt        int             `json:"attempt"`
	ApprovalPolicy string          `json:"approval_policy,omitempty"`
	ApprovalState  string          `json:"approval_state,omitempty"`
	StartedAt      time.Time       `json:"started_at,omitempty"`
	LastActivityAt time.Time       `json:"last_activity_at,omitempty"`
	CompletedAt    time.Time       `json:"completed_at,omitempty"`
	Metrics        *runMetricsJSON `json:"metrics,omitempty"`
	Error          string          `json:"error,omitempty"`
	Output         *runOutputJSON  `json:"output,omitempty"`
}

type runMetricsJSON struct {
	TokensIn                  *int64    `json:"tokens_in,omitempty"`
	TokensOut                 *int64    `json:"tokens_out,omitempty"`
	TotalTokens               *int64    `json:"total_tokens,omitempty"`
	CostUSD                   *float64  `json:"cost_usd,omitempty"`
	DurationMS                *int64    `json:"duration_ms,omitempty"`
	ThroughputTokensPerSecond *float64  `json:"throughput_tokens_per_second,omitempty"`
	UpdatedAt                 time.Time `json:"updated_at,omitempty"`
}

type trackerRateLimitJSON struct {
	Limit             *int64    `json:"limit,omitempty"`
	Remaining         *int64    `json:"remaining,omitempty"`
	ResetAt           time.Time `json:"reset_at,omitempty"`
	RetryAfterSeconds *int64    `json:"retry_after_seconds,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

type executionJSON struct {
	Mode              string   `json:"mode"`
	ReuseMode         string   `json:"reuse_mode,omitempty"`
	Reused            bool     `json:"reused,omitempty"`
	ContainerID       string   `json:"container_id,omitempty"`
	ContainerName     string   `json:"container_name,omitempty"`
	ProfileKey        string   `json:"profile_key,omitempty"`
	LineageKey        string   `json:"lineage_key,omitempty"`
	Image             string   `json:"image,omitempty"`
	Network           string   `json:"network,omitempty"`
	NetworkPolicyMode string   `json:"network_policy_mode,omitempty"`
	NetworkAllow      []string `json:"network_allow,omitempty"`
	CPUs              float64  `json:"cpus,omitempty"`
	Memory            string   `json:"memory,omitempty"`
	PIDsLimit         int      `json:"pids_limit,omitempty"`
	AuthSource        string   `json:"auth_source,omitempty"`
	SecurityPreset    string   `json:"security_preset,omitempty"`
	EnvCount          int      `json:"env_count,omitempty"`
	SecretMountCount  int      `json:"secret_mount_count,omitempty"`
	ToolMountCount    int      `json:"tool_mount_count,omitempty"`
}

type runOutputJSON struct {
	RunID           string    `json:"run_id"`
	SourceName      string    `json:"source_name"`
	IssueIdentifier string    `json:"issue_identifier,omitempty"`
	StdoutTail      string    `json:"stdout_tail,omitempty"`
	StderrTail      string    `json:"stderr_tail,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitempty"`
}

type retryJSON struct {
	IssueID         string    `json:"issue_id,omitempty"`
	IssueIdentifier string    `json:"issue_identifier"`
	SourceName      string    `json:"source_name"`
	Attempt         int       `json:"attempt"`
	DueAt           time.Time `json:"due_at,omitempty"`
	Error           string    `json:"error,omitempty"`
}

type approvalJSON struct {
	RequestID       string    `json:"request_id"`
	RunID           string    `json:"run_id,omitempty"`
	IssueID         string    `json:"issue_id,omitempty"`
	IssueIdentifier string    `json:"issue_identifier,omitempty"`
	AgentName       string    `json:"agent_name,omitempty"`
	ToolName        string    `json:"tool_name,omitempty"`
	ToolInput       string    `json:"tool_input,omitempty"`
	ApprovalPolicy  string    `json:"approval_policy,omitempty"`
	RequestedAt     time.Time `json:"requested_at,omitempty"`
	Resolvable      bool      `json:"resolvable"`
}

type approvalHistoryJSON struct {
	RequestID       string    `json:"request_id"`
	RunID           string    `json:"run_id,omitempty"`
	IssueID         string    `json:"issue_id,omitempty"`
	IssueIdentifier string    `json:"issue_identifier,omitempty"`
	AgentName       string    `json:"agent_name,omitempty"`
	ToolName        string    `json:"tool_name,omitempty"`
	ApprovalPolicy  string    `json:"approval_policy,omitempty"`
	Decision        string    `json:"decision,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	RequestedAt     time.Time `json:"requested_at,omitempty"`
	DecidedAt       time.Time `json:"decided_at,omitempty"`
	Outcome         string    `json:"outcome,omitempty"`
}

type messageJSON struct {
	RequestID       string    `json:"request_id"`
	RunID           string    `json:"run_id,omitempty"`
	IssueID         string    `json:"issue_id,omitempty"`
	IssueIdentifier string    `json:"issue_identifier,omitempty"`
	SourceName      string    `json:"source_name,omitempty"`
	AgentName       string    `json:"agent_name,omitempty"`
	Kind            string    `json:"kind,omitempty"`
	Summary         string    `json:"summary,omitempty"`
	Body            string    `json:"body,omitempty"`
	RequestedAt     time.Time `json:"requested_at,omitempty"`
	Resolvable      bool      `json:"resolvable"`
}

type messageHistoryJSON struct {
	RequestID       string    `json:"request_id"`
	RunID           string    `json:"run_id,omitempty"`
	IssueID         string    `json:"issue_id,omitempty"`
	IssueIdentifier string    `json:"issue_identifier,omitempty"`
	SourceName      string    `json:"source_name,omitempty"`
	AgentName       string    `json:"agent_name,omitempty"`
	Kind            string    `json:"kind,omitempty"`
	Summary         string    `json:"summary,omitempty"`
	Body            string    `json:"body,omitempty"`
	Reply           string    `json:"reply,omitempty"`
	ResolvedVia     string    `json:"resolved_via,omitempty"`
	RequestedAt     time.Time `json:"requested_at,omitempty"`
	RepliedAt       time.Time `json:"replied_at,omitempty"`
	Outcome         string    `json:"outcome,omitempty"`
}

type eventJSON struct {
	Time    time.Time `json:"time,omitempty"`
	Level   string    `json:"level,omitempty"`
	Source  string    `json:"source,omitempty"`
	RunID   string    `json:"run_id,omitempty"`
	Issue   string    `json:"issue,omitempty"`
	Message string    `json:"message,omitempty"`
}

type streamUpdate struct {
	GeneratedAt time.Time `json:"generated_at"`
	Snapshot    struct {
		SourceCount      int `json:"source_count"`
		ActiveRunCount   int `json:"active_run_count"`
		RetryCount       int `json:"retry_count"`
		ApprovalCount    int `json:"approval_count"`
		RecentEventCount int `json:"recent_event_count"`
	} `json:"snapshot"`
}

type configSummaryProvider interface {
	ConfigSummary() ops.ConfigSummary
}

func New(cfg *config.Config, logger *slog.Logger, runtime runtimeView) *Server {
	apiKey := strings.TrimSpace(cfg.Server.APIKey)
	ephemeralKey := false
	if apiKey == "" && requiresAPIKey(cfg.Server.Host) {
		apiKey = generateAPIKey()
		ephemeralKey = true
	}
	server := &Server{
		addr:          net.JoinHostPort(cfg.Server.Host, fmt.Sprintf("%d", cfg.Server.Port)),
		apiKey:        apiKey,
		logger:        logger,
		runtime:       runtime,
		configSummary: ops.SummarizeConfig(cfg),
		frontendDir:   findFrontendDistDir(),
		frontendFS:    embeddedFrontendFS(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", server.handleIndex)
	mux.HandleFunc("/healthz", server.handleHealth)
	mux.HandleFunc("/api/v1/stream", server.handleStream)
	mux.HandleFunc("/api/v1/status", server.handleStatus)
	mux.HandleFunc("/api/v1/config", server.handleConfig)
	mux.HandleFunc("/api/v1/config/raw", server.handleConfigRaw)
	mux.HandleFunc("/api/v1/config/validate", server.handleConfigValidate)
	mux.HandleFunc("/api/v1/config/dry-run", server.handleConfigDryRun)
	mux.HandleFunc("/api/v1/config/save", server.handleConfigSave)
	mux.HandleFunc("/api/v1/config/backups", server.handleConfigBackups)
	mux.HandleFunc("/api/v1/config/backups/create", server.handleConfigBackupCreate)
	mux.HandleFunc("/api/v1/config/backups/", server.handleConfigBackupDetail)
	mux.HandleFunc("/api/v1/poll", server.handleForcePoll)
	mux.HandleFunc("/api/v1/sources", server.handleSources)
	mux.HandleFunc("/api/v1/sources/", server.handleSourceAction)
	mux.HandleFunc("/api/v1/runs", server.handleRuns)
	mux.HandleFunc("/api/v1/retries", server.handleRetries)
	mux.HandleFunc("/api/v1/events", server.handleEvents)
	mux.HandleFunc("/api/v1/approvals", server.handleApprovals)
	mux.HandleFunc("/api/v1/approvals/", server.handleApprovalAction)
	mux.HandleFunc("/api/v1/messages", server.handleMessages)
	mux.HandleFunc("/api/v1/messages/", server.handleMessageAction)
	mux.HandleFunc("/api/v1/runs/", server.handleRunAction)
	mux.HandleFunc("/api/v1/packs/save", server.handlePackSave)

	server.httpServer = &http.Server{
		Addr:              server.addr,
		Handler:           server.withAPIAuth(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if ephemeralKey {
		logger.Info("generated ephemeral api key for web api", "addr", server.addr, "api_key_hint", apiKey[:8]+"…")
	}
	return server
}

func (s *Server) currentConfigSummary() ops.ConfigSummary {
	if provider, ok := s.runtime.(configSummaryProvider); ok {
		return provider.ConfigSummary()
	}
	return s.getConfigSummary()
}

func (s *Server) Addr() string {
	return s.addr
}

func generateAPIKey() string {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("maestro-%d", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(raw)
}

func requiresAPIKey(host string) bool {
	trimmed := strings.Trim(strings.TrimSpace(host), "[]")
	if trimmed == "" || strings.EqualFold(trimmed, "localhost") {
		return false
	}
	ip := net.ParseIP(trimmed)
	if ip != nil {
		return !ip.IsLoopback()
	}
	return true
}

func (s *Server) withAPIAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if s.authorized(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
	})
}

func (s *Server) authorized(r *http.Request) bool {
	if strings.TrimSpace(s.apiKey) == "" {
		return true
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(auth, "Bearer ") && strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")) == s.apiKey {
		return true
	}
	if strings.TrimSpace(r.URL.Query().Get("api_key")) == s.apiKey {
		return true
	}
	return false
}

func (s *Server) Run(ctx context.Context) error {
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	s.httpServer.BaseContext = func(net.Listener) context.Context {
		return serverCtx
	}
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("api server listening", "addr", s.addr)
		err := s.httpServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		cancelServer()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return <-errCh
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	if s.serveFrontendFile(w, r) {
		return
	}
	http.NotFound(w, r)
}

func (s *Server) serveFrontendFile(w http.ResponseWriter, r *http.Request) bool {
	if strings.TrimSpace(s.frontendDir) == "" {
		return s.serveEmbeddedFrontendFile(w, r)
	}
	cleanURLPath := path.Clean("/" + strings.TrimSpace(r.URL.Path))
	if cleanURLPath == "." {
		cleanURLPath = "/"
	}
	relativePath := strings.TrimPrefix(cleanURLPath, "/")
	if relativePath == "" {
		relativePath = "index.html"
	}
	assetPath := filepath.Join(s.frontendDir, filepath.FromSlash(relativePath))
	if info, err := os.Stat(assetPath); err == nil && !info.IsDir() {
		http.ServeFile(w, r, assetPath)
		return true
	}
	indexPath := filepath.Join(s.frontendDir, "index.html")
	if _, err := os.Stat(indexPath); err == nil {
		http.ServeFile(w, r, indexPath)
		return true
	}
	return s.serveEmbeddedFrontendFile(w, r)
}

func findFrontendDistDir() string {
	candidates := []string{
		filepath.Join("web", "dist"),
		filepath.Join("..", "..", "web", "dist"),
	}
	for _, candidate := range candidates {
		indexPath := filepath.Join(candidate, "index.html")
		if info, err := os.Stat(indexPath); err == nil && !info.IsDir() {
			abs, absErr := filepath.Abs(candidate)
			if absErr == nil {
				return abs
			}
			return candidate
		}
	}
	return ""
}

func embeddedFrontendFS() fs.FS {
	sub, err := fs.Sub(embeddedFrontend, "static/app")
	if err != nil {
		return nil
	}
	return sub
}

func (s *Server) serveEmbeddedFrontendFile(w http.ResponseWriter, r *http.Request) bool {
	if s.frontendFS == nil {
		return false
	}
	cleanURLPath := path.Clean("/" + strings.TrimSpace(r.URL.Path))
	if cleanURLPath == "." {
		cleanURLPath = "/"
	}
	relativePath := strings.TrimPrefix(cleanURLPath, "/")
	if relativePath == "" {
		relativePath = "index.html"
	}
	if file, err := s.frontendFS.Open(relativePath); err == nil {
		_ = file.Close()
		http.FileServerFS(s.frontendFS).ServeHTTP(w, r)
		return true
	}
	indexFile, err := s.frontendFS.Open("index.html")
	if err != nil {
		return false
	}
	_ = indexFile.Close()
	rewritten := r.Clone(r.Context())
	rewritten.URL.Path = "/index.html"
	http.FileServerFS(s.frontendFS).ServeHTTP(w, rewritten)
	return true
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"time": time.Now().UTC(),
	})
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ctx := r.Context()
	lastPayload := ""
	send := func() error {
		payload := s.streamPayload()
		stableRaw, err := json.Marshal(payload.Snapshot)
		if err != nil {
			return err
		}
		payloadText := string(stableRaw)
		if payloadText == lastPayload {
			return nil
		}
		lastPayload = payloadText
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: update\ndata: %s\n\n", raw); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	if err := send(); err != nil {
		s.logger.Warn("stream send failed", "error", err)
		return
	}

	ticker := time.NewTicker(streamTickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := send(); err != nil {
				s.logger.Warn("stream send failed", "error", err)
				return
			}
		}
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	summary := s.currentConfigSummary()
	writeJSON(w, http.StatusOK, statusResponse{
		GeneratedAt: time.Now().UTC(),
		Config:      summary,
		Snapshot:    encodeSnapshot(s.runtime.Snapshot()),
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, s.currentConfigSummary())
}

func (s *Server) handleConfigRaw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	raw, editable, err := s.readConfigRaw()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	summary := s.currentConfigSummary()
	writeJSON(w, http.StatusOK, configRawResponse{
		GeneratedAt: time.Now().UTC(),
		ConfigPath:  summary.ConfigPath,
		Editable:    editable,
		YAML:        raw,
	})
}

func (s *Server) handleConfigValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	req, ok := decodeConfigValidateRequest(w, r)
	if !ok {
		return
	}
	var cfg *config.Config
	cfg, err := config.LoadBytesWithEnv(s.validationConfigPath(), []byte(req.YAML), s.validationEnvLookup())
	if err == nil {
		err = config.ValidateMVP(cfg)
	}
	currentSummary := s.currentConfigSummary()
	if err != nil {
		writeJSON(w, http.StatusOK, configValidateResponse{
			OK:            false,
			GeneratedAt:   time.Now().UTC(),
			ConfigPath:    currentSummary.ConfigPath,
			Editable:      currentSummary.ConfigPath != "",
			RestartNeeded: false,
			ValidationErr: err.Error(),
		})
		return
	}
	summary := ops.SummarizeConfig(cfg)
	writeJSON(w, http.StatusOK, configValidateResponse{
		OK:            true,
		GeneratedAt:   time.Now().UTC(),
		ConfigPath:    summary.ConfigPath,
		Editable:      summary.ConfigPath != "",
		RestartNeeded: false,
		Config:        &summary,
	})
}

func (s *Server) handleConfigDryRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	req, ok := decodeConfigValidateRequest(w, r)
	if !ok {
		return
	}
	current, editable, err := s.readConfigRaw()
	currentSummary := s.currentConfigSummary()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, configValidateResponse{
			OK:            false,
			GeneratedAt:   time.Now().UTC(),
			ConfigPath:    currentSummary.ConfigPath,
			Editable:      false,
			RestartNeeded: false,
			ValidationErr: err.Error(),
		})
		return
	}
	var cfg *config.Config
	cfg, err = config.LoadBytesWithEnv(s.validationConfigPath(), []byte(req.YAML), s.validationEnvLookup())
	if err == nil {
		err = config.ValidateMVP(cfg)
	}
	if err != nil {
		writeJSON(w, http.StatusOK, configValidateResponse{
			OK:            false,
			GeneratedAt:   time.Now().UTC(),
			ConfigPath:    currentSummary.ConfigPath,
			Editable:      editable,
			RestartNeeded: false,
			ValidationErr: err.Error(),
			Diff:          unifiedConfigDiff(current, req.YAML),
		})
		return
	}
	summary := ops.SummarizeConfig(cfg)
	writeJSON(w, http.StatusOK, configValidateResponse{
		OK:            true,
		GeneratedAt:   time.Now().UTC(),
		ConfigPath:    summary.ConfigPath,
		Editable:      editable,
		RestartNeeded: false,
		Diff:          unifiedConfigDiff(current, req.YAML),
		Config:        &summary,
	})
}

func (s *Server) handleConfigSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	req, ok := decodeConfigValidateRequest(w, r)
	if !ok {
		return
	}
	currentSummary := s.currentConfigSummary()
	if strings.TrimSpace(currentSummary.ConfigPath) == "" {
		writeJSON(w, http.StatusBadRequest, configValidateResponse{
			OK:            false,
			GeneratedAt:   time.Now().UTC(),
			Editable:      false,
			ValidationErr: "config file is not editable in this runtime",
		})
		return
	}
	var cfg *config.Config
	cfg, err := config.LoadBytesWithEnv(currentSummary.ConfigPath, []byte(req.YAML), s.validationEnvLookup())
	if err == nil {
		err = config.ValidateMVP(cfg)
	}
	current, _, currentErr := s.readConfigRaw()
	if currentErr != nil {
		current = ""
	}
	if err != nil {
		writeJSON(w, http.StatusOK, configValidateResponse{
			OK:            false,
			GeneratedAt:   time.Now().UTC(),
			ConfigPath:    currentSummary.ConfigPath,
			Editable:      true,
			RestartNeeded: false,
			ValidationErr: err.Error(),
			Diff:          unifiedConfigDiff(current, req.YAML),
		})
		return
	}
	if err := writeConfigAtomically(currentSummary.ConfigPath, []byte(req.YAML)); err != nil {
		writeJSON(w, http.StatusInternalServerError, configValidateResponse{
			OK:            false,
			GeneratedAt:   time.Now().UTC(),
			ConfigPath:    currentSummary.ConfigPath,
			Editable:      true,
			RestartNeeded: false,
			ValidationErr: err.Error(),
			Diff:          unifiedConfigDiff(current, req.YAML),
		})
		return
	}
	summary := ops.SummarizeConfig(cfg)
	s.setConfigSummary(summary)
	writeJSON(w, http.StatusOK, configValidateResponse{
		OK:            true,
		GeneratedAt:   time.Now().UTC(),
		ConfigPath:    summary.ConfigPath,
		Editable:      true,
		RestartNeeded: false,
		Diff:          unifiedConfigDiff(current, req.YAML),
		Config:        &summary,
	})
}

func (s *Server) handleConfigBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	summary := s.currentConfigSummary()
	items, err := listConfigBackups(summary.ConfigPath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, configBackupResponse{
		GeneratedAt: time.Now().UTC(),
		ConfigPath:  summary.ConfigPath,
		Count:       len(items),
		Items:       items,
	})
}

func (s *Server) handleConfigBackupCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	summary := s.currentConfigSummary()
	if strings.TrimSpace(summary.ConfigPath) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "no config file is associated with this runtime",
		})
		return
	}
	backup, err := createConfigBackup(summary.ConfigPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"generated_at": time.Now().UTC(),
		"backup":       backup,
	})
}

func (s *Server) handleConfigBackupDetail(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
	default:
		writeMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/v1/config/backups/")
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." {
		http.NotFound(w, r)
		return
	}
	summary := s.currentConfigSummary()
	items, err := listConfigBackups(summary.ConfigPath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	var selected *configBackupSummary
	for i := range items {
		if items[i].Name == name {
			selected = &items[i]
			break
		}
	}
	if selected == nil {
		http.NotFound(w, r)
		return
	}
	raw, err := os.ReadFile(selected.Path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	current, _, _ := s.readConfigRaw()
	if r.Method == http.MethodPost {
		var cfg *config.Config
		cfg, err := config.LoadBytesWithEnv(summary.ConfigPath, raw, s.validationEnvLookup())
		if err == nil {
			err = config.ValidateMVP(cfg)
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"ok":    false,
				"error": err.Error(),
			})
			return
		}
		if err := writeConfigAtomically(summary.ConfigPath, raw); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"ok":    false,
				"error": err.Error(),
			})
			return
		}
		s.setConfigSummary(ops.SummarizeConfig(cfg))
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":             true,
			"generated_at":   time.Now().UTC(),
			"restart_needed": false,
			"backup":         selected,
		})
		return
	}
	writeJSON(w, http.StatusOK, configBackupDetailResponse{
		GeneratedAt: time.Now().UTC(),
		ConfigPath:  summary.ConfigPath,
		Backup:      selected,
		YAML:        string(raw),
		Diff:        unifiedConfigDiff(string(raw), current),
	})
}

func (s *Server) handleApprovals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	snapshot := s.runtime.Snapshot()
	writeCollection(w, encodeApprovals(snapshot.PendingApprovals))
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	snapshot := s.runtime.Snapshot()
	writeCollection(w, encodeMessages(snapshot.PendingMessages))
}

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	snapshot := s.runtime.Snapshot()
	writeCollection(w, encodeSourceSummaries(snapshot.SourceSummaries))
}

func (s *Server) handleSourceAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/sources/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[1] != "poll" {
		http.NotFound(w, r)
		return
	}

	sourceName := parts[0]
	result, err := s.runtime.RequestForcePoll(sourceName)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, orchestrator.ErrSourceNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]any{
			"ok":     false,
			"error":  err.Error(),
			"action": "force_poll",
			"source": sourceName,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"action":           "force_poll",
		"scope":            result.Scope,
		"requested_source": sourceName,
		"results":          result.Results,
	})
}

func (s *Server) handleForcePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	result, err := s.runtime.RequestForcePoll("")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":     false,
			"error":  err.Error(),
			"action": "force_poll",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"action":  "force_poll",
		"scope":   result.Scope,
		"results": result.Results,
	})
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	snapshot := s.runtime.Snapshot()
	runs, outputs := encodeRuns(snapshot.ActiveRuns, snapshot.RunOutputs, snapshot.SourceSummaries)
	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at": time.Now().UTC(),
		"count":        len(runs),
		"items":        runs,
		"outputs":      outputs,
	})
}

func (s *Server) handleRetries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	snapshot := s.runtime.Snapshot()
	writeCollection(w, encodeRetries(snapshot.Retries))
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	snapshot := s.runtime.Snapshot()
	writeCollection(w, encodeEvents(snapshot.RecentEvents))
}

func (s *Server) handleApprovalAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/approvals/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}

	requestID := parts[0]
	action := parts[1]
	decision := ""
	switch action {
	case harness.DecisionApprove:
		decision = harness.DecisionApprove
	case harness.DecisionReject:
		decision = harness.DecisionReject
	default:
		http.NotFound(w, r)
		return
	}

	if err := s.runtime.ResolveApproval(requestID, decision); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":      false,
			"error":   err.Error(),
			"request": requestID,
			"action":  decision,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"request": requestID,
		"action":  decision,
	})
}

func (s *Server) handleMessageAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/messages/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[1] != "reply" {
		http.NotFound(w, r)
		return
	}

	var payload struct {
		Reply string `json:"reply"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "invalid reply payload",
		})
		return
	}
	payload.Reply = strings.TrimSpace(payload.Reply)
	if payload.Reply == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "reply is required",
		})
		return
	}

	requestID := parts[0]
	if err := s.runtime.ResolveMessage(requestID, payload.Reply, "web"); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":      false,
			"error":   err.Error(),
			"request": requestID,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"request": requestID,
		"action":  "reply",
	})
}

func (s *Server) handleRunAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/runs/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	runID := parts[0]
	action := parts[1]
	if action != "stop" {
		http.NotFound(w, r)
		return
	}

	if err := s.runtime.StopRun(runID, "stopped from the web console"); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": err.Error(),
			"run":   runID,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"run":    runID,
		"action": "stop",
	})
}

func (s *Server) handlePackSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var req packSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, packSaveResponse{
			OK:            false,
			GeneratedAt:   time.Now().UTC(),
			RestartNeeded: false,
			ValidationErr: fmt.Sprintf("decode request: %v", err),
		})
		return
	}

	summary, err := s.savePack(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, packSaveResponse{
			OK:            false,
			GeneratedAt:   time.Now().UTC(),
			RestartNeeded: false,
			ValidationErr: err.Error(),
		})
		return
	}

	s.setConfigSummary(summary)
	writeJSON(w, http.StatusOK, packSaveResponse{
		OK:            true,
		GeneratedAt:   time.Now().UTC(),
		RestartNeeded: false,
		Config:        &summary,
	})
}

func writeMethodNotAllowed(w http.ResponseWriter, methods ...string) {
	if len(methods) > 0 {
		w.Header().Set("Allow", strings.Join(methods, ", "))
	}
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
}

func (s *Server) savePack(req packSaveRequest) (ops.ConfigSummary, error) {
	summary := s.currentConfigSummary()
	if strings.TrimSpace(summary.ConfigPath) == "" {
		return ops.ConfigSummary{}, fmt.Errorf("config file is not editable in this runtime")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return ops.ConfigSummary{}, fmt.Errorf("pack name is required")
	}

	packDir, packPath, promptPath, contextPath, err := s.resolvePackPaths(req)
	if err != nil {
		return ops.ConfigSummary{}, err
	}
	previousFiles, err := snapshotPackFiles(packPath, promptPath, contextPath)
	if err != nil {
		return ops.ConfigSummary{}, err
	}
	restore := func() {
		_ = restorePackFiles(previousFiles)
	}

	if err := os.MkdirAll(packDir, 0o755); err != nil {
		return ops.ConfigSummary{}, fmt.Errorf("create pack dir: %w", err)
	}

	existingEnv, err := existingPackEnv(packPath)
	if err != nil {
		return ops.ConfigSummary{}, err
	}
	envMap, err := buildPackEnv(existingEnv, req.EnvKeys)
	if err != nil {
		return ops.ConfigSummary{}, err
	}

	pack := config.AgentPackConfig{
		Name:           name,
		Description:    strings.TrimSpace(req.Description),
		InstanceName:   strings.TrimSpace(req.InstanceName),
		Harness:        strings.TrimSpace(req.Harness),
		Workspace:      strings.TrimSpace(req.Workspace),
		Prompt:         filepath.Base(promptPath),
		ApprovalPolicy: strings.TrimSpace(req.ApprovalPolicy),
		MaxConcurrent:  req.MaxConcurrent,
		Env:            envMap,
		Tools:          trimStrings(req.Tools),
		Skills:         trimStrings(req.Skills),
		ContextFiles:   []string{filepath.Base(contextPath)},
	}
	if pack.InstanceName == "" {
		pack.InstanceName = name
	}
	if pack.Harness == "" {
		pack.Harness = "claude-code"
	}
	if pack.Workspace == "" {
		pack.Workspace = "git-clone"
	}
	if pack.ApprovalPolicy == "" {
		pack.ApprovalPolicy = "manual"
	}
	if pack.MaxConcurrent <= 0 {
		pack.MaxConcurrent = 1
	}

	rawPack, err := yaml.Marshal(&pack)
	if err != nil {
		return ops.ConfigSummary{}, fmt.Errorf("encode pack yaml: %w", err)
	}
	if err := os.WriteFile(packPath, rawPack, 0o600); err != nil {
		restore()
		return ops.ConfigSummary{}, fmt.Errorf("write pack yaml: %w", err)
	}
	if err := os.WriteFile(promptPath, []byte(strings.TrimSpace(req.PromptBody)+"\n"), 0o600); err != nil {
		restore()
		return ops.ConfigSummary{}, fmt.Errorf("write prompt: %w", err)
	}
	if err := os.WriteFile(contextPath, []byte(strings.TrimSpace(req.ContextBody)+"\n"), 0o600); err != nil {
		restore()
		return ops.ConfigSummary{}, fmt.Errorf("write context: %w", err)
	}

	var cfg *config.Config
	rawConfig, readErr := os.ReadFile(summary.ConfigPath)
	if readErr != nil {
		restore()
		return ops.ConfigSummary{}, fmt.Errorf("read config for validation: %w", readErr)
	}
	cfg, err = config.LoadBytesWithEnv(summary.ConfigPath, rawConfig, s.validationEnvLookup())
	if err == nil {
		err = config.ValidateMVP(cfg)
	}
	if err != nil {
		restore()
		return ops.ConfigSummary{}, fmt.Errorf("validate updated pack: %w", err)
	}

	return ops.SummarizeConfig(cfg), nil
}

func (s *Server) resolvePackPaths(req packSaveRequest) (string, string, string, string, error) {
	summary := s.currentConfigSummary()
	base := strings.TrimSpace(summary.AgentPacksDir)
	if base == "" {
		base = filepath.Join(filepath.Dir(summary.ConfigPath), "agents")
	}
	if !filepath.IsAbs(base) {
		base = filepath.Join(filepath.Dir(summary.ConfigPath), base)
	}
	base = filepath.Clean(base)

	for _, agent := range summary.Agents {
		if agent.AgentPack == req.OriginalName && strings.TrimSpace(agent.PackPath) != "" {
			dir := filepath.Dir(agent.PackPath)
			return dir, agent.PackPath, filepath.Join(dir, "prompt.md"), filepath.Join(dir, "context.md"), nil
		}
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return "", "", "", "", fmt.Errorf("pack name is required")
	}
	dir, err := containedPath(base, name)
	if err != nil {
		return "", "", "", "", err
	}
	return dir, filepath.Join(dir, "agent.yaml"), filepath.Join(dir, "prompt.md"), filepath.Join(dir, "context.md"), nil
}

func containedPath(base string, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("pack name %q must stay within the agent packs directory", relative)
	}
	target := filepath.Clean(filepath.Join(base, relative))
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", fmt.Errorf("resolve pack path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("pack name %q must stay within the agent packs directory", relative)
	}
	return target, nil
}

type fileSnapshot struct {
	path   string
	exists bool
	data   []byte
}

func snapshotPackFiles(paths ...string) ([]fileSnapshot, error) {
	snapshots := make([]fileSnapshot, 0, len(paths))
	for _, filePath := range paths {
		raw, err := os.ReadFile(filePath)
		if err == nil {
			snapshots = append(snapshots, fileSnapshot{path: filePath, exists: true, data: raw})
			continue
		}
		if errors.Is(err, os.ErrNotExist) {
			snapshots = append(snapshots, fileSnapshot{path: filePath, exists: false})
			continue
		}
		return nil, fmt.Errorf("read %s: %w", filePath, err)
	}
	return snapshots, nil
}

func restorePackFiles(files []fileSnapshot) error {
	for _, file := range files {
		if !file.exists {
			if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		if err := os.WriteFile(file.path, file.data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func existingPackEnv(packPath string) (map[string]string, error) {
	raw, err := os.ReadFile(packPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read current pack yaml: %w", err)
	}
	var pack config.AgentPackConfig
	if err := yaml.Unmarshal(raw, &pack); err != nil {
		return nil, fmt.Errorf("decode current pack yaml: %w", err)
	}
	return pack.Env, nil
}

func buildPackEnv(existing map[string]string, requested []string) (map[string]string, error) {
	keys := trimStrings(requested)
	if len(keys) == 0 {
		return existing, nil
	}
	result := map[string]string{}
	for _, key := range keys {
		value, ok := existing[key]
		if !ok {
			return nil, fmt.Errorf("new env key %q cannot be added from the browser yet; keep existing keys only", key)
		}
		result[key] = value
	}
	return result, nil
}

func trimStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func (s *Server) validationEnvLookup() config.EnvLookup {
	placeholders := map[string]string{}
	for _, source := range s.currentConfigSummary().Sources {
		key := strings.TrimPrefix(strings.TrimSpace(source.TokenEnv), "$")
		if key == "" {
			continue
		}
		placeholders[key] = "maestro-ui-validation"
	}
	return func(name string) (string, bool) {
		if value, ok := os.LookupEnv(name); ok {
			return value, true
		}
		value, ok := placeholders[name]
		return value, ok
	}
}

func writeCollection[T any](w http.ResponseWriter, items []T) {
	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at": time.Now().UTC(),
		"count":        len(items),
		"items":        items,
	})
}

func decodeConfigValidateRequest(w http.ResponseWriter, r *http.Request) (configValidateRequest, bool) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var req configValidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": fmt.Sprintf("decode request: %v", err),
		})
		return configValidateRequest{}, false
	}
	return req, true
}

func (s *Server) validationConfigPath() string {
	if strings.TrimSpace(s.currentConfigSummary().ConfigPath) != "" {
		return s.currentConfigSummary().ConfigPath
	}
	return filepath.Join(os.TempDir(), "maestro-demo-config.yaml")
}

func (s *Server) readConfigRaw() (string, bool, error) {
	summary := s.currentConfigSummary()
	if strings.TrimSpace(summary.ConfigPath) == "" {
		return "", false, fmt.Errorf("no config file is associated with this runtime")
	}
	raw, err := os.ReadFile(summary.ConfigPath)
	if err != nil {
		return "", false, err
	}
	return string(raw), true, nil
}

func writeConfigAtomically(path string, raw []byte) error {
	if _, err := createConfigBackup(path); err != nil {
		return err
	}
	tmpPath := fmt.Sprintf("%s.tmp", path)
	if err := os.WriteFile(tmpPath, raw, 0o600); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func createConfigBackup(path string) (configBackupSummary, error) {
	backupPath := fmt.Sprintf("%s.bak.%s", path, time.Now().UTC().Format("20060102T150405"))
	current, err := os.ReadFile(path)
	if err != nil {
		return configBackupSummary{}, fmt.Errorf("read current config: %w", err)
	}
	if err := os.WriteFile(backupPath, current, 0o600); err != nil {
		return configBackupSummary{}, fmt.Errorf("write backup config: %w", err)
	}
	info, err := os.Stat(backupPath)
	if err != nil {
		return configBackupSummary{}, fmt.Errorf("stat backup config: %w", err)
	}
	return configBackupSummary{
		Name:      filepath.Base(backupPath),
		Path:      backupPath,
		CreatedAt: info.ModTime().UTC(),
	}, nil
}

func unifiedConfigDiff(before string, after string) string {
	if before == after {
		return ""
	}
	text, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(before),
		B:        difflib.SplitLines(after),
		FromFile: "current",
		ToFile:   "proposed",
		Context:  3,
	})
	if err != nil {
		return ""
	}
	return text
}

func listConfigBackups(configPath string) ([]configBackupSummary, error) {
	if strings.TrimSpace(configPath) == "" {
		return nil, fmt.Errorf("no config file is associated with this runtime")
	}
	dir := filepath.Dir(configPath)
	base := filepath.Base(configPath) + ".bak."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var items []configBackupSummary
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), base) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		items = append(items, configBackupSummary{
			Name:      entry.Name(),
			Path:      filepath.Join(dir, entry.Name()),
			CreatedAt: info.ModTime(),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		http.Error(w, fmt.Sprintf("encode json: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func (s *Server) streamPayload() streamUpdate {
	snapshot := s.runtime.Snapshot()
	payload := streamUpdate{GeneratedAt: time.Now().UTC()}
	payload.Snapshot.SourceCount = len(snapshot.SourceSummaries)
	payload.Snapshot.ActiveRunCount = len(snapshot.ActiveRuns)
	payload.Snapshot.RetryCount = len(snapshot.Retries)
	payload.Snapshot.ApprovalCount = len(snapshot.PendingApprovals)
	payload.Snapshot.ApprovalCount += len(snapshot.PendingMessages)
	payload.Snapshot.RecentEventCount = len(snapshot.RecentEvents)
	return payload
}

func encodeSnapshot(snapshot orchestrator.Snapshot) snapshotJSON {
	return apiSnapshot(snapshot)
}

func encodeSourceSummaries(items []orchestrator.SourceSummary) []sourceSummaryJSON {
	return apiSourceSummaries(items)
}

func encodeRuns(runs []domain.AgentRun, outputs []orchestrator.RunOutputView, sources []orchestrator.SourceSummary) ([]runJSON, []runOutputJSON) {
	return apiRunsAndOutputs(runs, outputs, executionBySource(sources))
}

func encodeRun(run domain.AgentRun, output *runOutputJSON, execution *executionJSON) runJSON {
	return apiRun(run, output, execution)
}

func encodeIssue(issue domain.Issue) issueJSON {
	return apiIssue(issue)
}

func encodeRunOutputs(items []orchestrator.RunOutputView) []runOutputJSON {
	return apiRunOutputs(items)
}

func outputsByRunID(items []runOutputJSON) map[string]*runOutputJSON {
	out := make(map[string]*runOutputJSON, len(items))
	for i := range items {
		out[items[i].RunID] = &items[i]
	}
	return out
}

func encodeRetries(items []orchestrator.RetryView) []retryJSON {
	return apiRetries(items)
}

func encodeApprovals(items []orchestrator.ApprovalView) []approvalJSON {
	return apiApprovals(items)
}

func encodeApprovalHistory(items []orchestrator.ApprovalHistoryEntry) []approvalHistoryJSON {
	return apiApprovalHistory(items)
}

func encodeMessages(items []orchestrator.MessageView) []messageJSON {
	return apiMessages(items)
}

func encodeMessageHistory(items []orchestrator.MessageHistoryEntry) []messageHistoryJSON {
	return apiMessageHistory(items)
}

func encodeEvents(items []orchestrator.Event) []eventJSON {
	return apiEvents(items)
}
