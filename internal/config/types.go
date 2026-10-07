package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var raw string
	if err := unmarshal(&raw); err != nil {
		return err
	}

	if strings.TrimSpace(raw) == "" {
		d.Duration = 0
		return nil
	}

	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", raw, err)
	}

	d.Duration = parsed
	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	return d.Duration.String(), nil
}

type CodexConfig struct {
	Model             string         `yaml:"model"`
	Reasoning         string         `yaml:"reasoning"`
	MaxTurns          int            `yaml:"max_turns"`
	ThreadSandbox     string         `yaml:"thread_sandbox"`
	TurnSandboxPolicy map[string]any `yaml:"turn_sandbox_policy"`
	ExtraArgs         []string       `yaml:"extra_args"`
}

type ClaudeConfig struct {
	Model     string   `yaml:"model"`
	Reasoning string   `yaml:"reasoning"`
	MaxTurns  int      `yaml:"max_turns"`
	ExtraArgs []string `yaml:"extra_args"`
}

type DockerMountConfig struct {
	Source   string `yaml:"source"`
	Target   string `yaml:"target"`
	ReadOnly bool   `yaml:"read_only"`
}

type DockerCacheMountConfig struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}

type DockerSecretEnvConfig struct {
	Preset string `yaml:"preset"`
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}

type DockerAccessMountConfig struct {
	Preset string `yaml:"preset"`
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}

type DockerSecretsConfig struct {
	Env    []DockerSecretEnvConfig   `yaml:"env"`
	Mounts []DockerAccessMountConfig `yaml:"mounts"`
}

type DockerToolsConfig struct {
	Mounts []DockerAccessMountConfig `yaml:"mounts"`
}

type DockerAuthConfig struct {
	Mode   string `yaml:"mode"`
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}

type DockerSecurityConfig struct {
	Preset           string   `yaml:"preset"`
	NoNewPrivileges  *bool    `yaml:"no_new_privileges"`
	ReadOnlyRootFS   *bool    `yaml:"read_only_root_fs"`
	DropCapabilities []string `yaml:"drop_capabilities"`
	Tmpfs            []string `yaml:"tmpfs"`
}

type DockerCacheConfig struct {
	Profiles []string                 `yaml:"profiles"`
	Mounts   []DockerCacheMountConfig `yaml:"mounts"`
}

type DockerNetworkPolicyConfig struct {
	Mode  string   `yaml:"mode"`
	Allow []string `yaml:"allow"`
}

type DockerReuseConfig struct {
	Mode string `yaml:"mode"`
}

type DockerConfig struct {
	Image              string                     `yaml:"image"`
	ImagePinMode       string                     `yaml:"image_pin_mode"`
	WorkspaceMountPath string                     `yaml:"workspace_mount_path"`
	PullPolicy         string                     `yaml:"pull_policy"`
	Mounts             []DockerMountConfig        `yaml:"mounts"`
	EnvPassthrough     []string                   `yaml:"env_passthrough"`
	Network            string                     `yaml:"network"`
	NetworkPolicy      *DockerNetworkPolicyConfig `yaml:"network_policy"`
	CPUs               float64                    `yaml:"cpus"`
	Memory             string                     `yaml:"memory"`
	PIDsLimit          int                        `yaml:"pids_limit"`
	Secrets            *DockerSecretsConfig       `yaml:"secrets"`
	Tools              *DockerToolsConfig         `yaml:"tools"`
	Auth               *DockerAuthConfig          `yaml:"auth"`
	Security           *DockerSecurityConfig      `yaml:"security"`
	Cache              *DockerCacheConfig         `yaml:"cache"`
	Reuse              *DockerReuseConfig         `yaml:"reuse"`
}

const (
	DockerAuthClaudeAPIKey         = "claude-api-key"
	DockerAuthClaudeProxy          = "claude-proxy"
	DockerAuthClaudeConfig         = "claude-config-mount"
	DockerAuthCodexAPIKey          = "codex-api-key"
	DockerAuthCodexConfig          = "codex-config-mount"
	DockerImagePinModeAllow        = "allow"
	DockerImagePinModeRequire      = "require"
	DockerCacheProfileClaude       = "claude-cache"
	DockerCacheProfileCodex        = "codex-cache"
	DockerCacheProfileNPM          = "npm-cache"
	DockerCacheProfileGo           = "go-cache"
	DockerCacheProfilePip          = "pip-cache"
	DockerCacheProfileCargo        = "cargo-cache"
	DockerNetworkPolicyNone        = "none"
	DockerNetworkPolicyBridge      = "bridge"
	DockerNetworkPolicyAllowlist   = "allowlist"
	DockerSecurityPresetDefault    = "default"
	DockerSecurityPresetLockedDown = "locked-down"
	DockerSecurityPresetCompat     = "compat"
	DockerReuseModeNone            = "none"
	DockerReuseModeStateless       = "stateless"
	DockerReuseModeLineage         = "lineage"
)

