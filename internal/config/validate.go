package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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
		if err := validateDockerConfig(agent); err != nil {
			return err
		}
		if cfg.CodexDefaults != nil && cfg.CodexDefaults.MaxTurns < 0 {
			return fmt.Errorf("codex_defaults max_turns must not be negative")
		}
		if cfg.ClaudeDefaults != nil && cfg.ClaudeDefaults.MaxTurns < 0 {
			return fmt.Errorf("claude_defaults max_turns must not be negative")
		}
		if agent.Codex != nil && agent.Codex.MaxTurns < 0 {
			return fmt.Errorf("agent %q codex max_turns must not be negative", agent.Name)
		}
		if agent.Claude != nil && agent.Claude.MaxTurns < 0 {
			return fmt.Errorf("agent %q claude max_turns must not be negative", agent.Name)
		}
		if !slices.Contains([]string{"git-clone", "none"}, agent.Workspace) {
			return fmt.Errorf("agent %q requires workspace git-clone or none", agent.Name)
		}
		if hasRepoPack && agent.Workspace != "git-clone" {
			return fmt.Errorf("agent %q requires workspace git-clone for repo pack %q", agent.Name, repoPackPath)
		}
		if !slices.Contains([]string{"auto", "manual"}, agent.ApprovalPolicy) {
			return fmt.Errorf("agent %q requires approval_policy to be one of auto, manual", agent.Name)
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
		if IsZeroFilter(source.EffectiveIssueFilter()) && IsZeroFilter(source.EffectiveEpicFilter()) {
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
		if source.EffectiveMaxActiveRuns() < 1 {
			return fmt.Errorf("source %q max_active_runs must be at least 1", source.Name)
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
	switch strings.TrimSpace(cfg.Hooks.Execution) {
	case "", "host", "container":
	default:
		return fmt.Errorf("hooks.execution must be one of: host, container")
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
	if isScpStyleRepoURL(raw) {
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

func validateDockerConfig(agent AgentTypeConfig) error {
	agentName := agent.Name
	docker := agent.Docker
	if docker == nil {
		return nil
	}
	if strings.TrimSpace(docker.Image) == "" {
		return fmt.Errorf("agent %q docker.image is required", agentName)
	}
	if mode := NormalizeDockerImagePinMode(docker.ImagePinMode); mode != "" && !KnownDockerImagePinMode(mode) {
		return fmt.Errorf("agent %q docker.image_pin_mode must be one of allow, require", agentName)
	}
	if NormalizeDockerImagePinMode(docker.ImagePinMode) == DockerImagePinModeRequire && !DockerImageIsPinned(docker.Image) {
		return fmt.Errorf("agent %q docker.image must be digest-pinned when docker.image_pin_mode=require", agentName)
	}
	if path := strings.TrimSpace(docker.WorkspaceMountPath); path != "" && !filepath.IsAbs(path) {
		return fmt.Errorf("agent %q docker.workspace_mount_path must be absolute", agentName)
	}
	if policy := strings.TrimSpace(docker.PullPolicy); policy != "" {
		switch policy {
		case "missing", "always", "never":
		default:
			return fmt.Errorf("agent %q docker.pull_policy must be one of missing, always, never", agentName)
		}
	}
	switch mode := strings.TrimSpace(docker.Network); mode {
	case "", "bridge", "none", "host":
	default:
		return fmt.Errorf("agent %q docker.network must be one of bridge, none, host", agentName)
	}
	if err := validateDockerNetworkPolicyConfig(agentName, docker.NetworkPolicy); err != nil {
		return err
	}
	if docker.CPUs < 0 {
		return fmt.Errorf("agent %q docker.cpus must be zero or greater", agentName)
	}
	if docker.PIDsLimit < 0 {
		return fmt.Errorf("agent %q docker.pids_limit must be zero or greater", agentName)
	}
	if err := validateDockerAuthConfig(agentName, docker.Auth); err != nil {
		return err
	}
	if err := validateDockerSecretsConfig(agentName, docker.Secrets); err != nil {
		return err
	}
	if err := validateDockerToolsConfig(agentName, docker.Tools); err != nil {
		return err
	}
	if err := validateDockerSecurityConfig(agentName, docker.Security); err != nil {
		return err
	}
	if err := validateDockerCacheConfig(agentName, docker.Cache); err != nil {
		return err
	}
	if err := validateDockerReuseConfig(agentName, docker.Reuse); err != nil {
		return err
	}
	for _, envName := range docker.EnvPassthrough {
		trimmed := strings.TrimPrefix(strings.TrimSpace(envName), "$")
		if trimmed == "" || strings.Contains(trimmed, "=") {
			return fmt.Errorf("agent %q docker.env_passthrough contains invalid name %q", agentName, envName)
		}
	}
	if err := validateDockerNetworkPolicyCompatibility(agent); err != nil {
		return err
	}
	for _, mount := range docker.Mounts {
		if strings.TrimSpace(mount.Source) == "" {
			return fmt.Errorf("agent %q docker.mounts[].source is required", agentName)
		}
		if strings.TrimSpace(mount.Target) == "" {
			return fmt.Errorf("agent %q docker.mounts[].target is required", agentName)
		}
		if !filepath.IsAbs(strings.TrimSpace(mount.Target)) {
			return fmt.Errorf("agent %q docker.mounts[].target must be absolute", agentName)
		}
		if !mount.ReadOnly {
			return fmt.Errorf("agent %q docker.mounts[] must be read_only in phase 1", agentName)
		}
	}
	if err := validateDockerAccessConflicts(agent); err != nil {
		return err
	}
	return nil
}

func validateDockerNetworkPolicyConfig(agentName string, policy *DockerNetworkPolicyConfig) error {
	if policy == nil {
		return nil
	}
	mode := NormalizeDockerNetworkPolicyMode(policy.Mode)
	if mode == "" {
		if len(policy.Allow) > 0 {
			return fmt.Errorf("agent %q docker.network_policy.mode is required when docker.network_policy.allow is configured", agentName)
		}
		return nil
	}
	switch mode {
	case DockerNetworkPolicyNone, DockerNetworkPolicyBridge, DockerNetworkPolicyAllowlist:
	default:
		return fmt.Errorf("agent %q docker.network_policy.mode must be one of none, bridge, allowlist", agentName)
	}
	if mode != DockerNetworkPolicyAllowlist && len(policy.Allow) > 0 {
		return fmt.Errorf("agent %q docker.network_policy.allow is only supported when docker.network_policy.mode is allowlist", agentName)
	}
	if mode == DockerNetworkPolicyAllowlist && len(policy.Allow) == 0 {
		return fmt.Errorf("agent %q docker.network_policy.allow must include at least one host or domain for allowlist mode", agentName)
	}
	for _, raw := range policy.Allow {
		entry := NormalizeDockerNetworkAllowEntry(raw)
		if entry == "" {
			return fmt.Errorf("agent %q docker.network_policy.allow contains an empty entry", agentName)
		}
		if strings.Contains(entry, "://") {
			return fmt.Errorf("agent %q docker.network_policy.allow entry %q must not include a URL scheme", agentName, raw)
		}
		if strings.ContainsAny(entry, "/?#") {
			return fmt.Errorf("agent %q docker.network_policy.allow entry %q must be a host or domain only", agentName, raw)
		}
		switch {
		case strings.HasPrefix(entry, "*."):
			if err := validateDockerNetworkHostname(entry[2:]); err != nil {
				return fmt.Errorf("agent %q docker.network_policy.allow entry %q is invalid: %w", agentName, raw, err)
			}
		case net.ParseIP(strings.Trim(entry, "[]")) != nil:
		default:
			if err := validateDockerNetworkHostname(entry); err != nil {
				return fmt.Errorf("agent %q docker.network_policy.allow entry %q is invalid: %w", agentName, raw, err)
			}
		}
	}
	return nil
}

func validateDockerNetworkPolicyCompatibility(agent AgentTypeConfig) error {
	if agent.Docker == nil || agent.Docker.NetworkPolicy == nil {
		return nil
	}

	mode := NormalizeDockerNetworkPolicyMode(agent.Docker.NetworkPolicy.Mode)
	network := strings.TrimSpace(agent.Docker.Network)
	switch mode {
	case DockerNetworkPolicyNone:
		if network != "" && network != DockerNetworkPolicyNone {
			return fmt.Errorf("agent %q docker.network=%q conflicts with docker.network_policy.mode=none", agent.Name, network)
		}
	case DockerNetworkPolicyBridge:
		if network != "" && network != DockerNetworkPolicyBridge {
			return fmt.Errorf("agent %q docker.network=%q conflicts with docker.network_policy.mode=bridge", agent.Name, network)
		}
	case DockerNetworkPolicyAllowlist:
		if network != "" && network != DockerNetworkPolicyBridge {
			return fmt.Errorf("agent %q docker.network=%q conflicts with docker.network_policy.mode=allowlist; allowlist mode requires bridge networking", agent.Name, network)
		}
		for key := range agent.Env {
			if isProxyEnvName(key) {
				return fmt.Errorf("agent %q env %q conflicts with docker.network_policy.mode=allowlist; Maestro manages proxy env in allowlist mode", agent.Name, key)
			}
		}
		for _, key := range agent.Docker.EnvPassthrough {
			if isProxyEnvName(key) {
				return fmt.Errorf("agent %q docker.env_passthrough %q conflicts with docker.network_policy.mode=allowlist; Maestro manages proxy env in allowlist mode", agent.Name, key)
			}
		}
	}
	return nil
}

func validateDockerNetworkHostname(host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("host is empty")
	}
	if strings.Contains(host, ":") {
		return fmt.Errorf("ports are not supported")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			return fmt.Errorf("host contains an empty label")
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("label %q must not start or end with '-'", label)
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z':
			case r >= '0' && r <= '9':
			case r >= 'A' && r <= 'Z':
			case r == '-':
			default:
				return fmt.Errorf("label %q contains invalid character %q", label, r)
			}
		}
	}
	return nil
}

func isProxyEnvName(name string) bool {
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "$")) {
	case "http_proxy", "https_proxy", "all_proxy", "no_proxy":
		return true
	default:
		return false
	}
}

