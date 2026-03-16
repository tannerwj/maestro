package ops

import (
	"strings"
	"time"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/state"
)

type ConfigSummary struct {
	ConfigPath    string                `json:"config_path"`
	WorkspaceRoot string                `json:"workspace_root"`
	StateDir      string                `json:"state_dir"`
	LogDir        string                `json:"log_dir"`
	LogMaxFiles   int                   `json:"log_max_files"`
	Sources       []ConfigSourceSummary `json:"sources"`
	Agents        []ConfigAgentSummary  `json:"agents"`
}

type ConfigSourceSummary struct {
	Name         string   `json:"name"`
	Tracker      string   `json:"tracker"`
	BaseURL      string   `json:"base_url,omitempty"`
	Project      string   `json:"project,omitempty"`
	Group        string   `json:"group,omitempty"`
	Repo         string   `json:"repo,omitempty"`
	FilterLabels []string `json:"filter_labels,omitempty"`
	FilterStates []string `json:"filter_states,omitempty"`
	Assignee     string   `json:"assignee,omitempty"`
	PollInterval string   `json:"poll_interval"`
	TokenEnv     string   `json:"token_env,omitempty"`
}

type ConfigAgentSummary struct {
	Name           string   `json:"name"`
	InstanceName   string   `json:"instance_name,omitempty"`
	AgentPack      string   `json:"agent_pack,omitempty"`
	Harness        string   `json:"harness"`
	Workspace      string   `json:"workspace"`
	ApprovalPolicy string   `json:"approval_policy"`
	Prompt         string   `json:"prompt"`
	ContextFiles   []string `json:"context_files,omitempty"`
	Tools          []string `json:"tools,omitempty"`
	Skills         []string `json:"skills,omitempty"`
	EnvKeys        []string `json:"env_keys,omitempty"`
}

type StateSummary struct {
	SourceName        string                 `json:"source_name"`
	Health            string                 `json:"health"`
	Path              string                 `json:"path"`
	Version           int                    `json:"version"`
	FinishedCount     int                    `json:"finished_count"`
	DoneCount         int                    `json:"done_count"`
	FailedCount       int                    `json:"failed_count"`
	RetryCount        int                    `json:"retry_count"`
	PendingCount      int                    `json:"pending_approvals_count"`
	ApprovalHistCount int                    `json:"approval_history_count"`
	LastError         string                 `json:"last_error,omitempty"`
	ActiveRun         *state.PersistedRun    `json:"active_run,omitempty"`
	Finished          []StateTerminalSummary `json:"finished,omitempty"`
	Retries           []state.RetryEntry     `json:"retries,omitempty"`
	PendingApprovals  []StateApprovalSummary `json:"pending_approvals,omitempty"`
	ApprovalHistory   []StateDecisionSummary `json:"approval_history,omitempty"`
}

type StateTerminalSummary struct {
	IssueID    string    `json:"issue_id"`
	Identifier string    `json:"identifier"`
	Status     string    `json:"status"`
	Attempt    int       `json:"attempt"`
	FinishedAt time.Time `json:"finished_at"`
}

type StateApprovalSummary struct {
	RequestID       string    `json:"request_id"`
	RunID           string    `json:"run_id"`
	IssueIdentifier string    `json:"issue_identifier,omitempty"`
	AgentName       string    `json:"agent_name,omitempty"`
	ToolName        string    `json:"tool_name"`
	ApprovalPolicy  string    `json:"approval_policy,omitempty"`
	RequestedAt     time.Time `json:"requested_at"`
	Resolvable      bool      `json:"resolvable"`
}

type StateDecisionSummary struct {
	RequestID       string    `json:"request_id"`
	RunID           string    `json:"run_id"`
	IssueIdentifier string    `json:"issue_identifier,omitempty"`
	ToolName        string    `json:"tool_name,omitempty"`
	Decision        string    `json:"decision"`
	Outcome         string    `json:"outcome,omitempty"`
	DecidedAt       time.Time `json:"decided_at"`
}

