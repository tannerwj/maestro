package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjohnson/maestro/internal/config"
)

func testDefaults(maxConcurrent int) config.DefaultsConfig {
	return config.DefaultsConfig{
		MaxConcurrentGlobal: maxConcurrent,
		StallTimeout:        config.Duration{Duration: time.Minute},
		LabelPrefix:         "maestro",
	}
}

func TestValidateMVPRejectsZeroGlobalConcurrency(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(0),
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
				Name:            "code-pr",
				Harness:         "claude-code",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
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

func TestValidateMVPRejectsNegativeSourceStallTimeout(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
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
				Name:            "repo-maintainer",
				Harness:         "codex",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected linear/codex mvp config to validate: %v", err)
	}

	cfg.Sources[0].StallTimeout = config.Duration{Duration: -time.Minute}
	if err := config.ValidateMVP(cfg); err == nil || !strings.Contains(err.Error(), "stall_timeout") {
		t.Fatalf("negative source stall_timeout error = %v, want a validation error", err)
	}
}

func TestValidateMVPAcceptsWorkspaceNoneWithoutRepo(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{
			{
				Name:      "ops-linear",
				Tracker:   "linear",
				AgentType: "triage",
				Connection: config.SourceConnection{
					Project: "project-1",
					Token:   "token",
				},
				Filter: config.FilterConfig{States: []string{"Todo"}},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:            "triage",
				Harness:         "codex",
				Workspace:       "none",
				Prompt:          promptPath,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected workspace:none config to validate without repo: %v", err)
	}
}

func TestValidateMVPAcceptsScpStyleRepoURL(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{
			{
				Name:      "ops-linear",
				Tracker:   "linear",
				Repo:      "git@gitlab.example.com:team/project.git",
				AgentType: "triage",
				Connection: config.SourceConnection{
					Project: "project-1",
					Token:   "token",
				},
				Filter: config.FilterConfig{States: []string{"Todo"}},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:            "triage",
				Harness:         "codex",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected SCP-style repo URL to validate: %v", err)
	}
}

func TestValidateMVPRejectsInvalidHooksExecution(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks: config.HooksConfig{
			Timeout:   config.Duration{Duration: 30 * time.Second},
			Execution: "spaceship",
		},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{
			{
				Name:      "ops-linear",
				Tracker:   "linear",
				AgentType: "triage",
				Connection: config.SourceConnection{
					Project: "project-1",
					Token:   "token",
				},
				Filter: config.FilterConfig{States: []string{"Todo"}},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:            "triage",
				Harness:         "codex",
				Workspace:       "none",
				Prompt:          promptPath,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
	}

	err := config.ValidateMVP(cfg)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "hooks.execution") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateMVPAcceptsDockerReuseModes(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	for _, mode := range []string{config.DockerReuseModeNone, config.DockerReuseModeStateless, config.DockerReuseModeLineage} {
		cfg := &config.Config{
			Defaults: testDefaults(1),
			Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
			State: config.StateConfig{
				Dir:             filepath.Join(root, "state"),
				RetryBase:       config.Duration{Duration: time.Second},
				MaxRetryBackoff: config.Duration{Duration: time.Minute},
				MaxAttempts:     3,
			},
			Sources: []config.SourceConfig{{
				Name:      "ops-linear",
				Tracker:   "linear",
				AgentType: "triage",
				Connection: config.SourceConnection{
					Project: "project-1",
					Token:   "token",
				},
				Filter: config.FilterConfig{States: []string{"Todo"}},
			}},
			AgentTypes: []config.AgentTypeConfig{{
				Name:            "triage",
				Harness:         "codex",
				Workspace:       "none",
				Prompt:          promptPath,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
				Docker: &config.DockerConfig{
					Image: "maestro-agent:latest",
					Reuse: &config.DockerReuseConfig{Mode: mode},
				},
			}},
		}
		if err := config.ValidateMVP(cfg); err != nil {
			t.Fatalf("expected docker reuse mode %q to validate: %v", mode, err)
		}
	}
}

func TestValidateMVPRejectsUnknownDockerReuseMode(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{{
			Name:      "ops-linear",
			Tracker:   "linear",
			AgentType: "triage",
			Connection: config.SourceConnection{
				Project: "project-1",
				Token:   "token",
			},
			Filter: config.FilterConfig{States: []string{"Todo"}},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "triage",
			Harness:         "codex",
			Workspace:       "none",
			Prompt:          promptPath,
			ApprovalPolicy:  "auto",
			ApprovalTimeout: config.Duration{Duration: time.Hour},
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
			Docker: &config.DockerConfig{
				Image: "maestro-agent:latest",
				Reuse: &config.DockerReuseConfig{Mode: "shared-ish"},
			},
		}},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "docker.reuse.mode") {
		t.Fatalf("validate error = %v, want docker.reuse.mode failure", err)
	}
}

func TestValidateMVPRejectsInvalidColonRepoURL(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{
			{
				Name:      "ops-linear",
				Tracker:   "linear",
				Repo:      "://missing",
				AgentType: "triage",
				Connection: config.SourceConnection{
					Project: "project-1",
					Token:   "token",
				},
				Filter: config.FilterConfig{States: []string{"Todo"}},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:            "triage",
				Harness:         "codex",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "invalid repo url") {
		t.Fatalf("validation error = %v, want invalid repo url", err)
	}
}

func TestValidateMVPAcceptsRepoPackWithoutPromptFile(t *testing.T) {
	root := t.TempDir()

	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{
			{
				Name:      "repo-owned",
				Tracker:   "linear",
				Repo:      "https://gitlab.example.com/team/project.git",
				AgentType: "code-pr",
				Connection: config.SourceConnection{
					Project: "project-1",
					Token:   "token",
				},
				Filter: config.FilterConfig{States: []string{"Todo"}},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:            "code-pr",
				AgentPack:       "repo:.maestro",
				Harness:         "codex",
				Workspace:       "git-clone",
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected repo pack config to validate without prompt file: %v", err)
	}
}

func TestValidateMVPRejectsRepoPackWithoutGitCloneWorkspace(t *testing.T) {
	root := t.TempDir()

	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{
			{
				Name:      "repo-owned",
				Tracker:   "linear",
				AgentType: "ops",
				Connection: config.SourceConnection{
					Project: "project-1",
					Token:   "token",
				},
				Filter: config.FilterConfig{States: []string{"Todo"}},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:            "ops",
				AgentPack:       "repo:.maestro",
				Harness:         "codex",
				Workspace:       "none",
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "requires workspace git-clone") {
		t.Fatalf("validation error = %v, want repo pack workspace error", err)
	}
}

func TestValidateMVPAcceptsClaudeManualApproval(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
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
				Name:            "triage",
				Harness:         "claude-code",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "manual",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected claude manual config to validate: %v", err)
	}
}

func TestValidateMVPAcceptsClaudeMultiTurnOverride(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
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
				AgentType: "code-pr",
				Connection: config.SourceConnection{
					BaseURL: "https://gitlab.example.com",
					Project: "team/project",
					Token:   "token",
				},
				Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:            "code-pr",
				Harness:         "claude-code",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "manual",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
				Claude: &config.ClaudeConfig{
					MaxTurns: 2,
				},
			},
		},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected claude multi-turn override to validate: %v", err)
	}
}

func TestValidateMVPAcceptsDockerHarnessConfig(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	authDir := filepath.Join(root, "auth")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	if err := os.MkdirAll(authDir, 0o755); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{{
			Name:      "platform-dev",
			Tracker:   "gitlab",
			AgentType: "code-pr",
			Connection: config.SourceConnection{
				BaseURL: "https://gitlab.example.com",
				Project: "team/project",
				Token:   "token",
			},
			Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "code-pr",
			Harness:         "claude-code",
			Workspace:       "git-clone",
			Prompt:          promptPath,
			ApprovalPolicy:  "manual",
			ApprovalTimeout: config.Duration{Duration: time.Hour},
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
			Docker: &config.DockerConfig{
				Image:              "maestro-agent:latest",
				WorkspaceMountPath: "/workspace",
				PullPolicy:         "always",
				Mounts: []config.DockerMountConfig{{
					Source:   authDir,
					Target:   "/root/.claude",
					ReadOnly: true,
				}},
				Auth: &config.DockerAuthConfig{
					Mode:   config.DockerAuthClaudeConfig,
					Source: authDir,
				},
				Security: &config.DockerSecurityConfig{
					NoNewPrivileges:  boolPtrTest(true),
					ReadOnlyRootFS:   boolPtrTest(false),
					DropCapabilities: []string{"NET_RAW"},
					Tmpfs:            []string{"/var/tmp"},
				},
				Cache: &config.DockerCacheConfig{
					Profiles: []string{config.DockerCacheProfileGo},
					Mounts: []config.DockerCacheMountConfig{{
						Source: filepath.Join(root, "cache"),
						Target: "/tmp/maestro-home/.cache/go-build",
					}},
				},
				EnvPassthrough: []string{"ANTHROPIC_API_KEY"},
				Network:        "none",
				CPUs:           2,
				Memory:         "4g",
				PIDsLimit:      256,
			},
		}},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected docker config to validate: %v", err)
	}
}