func validateDockerAuthConfig(agentName string, auth *DockerAuthConfig) error {
	if auth == nil {
		return nil
	}
	mode := NormalizeDockerAuthMode(auth.Mode)
	if mode == "" {
		if strings.TrimSpace(auth.Source) != "" || strings.TrimSpace(auth.Target) != "" {
			return fmt.Errorf("agent %q docker.auth.mode is required when docker.auth is configured", agentName)
		}
		return nil
	}
	switch mode {
	case DockerAuthClaudeAPIKey, DockerAuthClaudeProxy, DockerAuthClaudeConfig, DockerAuthCodexAPIKey, DockerAuthCodexConfig:
	default:
		return fmt.Errorf("agent %q docker.auth.mode must be one of claude-api-key, claude-proxy, claude-config-mount, codex-api-key, codex-config-mount", agentName)
	}
	if DockerAuthModeUsesEnv(mode) {
		if source := strings.TrimPrefix(strings.TrimSpace(auth.Source), "$"); source != "" && !isValidEnvName(source) {
			return fmt.Errorf("agent %q docker.auth.source contains invalid env name %q", agentName, auth.Source)
		}
		if target := strings.TrimPrefix(strings.TrimSpace(auth.Target), "$"); target != "" && !isValidEnvName(target) {
			return fmt.Errorf("agent %q docker.auth.target contains invalid env name %q", agentName, auth.Target)
		}
		return nil
	}
	if strings.TrimSpace(auth.Source) == "" {
		return fmt.Errorf("agent %q docker.auth.source is required for mode %s", agentName, mode)
	}
	if target := strings.TrimSpace(auth.Target); target != "" && !filepath.IsAbs(target) {
		return fmt.Errorf("agent %q docker.auth.target must be absolute for mode %s", agentName, mode)
	}
	return nil
}

