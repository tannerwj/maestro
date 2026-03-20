package config

import (
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/tjohnson/maestro/internal/prompt"
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
	channelKinds := make(map[string]string, len(cfg.Channels))
	for i, channel := range cfg.Channels {
		name := strings.TrimSpace(channel.Name)
		if name == "" {
			return fmt.Errorf("channels[%d].name is required", i)
		}
		if _, exists := channelKinds[name]; exists {
			return fmt.Errorf("channels[%d].name %q is duplicated", i, name)
		}
		kind := strings.TrimSpace(channel.Kind)
		if kind == "" {
			return fmt.Errorf("channel %q kind is required", name)
		}
		if !slices.Contains([]string{"slack", "teams", "gitlab", "tui"}, kind) {
			return fmt.Errorf("channel %q kind must be one of slack, teams, gitlab, tui", name)
		}
		channelKinds[name] = kind
	}

	for i, agent := range cfg.AgentTypes {
		repoPackPath, hasRepoPack := ParseRepoPackRef(agent.AgentPack)
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
		if agent.Harness == "codex" && agent.Claude != nil {
			return fmt.Errorf("agent %q has harness codex but includes claude config", agent.Name)
		}
		if agent.Harness == "claude-code" && agent.Codex != nil {
			return fmt.Errorf("agent %q has harness claude-code but includes codex config", agent.Name)
		}
		if cfg.CodexDefaults != nil && cfg.CodexDefaults.MaxTurns < 0 {
			return fmt.Errorf("codex_defaults max_turns must be at least 1")
		}
		if cfg.ClaudeDefaults != nil && cfg.ClaudeDefaults.MaxTurns < 0 {
			return fmt.Errorf("claude_defaults max_turns must be at least 1")
		}
		if agent.Codex != nil && agent.Codex.MaxTurns < 0 {
			return fmt.Errorf("agent %q codex max_turns must be at least 1", agent.Name)
		}
		if agent.Claude != nil && agent.Claude.MaxTurns < 0 {
			return fmt.Errorf("agent %q claude max_turns must be at least 1", agent.Name)
		}
		if agent.Harness == "claude-code" {
			resolved := ResolveClaudeConfig(cfg.ClaudeDefaults, agent.Claude)
			if resolved.MaxTurns != 1 {
				return fmt.Errorf("agent %q claude max_turns must be exactly 1 until multi-turn claude support exists", agent.Name)
			}
		}
		if !slices.Contains([]string{"git-clone", "none"}, agent.Workspace) {
			return fmt.Errorf("agent %q requires workspace git-clone or none", agent.Name)
		}
		if hasRepoPack && agent.Workspace != "git-clone" {
			return fmt.Errorf("agent %q requires workspace git-clone for repo pack %q", agent.Name, repoPackPath)
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
		if agent.ApprovalTimeout.Duration <= 0 {
			return fmt.Errorf("agent %q approval_timeout must be greater than zero", agent.Name)
		}
		if !hasRepoPack {
			if strings.TrimSpace(agent.Prompt) == "" {
				return fmt.Errorf("agent %q prompt path is required", agent.Name)
			}
			if _, err := os.Stat(agent.Prompt); err != nil {
				return fmt.Errorf("agent %q prompt %q: %w", agent.Name, agent.Prompt, err)
			}
			if _, err := prompt.ParseFile(agent.Prompt); err != nil {
				return fmt.Errorf("agent %q prompt %q: %w", agent.Name, agent.Prompt, err)
			}
		}
		if strings.TrimSpace(agent.Communication) != "" {
			kind, ok := channelKinds[agent.Communication]
			if !ok {
				return fmt.Errorf("agent %q references unknown communication channel %q", agent.Name, agent.Communication)
			}
			if kind == "slack" {
				tokenEnv := strings.TrimSpace(channelConfigString(channelByName(cfg.Channels, agent.Communication).Config, "token_env"))
				appTokenEnv := strings.TrimSpace(channelConfigString(channelByName(cfg.Channels, agent.Communication).Config, "app_token_env"))
				if tokenEnv == "" {
					return fmt.Errorf("slack channel %q requires config.token_env", agent.Communication)
				}
				if appTokenEnv == "" {
					return fmt.Errorf("slack channel %q requires config.app_token_env", agent.Communication)
				}
			}
		}
	}

	if strings.TrimSpace(cfg.Defaults.LabelPrefix) == "" {
		return fmt.Errorf("defaults.label_prefix must be non-empty")
	}
	reservedLabels := reservedLifecycleLabels(cfg.Defaults.LabelPrefix)
	if err := validateLifecycleTransition("defaults.on_complete", cfg.Defaults.OnComplete, reservedLabels); err != nil {
		return err
	}
	if err := validateLifecycleTransition("defaults.on_failure", cfg.Defaults.OnFailure, reservedLabels); err != nil {
		return err
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
		if strings.TrimSpace(source.AgentType) == "" {
			return fmt.Errorf("source %q agent_type is required", source.Name)
		}
		agent, ok := agentsByName[source.AgentType]
		if !ok {
			return fmt.Errorf("source %q references unknown agent_type %q", source.Name, source.AgentType)
		}
		if requiresSourceRepo(agent.Workspace, source.Tracker) && strings.TrimSpace(source.Repo) == "" {
			return fmt.Errorf("source %q requires repo for git-clone workspace", source.Name)
		}
		if strings.TrimSpace(source.Repo) != "" {
			if err := validateRepoURL(source.Repo); err != nil {
				return fmt.Errorf("source %q: %w", source.Name, err)
			}
		}
		if source.EffectiveRetryBase(cfg.State) <= 0 {
			return fmt.Errorf("source %q retry_base must be greater than zero", source.Name)
		}
		if source.EffectiveMaxRetryBackoff(cfg.State) < source.EffectiveRetryBase(cfg.State) {
			return fmt.Errorf("source %q max_retry_backoff must be at least retry_base", source.Name)
		}
		if source.EffectiveMaxAttempts(cfg.State) < 1 {
			return fmt.Errorf("source %q max_attempts must be at least 1", source.Name)
		}
		if err := validateLifecycleLabels(source, cfg.Defaults.OnComplete, cfg.Defaults.OnFailure, reservedLabels); err != nil {
			return err
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
	if strings.Contains(cfg.Controls.BeforeWork.Prompt, "{{") {
		return fmt.Errorf("controls.before_work.prompt must be plain text for v0.1")
	}
	switch strings.TrimSpace(cfg.Controls.BeforeWork.Mode) {
	case "", "review", "reply":
	default:
		return fmt.Errorf("controls.before_work.mode must be one of: review, reply")
	}
	if cfg.Logging.MaxFiles < 0 {
		return fmt.Errorf("logging.max_files must be zero or greater")
	}
	if cfg.Server.Enabled {
		if strings.TrimSpace(cfg.Server.Host) == "" {
			return fmt.Errorf("server.host is required when server.enabled is true")
		}
		if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
			return fmt.Errorf("server.port must be between 1 and 65535 when server.enabled is true")
		}
	}
	return nil
}

func channelByName(channels []ChannelConfig, name string) ChannelConfig {
	for _, channel := range channels {
		if channel.Name == name {
			return channel
		}
	}
	return ChannelConfig{}
}

func channelConfigString(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return ""
	}
	value, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
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

func requiresSourceRepo(workspace string, tracker string) bool {
	return workspace == "git-clone" && (tracker == "linear" || tracker == "gitlab-epic")
}

func reservedLifecycleLabels(prefix string) map[string]struct{} {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		prefix = "maestro"
	}
	return map[string]struct{}{
		prefix + ":active": {},
		prefix + ":retry":  {},
		prefix + ":done":   {},
		prefix + ":failed": {},
	}
}

func validateLifecycleLabels(source SourceConfig, defaultComplete *LifecycleTransition, defaultFailure *LifecycleTransition, reserved map[string]struct{}) error {
	if err := validateLifecycleTransition(fmt.Sprintf("source %q on_complete", source.Name), ResolveLifecycleTransition(defaultComplete, source.OnComplete), reserved); err != nil {
		return err
	}
	if err := validateLifecycleTransition(fmt.Sprintf("source %q on_failure", source.Name), ResolveLifecycleTransition(defaultFailure, source.OnFailure), reserved); err != nil {
		return err
	}
	return nil
}

func validateLifecycleTransition(name string, transition *LifecycleTransition, reserved map[string]struct{}) error {
	if transition == nil {
		return nil
	}
	for _, label := range transition.AddLabels {
		normalized := strings.ToLower(strings.TrimSpace(label))
		if _, ok := reserved[normalized]; ok {
			return fmt.Errorf("%s.add_labels must not include reserved lifecycle label %q", name, label)
		}
	}
	return nil
}