func TestValidateMVPAcceptsDockerStructuredAccessConfig(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{{
			Name:      "platform-dev",
			Tracker:   "gitlab",
			AgentType: "code-pr",
			Connection: config.SourceConnection{
				BaseURL: "https://gitlab.example.com",
				Project: "team/project",
				Token:   "token",
			},
			Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "code-pr",
			Harness:         "claude-code",
			Workspace:       "git-clone",
			Prompt:          promptPath,
			ApprovalPolicy:  "manual",
			ApprovalTimeout: config.Duration{Duration: time.Hour},
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
			Docker: &config.DockerConfig{
				Image: "maestro-agent:latest",
				Secrets: &config.DockerSecretsConfig{
					Env: []config.DockerSecretEnvConfig{{
						Preset: config.DockerSecretEnvPresetAnthropicBaseURL,
					}},
					Mounts: []config.DockerAccessMountConfig{{
						Preset: config.DockerMountPresetNetrc,
						Source: filepath.Join(root, "netrc"),
					}},
				},
				Tools: &config.DockerToolsConfig{
					Mounts: []config.DockerAccessMountConfig{{
						Preset: config.DockerMountPresetGitConfig,
						Source: filepath.Join(root, "gitconfig"),
					}},
				},
			},
		}},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected structured docker access config to validate: %v", err)
	}
}