func validateDockerSecretsConfig(agentName string, secrets *DockerSecretsConfig) error {
	if secrets == nil {
		return nil
	}
	for _, item := range secrets.Env {
		source, target, _, err := ResolveDockerSecretEnvConfig(item)
		if err != nil {
			return fmt.Errorf("agent %q docker.secrets.env[] %v", agentName, err)
		}
		if !isValidEnvName(source) {
			return fmt.Errorf("agent %q docker.secrets.env[].source contains invalid env name %q", agentName, source)
		}
		if !isValidEnvName(target) {
			return fmt.Errorf("agent %q docker.secrets.env[].target contains invalid env name %q", agentName, target)
		}
	}
	for _, item := range secrets.Mounts {
		if strings.TrimSpace(item.Source) == "" {
			return fmt.Errorf("agent %q docker.secrets.mounts[].source is required", agentName)
		}
		_, target, _, err := ResolveDockerAccessMountConfig(item, DockerHomeDefault)
		if err != nil {
			return fmt.Errorf("agent %q docker.secrets.mounts[] %v", agentName, err)
		}
		if !filepath.IsAbs(strings.TrimSpace(target)) {
			return fmt.Errorf("agent %q docker.secrets.mounts[].target must be absolute", agentName)
		}
	}
	return nil
}