type Config struct {
	ConfigPath string `yaml:"-"`
	ConfigDir  string `yaml:"-"`

	AgentPacksDir  string               `yaml:"agent_packs_dir"`
	SourceDefaults SourceDefaultsConfig `yaml:"source_defaults"`
	AgentDefaults  AgentDefaultsConfig  `yaml:"agent_defaults"`
	Defaults       DefaultsConfig       `yaml:"defaults"`
	CodexDefaults  *CodexConfig         `yaml:"codex_defaults"`
	ClaudeDefaults *ClaudeConfig        `yaml:"claude_defaults"`
	User           UserConfig           `yaml:"user"`
	Sources        []SourceConfig       `yaml:"sources"`
	AgentTypes     []AgentTypeConfig    `yaml:"agent_types"`
	Channels       []ChannelConfig      `yaml:"channels"`
	Workspace      WorkspaceConfig      `yaml:"workspace"`
	State          StateConfig          `yaml:"state"`
	Hooks          HooksConfig          `yaml:"hooks"`
	Controls       ControlsConfig       `yaml:"controls"`
	Server         ServerConfig         `yaml:"server"`
	Logging        LoggingConfig        `yaml:"logging"`
}

type DefaultsConfig struct {
	PollInterval        Duration             `yaml:"poll_interval"`
	MaxConcurrentGlobal int                  `yaml:"max_concurrent_global"`
	StallTimeout        Duration             `yaml:"stall_timeout"`
	LabelPrefix         string               `yaml:"label_prefix"`
	AgentContext        string               `yaml:"agent_context"`
	OnDispatch          *DispatchTransition  `yaml:"on_dispatch"`
	OnComplete          *LifecycleTransition `yaml:"on_complete"`
	OnFailure           *LifecycleTransition `yaml:"on_failure"`
}

type LifecycleTransition struct {
	AddLabels    []string `yaml:"add_labels"`
	RemoveLabels []string `yaml:"remove_labels"`
	State        string   `yaml:"state"`
}

type DispatchTransition struct {
	AddLabels    []string `yaml:"add_labels"`
	RemoveLabels []string `yaml:"remove_labels"`
	State        string   `yaml:"state"`
}

type SourceDefaultsConfig struct {
	GitLab     SourceDefaultsEntry `yaml:"gitlab"`
	GitLabEpic SourceDefaultsEntry `yaml:"gitlab_epic"`
	Linear     SourceDefaultsEntry `yaml:"linear"`
}

type SourceDefaultsEntry struct {
	Connection      SourceConnection `yaml:"connection"`
	Repo            string           `yaml:"repo"`
	Filter          FilterConfig     `yaml:"filter"`
	EpicFilter      FilterConfig     `yaml:"epic_filter"`
	IssueFilter     FilterConfig     `yaml:"issue_filter"`
	AgentType       string           `yaml:"agent_type"`
	MaxActiveRuns   int              `yaml:"max_active_runs"`
	PollInterval    Duration         `yaml:"poll_interval"`
	StallTimeout    Duration         `yaml:"stall_timeout"`
	RetryBase       Duration         `yaml:"retry_base"`
	MaxRetryBackoff Duration         `yaml:"max_retry_backoff"`
	MaxAttempts     int              `yaml:"max_attempts"`
	RespectBlockers *bool            `yaml:"respect_blockers"`
}

type AgentDefaultsConfig struct {
	Description     string            `yaml:"description"`
	InstanceName    string            `yaml:"instance_name"`
	Harness         string            `yaml:"harness"`
	Workspace       string            `yaml:"workspace"`
	Prompt          string            `yaml:"prompt"`
	ApprovalPolicy  string            `yaml:"approval_policy"`
	ApprovalTimeout Duration          `yaml:"approval_timeout"`
	Communication   string            `yaml:"communication"`
	MaxConcurrent   int               `yaml:"max_concurrent"`
	StallTimeout    Duration          `yaml:"stall_timeout"`
	Env             map[string]string `yaml:"env"`
	Tools           []string          `yaml:"tools"`
	Skills          []string          `yaml:"skills"`
	ContextFiles    []string          `yaml:"context_files"`
	Docker          *DockerConfig     `yaml:"docker"`
}

type UserConfig struct {
	Name           string `yaml:"name"`
	GitLabUsername string `yaml:"gitlab_username"`
	LinearUsername string `yaml:"linear_username"`
}