func TestValidateMVPRejectsWritableDockerMount(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{{
			Name:      "platform-dev",
			Tracker:   "gitlab",
			AgentType: "code-pr",
			Connection: config.SourceConnection{
				BaseURL: "https://gitlab.example.com",
				Project: "team/project",
				Token:   "token",
			},
			Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "code-pr",
			Harness:         "claude-code",
			Workspace:       "git-clone",
			Prompt:          promptPath,
			ApprovalPolicy:  "manual",
			ApprovalTimeout: config.Duration{Duration: time.Hour},
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
			Docker: &config.DockerConfig{
				Image: "maestro-agent:latest",
				Mounts: []config.DockerMountConfig{{
					Source:   filepath.Join(root, "auth"),
					Target:   "/root/.claude",
					ReadOnly: false,
				}},
			},
		}},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "read_only") {
		t.Fatalf("validation error = %v, want read_only error", err)
	}
}

func TestValidateMVPRejectsInvalidDockerPullPolicy(t *testing.T) {
	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             "/tmp/state",
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{{
			Name:      "platform-dev",
			Tracker:   "gitlab",
			AgentType: "code-pr",
			Connection: config.SourceConnection{
				BaseURL: "https://gitlab.example.com",
				Project: "team/project",
				Token:   "token",
			},
			Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "code-pr",
			Harness:         "claude-code",
			Workspace:       "git-clone",
			Prompt:          "/tmp/prompt.md",
			ApprovalPolicy:  "manual",
			ApprovalTimeout: config.Duration{Duration: time.Hour},
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
			Docker: &config.DockerConfig{
				Image:      "maestro-agent:latest",
				PullPolicy: "sometimes",
			},
		}},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "pull_policy") {
		t.Fatalf("validation error = %v, want pull_policy error", err)
	}
}