func SummarizeConfig(cfg *config.Config) ConfigSummary {
	summary := ConfigSummary{
		ConfigPath:    cfg.ConfigPath,
		WorkspaceRoot: cfg.Workspace.Root,
		StateDir:      cfg.State.Dir,
		LogDir:        cfg.Logging.Dir,
		LogMaxFiles:   cfg.Logging.MaxFiles,
	}
	for _, source := range cfg.Sources {
		summary.Sources = append(summary.Sources, ConfigSourceSummary{
			Name:         source.Name,
			Tracker:      source.Tracker,
			BaseURL:      source.Connection.BaseURL,
			Project:      source.Connection.Project,
			Group:        source.Connection.GroupPath(),
			Repo:         source.Repo,
			FilterLabels: append([]string(nil), source.Filter.Labels...),
			FilterStates: append([]string(nil), source.Filter.States...),
			Assignee:     source.Filter.Assignee,
			PollInterval: source.PollInterval.Duration.String(),
			TokenEnv:     source.Connection.TokenEnv,
		})
	}
	for _, agent := range cfg.AgentTypes {
		envKeys := make([]string, 0, len(agent.Env))
		for key := range agent.Env {
			envKeys = append(envKeys, key)
		}
		summary.Agents = append(summary.Agents, ConfigAgentSummary{
			Name:           agent.Name,
			InstanceName:   agent.InstanceName,
			AgentPack:      agent.AgentPack,
			Harness:        agent.Harness,
			Workspace:      agent.Workspace,
			ApprovalPolicy: agent.ApprovalPolicy,
			Prompt:         agent.Prompt,
			ContextFiles:   append([]string(nil), agent.ContextFiles...),
			Tools:          append([]string(nil), agent.Tools...),
			Skills:         append([]string(nil), agent.Skills...),
			EnvKeys:        envKeys,
		})
	}
	return summary
}

func SummarizeState(sourceName string, path string, snapshot state.Snapshot) StateSummary {
	summary := StateSummary{
		SourceName:        sourceName,
		Path:              path,
		Version:           snapshot.Version,
		FinishedCount:     len(snapshot.Finished),
		RetryCount:        len(snapshot.RetryQueue),
		PendingCount:      len(snapshot.PendingApprovals),
		ApprovalHistCount: len(snapshot.ApprovalHistory),
		ActiveRun:         snapshot.ActiveRun,
	}

	for _, finished := range snapshot.Finished {
		switch finished.Status {
		case domain.RunStatusDone:
			summary.DoneCount++
		case domain.RunStatusFailed:
			summary.FailedCount++
		}
		summary.Finished = append(summary.Finished, StateTerminalSummary{
			IssueID:    finished.IssueID,
			Identifier: finished.Identifier,
			Status:     string(finished.Status),
			Attempt:    finished.Attempt,
			FinishedAt: finished.FinishedAt,
		})
	}
	for _, retry := range snapshot.RetryQueue {
		summary.Retries = append(summary.Retries, retry)
	}
	for _, approval := range snapshot.PendingApprovals {
		summary.PendingApprovals = append(summary.PendingApprovals, StateApprovalSummary{
			RequestID:       approval.RequestID,
			RunID:           approval.RunID,
			IssueIdentifier: approval.IssueIdentifier,
			AgentName:       approval.AgentName,
			ToolName:        approval.ToolName,
			ApprovalPolicy:  approval.ApprovalPolicy,
			RequestedAt:     approval.RequestedAt,
			Resolvable:      approval.Resolvable,
		})
	}
	for _, decision := range snapshot.ApprovalHistory {
		summary.ApprovalHistory = append(summary.ApprovalHistory, StateDecisionSummary{
			RequestID:       decision.RequestID,
			RunID:           decision.RunID,
			IssueIdentifier: decision.IssueIdentifier,
			ToolName:        decision.ToolName,
			Decision:        decision.Decision,
			Outcome:         decision.Outcome,
			DecidedAt:       decision.DecidedAt,
		})
	}
	summary.LastError = summarizeStateLastError(snapshot)
	summary.Health = summarizeStateHealth(summary)
	return summary
}

func summarizeStateLastError(snapshot state.Snapshot) string {
	var latestTime time.Time
	lastError := ""
	for _, retry := range snapshot.RetryQueue {
		if strings.TrimSpace(retry.Error) == "" {
			continue
		}
		if latestTime.IsZero() || retry.DueAt.After(latestTime) {
			latestTime = retry.DueAt
			lastError = retry.Error
		}
	}
	for _, finished := range snapshot.Finished {
		if strings.TrimSpace(finished.Error) == "" {
			continue
		}
		if latestTime.IsZero() || finished.FinishedAt.After(latestTime) {
			latestTime = finished.FinishedAt
			lastError = finished.Error
		}
	}
	return lastError
}

func summarizeStateHealth(summary StateSummary) string {
	switch {
	case summary.RetryCount > 0:
		return "retrying"
	case summary.ActiveRun != nil && summary.FailedCount > 0:
		return "active+degraded"
	case summary.ActiveRun != nil:
		return "active"
	case summary.PendingCount > 0 && summary.FailedCount > 0:
		return "awaiting-approval+degraded"
	case summary.PendingCount > 0:
		return "awaiting-approval"
	case summary.FailedCount > 0:
		return "degraded"
	case summary.FinishedCount > 0:
		return "idle"
	default:
		return "empty"
	}
}
