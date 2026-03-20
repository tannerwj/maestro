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

type Config struct {
	ConfigPath string `yaml:"-"`
	ConfigDir  string `yaml:"-"`

	AgentPacksDir  string               `yaml:"agent_packs_dir"`
	SourceDefaults SourceDefaultsConfig `yaml:"source_defaults"`
	AgentDefaults  AgentDefaultsConfig  `yaml:"agent_defaults"`
	Defaults       DefaultsConfig       `yaml:"defaults"`
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
	PollInterval        Duration `yaml:"poll_interval"`
	MaxConcurrentGlobal int      `yaml:"max_concurrent_global"`
	StallTimeout        Duration `yaml:"stall_timeout"`
}

type SourceDefaultsConfig struct {
	GitLab     SourceDefaultsEntry `yaml:"gitlab"`
	GitLabEpic SourceDefaultsEntry `yaml:"gitlab_epic"`
	Linear     SourceDefaultsEntry `yaml:"linear"`
}

type SourceDefaultsEntry struct {
	Connection   SourceConnection `yaml:"connection"`
	Repo         string           `yaml:"repo"`
	Filter       FilterConfig     `yaml:"filter"`
	EpicFilter   FilterConfig     `yaml:"epic_filter"`
	IssueFilter  FilterConfig     `yaml:"issue_filter"`
	AgentType    string           `yaml:"agent_type"`
	PollInterval Duration         `yaml:"poll_interval"`
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
}

type UserConfig struct {
	Name           string `yaml:"name"`
	GitLabUsername string `yaml:"gitlab_username"`
	LinearUsername string `yaml:"linear_username"`
}

type SourceConfig struct {
	Name         string           `yaml:"name"`
	DisplayGroup string           `yaml:"display_group"`
	Tags         []string         `yaml:"tags"`
	Tracker      string           `yaml:"tracker"`
	Connection   SourceConnection `yaml:"connection"`
	Repo         string           `yaml:"repo"`
	Filter       FilterConfig     `yaml:"filter"`
	EpicFilter   FilterConfig     `yaml:"epic_filter"`
	IssueFilter  FilterConfig     `yaml:"issue_filter"`
	AgentType    string           `yaml:"agent_type"`
	PollInterval Duration         `yaml:"poll_interval"`
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
}

type LoggingConfig struct {
	Level    string `yaml:"level"`
	Dir      string `yaml:"dir"`
	MaxFiles int    `yaml:"max_files"`
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
		if !isZeroFilter(s.IssueFilter) {
			return s.IssueFilter
		}
		if !isZeroFilter(s.Filter) {
			filter := s.Filter
			filter.Labels = nil
			return filter
		}
	}
	return s.Filter
}

func (s SourceConfig) EffectiveEpicFilter() FilterConfig {
	if s.Tracker == "gitlab-epic" {
		if !isZeroFilter(s.EpicFilter) {
			return s.EpicFilter
		}
		if !isZeroFilter(s.Filter) {
			filter := s.Filter
			filter.Assignee = ""
			return filter
		}
	}
	return s.Filter
}

func isZeroFilter(filter FilterConfig) bool {
	return len(filter.Labels) == 0 && len(filter.IIDs) == 0 && len(filter.States) == 0 && strings.TrimSpace(filter.Assignee) == ""
}

func ScopedStateDir(cfg *Config, source SourceConfig) string {
	if len(cfg.Sources) <= 1 {
		return cfg.State.Dir
	}
	return filepath.Join(cfg.State.Dir, safeConfigKey(source.Name))
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