func TestValidateMVPRejectsInvalidDockerImagePinMode(t *testing.T) {
	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             "/tmp/state",
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{{
			Name:      "platform-dev",
			Tracker:   "gitlab",
			AgentType: "code-pr",
			Connection: config.SourceConnection{
				BaseURL: "https://gitlab.example.com",
				Project: "team/project",
				Token:   "token",
			},
			Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "code-pr",
			Harness:         "claude-code",
			Workspace:       "git-clone",
			Prompt:          "/tmp/prompt.md",
			ApprovalPolicy:  "manual",
			ApprovalTimeout: config.Duration{Duration: time.Hour},
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
			Docker: &config.DockerConfig{
				Image:        "maestro-agent:latest",
				ImagePinMode: "warn",
			},
		}},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "image_pin_mode") {
		t.Fatalf("validation error = %v, want image_pin_mode error", err)
	}
}

func TestValidateMVPRejectsUnpinnedRequiredDockerImage(t *testing.T) {
	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             "/tmp/state",
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{{
			Name:      "platform-dev",
			Tracker:   "gitlab",
			AgentType: "code-pr",
			Connection: config.SourceConnection{
				BaseURL: "https://gitlab.example.com",
				Project: "team/project",
				Token:   "token",
			},
			Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "code-pr",
			Harness:         "claude-code",
			Workspace:       "git-clone",
			Prompt:          "/tmp/prompt.md",
			ApprovalPolicy:  "manual",
			ApprovalTimeout: config.Duration{Duration: time.Hour},
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
			Docker: &config.DockerConfig{
				Image:        "maestro-agent:latest",
				ImagePinMode: config.DockerImagePinModeRequire,
			},
		}},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "digest-pinned") {
		t.Fatalf("validation error = %v, want digest pinning error", err)
	}
}

func TestValidateMVPRejectsInvalidDockerAuthMode(t *testing.T) {
	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             "/tmp/state",
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{{
			Name:      "platform-dev",
			Tracker:   "gitlab",
			AgentType: "code-pr",
			Connection: config.SourceConnection{
				BaseURL: "https://gitlab.example.com",
				Project: "team/project",
				Token:   "token",
			},
			Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "code-pr",
			Harness:         "claude-code",
			Workspace:       "git-clone",
			Prompt:          "/tmp/prompt.md",
			ApprovalPolicy:  "manual",
			ApprovalTimeout: config.Duration{Duration: time.Hour},
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
			Docker: &config.DockerConfig{
				Image: "maestro-agent:latest",
				Auth: &config.DockerAuthConfig{
					Mode: "unknown",
				},
			},
		}},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "docker.auth.mode") {
		t.Fatalf("validation error = %v, want docker.auth.mode error", err)
	}
}

func TestValidateMVPRejectsInvalidDockerSecurityPreset(t *testing.T) {
	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             "/tmp/state",
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{{
			Name:      "platform-dev",
			Tracker:   "gitlab",
			AgentType: "code-pr",
			Connection: config.SourceConnection{
				BaseURL: "https://gitlab.example.com",
				Project: "team/project",
				Token:   "token",
			},
			Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "code-pr",
			Harness:         "claude-code",
			Workspace:       "git-clone",
			Prompt:          "/tmp/prompt.md",
			ApprovalPolicy:  "manual",
			ApprovalTimeout: config.Duration{Duration: time.Hour},
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
			Docker: &config.DockerConfig{
				Image: "maestro-agent:latest",
				Security: &config.DockerSecurityConfig{
					Preset: "strictest",
				},
			},
		}},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "docker.security.preset") {
		t.Fatalf("validation error = %v, want docker.security.preset error", err)
	}
}

