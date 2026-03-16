package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjohnson/maestro/internal/config"
)

func TestValidateMVPRejectsZeroGlobalConcurrency(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: config.DefaultsConfig{MaxConcurrentGlobal: 0, StallTimeout: config.Duration{Duration: time.Minute}},
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{
			{
				Name:      "platform-dev",
				Tracker:   "linear",
				Repo:      "https://gitlab.example.com/team/project.git",
				AgentType: "code-pr",
				Connection: config.GitLabConnection{
					BaseURL: "https://example.com",
					Project: "team/project",
					Token:   "token",
				},
				Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:           "code-pr",
				Harness:        "claude-code",
				Workspace:      "git-clone",
				Prompt:         promptPath,
				ApprovalPolicy: "auto",
				MaxConcurrent:  1,
				StallTimeout:   config.Duration{Duration: time.Minute},
			},
		},
	}

	err := config.ValidateMVP(cfg)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "must be at least 1") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateMVPAcceptsLinearCodexSource(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: config.DefaultsConfig{MaxConcurrentGlobal: 1, StallTimeout: config.Duration{Duration: time.Minute}},
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{
			{
				Name:      "personal-linear",
				Tracker:   "linear",
				Repo:      "https://gitlab.example.com/team/maestro-testbed.git",
				AgentType: "repo-maintainer",
				Connection: config.SourceConnection{
					Project: "project-1",
					Token:   "token",
				},
				Filter: config.FilterConfig{States: []string{"Todo"}},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:           "repo-maintainer",
				Harness:        "codex",
				Workspace:      "git-clone",
				Prompt:         promptPath,
				ApprovalPolicy: "auto",
				MaxConcurrent:  1,
				StallTimeout:   config.Duration{Duration: time.Minute},
			},
		},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected linear/codex mvp config to validate: %v", err)
	}
}

func TestValidateMVPAcceptsClaudeManualApproval(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: config.DefaultsConfig{MaxConcurrentGlobal: 1, StallTimeout: config.Duration{Duration: time.Minute}},
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{
			{
				Name:      "platform-dev",
				Tracker:   "gitlab",
				AgentType: "triage",
				Connection: config.GitLabConnection{
					BaseURL: "https://gitlab.example.com",
					Project: "team/project",
					Token:   "token",
				},
				Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:           "triage",
				Harness:        "claude-code",
				Workspace:      "git-clone",
				Prompt:         promptPath,
				ApprovalPolicy: "manual",
				MaxConcurrent:  1,
				StallTimeout:   config.Duration{Duration: time.Minute},
			},
		},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected claude manual config to validate: %v", err)
	}
}

func TestValidateMVPAcceptsGitLabEpicSource(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: config.DefaultsConfig{MaxConcurrentGlobal: 1, StallTimeout: config.Duration{Duration: time.Minute}},
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{
			{
				Name:      "platform-epics",
				Tracker:   "gitlab-epic",
				Repo:      "https://gitlab.com/team/project.git",
				AgentType: "triage",
				Connection: config.SourceConnection{
					BaseURL: "https://gitlab.example.com",
					Group:   "team/platform",
					Token:   "token",
				},
				Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:           "triage",
				Harness:        "claude-code",
				Workspace:      "git-clone",
				Prompt:         promptPath,
				ApprovalPolicy: "auto",
				MaxConcurrent:  1,
				StallTimeout:   config.Duration{Duration: time.Minute},
			},
		},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected gitlab epic config to validate: %v", err)
	}
}

