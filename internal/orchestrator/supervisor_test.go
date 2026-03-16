package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/testutil"
	"github.com/tjohnson/maestro/internal/workspace"
)

func TestServicesShareGlobalDispatchLimiter(t *testing.T) {
	root := t.TempDir()
	repoURL := createSupervisorGitRepo(t)
	limiter := newSemaphoreLimiter(1)

	firstCfg := supervisorTestConfig(t, filepath.Join(root, "first"), "source-a")
	secondCfg := supervisorTestConfig(t, filepath.Join(root, "second"), "source-b")

	firstHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}
	secondHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}

	firstSvc, err := NewServiceWithDeps(firstCfg, supervisorTestLogger(), Dependencies{
		Tracker:   &testutil.FakeTracker{Issues: supervisorSingleIssue(firstCfg, repoURL, "gitlab:team/project#1", "team/project#1")},
		Harness:   firstHarness,
		Workspace: workspace.NewManager(firstCfg.Workspace.Root),
		Limiter:   limiter,
	})
	if err != nil {
		t.Fatalf("new first service: %v", err)
	}
	secondSvc, err := NewServiceWithDeps(secondCfg, supervisorTestLogger(), Dependencies{
		Tracker:   &testutil.FakeTracker{Issues: supervisorSingleIssue(secondCfg, repoURL, "gitlab:team/project#2", "team/project#2")},
		Harness:   secondHarness,
		Workspace: workspace.NewManager(secondCfg.Workspace.Root),
		Limiter:   limiter,
	})
	if err != nil {
		t.Fatalf("new second service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	firstErrCh := make(chan error, 1)
	secondErrCh := make(chan error, 1)
	go func() { firstErrCh <- firstSvc.Run(ctx) }()
	go func() { secondErrCh <- secondSvc.Run(ctx) }()

	supervisorWaitFor(t, 2*time.Second, func() bool {
		totalStarted := len(firstHarness.StartedRuns) + len(secondHarness.StartedRuns)
		return totalStarted == 1
	})
	time.Sleep(100 * time.Millisecond)
	if got := len(firstHarness.StartedRuns) + len(secondHarness.StartedRuns); got != 1 {
		t.Fatalf("total started runs = %d, want 1 while limiter held", got)
	}

	firstReleased := false
	secondReleased := false
	if len(firstHarness.StartedRuns) == 1 {
		close(firstHarness.WaitBlock)
		firstReleased = true
	} else {
		close(secondHarness.WaitBlock)
		secondReleased = true
	}
	supervisorWaitFor(t, 2*time.Second, func() bool {
		return len(firstHarness.StartedRuns) == 1 && len(secondHarness.StartedRuns) == 1
	})
	if !firstReleased && len(firstHarness.StartedRuns) == 1 && firstSvc.Snapshot().ActiveRun != nil {
		close(firstHarness.WaitBlock)
		firstReleased = true
	}
	if !secondReleased && len(secondHarness.StartedRuns) == 1 && secondSvc.Snapshot().ActiveRun != nil {
		close(secondHarness.WaitBlock)
		secondReleased = true
	}
	supervisorWaitFor(t, 2*time.Second, func() bool {
		return firstSvc.Snapshot().ActiveRun == nil && secondSvc.Snapshot().ActiveRun == nil
	})

	cancel()
	if err := <-firstErrCh; err != nil {
		t.Fatalf("first service run: %v", err)
	}
	if err := <-secondErrCh; err != nil {
		t.Fatalf("second service run: %v", err)
	}
}

func TestServicesShareAgentLimiter(t *testing.T) {
	root := t.TempDir()
	repoURL := createSupervisorGitRepo(t)
	globalLimiter := newSemaphoreLimiter(2)
	agentLimiter := newSemaphoreLimiter(1)

	firstCfg := supervisorTestConfig(t, filepath.Join(root, "first"), "source-a")
	secondCfg := supervisorTestConfig(t, filepath.Join(root, "second"), "source-b")

	firstHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}
	secondHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}

	firstSvc, err := NewServiceWithDeps(firstCfg, supervisorTestLogger(), Dependencies{
		Tracker:   &testutil.FakeTracker{Issues: supervisorSingleIssue(firstCfg, repoURL, "gitlab:team/project#11", "team/project#11")},
		Harness:   firstHarness,
		Workspace: workspace.NewManager(firstCfg.Workspace.Root),
		Limiter:   newCompositeLimiter(globalLimiter, agentLimiter),
	})
	if err != nil {
		t.Fatalf("new first service: %v", err)
	}
	secondSvc, err := NewServiceWithDeps(secondCfg, supervisorTestLogger(), Dependencies{
		Tracker:   &testutil.FakeTracker{Issues: supervisorSingleIssue(secondCfg, repoURL, "gitlab:team/project#12", "team/project#12")},
		Harness:   secondHarness,
		Workspace: workspace.NewManager(secondCfg.Workspace.Root),
		Limiter:   newCompositeLimiter(globalLimiter, agentLimiter),
	})
	if err != nil {
		t.Fatalf("new second service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstErrCh := make(chan error, 1)
	secondErrCh := make(chan error, 1)
	go func() { firstErrCh <- firstSvc.Run(ctx) }()
	go func() { secondErrCh <- secondSvc.Run(ctx) }()

	supervisorWaitFor(t, 2*time.Second, func() bool {
		return len(firstHarness.StartedRuns)+len(secondHarness.StartedRuns) == 1
	})
	time.Sleep(100 * time.Millisecond)
	if got := len(firstHarness.StartedRuns) + len(secondHarness.StartedRuns); got != 1 {
		t.Fatalf("total started runs = %d, want 1 with shared agent limiter", got)
	}

	if len(firstHarness.StartedRuns) == 1 {
		close(firstHarness.WaitBlock)
	} else {
		close(secondHarness.WaitBlock)
	}
	supervisorWaitFor(t, 2*time.Second, func() bool {
		return len(firstHarness.StartedRuns) == 1 && len(secondHarness.StartedRuns) == 1
	})
	if len(firstHarness.StartedRuns) == 1 && firstSvc.Snapshot().ActiveRun != nil {
		close(firstHarness.WaitBlock)
	}
	if len(secondHarness.StartedRuns) == 1 && secondSvc.Snapshot().ActiveRun != nil {
		close(secondHarness.WaitBlock)
	}
	supervisorWaitFor(t, 2*time.Second, func() bool {
		return firstSvc.Snapshot().ActiveRun == nil && secondSvc.Snapshot().ActiveRun == nil
	})

	cancel()
	if err := <-firstErrCh; err != nil {
		t.Fatalf("first service run: %v", err)
	}
	if err := <-secondErrCh; err != nil {
		t.Fatalf("second service run: %v", err)
	}
}

func TestDifferentAgentsRunConcurrentlyWithinGlobalLimit(t *testing.T) {
	root := t.TempDir()
	repoURL := createSupervisorGitRepo(t)
	globalLimiter := newSemaphoreLimiter(2)

	firstCfg := supervisorTestConfig(t, filepath.Join(root, "first"), "source-a")
	secondCfg := supervisorTestConfig(t, filepath.Join(root, "second"), "source-b")
	secondCfg.AgentTypes[0].Name = "triage"
	secondCfg.Sources[0].AgentType = "triage"

	firstHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}
	secondHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}

	firstSvc, err := NewServiceWithDeps(firstCfg, supervisorTestLogger(), Dependencies{
		Tracker:   &testutil.FakeTracker{Issues: supervisorSingleIssue(firstCfg, repoURL, "gitlab:team/project#21", "team/project#21")},
		Harness:   firstHarness,
		Workspace: workspace.NewManager(firstCfg.Workspace.Root),
		Limiter:   newCompositeLimiter(globalLimiter, newSemaphoreLimiter(1)),
	})
	if err != nil {
		t.Fatalf("new first service: %v", err)
	}
	secondSvc, err := NewServiceWithDeps(secondCfg, supervisorTestLogger(), Dependencies{
		Tracker:   &testutil.FakeTracker{Issues: supervisorSingleIssue(secondCfg, repoURL, "gitlab:team/project#22", "team/project#22")},
		Harness:   secondHarness,
		Workspace: workspace.NewManager(secondCfg.Workspace.Root),
		Limiter:   newCompositeLimiter(globalLimiter, newSemaphoreLimiter(1)),
	})
	if err != nil {
		t.Fatalf("new second service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstErrCh := make(chan error, 1)
	secondErrCh := make(chan error, 1)
	go func() { firstErrCh <- firstSvc.Run(ctx) }()
	go func() { secondErrCh <- secondSvc.Run(ctx) }()

	supervisorWaitFor(t, 2*time.Second, func() bool {
		return len(firstHarness.StartedRuns) == 1 && len(secondHarness.StartedRuns) == 1
	})
	if firstSvc.Snapshot().ActiveRun == nil || secondSvc.Snapshot().ActiveRun == nil {
		t.Fatalf("expected both services active, got first=%v second=%v", firstSvc.Snapshot().ActiveRun, secondSvc.Snapshot().ActiveRun)
	}

	close(firstHarness.WaitBlock)
	close(secondHarness.WaitBlock)
	supervisorWaitFor(t, 2*time.Second, func() bool {
		return firstSvc.Snapshot().ActiveRun == nil && secondSvc.Snapshot().ActiveRun == nil
	})

	cancel()
	if err := <-firstErrCh; err != nil {
		t.Fatalf("first service run: %v", err)
	}
	if err := <-secondErrCh; err != nil {
		t.Fatalf("second service run: %v", err)
	}
}

func TestStoppingOneServiceRunDoesNotStopPeer(t *testing.T) {
	root := t.TempDir()
	repoURL := createSupervisorGitRepo(t)
	globalLimiter := newSemaphoreLimiter(2)

	firstCfg := supervisorTestConfig(t, filepath.Join(root, "first"), "source-a")
	secondCfg := supervisorTestConfig(t, filepath.Join(root, "second"), "source-b")
	secondCfg.AgentTypes[0].Name = "triage"
	secondCfg.Sources[0].AgentType = "triage"

	firstTracker := &testutil.FakeTracker{Issues: supervisorSingleIssue(firstCfg, repoURL, "gitlab:team/project#31", "team/project#31")}
	secondTracker := &testutil.FakeTracker{Issues: supervisorSingleIssue(secondCfg, repoURL, "gitlab:team/project#32", "team/project#32")}
	firstHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}
	secondHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}

	firstSvc, err := NewServiceWithDeps(firstCfg, supervisorTestLogger(), Dependencies{
		Tracker:   firstTracker,
		Harness:   firstHarness,
		Workspace: workspace.NewManager(firstCfg.Workspace.Root),
		Limiter:   newCompositeLimiter(globalLimiter, newSemaphoreLimiter(2)),
	})
	if err != nil {
		t.Fatalf("new first service: %v", err)
	}
	secondSvc, err := NewServiceWithDeps(secondCfg, supervisorTestLogger(), Dependencies{
		Tracker:   secondTracker,
		Harness:   secondHarness,
		Workspace: workspace.NewManager(secondCfg.Workspace.Root),
		Limiter:   newCompositeLimiter(globalLimiter, newSemaphoreLimiter(2)),
	})
	if err != nil {
		t.Fatalf("new second service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	firstErrCh := make(chan error, 1)
	secondErrCh := make(chan error, 1)
	go func() { firstErrCh <- firstSvc.Run(ctx) }()
	go func() { secondErrCh <- secondSvc.Run(ctx) }()

	supervisorWaitFor(t, 2*time.Second, func() bool {
		return firstSvc.Snapshot().ActiveRun != nil && secondSvc.Snapshot().ActiveRun != nil
	})

	firstTracker.SetIssueState("gitlab:team/project#31", "closed")

	supervisorWaitFor(t, 2*time.Second, func() bool {
		return len(firstHarness.StopCalls) == 1 && firstSvc.Snapshot().ActiveRun == nil
	})
	if secondSvc.Snapshot().ActiveRun == nil {
		t.Fatal("expected peer service to remain active")
	}

	snapshot := (&Supervisor{services: []*Service{firstSvc, secondSvc}}).Snapshot()
	if len(snapshot.SourceSummaries) != 2 {
		t.Fatalf("source summaries = %d, want 2", len(snapshot.SourceSummaries))
	}

	close(secondHarness.WaitBlock)
	supervisorWaitFor(t, 2*time.Second, func() bool {
		return secondSvc.Snapshot().ActiveRun == nil
	})

	cancel()
	if err := <-firstErrCh; err != nil {
		t.Fatalf("first service run: %v", err)
	}
	if err := <-secondErrCh; err != nil {
		t.Fatalf("second service run: %v", err)
	}
}

func TestRetryOnOneServiceDoesNotBlockPeerWhenCapacityExists(t *testing.T) {
	root := t.TempDir()
	repoURL := createSupervisorGitRepo(t)
	globalLimiter := newSemaphoreLimiter(2)

	firstCfg := supervisorTestConfig(t, filepath.Join(root, "first"), "source-a")
	secondCfg := supervisorTestConfig(t, filepath.Join(root, "second"), "source-b")
	secondCfg.AgentTypes[0].Name = "triage"
	secondCfg.Sources[0].AgentType = "triage"

	firstHarness := &testutil.FakeHarness{WaitErrs: []error{errors.New("boom"), nil}}
	secondHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}

	firstSvc, err := NewServiceWithDeps(firstCfg, supervisorTestLogger(), Dependencies{
		Tracker:   &testutil.FakeTracker{Issues: supervisorSingleIssue(firstCfg, repoURL, "gitlab:team/project#41", "team/project#41")},
		Harness:   firstHarness,
		Workspace: workspace.NewManager(firstCfg.Workspace.Root),
		Limiter:   newCompositeLimiter(globalLimiter, newSemaphoreLimiter(2)),
	})
	if err != nil {
		t.Fatalf("new first service: %v", err)
	}
	secondSvc, err := NewServiceWithDeps(secondCfg, supervisorTestLogger(), Dependencies{
		Tracker:   &testutil.FakeTracker{Issues: supervisorSingleIssue(secondCfg, repoURL, "gitlab:team/project#42", "team/project#42")},
		Harness:   secondHarness,
		Workspace: workspace.NewManager(secondCfg.Workspace.Root),
		Limiter:   newCompositeLimiter(globalLimiter, newSemaphoreLimiter(2)),
	})
	if err != nil {
		t.Fatalf("new second service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	firstErrCh := make(chan error, 1)
	secondErrCh := make(chan error, 1)
	go func() { firstErrCh <- firstSvc.Run(ctx) }()
	go func() { secondErrCh <- secondSvc.Run(ctx) }()

	supervisorWaitFor(t, 2*time.Second, func() bool {
		return len(secondHarness.StartedRuns) == 1 && secondSvc.Snapshot().ActiveRun != nil
	})
	supervisorWaitFor(t, 2*time.Second, func() bool {
		return len(firstHarness.StartedRuns) >= 2
	})

	if secondSvc.Snapshot().ActiveRun == nil {
		t.Fatal("expected peer service to stay active during retry")
	}

	close(secondHarness.WaitBlock)
	supervisorWaitFor(t, 2*time.Second, func() bool {
		return secondSvc.Snapshot().ActiveRun == nil && firstSvc.Snapshot().ActiveRun == nil
	})

	cancel()
	if err := <-firstErrCh; err != nil {
		t.Fatalf("first service run: %v", err)
	}
	if err := <-secondErrCh; err != nil {
		t.Fatalf("second service run: %v", err)
	}
}

func supervisorTestConfig(t *testing.T, root string, sourceName string) *config.Config {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("Issue {{.Issue.Identifier}}"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	return &config.Config{
		User: config.UserConfig{Name: "TJ", GitLabUsername: "tjohnson"},
		Defaults: config.DefaultsConfig{
			PollInterval:        config.Duration{Duration: 20 * time.Millisecond},
			MaxConcurrentGlobal: 1,
			StallTimeout:        config.Duration{Duration: time.Minute},
		},
		Sources: []config.SourceConfig{{
			Name:         sourceName,
			Tracker:      "gitlab",
			AgentType:    "code-pr",
			PollInterval: config.Duration{Duration: 20 * time.Millisecond},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:           "code-pr",
			InstanceName:   sourceName,
			Harness:        "claude-code",
			Workspace:      "git-clone",
			Prompt:         promptPath,
			ApprovalPolicy: "auto",
			MaxConcurrent:  1,
			StallTimeout:   config.Duration{Duration: time.Minute},
		}},
		Workspace: config.WorkspaceConfig{Root: filepath.Join(root, "workspaces")},
		State: config.StateConfig{
			Dir:             filepath.Join(root, "state"),
			RetryBase:       config.Duration{Duration: 20 * time.Millisecond},
			MaxRetryBackoff: config.Duration{Duration: 20 * time.Millisecond},
			MaxAttempts:     3,
		},
		Hooks: config.HooksConfig{
			Timeout: config.Duration{Duration: 30 * time.Second},
		},
	}
}

func supervisorSingleIssue(cfg *config.Config, repoURL string, id string, identifier string) []domain.Issue {
	return []domain.Issue{{
		ID:          id,
		Identifier:  identifier,
		Title:       "Test issue",
		SourceName:  cfg.Sources[0].Name,
		TrackerKind: "gitlab",
		Meta: map[string]string{
			"repo_url": repoURL,
		},
	}}
}

func createSupervisorGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	supervisorRunGit(t, root, "init")
	supervisorRunGit(t, root, "add", "README.md")
	supervisorRunGit(t, root, "-c", "user.name=Test User", "-c", "user.email=test@example.com", "commit", "-m", "init")
	bare := filepath.Join(t.TempDir(), "repo.git")
	supervisorRunGit(t, root, "clone", "--bare", root, bare)
	return bare
}

func supervisorRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, string(output))
	}
}

func supervisorTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func supervisorWaitFor(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not satisfied before timeout")
}