type SourceConfig struct {
	Name            string               `yaml:"name"`
	DisplayGroup    string               `yaml:"display_group"`
	Tags            []string             `yaml:"tags"`
	Tracker         string               `yaml:"tracker"`
	Connection      SourceConnection     `yaml:"connection"`
	Repo            string               `yaml:"repo"`
	ProjectURL      string               `yaml:"project_url"`
	Filter          FilterConfig         `yaml:"filter"`
	EpicFilter      FilterConfig         `yaml:"epic_filter"`
	IssueFilter     FilterConfig         `yaml:"issue_filter"`
	AgentType       string               `yaml:"agent_type"`
	MaxActiveRuns   int                  `yaml:"max_active_runs"`
	PollInterval    Duration             `yaml:"poll_interval"`
	RetryBase       Duration             `yaml:"retry_base"`
	MaxRetryBackoff Duration             `yaml:"max_retry_backoff"`
	MaxAttempts     int                  `yaml:"max_attempts"`
	RespectBlockers *bool                `yaml:"respect_blockers"`
	StallTimeout    Duration             `yaml:"stall_timeout"`
	OnDispatch      *DispatchTransition  `yaml:"on_dispatch"`
	OnComplete      *LifecycleTransition `yaml:"on_complete"`
	OnFailure       *LifecycleTransition `yaml:"on_failure"`

	LabelPrefix string `yaml:"label_prefix"`
}

type SourceConnection struct {
	BaseURL  string `yaml:"base_url"`
	TokenEnv string `yaml:"token_env"`
	Project  string `yaml:"project"`
	Group    string `yaml:"group"`
	Team     string `yaml:"team"`
	Token    string `yaml:"-"`
}

type GitLabConnection = SourceConnection

type FilterConfig struct {
	Labels   []string `yaml:"labels"`
	IIDs     []int    `yaml:"iids"`
	Assignee string   `yaml:"assignee"`
	States   []string `yaml:"states"`
}

type AgentTypeConfig struct {
	Name            string            `yaml:"name"`
	AgentPack       string            `yaml:"agent_pack"`
	Description     string            `yaml:"description"`
	InstanceName    string            `yaml:"instance_name"`
	Harness         string            `yaml:"harness"`
	Workspace       string            `yaml:"workspace"`
	Prompt          string            `yaml:"prompt"`
	ApprovalPolicy  string            `yaml:"approval_policy"`
	ApprovalTimeout Duration          `yaml:"approval_timeout"`
	Communication   string            `yaml:"communication"`
	MaxConcurrent   int               `yaml:"max_concurrent"`
	StallTimeout    Duration          `yaml:"stall_timeout"`
	Env             map[string]string `yaml:"env"`
	Tools           []string          `yaml:"tools"`
	Skills          []string          `yaml:"skills"`
	ContextFiles    []string          `yaml:"context_files"`

	Codex  *CodexConfig  `yaml:"codex"`
	Claude *ClaudeConfig `yaml:"claude"`
	Docker *DockerConfig `yaml:"docker"`

	PackPath      string `yaml:"-"`
	RepoPackPath  string `yaml:"-"`
	PackClaudeDir string `yaml:"-"`
	PackCodexDir  string `yaml:"-"`
	Context       string `yaml:"-"`
}

type ChannelConfig struct {
	Name   string         `yaml:"name"`
	Kind   string         `yaml:"kind"`
	Config map[string]any `yaml:"config"`
}

type WorkspaceConfig struct {
	Root string `yaml:"root"`
}

type StateConfig struct {
	Dir             string   `yaml:"dir"`
	RetryBase       Duration `yaml:"retry_base"`
	MaxRetryBackoff Duration `yaml:"max_retry_backoff"`
	MaxAttempts     int      `yaml:"max_attempts"`
}

type HooksConfig struct {
	AfterCreate  string   `yaml:"after_create"`
	BeforeRun    string   `yaml:"before_run"`
	AfterRun     string   `yaml:"after_run"`
	BeforeRemove string   `yaml:"before_remove"`
	Timeout      Duration `yaml:"timeout"`
	Execution    string   `yaml:"execution"`
}

type ControlsConfig struct {
	BeforeWork BeforeWorkControlConfig `yaml:"before_work"`
}

type BeforeWorkControlConfig struct {
	Enabled bool   `yaml:"enabled"`
	Mode    string `yaml:"mode"`
	Prompt  string `yaml:"prompt"`
}

type ServerConfig struct {
	Enabled bool   `yaml:"enabled"`
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
	APIKey  string `yaml:"api_key"`
}

type LoggingConfig struct {
	Level    string `yaml:"level"`
	Dir      string `yaml:"dir"`
	MaxFiles int    `yaml:"max_files"`
}

func (s SourceConfig) EffectiveRetryBase(state StateConfig) time.Duration {
	if s.RetryBase.Duration > 0 {
		return s.RetryBase.Duration
	}
	return state.RetryBase.Duration
}

func (s SourceConfig) EffectiveMaxRetryBackoff(state StateConfig) time.Duration {
	if s.MaxRetryBackoff.Duration > 0 {
		return s.MaxRetryBackoff.Duration
	}
	return state.MaxRetryBackoff.Duration
}

func (s SourceConfig) EffectiveMaxAttempts(state StateConfig) int {
	if s.MaxAttempts > 0 {
		return s.MaxAttempts
	}
	return state.MaxAttempts
}

