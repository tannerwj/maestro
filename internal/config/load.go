package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Load reads the YAML config, applies defaults, resolves env-backed secrets, and normalizes paths.
func Load(path string) (*Config, error) {
	if path == "" {
		path = defaultConfigPath()
	}

	absPath, err := expandPath(path)
	if err != nil {
		return nil, err
	}
	absPath, err = filepath.Abs(absPath)
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}

	return LoadBytes(absPath, raw)
}

// LoadBytes reads a YAML config payload as if it lived at path.
func LoadBytes(path string, raw []byte) (*Config, error) {
	absPath, err := expandPath(path)
	if err != nil {
		return nil, err
	}
	absPath, err = filepath.Abs(absPath)
	if err != nil {
		return nil, err
	}

	return loadBytesAtPath(absPath, raw)
}

func loadBytesAtPath(absPath string, raw []byte) (*Config, error) {
	cfg := &Config{}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}

	cfg.ConfigPath = absPath
	cfg.ConfigDir = filepath.Dir(absPath)
	applySystemDefaults(cfg)
	applySourceDefaults(cfg)
	applyAgentDefaults(cfg)

	if err := resolveAgentPacks(cfg); err != nil {
		return nil, err
	}
	applyDefaults(cfg)

	if err := resolvePaths(cfg); err != nil {
		return nil, err
	}
	if err := resolveAgentContexts(cfg); err != nil {
		return nil, err
	}
	if err := resolveEnv(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func applySystemDefaults(cfg *Config) {
	if cfg.Defaults.PollInterval.Duration == 0 {
		cfg.Defaults.PollInterval = Duration{Duration: 60 * time.Second}
	}
	if cfg.Defaults.MaxConcurrentGlobal == 0 {
		cfg.Defaults.MaxConcurrentGlobal = 1
	}
	if cfg.Defaults.StallTimeout.Duration == 0 {
		cfg.Defaults.StallTimeout = Duration{Duration: 10 * time.Minute}
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.AgentDefaults.ApprovalTimeout.Duration == 0 {
		cfg.AgentDefaults.ApprovalTimeout = Duration{Duration: 24 * time.Hour}
	}
	if cfg.AgentPacksDir == "" {
		cfg.AgentPacksDir = filepath.Join(cfg.ConfigDir, "agents")
	}
	if cfg.Workspace.Root == "" {
		cfg.Workspace.Root = defaultWorkspaceRoot()
	}
	if cfg.State.Dir == "" {
		cfg.State.Dir = defaultStateDir()
	}
	if cfg.State.RetryBase.Duration == 0 {
		cfg.State.RetryBase = Duration{Duration: 10 * time.Second}
	}
	if cfg.State.MaxRetryBackoff.Duration == 0 {
		cfg.State.MaxRetryBackoff = Duration{Duration: 5 * time.Minute}
	}
	if cfg.State.MaxAttempts == 0 {
		cfg.State.MaxAttempts = 3
	}
	if cfg.Logging.Dir == "" {
		cfg.Logging.Dir = defaultLogDir()
	}
	if cfg.Logging.MaxFiles == 0 {
		cfg.Logging.MaxFiles = 20
	}
	if cfg.Hooks.Timeout.Duration == 0 {
		cfg.Hooks.Timeout = Duration{Duration: 30 * time.Second}
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8742
	}
}

func applyDefaults(cfg *Config) {
	for i := range cfg.Sources {
		if cfg.Sources[i].PollInterval.Duration == 0 {
			cfg.Sources[i].PollInterval = cfg.Defaults.PollInterval
		}
	}
	for i := range cfg.AgentTypes {
		if cfg.AgentTypes[i].StallTimeout.Duration == 0 {
			cfg.AgentTypes[i].StallTimeout = cfg.Defaults.StallTimeout
		}
	}
}

func applySourceDefaults(cfg *Config) {
	for i := range cfg.Sources {
		defaults := sourceDefaultsForTracker(cfg.SourceDefaults, cfg.Sources[i].Tracker)
		mergeSourceDefaults(&cfg.Sources[i], defaults)
	}
}

func sourceDefaultsForTracker(defaults SourceDefaultsConfig, tracker string) SourceDefaultsEntry {
	switch tracker {
	case "gitlab":
		return defaults.GitLab
	case "gitlab-epic":
		return defaults.GitLabEpic
	case "linear":
		return defaults.Linear
	default:
		return SourceDefaultsEntry{}
	}
}

func mergeSourceDefaults(target *SourceConfig, defaults SourceDefaultsEntry) {
	if strings.TrimSpace(target.Connection.BaseURL) == "" {
		target.Connection.BaseURL = defaults.Connection.BaseURL
	}
	if strings.TrimSpace(target.Connection.TokenEnv) == "" {
		target.Connection.TokenEnv = defaults.Connection.TokenEnv
	}
	if strings.TrimSpace(target.Connection.Project) == "" {
		target.Connection.Project = defaults.Connection.Project
	}
	if strings.TrimSpace(target.Connection.Group) == "" {
		target.Connection.Group = defaults.Connection.Group
	}
	if strings.TrimSpace(target.Connection.Team) == "" {
		target.Connection.Team = defaults.Connection.Team
	}
	if strings.TrimSpace(target.Repo) == "" {
		target.Repo = defaults.Repo
	}
	target.Filter = mergeFilterDefaults(target.Filter, defaults.Filter)
	target.EpicFilter = mergeFilterDefaults(target.EpicFilter, defaults.EpicFilter)
	target.IssueFilter = mergeFilterDefaults(target.IssueFilter, defaults.IssueFilter)
	if strings.TrimSpace(target.AgentType) == "" {
		target.AgentType = defaults.AgentType
	}
	if target.PollInterval.Duration == 0 {
		target.PollInterval = defaults.PollInterval
	}
}

func mergeFilterDefaults(target FilterConfig, defaults FilterConfig) FilterConfig {
	if len(target.Labels) == 0 {
		target.Labels = append([]string(nil), defaults.Labels...)
	}
	if len(target.IIDs) == 0 {
		target.IIDs = append([]int(nil), defaults.IIDs...)
	}
	if strings.TrimSpace(target.Assignee) == "" {
		target.Assignee = defaults.Assignee
	}
	if len(target.States) == 0 {
		target.States = append([]string(nil), defaults.States...)
	}
	return target
}

func applyAgentDefaults(cfg *Config) {
	for i := range cfg.AgentTypes {
		mergeAgentDefaults(&cfg.AgentTypes[i], cfg.AgentDefaults)
	}
}

func mergeAgentDefaults(target *AgentTypeConfig, defaults AgentDefaultsConfig) {
	if strings.TrimSpace(target.Description) == "" {
		target.Description = defaults.Description
	}
	if strings.TrimSpace(target.InstanceName) == "" {
		target.InstanceName = defaults.InstanceName
	}
	if strings.TrimSpace(target.Harness) == "" {
		target.Harness = defaults.Harness
	}
	if strings.TrimSpace(target.Workspace) == "" {
		target.Workspace = defaults.Workspace
	}
	if strings.TrimSpace(target.Prompt) == "" {
		target.Prompt = defaults.Prompt
	}
	if strings.TrimSpace(target.ApprovalPolicy) == "" {
		target.ApprovalPolicy = defaults.ApprovalPolicy
	}
	if target.ApprovalTimeout.Duration == 0 {
		target.ApprovalTimeout = defaults.ApprovalTimeout
	}
	if strings.TrimSpace(target.Communication) == "" {
		target.Communication = defaults.Communication
	}
	if target.MaxConcurrent == 0 {
		target.MaxConcurrent = defaults.MaxConcurrent
	}
	if target.StallTimeout.Duration == 0 {
		target.StallTimeout = defaults.StallTimeout
	}
	target.Env = mergeStringMap(defaults.Env, target.Env)
	target.Tools = appendUnique(defaults.Tools, target.Tools)
	target.Skills = appendUnique(defaults.Skills, target.Skills)
	target.ContextFiles = appendUnique(defaults.ContextFiles, target.ContextFiles)
}

func resolvePaths(cfg *Config) error {
	var err error

	cfg.Workspace.Root, err = expandPath(cfg.Workspace.Root)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(cfg.Workspace.Root) {
		cfg.Workspace.Root = filepath.Join(cfg.ConfigDir, cfg.Workspace.Root)
	}
	cfg.Workspace.Root, err = filepath.Abs(cfg.Workspace.Root)
	if err != nil {
		return err
	}

	cfg.Logging.Dir, err = expandPath(cfg.Logging.Dir)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(cfg.Logging.Dir) {
		cfg.Logging.Dir = filepath.Join(cfg.ConfigDir, cfg.Logging.Dir)
	}
	cfg.Logging.Dir, err = filepath.Abs(cfg.Logging.Dir)
	if err != nil {
		return err
	}

	cfg.State.Dir, err = expandPath(cfg.State.Dir)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(cfg.State.Dir) {
		cfg.State.Dir = filepath.Join(cfg.ConfigDir, cfg.State.Dir)
	}
	cfg.State.Dir, err = filepath.Abs(cfg.State.Dir)
	if err != nil {
		return err
	}

	cfg.AgentPacksDir, err = expandPath(cfg.AgentPacksDir)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(cfg.AgentPacksDir) {
		cfg.AgentPacksDir = filepath.Join(cfg.ConfigDir, cfg.AgentPacksDir)
	}
	cfg.AgentPacksDir, err = filepath.Abs(cfg.AgentPacksDir)
	if err != nil {
		return err
	}

	for i := range cfg.AgentTypes {
		prompt := cfg.AgentTypes[i].Prompt
		if prompt != "" {
			if !filepath.IsAbs(prompt) {
				prompt = filepath.Join(cfg.ConfigDir, prompt)
			}
			cfg.AgentTypes[i].Prompt = filepath.Clean(prompt)
		}

		for j := range cfg.AgentTypes[i].ContextFiles {
			contextPath := cfg.AgentTypes[i].ContextFiles[j]
			if contextPath == "" {
				continue
			}
			if !filepath.IsAbs(contextPath) {
				contextPath = filepath.Join(cfg.ConfigDir, contextPath)
			}
			cfg.AgentTypes[i].ContextFiles[j] = filepath.Clean(contextPath)
		}
	}

	return nil
}

func resolveEnv(cfg *Config) error {
	for i := range cfg.Sources {
		resolveFilterAssignee(cfg, &cfg.Sources[i].Filter, cfg.Sources[i].Tracker)
		resolveFilterAssignee(cfg, &cfg.Sources[i].EpicFilter, cfg.Sources[i].Tracker)
		resolveFilterAssignee(cfg, &cfg.Sources[i].IssueFilter, cfg.Sources[i].Tracker)

		tokenEnv := strings.TrimSpace(cfg.Sources[i].Connection.TokenEnv)
		if tokenEnv == "" {
			continue
		}
		value := strings.TrimSpace(os.Getenv(tokenEnv))
		if value == "" {
			return fmt.Errorf("source %q token env %q is unset or empty", cfg.Sources[i].Name, tokenEnv)
		}
		cfg.Sources[i].Connection.Token = value
	}

	return nil
}

func resolveFilterAssignee(cfg *Config, filter *FilterConfig, tracker string) {
	if filter.Assignee != "$MAESTRO_USER" {
		return
	}
	switch tracker {
	case "linear":
		filter.Assignee = cfg.User.LinearUsername
	default:
		filter.Assignee = cfg.User.GitLabUsername
	}
}

func defaultConfigPath() string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if strings.TrimSpace(configHome) == "" {
		home, _ := os.UserHomeDir()
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "maestro", "maestro.yaml")
}

func defaultWorkspaceRoot() string {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if strings.TrimSpace(dataHome) == "" {
		home, _ := os.UserHomeDir()
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "maestro", "workspaces")
}

func defaultLogDir() string {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if strings.TrimSpace(stateHome) == "" {
		home, _ := os.UserHomeDir()
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "maestro", "logs")
}

func defaultStateDir() string {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if strings.TrimSpace(stateHome) == "" {
		home, _ := os.UserHomeDir()
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "maestro")
}