func TestValidateMVPRejectsCredentialBearingRepoURL(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: config.DefaultsConfig{MaxConcurrentGlobal: 1, StallTimeout: config.Duration{Duration: time.Minute}},
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{
			{
				Name:      "platform-epics",
				Tracker:   "gitlab-epic",
				Repo:      "https://oauth2:secret@example.com/team/project.git",
				AgentType: "triage",
				Connection: config.SourceConnection{
					BaseURL: "https://gitlab.example.com",
					Group:   "team/platform",
					Token:   "token",
				},
				Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:           "triage",
				Harness:        "claude-code",
				Workspace:      "git-clone",
				Prompt:         promptPath,
				ApprovalPolicy: "auto",
				MaxConcurrent:  1,
				StallTimeout:   config.Duration{Duration: time.Minute},
			},
		},
	}

	err := config.ValidateMVP(cfg)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "must not embed credentials") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateMVPAcceptsMultipleSourcesAndAgents(t *testing.T) {
	root := t.TempDir()
	firstPrompt := filepath.Join(root, "prompt-1.md")
	secondPrompt := filepath.Join(root, "prompt-2.md")
	for _, path := range []string{firstPrompt, secondPrompt} {
		if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
			t.Fatalf("write prompt: %v", err)
		}
	}

	cfg := &config.Config{
		Defaults: config.DefaultsConfig{MaxConcurrentGlobal: 1, StallTimeout: config.Duration{Duration: time.Minute}},
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{
			{
				Name:      "gitlab-one",
				Tracker:   "gitlab",
				AgentType: "code-pr",
				Connection: config.SourceConnection{
					BaseURL: "https://gitlab.example.com",
					Project: "team/project",
					Token:   "token",
				},
				Filter: config.FilterConfig{Labels: []string{"ready-a"}},
			},
			{
				Name:      "linear-two",
				Tracker:   "linear",
				AgentType: "triage",
				Repo:      "https://gitlab.example.com/team/project.git",
				Connection: config.SourceConnection{
					Project: "project-1",
					Token:   "token",
				},
				Filter: config.FilterConfig{States: []string{"Todo"}},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:           "code-pr",
				Harness:        "claude-code",
				Workspace:      "git-clone",
				Prompt:         firstPrompt,
				ApprovalPolicy: "auto",
				MaxConcurrent:  1,
				StallTimeout:   config.Duration{Duration: time.Minute},
			},
			{
				Name:           "triage",
				Harness:        "codex",
				Workspace:      "git-clone",
				Prompt:         secondPrompt,
				ApprovalPolicy: "auto",
				MaxConcurrent:  1,
				StallTimeout:   config.Duration{Duration: time.Minute},
			},
		},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected multi-source config to validate: %v", err)
	}
}

func TestValidateMVPRejectsGitLabEpicAssigneeOnEpicFilter(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: config.DefaultsConfig{MaxConcurrentGlobal: 1, StallTimeout: config.Duration{Duration: time.Minute}},
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{
			{
				Name:      "platform-epics",
				Tracker:   "gitlab-epic",
				Repo:      "https://gitlab.com/team/project.git",
				AgentType: "triage",
				Connection: config.SourceConnection{
					BaseURL: "https://gitlab.example.com",
					Group:   "team/platform",
					Token:   "token",
				},
				EpicFilter: config.FilterConfig{Labels: []string{"agent:ready"}, Assignee: "tj"},
				IssueFilter: config.FilterConfig{
					Labels: []string{"backend"},
				},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:           "triage",
				Harness:        "claude-code",
				Workspace:      "git-clone",
				Prompt:         promptPath,
				ApprovalPolicy: "auto",
				MaxConcurrent:  1,
				StallTimeout:   config.Duration{Duration: time.Minute},
			},
		},
	}

	err := config.ValidateMVP(cfg)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "epic_filter.assignee is unsupported") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateMVPAcceptsGitLabEpicIIDFilter(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: config.DefaultsConfig{MaxConcurrentGlobal: 1, StallTimeout: config.Duration{Duration: time.Minute}},
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{
			{
				Name:      "platform-epics",
				Tracker:   "gitlab-epic",
				Repo:      "https://gitlab.example.com/team/project.git",
				AgentType: "triage",
				Connection: config.SourceConnection{
					BaseURL: "https://gitlab.example.com",
					Group:   "team/platform",
					Token:   "token",
				},
				EpicFilter: config.FilterConfig{IIDs: []int{1, 2}},
				IssueFilter: config.FilterConfig{
					Labels: []string{"agent:ready"},
				},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:           "triage",
				Harness:        "claude-code",
				Workspace:      "git-clone",
				Prompt:         promptPath,
				ApprovalPolicy: "auto",
				MaxConcurrent:  1,
				StallTimeout:   config.Duration{Duration: time.Minute},
			},
		},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected gitlab epic iid filter to validate: %v", err)
	}
}