func (s SourceConfig) EffectiveStallTimeout(agent AgentTypeConfig) time.Duration {
	if s.StallTimeout.Duration > 0 {
		return s.StallTimeout.Duration
	}
	return agent.StallTimeout.Duration
}

func (s SourceConfig) EffectiveRespectBlockers() bool {
	if s.RespectBlockers != nil {
		return *s.RespectBlockers
	}
	return true
}

func (s SourceConfig) EffectiveMaxActiveRuns() int {
	if s.MaxActiveRuns > 0 {
		return s.MaxActiveRuns
	}
	return 1
}

func expandPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func (c SourceConnection) GroupPath() string {
	if strings.TrimSpace(c.Group) != "" {
		return c.Group
	}
	return c.Team
}

func (s SourceConfig) EffectiveIssueFilter() FilterConfig {
	if s.Tracker == "gitlab-epic" {
		if !IsZeroFilter(s.IssueFilter) {
			return s.IssueFilter
		}
		if !IsZeroFilter(s.Filter) {
			filter := s.Filter
			filter.Labels = nil
			return filter
		}
	}
	return s.Filter
}

func (s SourceConfig) EffectiveEpicFilter() FilterConfig {
	if s.Tracker == "gitlab-epic" {
		if !IsZeroFilter(s.EpicFilter) {
			return s.EpicFilter
		}
		if !IsZeroFilter(s.Filter) {
			filter := s.Filter
			filter.Assignee = ""
			return filter
		}
	}
	return s.Filter
}

func IsZeroFilter(filter FilterConfig) bool {
	return len(filter.Labels) == 0 && len(filter.IIDs) == 0 && len(filter.States) == 0 && strings.TrimSpace(filter.Assignee) == ""
}

func ScopedStateDir(cfg *Config, source SourceConfig) string {
	if len(cfg.Sources) <= 1 {
		return cfg.State.Dir
	}
	return filepath.Join(cfg.State.Dir, safeConfigKey(source.Name))
}

func cloneCodexConfig(src *CodexConfig) *CodexConfig {
	if src == nil {
		return nil
	}
	cloned := *src
	if src.TurnSandboxPolicy != nil {
		cloned.TurnSandboxPolicy = cloneStringAnyMap(src.TurnSandboxPolicy)
	}
	if src.ExtraArgs != nil {
		cloned.ExtraArgs = append([]string{}, src.ExtraArgs...)
	}
	return &cloned
}

func cloneClaudeConfig(src *ClaudeConfig) *ClaudeConfig {
	if src == nil {
		return nil
	}
	cloned := *src
	if src.ExtraArgs != nil {
		cloned.ExtraArgs = append([]string{}, src.ExtraArgs...)
	}
	return &cloned
}

func cloneDockerConfig(src *DockerConfig) *DockerConfig {
	if src == nil {
		return nil
	}
	cloned := *src
	if src.Mounts != nil {
		cloned.Mounts = append([]DockerMountConfig{}, src.Mounts...)
	}
	if src.EnvPassthrough != nil {
		cloned.EnvPassthrough = append([]string{}, src.EnvPassthrough...)
	}
	cloned.NetworkPolicy = cloneDockerNetworkPolicyConfig(src.NetworkPolicy)
	cloned.Secrets = cloneDockerSecretsConfig(src.Secrets)
	cloned.Tools = cloneDockerToolsConfig(src.Tools)
	cloned.Auth = cloneDockerAuthConfig(src.Auth)
	cloned.Security = cloneDockerSecurityConfig(src.Security)
	cloned.Cache = cloneDockerCacheConfig(src.Cache)
	cloned.Reuse = cloneDockerReuseConfig(src.Reuse)
	return &cloned
}

func cloneDockerNetworkPolicyConfig(src *DockerNetworkPolicyConfig) *DockerNetworkPolicyConfig {
	if src == nil {
		return nil
	}
	cloned := *src
	if src.Allow != nil {
		cloned.Allow = append([]string{}, src.Allow...)
	}
	return &cloned
}

func cloneDockerAuthConfig(src *DockerAuthConfig) *DockerAuthConfig {
	if src == nil {
		return nil
	}
	cloned := *src
	return &cloned
}

func cloneDockerReuseConfig(src *DockerReuseConfig) *DockerReuseConfig {
	if src == nil {
		return nil
	}
	cloned := *src
	return &cloned
}

func cloneDockerSecurityConfig(src *DockerSecurityConfig) *DockerSecurityConfig {
	if src == nil {
		return nil
	}
	cloned := *src
	cloned.Preset = src.Preset
	if src.DropCapabilities != nil {
		cloned.DropCapabilities = append([]string{}, src.DropCapabilities...)
	}
	if src.Tmpfs != nil {
		cloned.Tmpfs = append([]string{}, src.Tmpfs...)
	}
	if src.NoNewPrivileges != nil {
		cloned.NoNewPrivileges = cloneBoolPointer(src.NoNewPrivileges)
	}
	if src.ReadOnlyRootFS != nil {
		cloned.ReadOnlyRootFS = cloneBoolPointer(src.ReadOnlyRootFS)
	}
	return &cloned
}