func TestValidateMVPRejectsInvalidDockerCacheProfile(t *testing.T) {
	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             "/tmp/state",
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{{
			Name:      "platform-dev",
			Tracker:   "gitlab",
			AgentType: "code-pr",
			Connection: config.SourceConnection{
				BaseURL: "https://gitlab.example.com",
				Project: "team/project",
				Token:   "token",
			},
			Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "code-pr",
			Harness:         "claude-code",
			Workspace:       "git-clone",
			Prompt:          "/tmp/prompt.md",
			ApprovalPolicy:  "manual",
			ApprovalTimeout: config.Duration{Duration: time.Hour},
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
			Docker: &config.DockerConfig{
				Image: "maestro-agent:latest",
				Cache: &config.DockerCacheConfig{
					Profiles: []string{"unknown"},
				},
			},
		}},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "docker.cache.profiles") {
		t.Fatalf("validation error = %v, want docker.cache.profiles error", err)
	}
}

func TestValidateMVPRejectsDockerStructuredSecretEnvConflict(t *testing.T) {
	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             "/tmp/state",
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{{
			Name:      "platform-dev",
			Tracker:   "gitlab",
			AgentType: "code-pr",
			Connection: config.SourceConnection{
				BaseURL: "https://gitlab.example.com",
				Project: "team/project",
				Token:   "token",
			},
			Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "code-pr",
			Harness:         "claude-code",
			Workspace:       "git-clone",
			Prompt:          "/tmp/prompt.md",
			ApprovalPolicy:  "manual",
			ApprovalTimeout: config.Duration{Duration: time.Hour},
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
			Docker: &config.DockerConfig{
				Image:          "maestro-agent:latest",
				EnvPassthrough: []string{"ANTHROPIC_BASE_URL"},
				Secrets: &config.DockerSecretsConfig{
					Env: []config.DockerSecretEnvConfig{{
						Preset: config.DockerSecretEnvPresetAnthropicBaseURL,
					}},
				},
			},
		}},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "docker.secrets.env[0]") {
		t.Fatalf("validation error = %v, want docker.secrets.env conflict", err)
	}
}

func TestValidateMVPRejectsDockerStructuredMountOverlap(t *testing.T) {
	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             "/tmp/state",
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{{
			Name:      "platform-dev",
			Tracker:   "gitlab",
			AgentType: "code-pr",
			Connection: config.SourceConnection{
				BaseURL: "https://gitlab.example.com",
				Project: "team/project",
				Token:   "token",
			},
			Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "code-pr",
			Harness:         "claude-code",
			Workspace:       "git-clone",
			Prompt:          "/tmp/prompt.md",
			ApprovalPolicy:  "manual",
			ApprovalTimeout: config.Duration{Duration: time.Hour},
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
			Docker: &config.DockerConfig{
				Image: "maestro-agent:latest",
				Secrets: &config.DockerSecretsConfig{
					Mounts: []config.DockerAccessMountConfig{{
						Source: "/tmp/netrc",
						Target: "/workspace",
					}},
				},
			},
		}},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "conflicts with workspace mount") {
		t.Fatalf("validation error = %v, want workspace mount conflict", err)
	}
}

