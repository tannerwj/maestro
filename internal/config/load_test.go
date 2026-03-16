package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tjohnson/maestro/internal/config"
)

func TestLoadAndValidateMVP(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "secret-token")

	root := t.TempDir()
	promptDir := filepath.Join(root, "agents", "code-pr")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatalf("mkdir prompt dir: %v", err)
	}
	promptPath := filepath.Join(promptDir, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("Issue {{.Issue.Identifier}}"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	configPath := filepath.Join(root, "maestro.yaml")
	raw := `
defaults:
  poll_interval: 5s
  max_concurrent_global: 1
user:
  name: TJ
  gitlab_username: tjohnson
sources:
  - name: platform-dev
    tracker: gitlab
    connection:
      base_url: https://gitlab.example.com
      token_env: GITLAB_TOKEN
      project: team/project
    filter:
      labels: [agent:ready]
      assignee: $MAESTRO_USER
    agent_type: code-pr
agent_types:
  - name: code-pr
    harness: claude-code
    workspace: git-clone
    prompt: agents/code-pr/prompt.md
    approval_policy: auto
    max_concurrent: 1
workspace:
  root: ./workspaces
logging:
  level: debug
  dir: ./logs
`
	if err := os.WriteFile(configPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("validate config: %v", err)
	}

	if got, want := cfg.Sources[0].Connection.Token, "secret-token"; got != want {
		t.Fatalf("resolved token = %q, want %q", got, want)
	}
	if got, want := cfg.Sources[0].Filter.Assignee, "tjohnson"; got != want {
		t.Fatalf("resolved assignee = %q, want %q", got, want)
	}
	if got, want := cfg.Sources[0].PollInterval.Duration, 5*time.Second; got != want {
		t.Fatalf("poll interval = %s, want %s", got, want)
	}
	if got, want := cfg.State.MaxAttempts, 3; got != want {
		t.Fatalf("max attempts = %d, want %d", got, want)
	}
}

func TestLoadResolvesLinearAssignee(t *testing.T) {
	t.Setenv("LINEAR_TOKEN", "secret-token")

	root := t.TempDir()
	promptDir := filepath.Join(root, "agents", "code-pr")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatalf("mkdir prompt dir: %v", err)
	}
	promptPath := filepath.Join(promptDir, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("Issue {{.Issue.Identifier}}"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	configPath := filepath.Join(root, "maestro.yaml")
	raw := `
defaults:
  poll_interval: 5s
  max_concurrent_global: 1
user:
  name: TJ
  linear_username: tj
sources:
  - name: personal-linear
    tracker: linear
    connection:
      token_env: LINEAR_TOKEN
      project: project-1
    repo: https://gitlab.example.com/team/maestro-testbed.git
    filter:
      states: [Todo]
      assignee: $MAESTRO_USER
    agent_type: code-pr
agent_types:
  - name: code-pr
    harness: codex
    workspace: git-clone
    prompt: agents/code-pr/prompt.md
    approval_policy: auto
    max_concurrent: 1
workspace:
  root: ./workspaces
logging:
  level: debug
  dir: ./logs
`
	if err := os.WriteFile(configPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if err := config.ValidateMVP(cfg); err != nil {
		t.Fatalf("validate config: %v", err)
	}

	if got, want := cfg.Sources[0].Filter.Assignee, "tj"; got != want {
		t.Fatalf("resolved assignee = %q, want %q", got, want)
	}
}

func TestLoadResolvesGitLabEpicIssueFilterAssignee(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "secret-token")

	root := t.TempDir()
	promptDir := filepath.Join(root, "agents", "code-pr")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatalf("mkdir prompt dir: %v", err)
	}
	promptPath := filepath.Join(promptDir, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("Issue {{.Issue.Identifier}}"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	configPath := filepath.Join(root, "maestro.yaml")
	raw := `
defaults:
  poll_interval: 5s
  max_concurrent_global: 1
user:
  name: TJ
  gitlab_username: tj
sources:
  - name: platform-epics
    tracker: gitlab-epic
    connection:
      base_url: https://gitlab.example.com
      token_env: GITLAB_TOKEN
      group: team/platform
    repo: https://gitlab.example.com/team/project.git
    epic_filter:
      labels: [bucket:ready]
    issue_filter:
      labels: [agent:ready]
      assignee: $MAESTRO_USER
    agent_type: code-pr
agent_types:
  - name: code-pr
    harness: claude-code
    workspace: git-clone
    prompt: agents/code-pr/prompt.md
    approval_policy: auto
    max_concurrent: 1
workspace:
  root: ./workspaces
logging:
  level: debug
  dir: ./logs
`
	if err := os.WriteFile(configPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got, want := cfg.Sources[0].IssueFilter.Assignee, "tj"; got != want {
		t.Fatalf("resolved issue_filter assignee = %q, want %q", got, want)
	}
}

func TestLoadMergesAgentPackDefaultsAndOverrides(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "secret-token")

	root := t.TempDir()
	packsDir := filepath.Join(root, "agent-packs", "repo-maintainer")
	if err := os.MkdirAll(packsDir, 0o755); err != nil {
		t.Fatalf("mkdir pack dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packsDir, "prompt.md"), []byte("Agent {{.Agent.Name}} using {{.Agent.Harness}}"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packsDir, "context.md"), []byte("Run the narrowest verification."), 0o644); err != nil {
		t.Fatalf("write context: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packsDir, "agent.yaml"), []byte(`
name: repo-maintainer
description: Maintains repositories.
instance_name: maintainer
harness: claude-code
workspace: git-clone
prompt: prompt.md
approval_policy: auto
max_concurrent: 1
tools: [git, make]
skills: [small-diffs]
context_files: [context.md]
env:
  BASE_ENV: from-pack
`), 0o644); err != nil {
		t.Fatalf("write pack: %v", err)
	}

	configPath := filepath.Join(root, "maestro.yaml")
	raw := `
agent_packs_dir: ./agent-packs
defaults:
  poll_interval: 5s
  max_concurrent_global: 1
user:
  name: TJ
  gitlab_username: tjohnson
sources:
  - name: platform-dev
    tracker: gitlab
    connection:
      base_url: https://gitlab.example.com
      token_env: GITLAB_TOKEN
      project: team/project
    filter:
      labels: [agent:ready]
    agent_type: repo-maintainer
agent_types:
  - name: repo-maintainer
    agent_pack: repo-maintainer
    harness: codex
    env:
      EXTRA_ENV: from-config
workspace:
  root: ./workspaces
logging:
  level: debug
  dir: ./logs
`
	if err := os.WriteFile(configPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	agent := cfg.AgentTypes[0]
	if agent.PackPath != filepath.Join(root, "agent-packs", "repo-maintainer", "agent.yaml") {
		t.Fatalf("pack path = %q", agent.PackPath)
	}
	if agent.Harness != "codex" {
		t.Fatalf("harness = %q, want codex", agent.Harness)
	}
	if agent.InstanceName != "maintainer" {
		t.Fatalf("instance name = %q", agent.InstanceName)
	}
	if agent.Prompt != filepath.Join(root, "agent-packs", "repo-maintainer", "prompt.md") {
		t.Fatalf("prompt = %q", agent.Prompt)
	}
	if agent.ApprovalPolicy != "auto" {
		t.Fatalf("approval policy = %q", agent.ApprovalPolicy)
	}
	if got := agent.Env["BASE_ENV"]; got != "from-pack" {
		t.Fatalf("base env = %q", got)
	}
	if got := agent.Env["EXTRA_ENV"]; got != "from-config" {
		t.Fatalf("extra env = %q", got)
	}
	if got, want := agent.Tools, []string{"git", "make"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("tools = %v, want %v", got, want)
	}
	if got := agent.Context; got != "Run the narrowest verification." {
		t.Fatalf("context = %q", got)
	}
}

func TestLoadAppliesTrackerAndAgentDefaults(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "gitlab-secret")
	t.Setenv("LINEAR_TOKEN", "linear-secret")

	root := t.TempDir()
	promptDir := filepath.Join(root, "agents", "code-pr")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatalf("mkdir prompt dir: %v", err)
	}
	promptPath := filepath.Join(promptDir, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("Issue {{.Issue.Identifier}}"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	contextPath := filepath.Join(root, "shared-context.md")
	if err := os.WriteFile(contextPath, []byte("Shared operator context."), 0o644); err != nil {
		t.Fatalf("write context: %v", err)
	}

	configPath := filepath.Join(root, "maestro.yaml")
	raw := `
defaults:
  poll_interval: 5s
  max_concurrent_global: 3
  stall_timeout: 12m
source_defaults:
  gitlab:
    connection:
      base_url: https://gitlab.example.com
      token_env: GITLAB_TOKEN
    filter:
      assignee: $MAESTRO_USER
      labels: [agent:ready]
  gitlab_epic:
    connection:
      base_url: https://gitlab.example.com
      token_env: GITLAB_TOKEN
      group: team/platform
    repo: https://gitlab.example.com/team/platform/repo.git
    epic_filter:
      iids: [7]
    issue_filter:
      labels: [epic:ready]
      assignee: $MAESTRO_USER
  linear:
    connection:
      token_env: LINEAR_TOKEN
    filter:
      states: [Todo]
      assignee: $MAESTRO_USER
agent_defaults:
  harness: claude-code
  workspace: git-clone
  approval_policy: auto
  max_concurrent: 2
  context_files: [shared-context.md]
user:
  name: TJ
  gitlab_username: tjohnson
  linear_username: tj@example.com
sources:
  - name: project-a
    tracker: gitlab
    connection:
      project: team/project-a
    agent_type: code-pr
  - name: epic-a
    tracker: gitlab-epic
    issue_filter:
      labels: [epic:owned]
    agent_type: repo-maintainer
  - name: linear-a
    tracker: linear
    connection:
      project: Project A
    repo: https://gitlab.example.com/team/project-b.git
    agent_type: triage
agent_types:
  - name: code-pr
    prompt: agents/code-pr/prompt.md
  - name: repo-maintainer
    prompt: agents/code-pr/prompt.md
  - name: triage
    prompt: agents/code-pr/prompt.md
workspace:
  root: ./workspaces
logging:
  dir: ./logs
`
	if err := os.WriteFile(configPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got := cfg.Sources[0].Connection.BaseURL; got != "https://gitlab.example.com" {
		t.Fatalf("gitlab base url = %q", got)
	}
	if got := cfg.Sources[0].Filter.Assignee; got != "tjohnson" {
		t.Fatalf("gitlab assignee = %q", got)
	}
	if got := cfg.Sources[1].Connection.Group; got != "team/platform" {
		t.Fatalf("epic group = %q", got)
	}
	if got := cfg.Sources[1].EpicFilter.IIDs; len(got) != 1 || got[0] != 7 {
		t.Fatalf("epic iids = %v", got)
	}
	if got := cfg.Sources[1].IssueFilter.Labels; len(got) != 1 || got[0] != "epic:owned" {
		t.Fatalf("epic issue labels = %v", got)
	}
	if got := cfg.Sources[1].IssueFilter.Assignee; got != "tjohnson" {
		t.Fatalf("epic issue assignee = %q", got)
	}
	if got := cfg.Sources[2].Filter.Assignee; got != "tj@example.com" {
		t.Fatalf("linear assignee = %q", got)
	}
	for _, agent := range cfg.AgentTypes {
		if agent.Harness != "claude-code" {
			t.Fatalf("agent %s harness = %q", agent.Name, agent.Harness)
		}
		if agent.MaxConcurrent != 2 {
			t.Fatalf("agent %s max_concurrent = %d", agent.Name, agent.MaxConcurrent)
		}
		if len(agent.ContextFiles) != 1 || agent.ContextFiles[0] != contextPath {
			t.Fatalf("agent %s context files = %v", agent.Name, agent.ContextFiles)
		}
		if agent.Context != "Shared operator context." {
			t.Fatalf("agent %s context = %q", agent.Name, agent.Context)
		}
	}
}

func TestLoadAgentDefaultsOverridePackDefaults(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "gitlab-secret")

	root := t.TempDir()
	packDir := filepath.Join(root, "packs", "repo-maintainer")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("mkdir pack dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "prompt.md"), []byte("Prompt"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "context.md"), []byte("Context"), 0o644); err != nil {
		t.Fatalf("write context: %v", err)
	}
	rawPack := `
name: repo-maintainer
harness: claude-code
workspace: git-clone
prompt: prompt.md
approval_policy: manual
max_concurrent: 1
context_files: [context.md]
`
	if err := os.WriteFile(filepath.Join(packDir, "agent.yaml"), []byte(rawPack), 0o644); err != nil {
		t.Fatalf("write pack: %v", err)
	}

	configPath := filepath.Join(root, "maestro.yaml")
	raw := `
defaults:
  poll_interval: 5s
  max_concurrent_global: 1
agent_packs_dir: ./packs
agent_defaults:
  approval_policy: auto
  max_concurrent: 2
sources:
  - name: project-a
    tracker: gitlab
    connection:
      base_url: https://gitlab.example.com
      token_env: GITLAB_TOKEN
      project: team/project-a
    agent_type: repo-maintainer
agent_types:
  - name: repo-maintainer
    agent_pack: repo-maintainer
workspace:
  root: ./workspaces
logging:
  dir: ./logs
`
	if err := os.WriteFile(configPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	agent := cfg.AgentTypes[0]
	if got := agent.ApprovalPolicy; got != "auto" {
		t.Fatalf("approval policy = %q", got)
	}
	if got := agent.MaxConcurrent; got != 2 {
		t.Fatalf("max concurrent = %d", got)
	}
}
