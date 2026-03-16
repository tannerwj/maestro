package config

import (
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
)

// ValidateMVP enforces the intentionally narrow Phase 1 configuration contract.
func ValidateMVP(cfg *Config) error {
	if len(cfg.Sources) == 0 {
		return fmt.Errorf("config requires at least one source")
	}
	if len(cfg.AgentTypes) == 0 {
		return fmt.Errorf("config requires at least one agent type")
	}
	if cfg.Defaults.MaxConcurrentGlobal < 1 {
		return fmt.Errorf("defaults.max_concurrent_global must be at least 1")
	}
	if cfg.Defaults.StallTimeout.Duration <= 0 {
		return fmt.Errorf("defaults.stall_timeout must be greater than zero")
	}

	agentsByName := make(map[string]AgentTypeConfig, len(cfg.AgentTypes))
	for i, agent := range cfg.AgentTypes {
		if strings.TrimSpace(agent.Name) == "" {
			return fmt.Errorf("agent_types[%d].name is required", i)
		}
		if _, exists := agentsByName[agent.Name]; exists {
			return fmt.Errorf("agent_types[%d].name %q is duplicated", i, agent.Name)
		}
		agentsByName[agent.Name] = agent

		if agent.Harness != "claude-code" && agent.Harness != "codex" {
			return fmt.Errorf("agent %q requires harness claude-code or codex", agent.Name)
		}
		if agent.Workspace != "git-clone" {
			return fmt.Errorf("agent %q requires workspace git-clone", agent.Name)
		}
		if !slices.Contains([]string{"auto", "manual", "destructive-only"}, agent.ApprovalPolicy) {
			return fmt.Errorf("agent %q requires approval_policy to be one of auto, manual, destructive-only", agent.Name)
		}
		if agent.MaxConcurrent < 1 {
			return fmt.Errorf("agent %q max_concurrent must be at least 1", agent.Name)
		}
		if agent.StallTimeout.Duration <= 0 {
			return fmt.Errorf("agent %q stall_timeout must be greater than zero", agent.Name)
		}
		if strings.TrimSpace(agent.Prompt) == "" {
			return fmt.Errorf("agent %q prompt path is required", agent.Name)
		}
		if _, err := os.Stat(agent.Prompt); err != nil {
			return fmt.Errorf("agent %q prompt %q: %w", agent.Name, agent.Prompt, err)
		}
	}

	sourceNames := map[string]struct{}{}
	for i, source := range cfg.Sources {
		if strings.TrimSpace(source.Name) == "" {
			return fmt.Errorf("sources[%d].name is required", i)
		}
		if _, exists := sourceNames[source.Name]; exists {
			return fmt.Errorf("sources[%d].name %q is duplicated", i, source.Name)
		}
		sourceNames[source.Name] = struct{}{}

		if source.Tracker != "gitlab" && source.Tracker != "gitlab-epic" && source.Tracker != "linear" {
			return fmt.Errorf("source %q requires tracker=gitlab, gitlab-epic, or linear", source.Name)
		}
		if source.Tracker != "linear" && strings.TrimSpace(source.Connection.BaseURL) == "" {
			return fmt.Errorf("source %q connection.base_url is required", source.Name)
		}
		if source.Tracker == "gitlab" && strings.TrimSpace(source.Connection.Project) == "" {
			return fmt.Errorf("source %q connection.project is required", source.Name)
		}
		if source.Tracker == "gitlab-epic" && strings.TrimSpace(source.Connection.GroupPath()) == "" {
			return fmt.Errorf("source %q gitlab epic sources require connection.group", source.Name)
		}
		if isZeroFilter(source.EffectiveIssueFilter()) && isZeroFilter(source.EffectiveEpicFilter()) {
			return fmt.Errorf("source %q filter must include labels, states, or assignee", source.Name)
		}
		if source.Tracker == "gitlab-epic" && strings.TrimSpace(source.EffectiveEpicFilter().Assignee) != "" {
			return fmt.Errorf("source %q epic_filter.assignee is unsupported; use issue_filter.assignee for linked issues", source.Name)
		}
		if (source.Tracker == "linear" || source.Tracker == "gitlab-epic") && strings.TrimSpace(source.Repo) == "" {
			return fmt.Errorf("source %q requires repo for git-clone workspace", source.Name)
		}
		if err := validateRepoURL(source.Repo); err != nil {
			return fmt.Errorf("source %q: %w", source.Name, err)
		}

		if strings.TrimSpace(source.AgentType) == "" {
			return fmt.Errorf("source %q agent_type is required", source.Name)
		}
		if _, ok := agentsByName[source.AgentType]; !ok {
			return fmt.Errorf("source %q references unknown agent_type %q", source.Name, source.AgentType)
		}
	}
	if strings.TrimSpace(cfg.State.Dir) == "" {
		return fmt.Errorf("state dir is required")
	}
	if cfg.State.RetryBase.Duration <= 0 {
		return fmt.Errorf("state.retry_base must be greater than zero")
	}
	if cfg.State.MaxRetryBackoff.Duration < cfg.State.RetryBase.Duration {
		return fmt.Errorf("state.max_retry_backoff must be at least retry_base")
	}
	if cfg.State.MaxAttempts < 1 {
		return fmt.Errorf("state.max_attempts must be at least 1")
	}
	if cfg.Hooks.Timeout.Duration <= 0 {
		return fmt.Errorf("hooks.timeout must be greater than zero")
	}
	if cfg.Logging.MaxFiles < 0 {
		return fmt.Errorf("logging.max_files must be zero or greater")
	}
	return nil
}

func validateRepoURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid repo url %q: %w", raw, err)
	}
	if parsed.User != nil {
		return fmt.Errorf("repo urls must not embed credentials; use connection.token_env instead")
	}
	return nil
}