func TestValidateMVPRejectsContradictoryDockerNetworkPolicy(t *testing.T) {
	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             "/tmp/state",
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{{
			Name:      "platform-dev",
			Tracker:   "gitlab",
			AgentType: "code-pr",
			Connection: config.SourceConnection{
				BaseURL: "https://gitlab.example.com",
				Project: "team/project",
				Token:   "token",
			},
			Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "code-pr",
			Harness:         "claude-code",
			Workspace:       "git-clone",
			Prompt:          "/tmp/prompt.md",
			ApprovalPolicy:  "manual",
			ApprovalTimeout: config.Duration{Duration: time.Hour},
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
			Docker: &config.DockerConfig{
				Image:   "maestro-agent:latest",
				Network: "host",
				NetworkPolicy: &config.DockerNetworkPolicyConfig{
					Mode:  config.DockerNetworkPolicyAllowlist,
					Allow: []string{"api.openai.com"},
				},
			},
		}},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "conflicts with docker.network_policy.mode=allowlist") {
		t.Fatalf("validation error = %v, want network policy conflict", err)
	}
}

func TestValidateMVPRejectsAllowlistNetworkPolicyProxyEnvOverride(t *testing.T) {
	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             "/tmp/state",
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{{
			Name:      "platform-dev",
			Tracker:   "gitlab",
			AgentType: "code-pr",
			Connection: config.SourceConnection{
				BaseURL: "https://gitlab.example.com",
				Project: "team/project",
				Token:   "token",
			},
			Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "code-pr",
			Harness:         "claude-code",
			Workspace:       "git-clone",
			Prompt:          "/tmp/prompt.md",
			ApprovalPolicy:  "manual",
			ApprovalTimeout: config.Duration{Duration: time.Hour},
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
			Env:             map[string]string{"HTTPS_PROXY": "http://example.com:8080"},
			Docker: &config.DockerConfig{
				Image: "maestro-agent:latest",
				NetworkPolicy: &config.DockerNetworkPolicyConfig{
					Mode:  config.DockerNetworkPolicyAllowlist,
					Allow: []string{"api.openai.com"},
				},
			},
		}},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "Maestro manages proxy env") {
		t.Fatalf("validation error = %v, want proxy env conflict", err)
	}
}

func boolPtrTest(value bool) *bool {
	v := value
	return &v
}

func TestValidateMVPAcceptsClaudeDefaultsMultiTurn(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		ClaudeDefaults: &config.ClaudeConfig{
			MaxTurns: 2,
		},
		Sources: []config.SourceConfig{
			{
				Name:      "platform-dev",
				Tracker:   "gitlab",
				AgentType: "code-pr",
				Connection: config.SourceConnection{
					BaseURL: "https://gitlab.example.com",
					Project: "team/project",
					Token:   "token",
				},
				Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:            "code-pr",
				Harness:         "claude-code",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "manual",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected claude multi-turn defaults to validate: %v", err)
	}
}

func TestValidateMVPRejectsReservedLifecycleLabelsInCustomTransitions(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
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
				AgentType: "code-pr",
				Connection: config.SourceConnection{
					BaseURL: "https://gitlab.example.com",
					Project: "team/project",
					Token:   "token",
				},
				Filter: config.FilterConfig{Labels: []string{"maestro:coding"}},
				OnComplete: &config.LifecycleTransition{
					AddLabels: []string{"maestro:done"},
				},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:            "code-pr",
				Harness:         "codex",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "reserved lifecycle label") {
		t.Fatalf("validation error = %v, want reserved lifecycle label error", err)
	}
}

func TestValidateMVPRejectsZeroApprovalTimeout(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
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
				Name:            "triage",
				Harness:         "claude-code",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "manual",
				ApprovalTimeout: config.Duration{},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "approval_timeout") {
		t.Fatalf("validation error = %v, want approval_timeout error", err)
	}
}

func TestValidateMVPAcceptsGitLabEpicSource(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
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
				Name:            "triage",
				Harness:         "claude-code",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected gitlab epic config to validate: %v", err)
	}
}