func cloneDockerCacheConfig(src *DockerCacheConfig) *DockerCacheConfig {
	if src == nil {
		return nil
	}
	cloned := *src
	if src.Profiles != nil {
		cloned.Profiles = append([]string{}, src.Profiles...)
	}
	if src.Mounts != nil {
		cloned.Mounts = append([]DockerCacheMountConfig{}, src.Mounts...)
	}
	return &cloned
}

func mergeCodexConfig(base *CodexConfig, override *CodexConfig) *CodexConfig {
	if base == nil && override == nil {
		return nil
	}
	if base == nil {
		return cloneCodexConfig(override)
	}
	merged := cloneCodexConfig(base)
	if override == nil {
		return merged
	}
	if override.Model != "" {
		merged.Model = override.Model
	}
	if override.Reasoning != "" {
		merged.Reasoning = override.Reasoning
	}
	if override.MaxTurns != 0 {
		merged.MaxTurns = override.MaxTurns
	}
	if override.ThreadSandbox != "" {
		merged.ThreadSandbox = override.ThreadSandbox
	}
	if override.TurnSandboxPolicy != nil {
		merged.TurnSandboxPolicy = cloneStringAnyMap(override.TurnSandboxPolicy)
	}
	if override.ExtraArgs != nil {
		merged.ExtraArgs = append([]string{}, override.ExtraArgs...)
	}
	return merged
}

func mergeClaudeConfig(base *ClaudeConfig, override *ClaudeConfig) *ClaudeConfig {
	if base == nil && override == nil {
		return nil
	}
	if base == nil {
		return cloneClaudeConfig(override)
	}
	merged := cloneClaudeConfig(base)
	if override == nil {
		return merged
	}
	if override.Model != "" {
		merged.Model = override.Model
	}
	if override.Reasoning != "" {
		merged.Reasoning = override.Reasoning
	}
	if override.MaxTurns != 0 {
		merged.MaxTurns = override.MaxTurns
	}
	if override.ExtraArgs != nil {
		merged.ExtraArgs = append([]string{}, override.ExtraArgs...)
	}
	return merged
}

func mergeDockerConfig(base *DockerConfig, override *DockerConfig) *DockerConfig {
	if base == nil && override == nil {
		return nil
	}
	if base == nil {
		return cloneDockerConfig(override)
	}
	merged := cloneDockerConfig(base)
	if override == nil {
		return merged
	}
	if override.Image != "" {
		merged.Image = override.Image
	}
	if override.ImagePinMode != "" {
		merged.ImagePinMode = override.ImagePinMode
	}
	if override.WorkspaceMountPath != "" {
		merged.WorkspaceMountPath = override.WorkspaceMountPath
	}
	if override.PullPolicy != "" {
		merged.PullPolicy = override.PullPolicy
	}
	if override.Mounts != nil {
		merged.Mounts = append([]DockerMountConfig{}, override.Mounts...)
	}
	if override.EnvPassthrough != nil {
		merged.EnvPassthrough = append([]string{}, override.EnvPassthrough...)
	}
	if override.Network != "" {
		merged.Network = override.Network
	}
	merged.NetworkPolicy = mergeDockerNetworkPolicyConfig(merged.NetworkPolicy, override.NetworkPolicy)
	if override.CPUs != 0 {
		merged.CPUs = override.CPUs
	}
	if override.Memory != "" {
		merged.Memory = override.Memory
	}
	if override.PIDsLimit != 0 {
		merged.PIDsLimit = override.PIDsLimit
	}
	merged.Secrets = mergeDockerSecretsConfig(merged.Secrets, override.Secrets)
	merged.Tools = mergeDockerToolsConfig(merged.Tools, override.Tools)
	merged.Auth = mergeDockerAuthConfig(merged.Auth, override.Auth)
	merged.Security = mergeDockerSecurityConfig(merged.Security, override.Security)
	merged.Cache = mergeDockerCacheConfig(merged.Cache, override.Cache)
	merged.Reuse = mergeDockerReuseConfig(merged.Reuse, override.Reuse)
	return merged
}

func mergeDockerNetworkPolicyConfig(base *DockerNetworkPolicyConfig, override *DockerNetworkPolicyConfig) *DockerNetworkPolicyConfig {
	if base == nil && override == nil {
		return nil
	}
	if base == nil {
		return cloneDockerNetworkPolicyConfig(override)
	}
	merged := cloneDockerNetworkPolicyConfig(base)
	if override == nil {
		return merged
	}
	if override.Mode != "" {
		merged.Mode = override.Mode
	}
	if override.Allow != nil {
		merged.Allow = append([]string{}, override.Allow...)
	}
	return merged
}