func validateDockerToolsConfig(agentName string, tools *DockerToolsConfig) error {
	if tools == nil {
		return nil
	}
	for _, item := range tools.Mounts {
		if strings.TrimSpace(item.Source) == "" {
			return fmt.Errorf("agent %q docker.tools.mounts[].source is required", agentName)
		}
		_, target, _, err := ResolveDockerAccessMountConfig(item, DockerHomeDefault)
		if err != nil {
			return fmt.Errorf("agent %q docker.tools.mounts[] %v", agentName, err)
		}
		if !filepath.IsAbs(strings.TrimSpace(target)) {
			return fmt.Errorf("agent %q docker.tools.mounts[].target must be absolute", agentName)
		}
	}
	return nil
}

func validateDockerSecurityConfig(agentName string, security *DockerSecurityConfig) error {
	if security == nil {
		return nil
	}
	if preset := NormalizeDockerSecurityPreset(security.Preset); preset != "" && !KnownDockerSecurityPreset(preset) {
		return fmt.Errorf("agent %q docker.security.preset must be one of default, locked-down, compat", agentName)
	}
	for _, capName := range security.DropCapabilities {
		if strings.TrimSpace(capName) == "" {
			return fmt.Errorf("agent %q docker.security.drop_capabilities contains an empty capability name", agentName)
		}
	}
	for _, tmpfs := range security.Tmpfs {
		target := strings.TrimSpace(tmpfs)
		if target == "" {
			return fmt.Errorf("agent %q docker.security.tmpfs contains an empty mount target", agentName)
		}
		if !filepath.IsAbs(target) {
			return fmt.Errorf("agent %q docker.security.tmpfs target %q must be absolute", agentName, tmpfs)
		}
	}
	return nil
}

func validateDockerCacheConfig(agentName string, cache *DockerCacheConfig) error {
	if cache == nil {
		return nil
	}
	for _, profile := range cache.Profiles {
		if !KnownDockerCacheProfile(profile) {
			return fmt.Errorf("agent %q docker.cache.profiles contains unknown profile %q", agentName, profile)
		}
	}
	for _, mount := range cache.Mounts {
		if strings.TrimSpace(mount.Source) == "" {
			return fmt.Errorf("agent %q docker.cache.mounts[].source is required", agentName)
		}
		if strings.TrimSpace(mount.Target) == "" {
			return fmt.Errorf("agent %q docker.cache.mounts[].target is required", agentName)
		}
		if !filepath.IsAbs(strings.TrimSpace(mount.Target)) {
			return fmt.Errorf("agent %q docker.cache.mounts[].target must be absolute", agentName)
		}
	}
	return nil
}

func validateDockerReuseConfig(agentName string, reuse *DockerReuseConfig) error {
	if reuse == nil {
		return nil
	}
	mode := NormalizeDockerReuseMode(reuse.Mode)
	if mode == "" {
		return nil
	}
	if !KnownDockerReuseMode(mode) {
		return fmt.Errorf("agent %q docker.reuse.mode must be one of none, stateless, lineage", agentName)
	}
	return nil
}