func TestValidateMVPAcceptsSlackCommunicationChannel(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{{
			Name:      "platform-dev",
			Tracker:   "gitlab",
			AgentType: "code-pr",
			Connection: config.SourceConnection{
				BaseURL: "https://gitlab.example.com",
				Project: "team/project",
				Token:   "token",
			},
			Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "code-pr",
			Harness:         "claude-code",
			Workspace:       "git-clone",
			Prompt:          promptPath,
			ApprovalPolicy:  "manual",
			ApprovalTimeout: config.Duration{Duration: time.Hour},
			Communication:   "slack-dm",
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
		}},
		Channels: []config.ChannelConfig{{
			Name: "slack-dm",
			Kind: "slack",
			Config: map[string]any{
				"token_env":     "SLACK_BOT_TOKEN",
				"app_token_env": "SLACK_APP_TOKEN",
			},
		}},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected slack communication config to validate: %v", err)
	}
}

func TestValidateMVPRejectsUnknownCommunicationChannel(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{{
			Name:      "platform-dev",
			Tracker:   "gitlab",
			AgentType: "code-pr",
			Connection: config.SourceConnection{
				BaseURL: "https://gitlab.example.com",
				Project: "team/project",
				Token:   "token",
			},
			Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "code-pr",
			Harness:         "claude-code",
			Workspace:       "git-clone",
			Prompt:          promptPath,
			ApprovalPolicy:  "manual",
			ApprovalTimeout: config.Duration{Duration: time.Hour},
			Communication:   "missing-channel",
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
		}},
	}

	err := config.ValidateMVP(cfg)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "unknown communication channel") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateMVPRejectsInvalidEnabledServerConfig(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Server: config.ServerConfig{
			Enabled: true,
			Host:    "",
			Port:    70000,
		},
		Sources: []config.SourceConfig{
			{
				Name:      "platform-dev",
				Tracker:   "gitlab",
				AgentType: "code-pr",
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
				Name:            "code-pr",
				Harness:         "claude-code",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
	}

	if err := config.ValidateMVP(cfg); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateMVPRejectsCredentialBearingRepoURL(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
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
				Name:            "triage",
				Harness:         "claude-code",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
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

func TestValidateMVPRejectsMalformedPromptTemplate(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello {{.Issue.Identifier"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
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
				Connection: config.SourceConnection{
					BaseURL: "https://gitlab.example.com",
					Project: "team/project",
					Token:   "token",
				},
				Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:            "triage",
				Harness:         "claude-code",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
	}

	err := config.ValidateMVP(cfg)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "parse prompt template") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateMVPRejectsInvalidSourceRetryPolicy(t *testing.T) {
	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg := &config.Config{
		Defaults: testDefaults(1),
		Hooks:    config.HooksConfig{Timeout: config.Duration{Duration: 30 * time.Second}},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: 10 * time.Second},
			MaxRetryBackoff: config.Duration{Duration: time.Minute},
			MaxAttempts:     3,
		},
		Sources: []config.SourceConfig{
			{
				Name:            "platform-dev",
				Tracker:         "gitlab",
				AgentType:       "triage",
				RetryBase:       config.Duration{Duration: 2 * time.Minute},
				MaxRetryBackoff: config.Duration{Duration: time.Minute},
				Connection: config.SourceConnection{
					BaseURL: "https://gitlab.example.com",
					Project: "team/project",
					Token:   "token",
				},
				Filter: config.FilterConfig{Labels: []string{"agent:ready"}},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:            "triage",
				Harness:         "claude-code",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "manual",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
	}

	err := config.ValidateMVP(cfg)
	if err == nil || !strings.Contains(err.Error(), "max_retry_backoff") {
		t.Fatalf("validation error = %v, want source max_retry_backoff error", err)
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
		Defaults: testDefaults(1),
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
				Name:            "code-pr",
				Harness:         "claude-code",
				Workspace:       "git-clone",
				Prompt:          firstPrompt,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
			{
				Name:            "triage",
				Harness:         "codex",
				Workspace:       "git-clone",
				Prompt:          secondPrompt,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
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
		Defaults: testDefaults(1),
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
				Name:            "triage",
				Harness:         "claude-code",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
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
		Defaults: testDefaults(1),
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
				Name:            "triage",
				Harness:         "claude-code",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("expected gitlab epic iid filter to validate: %v", err)
	}
}