func mergeDockerAuthConfig(base *DockerAuthConfig, override *DockerAuthConfig) *DockerAuthConfig {
	if base == nil && override == nil {
		return nil
	}
	if base == nil {
		return cloneDockerAuthConfig(override)
	}
	merged := cloneDockerAuthConfig(base)
	if override == nil {
		return merged
	}
	if override.Mode != "" {
		merged.Mode = override.Mode
	}
	if override.Source != "" {
		merged.Source = override.Source
	}
	if override.Target != "" {
		merged.Target = override.Target
	}
	return merged
}

func mergeDockerReuseConfig(base *DockerReuseConfig, override *DockerReuseConfig) *DockerReuseConfig {
	if base == nil && override == nil {
		return nil
	}
	if base == nil {
		return cloneDockerReuseConfig(override)
	}
	merged := cloneDockerReuseConfig(base)
	if override == nil {
		return merged
	}
	if override.Mode != "" {
		merged.Mode = override.Mode
	}
	return merged
}

func mergeDockerSecurityConfig(base *DockerSecurityConfig, override *DockerSecurityConfig) *DockerSecurityConfig {
	if base == nil && override == nil {
		return nil
	}
	if base == nil {
		if override != nil && strings.TrimSpace(override.Preset) != "" {
			merged := DockerSecurityPresetConfig(override.Preset)
			return overlayDockerSecurityConfig(merged, override)
		}
		return cloneDockerSecurityConfig(override)
	}
	merged := cloneDockerSecurityConfig(base)
	if override == nil {
		return merged
	}
	if strings.TrimSpace(override.Preset) != "" {
		merged = DockerSecurityPresetConfig(override.Preset)
	}
	return overlayDockerSecurityConfig(merged, override)
}

func mergeDockerCacheConfig(base *DockerCacheConfig, override *DockerCacheConfig) *DockerCacheConfig {
	if base == nil && override == nil {
		return nil
	}
	if base == nil {
		return cloneDockerCacheConfig(override)
	}
	merged := cloneDockerCacheConfig(base)
	if override == nil {
		return merged
	}
	if override.Profiles != nil {
		merged.Profiles = append([]string{}, override.Profiles...)
	}
	if override.Mounts != nil {
		merged.Mounts = append([]DockerCacheMountConfig{}, override.Mounts...)
	}
	return merged
}

func cloneStringAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	cloned := make(map[string]any, len(src))
	for k, v := range src {
		cloned[k] = v
	}
	return cloned
}

func cloneDispatchTransition(src *DispatchTransition) *DispatchTransition {
	if src == nil {
		return nil
	}
	cloned := *src
	if src.AddLabels != nil {
		cloned.AddLabels = append([]string{}, src.AddLabels...)
	}
	if src.RemoveLabels != nil {
		cloned.RemoveLabels = append([]string{}, src.RemoveLabels...)
	}
	return &cloned
}

func cloneLifecycleTransition(src *LifecycleTransition) *LifecycleTransition {
	if src == nil {
		return nil
	}
	cloned := *src
	if src.AddLabels != nil {
		cloned.AddLabels = append([]string{}, src.AddLabels...)
	}
	if src.RemoveLabels != nil {
		cloned.RemoveLabels = append([]string{}, src.RemoveLabels...)
	}
	return &cloned
}

func ResolveDispatchTransition(defaults *DispatchTransition, override *DispatchTransition) *DispatchTransition {
	if defaults == nil && override == nil {
		return nil
	}
	if defaults == nil {
		return cloneDispatchTransition(override)
	}
	merged := cloneDispatchTransition(defaults)
	if override == nil {
		return merged
	}
	if override.AddLabels != nil {
		merged.AddLabels = append([]string{}, override.AddLabels...)
	}
	if override.RemoveLabels != nil {
		merged.RemoveLabels = append([]string{}, override.RemoveLabels...)
	}
	if override.State != "" {
		merged.State = override.State
	}
	return merged
}

func ResolveLifecycleTransition(defaults *LifecycleTransition, override *LifecycleTransition) *LifecycleTransition {
	if defaults == nil && override == nil {
		return nil
	}
	if defaults == nil {
		return cloneLifecycleTransition(override)
	}
	merged := cloneLifecycleTransition(defaults)
	if override == nil {
		return merged
	}
	if override.AddLabels != nil {
		merged.AddLabels = append([]string{}, override.AddLabels...)
	}
	if override.RemoveLabels != nil {
		merged.RemoveLabels = append([]string{}, override.RemoveLabels...)
	}
	if override.State != "" {
		merged.State = override.State
	}
	return merged
}