func validateDockerAccessConflicts(agent AgentTypeConfig) error {
	if agent.Docker == nil {
		return nil
	}

	docker := agent.Docker
	homeTarget := dockerConfigHomeTarget(agent)
	envTargets := map[string]string{}
	for key := range agent.Env {
		trimmed := strings.TrimPrefix(strings.TrimSpace(key), "$")
		if trimmed != "" {
			envTargets[trimmed] = "agent env"
		}
	}
	for _, key := range docker.EnvPassthrough {
		trimmed := strings.TrimPrefix(strings.TrimSpace(key), "$")
		if trimmed != "" {
			envTargets[trimmed] = "docker.env_passthrough"
		}
	}
	if docker.Auth != nil && DockerAuthModeUsesEnv(docker.Auth.Mode) {
		target := strings.TrimSpace(docker.Auth.Target)
		if target == "" {
			target = DockerAuthDefaultTarget(docker.Auth.Mode, homeTarget)
		}
		target = strings.TrimPrefix(target, "$")
		if target != "" {
			envTargets[target] = "docker.auth"
		}
	}
	if docker.Secrets != nil {
		for i, item := range docker.Secrets.Env {
			_, target, _, err := ResolveDockerSecretEnvConfig(item)
			if err != nil {
				continue
			}
			if owner, ok := envTargets[target]; ok {
				return fmt.Errorf("agent %q docker.secrets.env[%d] target %q conflicts with %s", agent.Name, i, target, owner)
			}
			envTargets[target] = fmt.Sprintf("docker.secrets.env[%d]", i)
		}
	}

	mountTargets := map[string]string{}
	workspaceTarget := strings.TrimSpace(docker.WorkspaceMountPath)
	if workspaceTarget == "" {
		workspaceTarget = "/workspace"
	}
	mountTargets[filepath.Clean(workspaceTarget)] = "workspace mount"
	for _, mount := range docker.Mounts {
		target := strings.TrimSpace(mount.Target)
		if target != "" {
			mountTargets[filepath.Clean(target)] = "docker.mounts"
		}
	}
	if docker.Auth != nil && DockerAuthModeUsesMount(docker.Auth.Mode) {
		target := strings.TrimSpace(docker.Auth.Target)
		if target == "" {
			target = DockerAuthDefaultTarget(docker.Auth.Mode, homeTarget)
		}
		if target != "" {
			mountTargets[filepath.Clean(target)] = "docker.auth"
		}
	}
	if docker.Cache != nil {
		for _, profile := range docker.Cache.Profiles {
			for _, target := range DockerCacheProfileTargets(profile, homeTarget) {
				mountTargets[filepath.Clean(target)] = "docker.cache.profiles"
			}
		}
		for _, mount := range docker.Cache.Mounts {
			if strings.TrimSpace(mount.Target) != "" {
				mountTargets[filepath.Clean(mount.Target)] = "docker.cache.mounts"
			}
		}
	}
	if docker.Secrets != nil {
		for i, item := range docker.Secrets.Mounts {
			_, target, _, err := ResolveDockerAccessMountConfig(item, homeTarget)
			if err != nil {
				continue
			}
			if owner, ok := conflictingDockerMountTarget(mountTargets, target); ok {
				return fmt.Errorf("agent %q docker.secrets.mounts[%d] target %q conflicts with %s", agent.Name, i, target, owner)
			}
			mountTargets[filepath.Clean(target)] = fmt.Sprintf("docker.secrets.mounts[%d]", i)
		}
	}
	if docker.Tools != nil {
		for i, item := range docker.Tools.Mounts {
			_, target, _, err := ResolveDockerAccessMountConfig(item, homeTarget)
			if err != nil {
				continue
			}
			if owner, ok := conflictingDockerMountTarget(mountTargets, target); ok {
				return fmt.Errorf("agent %q docker.tools.mounts[%d] target %q conflicts with %s", agent.Name, i, target, owner)
			}
			mountTargets[filepath.Clean(target)] = fmt.Sprintf("docker.tools.mounts[%d]", i)
		}
	}
	return nil
}

func dockerConfigHomeTarget(agent AgentTypeConfig) string {
	if value := strings.TrimSpace(agent.Env["HOME"]); value != "" {
		return value
	}
	return DockerHomeDefault
}

func conflictingDockerMountTarget(existing map[string]string, target string) (string, bool) {
	target = filepath.Clean(strings.TrimSpace(target))
	for existingTarget, owner := range existing {
		if dockerPathOverlaps(existingTarget, target) {
			return owner, true
		}
	}
	return "", false
}

func dockerPathOverlaps(left string, right string) bool {
	left = filepath.Clean(strings.TrimSpace(left))
	right = filepath.Clean(strings.TrimSpace(right))
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	return strings.HasPrefix(left, right+string(filepath.Separator)) || strings.HasPrefix(right, left+string(filepath.Separator))
}

func isValidEnvName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "=") {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		case r == '_':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

var scpStyleRepoURLPattern = regexp.MustCompile(`^[^@\s/:]+@[^:/\s]+:.+$`)

func isScpStyleRepoURL(raw string) bool {
	return scpStyleRepoURLPattern.MatchString(strings.TrimSpace(raw))
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