// ResolveCodexConfig merges hardcoded defaults, top-level codex_defaults, and per-agent override.
func ResolveCodexConfig(defaults *CodexConfig, override *CodexConfig) CodexConfig {
	base := &CodexConfig{
		Model:     "gpt-5.4",
		Reasoning: "high",
		MaxTurns:  1,
	}
	return *mergeCodexConfig(mergeCodexConfig(base, defaults), override)
}

// ResolveClaudeConfig merges hardcoded defaults, top-level claude_defaults, and per-agent override.
func ResolveClaudeConfig(defaults *ClaudeConfig, override *ClaudeConfig) ClaudeConfig {
	base := &ClaudeConfig{
		Model:     "claude-opus-4-6",
		Reasoning: "high",
		MaxTurns:  1,
	}
	return *mergeClaudeConfig(mergeClaudeConfig(base, defaults), override)
}

func ResolveDockerConfig(defaults *DockerConfig, override *DockerConfig) DockerConfig {
	base := &DockerConfig{
		ImagePinMode:       DockerImagePinModeAllow,
		WorkspaceMountPath: "/workspace",
		Network:            "bridge",
		PullPolicy:         "missing",
		Security:           DockerSecurityPresetConfig(DockerSecurityPresetDefault),
		Reuse:              &DockerReuseConfig{Mode: DockerReuseModeNone},
	}
	return *mergeDockerConfig(mergeDockerConfig(base, defaults), override)
}

func overlayDockerSecurityConfig(base *DockerSecurityConfig, override *DockerSecurityConfig) *DockerSecurityConfig {
	if base == nil && override == nil {
		return nil
	}
	if base == nil {
		return cloneDockerSecurityConfig(override)
	}
	merged := cloneDockerSecurityConfig(base)
	if override == nil {
		return merged
	}
	if strings.TrimSpace(override.Preset) != "" {
		merged.Preset = NormalizeDockerSecurityPreset(override.Preset)
	}
	if override.NoNewPrivileges != nil {
		merged.NoNewPrivileges = cloneBoolPointer(override.NoNewPrivileges)
	}
	if override.ReadOnlyRootFS != nil {
		merged.ReadOnlyRootFS = cloneBoolPointer(override.ReadOnlyRootFS)
	}
	if override.DropCapabilities != nil {
		merged.DropCapabilities = append([]string{}, override.DropCapabilities...)
	}
	if override.Tmpfs != nil {
		merged.Tmpfs = append([]string{}, override.Tmpfs...)
	}
	return merged
}

func KnownDockerImagePinMode(mode string) bool {
	switch NormalizeDockerImagePinMode(mode) {
	case "", DockerImagePinModeAllow, DockerImagePinModeRequire:
		return true
	default:
		return false
	}
}

func NormalizeDockerImagePinMode(mode string) string {
	return strings.TrimSpace(strings.ToLower(mode))
}

func DockerImageIsPinned(image string) bool {
	image = strings.TrimSpace(image)
	if image == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(image), "sha256:") {
		return isDockerHexDigest(image[len("sha256:"):])
	}
	at := strings.LastIndex(image, "@")
	if at < 0 {
		return false
	}
	digest := strings.TrimSpace(image[at+1:])
	if !strings.HasPrefix(strings.ToLower(digest), "sha256:") {
		return false
	}
	return isDockerHexDigest(digest[len("sha256:"):])
}

func isDockerHexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func KnownDockerSecurityPreset(name string) bool {
	switch NormalizeDockerSecurityPreset(name) {
	case "", DockerSecurityPresetDefault, DockerSecurityPresetLockedDown, DockerSecurityPresetCompat:
		return true
	default:
		return false
	}
}

func NormalizeDockerSecurityPreset(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return ""
	}
	return name
}

func DockerSecurityPresetConfig(name string) *DockerSecurityConfig {
	switch NormalizeDockerSecurityPreset(name) {
	case "", DockerSecurityPresetDefault:
		return &DockerSecurityConfig{
			Preset:           DockerSecurityPresetDefault,
			NoNewPrivileges:  cloneBoolPointer(boolPtr(true)),
			ReadOnlyRootFS:   cloneBoolPointer(boolPtr(true)),
			DropCapabilities: []string{"ALL"},
			Tmpfs:            []string{"/tmp"},
		}
	case DockerSecurityPresetLockedDown:
		return &DockerSecurityConfig{
			Preset:           DockerSecurityPresetLockedDown,
			NoNewPrivileges:  cloneBoolPointer(boolPtr(true)),
			ReadOnlyRootFS:   cloneBoolPointer(boolPtr(true)),
			DropCapabilities: []string{"ALL"},
			Tmpfs:            []string{"/tmp", "/var/tmp"},
		}
	case DockerSecurityPresetCompat:
		return &DockerSecurityConfig{
			Preset:          DockerSecurityPresetCompat,
			NoNewPrivileges: cloneBoolPointer(boolPtr(true)),
			ReadOnlyRootFS:  cloneBoolPointer(boolPtr(false)),
		}
	default:
		return &DockerSecurityConfig{Preset: NormalizeDockerSecurityPreset(name)}
	}
}

func DockerAuthModeUsesMount(mode string) bool {
	switch NormalizeDockerAuthMode(mode) {
	case DockerAuthClaudeConfig, DockerAuthCodexConfig:
		return true
	default:
		return false
	}
}

func DockerAuthModeUsesEnv(mode string) bool {
	switch NormalizeDockerAuthMode(mode) {
	case DockerAuthClaudeAPIKey, DockerAuthClaudeProxy, DockerAuthCodexAPIKey:
		return true
	default:
		return false
	}
}

func DockerAuthDefaultSource(mode string) string {
	switch NormalizeDockerAuthMode(mode) {
	case DockerAuthClaudeAPIKey:
		return "ANTHROPIC_API_KEY"
	case DockerAuthClaudeProxy:
		return "ANTHROPIC_AUTH_TOKEN"
	case DockerAuthCodexAPIKey:
		return "OPENAI_API_KEY"
	default:
		return ""
	}
}

func DockerAuthDefaultTarget(mode string, homeTarget string) string {
	switch NormalizeDockerAuthMode(mode) {
	case DockerAuthClaudeAPIKey:
		return "ANTHROPIC_API_KEY"
	case DockerAuthClaudeProxy:
		return "ANTHROPIC_AUTH_TOKEN"
	case DockerAuthCodexAPIKey:
		return "OPENAI_API_KEY"
	case DockerAuthClaudeConfig:
		return filepath.Join(homeTarget, ".claude")
	case DockerAuthCodexConfig:
		return filepath.Join(homeTarget, ".codex")
	default:
		return ""
	}
}

func DockerAuthModeWritesCodexAuth(mode string) bool {
	return NormalizeDockerAuthMode(mode) == DockerAuthCodexAPIKey
}

func NormalizeDockerAuthMode(mode string) string {
	return strings.TrimSpace(strings.ToLower(mode))
}

func NormalizeDockerNetworkPolicyMode(mode string) string {
	return strings.TrimSpace(strings.ToLower(mode))
}

func NormalizeDockerReuseMode(mode string) string {
	return strings.TrimSpace(strings.ToLower(mode))
}

func KnownDockerReuseMode(mode string) bool {
	switch NormalizeDockerReuseMode(mode) {
	case "", DockerReuseModeNone, DockerReuseModeStateless, DockerReuseModeLineage:
		return true
	default:
		return false
	}
}

func NormalizeDockerNetworkAllowEntry(raw string) string {
	entry := strings.TrimSpace(raw)
	if entry == "" {
		return ""
	}
	if strings.HasPrefix(entry, "*.") {
		return "*." + strings.TrimSuffix(strings.ToLower(strings.TrimPrefix(entry, "*.")), ".")
	}
	return strings.TrimSuffix(strings.ToLower(entry), ".")
}

func EffectiveDockerNetwork(docker *DockerConfig) string {
	if docker == nil {
		return ""
	}
	if docker.NetworkPolicy != nil {
		switch NormalizeDockerNetworkPolicyMode(docker.NetworkPolicy.Mode) {
		case DockerNetworkPolicyNone:
			return DockerNetworkPolicyNone
		case DockerNetworkPolicyBridge, DockerNetworkPolicyAllowlist:
			return DockerNetworkPolicyBridge
		}
	}
	return strings.TrimSpace(docker.Network)
}

func KnownDockerCacheProfile(profile string) bool {
	switch NormalizeDockerCacheProfile(profile) {
	case DockerCacheProfileClaude, DockerCacheProfileCodex, DockerCacheProfileNPM, DockerCacheProfileGo, DockerCacheProfilePip, DockerCacheProfileCargo:
		return true
	default:
		return false
	}
}

func NormalizeDockerCacheProfile(profile string) string {
	return strings.TrimSpace(strings.ToLower(profile))
}

func DockerCacheProfileTargets(profile string, homeTarget string) []string {
	switch NormalizeDockerCacheProfile(profile) {
	case DockerCacheProfileClaude:
		return []string{filepath.Join(homeTarget, ".cache", "claude")}
	case DockerCacheProfileCodex:
		return []string{filepath.Join(homeTarget, ".codex", "cache")}
	case DockerCacheProfileNPM:
		return []string{filepath.Join(homeTarget, ".npm")}
	case DockerCacheProfileGo:
		return []string{filepath.Join(homeTarget, ".cache", "go-build")}
	case DockerCacheProfilePip:
		return []string{filepath.Join(homeTarget, ".cache", "pip")}
	case DockerCacheProfileCargo:
		return []string{
			filepath.Join(homeTarget, ".cargo", "registry"),
			filepath.Join(homeTarget, ".cargo", "git"),
		}
	default:
		return nil
	}
}

func boolPtr(value bool) *bool {
	v := value
	return &v
}

func safeConfigKey(raw string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "source"
	}
	return b.String()
}
