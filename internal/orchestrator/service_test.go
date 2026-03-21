package orchestrator_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/harness"
	"github.com/tjohnson/maestro/internal/orchestrator"
	"github.com/tjohnson/maestro/internal/state"
	"github.com/tjohnson/maestro/internal/testutil"
	trackerbase "github.com/tjohnson/maestro/internal/tracker"
	"github.com/tjohnson/maestro/internal/workspace"
)

type getOnlyTracker struct {
	issue domain.Issue
}

func (g getOnlyTracker) Kind() string { return "get-only" }
func (g getOnlyTracker) Poll(ctx context.Context) ([]domain.Issue, error) {
	return nil, nil
}
func (g getOnlyTracker) Get(ctx context.Context, issueID string) (domain.Issue, error) {
	if g.issue.ID != issueID {
		return domain.Issue{}, errors.New("not found")
	}
	return g.issue, nil
}
func (g getOnlyTracker) PostOperationalComment(ctx context.Context, issueID string, body string) error {
	return nil
}
func (g getOnlyTracker) AddLifecycleLabel(ctx context.Context, issueID string, label string) error {
	return nil
}
func (g getOnlyTracker) RemoveLifecycleLabel(ctx context.Context, issueID string, label string) error {
	return nil
}
func (g getOnlyTracker) UpdateIssueState(ctx context.Context, issueID string, stateName string) error {
	return nil
}

func TestServiceRunsIssueOncePerProcess(t *testing.T) {
	cfg := testConfig(t)
	repoURL := createGitRepo(t)

	fakeTracker := &testutil.FakeTracker{
		Issues: []domain.Issue{
			{
				ID:          "gitlab:team/project#42",
				Identifier:  "team/project#42",
				Title:       "Add feature",
				SourceName:  cfg.Sources[0].Name,
				TrackerKind: "gitlab",
				Meta: map[string]string{
					"repo_url": repoURL,
				},
			},
		},
	}
	fakeHarness := &testutil.FakeHarness{}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun == nil
	})

	time.Sleep(100 * time.Millisecond)
	if got := len(fakeHarness.StartedRuns); got != 1 {
		t.Fatalf("started runs = %d, want 1", got)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceRunsWorkspaceNoneWithoutRepoMetadata(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgentTypes[0].Workspace = "none"
	packClaudeDir := filepath.Join(t.TempDir(), "claude")
	if err := os.MkdirAll(packClaudeDir, 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packClaudeDir, "CLAUDE.md"), []byte("pack instructions"), 0o644); err != nil {
		t.Fatalf("write claude instructions: %v", err)
	}
	cfg.AgentTypes[0].PackClaudeDir = packClaudeDir

	fakeTracker := &testutil.FakeTracker{
		Issues: []domain.Issue{
			{
				ID:          "linear:OPS-42",
				Identifier:  "OPS-42",
				Title:       "Runbook check",
				SourceName:  cfg.Sources[0].Name,
				TrackerKind: "linear",
			},
		},
	}
	fakeHarness := &testutil.FakeHarness{}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun == nil
	})

	workdir := fakeHarness.StartedRuns[0].Workdir
	if workdir == "" {
		t.Fatal("expected non-empty workdir")
	}
	entries, err := os.ReadDir(workdir)
	if err != nil {
		t.Fatalf("read workspace: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != ".claude" {
		t.Fatalf("workspace entries = %v, want [.claude]", entries)
	}
	if _, err := os.Stat(filepath.Join(workdir, ".claude", "CLAUDE.md")); err != nil {
		t.Fatalf("expected pack claude config in workspace: %v", err)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceResolvesRepoEmbeddedPackAfterClone(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgentTypes[0].AgentPack = "repo:.maestro"

	localPrompt := filepath.Join(t.TempDir(), "local-prompt.md")
	if err := os.WriteFile(localPrompt, []byte("LOCAL_PROMPT"), 0o644); err != nil {
		t.Fatalf("write local prompt: %v", err)
	}
	cfg.AgentTypes[0].Prompt = localPrompt

	repoURL := createGitRepoWithFiles(t, map[string]string{
		".maestro/prompt.md":        "REPO_PROMPT {{.Issue.Identifier}}\n{{.Agent.Context}}",
		".maestro/context/rules.md": "Repo context",
		".maestro/claude/CLAUDE.md": "Repo claude config",
		"README.md":                 "hello\n",
	})

	fakeTracker := &testutil.FakeTracker{
		Issues: []domain.Issue{
			{
				ID:          "gitlab:team/project#501",
				Identifier:  "team/project#501",
				Title:       "Use repo pack",
				SourceName:  cfg.Sources[0].Name,
				TrackerKind: "gitlab",
				Meta: map[string]string{
					"repo_url": repoURL,
				},
			},
		},
	}
	fakeHarness := &testutil.FakeHarness{}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun == nil
	})

	started := fakeHarness.StartedRuns[0]
	if !strings.Contains(started.Prompt, "REPO_PROMPT team/project#501") {
		t.Fatalf("prompt = %q, want repo prompt", started.Prompt)
	}
	if strings.Contains(started.Prompt, "LOCAL_PROMPT") {
		t.Fatalf("prompt = %q, want repo pack to override local prompt", started.Prompt)
	}
	if !strings.Contains(started.Prompt, "Repo context") {
		t.Fatalf("prompt = %q, want repo context", started.Prompt)
	}
	if _, err := os.Stat(filepath.Join(started.Workdir, ".claude", "CLAUDE.md")); err != nil {
		t.Fatalf("expected repo claude config in workspace: %v", err)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceRetriesFailedRunAndIncrementsAttempt(t *testing.T) {
	cfg := testConfig(t)
	repoURL := createGitRepo(t)

	fakeTracker := &testutil.FakeTracker{
		Issues: []domain.Issue{
			{
				ID:          "gitlab:team/project#99",
				Identifier:  "team/project#99",
				Title:       "Break build",
				SourceName:  cfg.Sources[0].Name,
				TrackerKind: "gitlab",
				Meta: map[string]string{
					"repo_url": repoURL,
				},
			},
		},
	}
	fakeHarness := &testutil.FakeHarness{
		WaitErrs: []error{errors.New("boom"), nil},
	}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		snapshot := svc.Snapshot()
		return len(fakeHarness.StartedRuns) == 2 && snapshot.ActiveRun == nil
	})

	if got := len(fakeHarness.StartedRuns); got != 2 {
		t.Fatalf("started runs = %d, want 2", got)
	}
	if !strings.Contains(fakeHarness.StartedRuns[0].Prompt, "attempt 0") {
		t.Fatalf("first prompt = %q, want attempt 0", fakeHarness.StartedRuns[0].Prompt)
	}
	if !strings.Contains(fakeHarness.StartedRuns[1].Prompt, "attempt 1") {
		t.Fatalf("second prompt = %q, want attempt 1", fakeHarness.StartedRuns[1].Prompt)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceStopsRetryingAfterMaxAttempts(t *testing.T) {
	cfg := testConfig(t)
	cfg.State.MaxAttempts = 2
	repoURL := createGitRepo(t)

	fakeTracker := &testutil.FakeTracker{
		Issues: singleIssue(cfg, repoURL, "gitlab:team/project#100", "team/project#100"),
	}
	fakeHarness := &testutil.FakeHarness{
		WaitErrs: []error{errors.New("boom"), errors.New("boom again")},
	}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 2 && svc.Snapshot().ActiveRun == nil
	})

	time.Sleep(100 * time.Millisecond)
	if got := len(fakeHarness.StartedRuns); got != 2 {
		t.Fatalf("started runs = %d, want 2", got)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceUsesSourceRetryMaxAttemptsOverride(t *testing.T) {
	cfg := testConfig(t)
	cfg.State.MaxAttempts = 5
	cfg.Sources[0].MaxAttempts = 2
	repoURL := createGitRepo(t)

	fakeTracker := &testutil.FakeTracker{
		Issues: singleIssue(cfg, repoURL, "gitlab:team/project#100b", "team/project#100b"),
	}
	fakeHarness := &testutil.FakeHarness{
		WaitErrs: []error{errors.New("boom"), errors.New("boom again")},
	}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 2 && svc.Snapshot().ActiveRun == nil
	})

	time.Sleep(100 * time.Millisecond)
	if got := len(fakeHarness.StartedRuns); got != 2 {
		t.Fatalf("started runs = %d, want 2", got)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceStopsActiveRunOnShutdown(t *testing.T) {
	cfg := testConfig(t)
	repoURL := createGitRepo(t)

	fakeTracker := &testutil.FakeTracker{
		Issues: []domain.Issue{
			{
				ID:          "gitlab:team/project#7",
				Identifier:  "team/project#7",
				Title:       "Long task",
				SourceName:  cfg.Sources[0].Name,
				TrackerKind: "gitlab",
				Meta: map[string]string{
					"repo_url": repoURL,
				},
			},
		},
	}
	fakeHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun != nil
	})

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
	if got := len(fakeHarness.StopCalls); got != 1 {
		t.Fatalf("stop calls = %d, want 1", got)
	}
}

func TestServicePersistsFinishedSuppressionAcrossRestart(t *testing.T) {
	root := t.TempDir()
	cfg := testConfigWithRoot(t, root)
	repoURL := createGitRepo(t)
	issue := domain.Issue{
		ID:          "gitlab:team/project#77",
		Identifier:  "team/project#77",
		Title:       "Persist me",
		SourceName:  cfg.Sources[0].Name,
		TrackerKind: "gitlab",
		UpdatedAt:   time.Now().UTC().Round(time.Second),
		Meta: map[string]string{
			"repo_url": repoURL,
		},
	}

	firstHarness := &testutil.FakeHarness{}
	firstSvc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   &testutil.FakeTracker{Issues: []domain.Issue{issue}},
		Harness:   firstHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new first service: %v", err)
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	errCh1 := make(chan error, 1)
	go func() {
		errCh1 <- firstSvc.Run(ctx1)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(firstHarness.StartedRuns) == 1 && firstSvc.Snapshot().ActiveRun == nil
	})
	cancel1()
	if err := <-errCh1; err != nil {
		t.Fatalf("run first service: %v", err)
	}

	secondHarness := &testutil.FakeHarness{}
	secondSvc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   &testutil.FakeTracker{Issues: []domain.Issue{issue}},
		Harness:   secondHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new second service: %v", err)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	errCh2 := make(chan error, 1)
	go func() {
		errCh2 <- secondSvc.Run(ctx2)
	}()

	time.Sleep(100 * time.Millisecond)
	if got := len(secondHarness.StartedRuns); got != 0 {
		t.Fatalf("started runs after restart = %d, want 0", got)
	}
	cancel2()
	if err := <-errCh2; err != nil {
		t.Fatalf("run second service: %v", err)
	}

	updatedIssue := issue
	updatedIssue.UpdatedAt = issue.UpdatedAt.Add(time.Minute)
	thirdHarness := &testutil.FakeHarness{}
	thirdSvc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   &testutil.FakeTracker{Issues: []domain.Issue{updatedIssue}},
		Harness:   thirdHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new third service: %v", err)
	}

	ctx3, cancel3 := context.WithCancel(context.Background())
	errCh3 := make(chan error, 1)
	go func() {
		errCh3 <- thirdSvc.Run(ctx3)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(thirdHarness.StartedRuns) == 1 && thirdSvc.Snapshot().ActiveRun == nil
	})
	cancel3()
	if err := <-errCh3; err != nil {
		t.Fatalf("run third service: %v", err)
	}
}

func TestServiceDoesNotRedispatchWhenOnlyLifecycleWritesChangedIssue(t *testing.T) {
	cfg := testConfig(t)
	repoURL := createGitRepo(t)
	now := time.Now().UTC().Round(time.Second)

	fakeTracker := &testutil.FakeTracker{
		Issues: []domain.Issue{
			{
				ID:          "linear:TAN-83",
				Identifier:  "TAN-83",
				Title:       "Smoke issue",
				SourceName:  cfg.Sources[0].Name,
				TrackerKind: "linear",
				UpdatedAt:   now,
				Meta: map[string]string{
					"repo_url": repoURL,
				},
			},
		},
	}
	fakeHarness := &testutil.FakeHarness{}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun == nil
	})

	time.Sleep(150 * time.Millisecond)
	if got := len(fakeHarness.StartedRuns); got != 1 {
		t.Fatalf("started runs after lifecycle writeback = %d, want 1", got)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceDoesNotRedispatchWhileCompletionWritebackIsInFlight(t *testing.T) {
	cfg := testConfig(t)
	repoURL := createGitRepo(t)
	now := time.Now().UTC().Round(time.Second)

	base := &testutil.FakeTracker{
		Issues: []domain.Issue{
			{
				ID:          "linear:TAN-84",
				Identifier:  "TAN-84",
				Title:       "Smoke issue",
				SourceName:  cfg.Sources[0].Name,
				TrackerKind: "linear",
				UpdatedAt:   now,
				Meta: map[string]string{
					"repo_url": repoURL,
				},
			},
		},
	}
	tracker := &blockingLifecycleTracker{
		FakeTracker: base,
		blockLabel:  trackerbase.LifecycleLabelDone,
		started:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	fakeHarness := &testutil.FakeHarness{}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   tracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	select {
	case <-tracker.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for completion writeback to block")
	}

	time.Sleep(150 * time.Millisecond)
	if got := len(fakeHarness.StartedRuns); got != 1 {
		t.Fatalf("started runs while completion writeback blocked = %d, want 1", got)
	}

	close(tracker.release)
	waitFor(t, 2*time.Second, func() bool {
		return svc.Snapshot().ActiveRun == nil
	})

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceRecoversActiveRunAsRetry(t *testing.T) {
	root := t.TempDir()
	cfg := testConfigWithRoot(t, root)
	repoURL := createGitRepo(t)
	now := time.Now().UTC().Round(time.Second)

	if err := state.NewStore(cfg.State.Dir).Save(state.Snapshot{
		ActiveRun: &state.PersistedRun{
			RunID:          "run-123",
			IssueID:        "gitlab:team/project#88",
			Identifier:     "team/project#88",
			Status:         domain.RunStatusActive,
			Attempt:        0,
			StartedAt:      now,
			LastActivityAt: now,
			IssueUpdatedAt: now,
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	fakeHarness := &testutil.FakeHarness{}
	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker: &testutil.FakeTracker{
			Issues: []domain.Issue{
				{
					ID:          "gitlab:team/project#88",
					Identifier:  "team/project#88",
					Title:       "Recovered task",
					SourceName:  cfg.Sources[0].Name,
					TrackerKind: "gitlab",
					UpdatedAt:   now,
					Meta: map[string]string{
						"repo_url": repoURL,
					},
				},
			},
		},
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun == nil
	})
	if !strings.Contains(fakeHarness.StartedRuns[0].Prompt, "attempt 1") {
		t.Fatalf("recovered prompt = %q, want attempt 1", fakeHarness.StartedRuns[0].Prompt)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceRecoversActiveRunAsRetryWithoutFreshPollCandidate(t *testing.T) {
	root := t.TempDir()
	cfg := testConfigWithRoot(t, root)
	repoURL := createGitRepo(t)
	now := time.Now().UTC().Round(time.Second)

	if err := state.NewStore(cfg.State.Dir).Save(state.Snapshot{
		ActiveRun: &state.PersistedRun{
			RunID:          "run-123",
			IssueID:        "gitlab:team/project#188",
			Identifier:     "team/project#188",
			Status:         domain.RunStatusActive,
			Attempt:        0,
			StartedAt:      now,
			LastActivityAt: now,
			IssueUpdatedAt: now,
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	issue := domain.Issue{
		ID:          "gitlab:team/project#188",
		Identifier:  "team/project#188",
		Title:       "Recovered without poll candidate",
		SourceName:  cfg.Sources[0].Name,
		TrackerKind: "gitlab",
		UpdatedAt:   now.Add(time.Minute),
		Meta: map[string]string{
			"repo_url": repoURL,
		},
	}

	fakeHarness := &testutil.FakeHarness{}
	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   getOnlyTracker{issue: issue},
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun == nil
	})

	if !strings.Contains(fakeHarness.StartedRuns[0].Prompt, "attempt 1") {
		t.Fatalf("recovered prompt = %q, want attempt 1", fakeHarness.StartedRuns[0].Prompt)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceTracksAndResolvesApprovalRequests(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgentTypes[0].ApprovalPolicy = "manual"
	repoURL := createGitRepo(t)

	fakeTracker := &testutil.FakeTracker{
		Issues: singleIssue(cfg, repoURL, "gitlab:team/project#55", "team/project#55"),
	}
	fakeHarness := &testutil.FakeHarness{
		WaitBlock:  make(chan struct{}),
		ApprovalCh: make(chan harness.ApprovalRequest, 1),
	}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun != nil
	})

	runID := svc.Snapshot().ActiveRun.ID
	fakeHarness.ApprovalCh <- harness.ApprovalRequest{
		RequestID:      "req-1",
		RunID:          runID,
		ToolName:       "write_file",
		ToolInput:      "create APPROVAL.txt",
		ApprovalPolicy: "manual",
		RequestedAt:    time.Now().Add(-time.Minute),
	}

	waitFor(t, 2*time.Second, func() bool {
		return len(svc.Snapshot().PendingApprovals) == 1
	})

	if err := svc.ResolveApproval("req-1", "approve"); err != nil {
		t.Fatalf("resolve approval: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		snapshot := svc.Snapshot()
		return len(snapshot.PendingApprovals) == 0 && len(snapshot.ApprovalHistory) == 1 && snapshot.ActiveRun != nil && snapshot.ActiveRun.ApprovalState == domain.ApprovalStateApproved
	})

	close(fakeHarness.WaitBlock)
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
	if len(fakeHarness.Decisions) != 1 || fakeHarness.Decisions[0].Decision != "approve" {
		t.Fatalf("approval decisions = %+v", fakeHarness.Decisions)
	}
}

func TestServiceKeepsApprovalPendingWhenApprovalFails(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgentTypes[0].ApprovalPolicy = "manual"
	repoURL := createGitRepo(t)

	fakeTracker := &testutil.FakeTracker{
		Issues: singleIssue(cfg, repoURL, "gitlab:team/project#56", "team/project#56"),
	}
	fakeHarness := &testutil.FakeHarness{
		WaitBlock:  make(chan struct{}),
		ApprovalCh: make(chan harness.ApprovalRequest, 1),
		ApproveErr: errors.New("approval transport failed"),
	}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun != nil
	})

	runID := svc.Snapshot().ActiveRun.ID
	fakeHarness.ApprovalCh <- harness.ApprovalRequest{
		RequestID:      "req-fail",
		RunID:          runID,
		ToolName:       "shell",
		ToolInput:      "rm -rf /tmp/demo",
		ApprovalPolicy: "manual",
		RequestedAt:    time.Now(),
	}

	waitFor(t, 2*time.Second, func() bool {
		return len(svc.Snapshot().PendingApprovals) == 1
	})

	if err := svc.ResolveApproval("req-fail", "approve"); err == nil {
		t.Fatal("expected approval failure")
	}

	snapshot := svc.Snapshot()
	if len(snapshot.PendingApprovals) != 1 {
		t.Fatalf("pending approvals = %d, want 1", len(snapshot.PendingApprovals))
	}
	if !snapshot.PendingApprovals[0].Resolvable {
		t.Fatal("expected failed approval request to remain resolvable")
	}
	if len(snapshot.ApprovalHistory) != 0 {
		t.Fatalf("approval history = %d, want 0", len(snapshot.ApprovalHistory))
	}

	close(fakeHarness.WaitBlock)
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceRejectsConcurrentApprovalResolve(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgentTypes[0].ApprovalPolicy = "manual"
	repoURL := createGitRepo(t)

	fakeTracker := &testutil.FakeTracker{
		Issues: singleIssue(cfg, repoURL, "gitlab:team/project#56b", "team/project#56b"),
	}
	fakeHarness := &blockingApproveHarness{
		FakeHarness: &testutil.FakeHarness{
			WaitBlock:  make(chan struct{}),
			ApprovalCh: make(chan harness.ApprovalRequest, 1),
		},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun != nil
	})

	runID := svc.Snapshot().ActiveRun.ID
	fakeHarness.ApprovalCh <- harness.ApprovalRequest{
		RequestID:      "req-race",
		RunID:          runID,
		ToolName:       "shell",
		ToolInput:      "echo hi",
		ApprovalPolicy: "manual",
		RequestedAt:    time.Now(),
	}

	waitFor(t, 2*time.Second, func() bool {
		return len(svc.Snapshot().PendingApprovals) == 1
	})

	firstErrCh := make(chan error, 1)
	go func() {
		firstErrCh <- svc.ResolveApproval("req-race", "approve")
	}()

	<-fakeHarness.started

	secondErr := svc.ResolveApproval("req-race", "approve")
	if secondErr == nil || !strings.Contains(secondErr.Error(), "already being resolved") {
		t.Fatalf("second resolve error = %v, want already being resolved", secondErr)
	}

	snapshot := svc.Snapshot()
	if len(snapshot.PendingApprovals) != 1 {
		t.Fatalf("pending approvals = %d, want 1", len(snapshot.PendingApprovals))
	}
	if snapshot.PendingApprovals[0].Resolvable {
		t.Fatal("expected approval to be marked non-resolvable while first resolve is in progress")
	}

	close(fakeHarness.release)
	if err := <-firstErrCh; err != nil {
		t.Fatalf("first resolve approval: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		snapshot := svc.Snapshot()
		return len(snapshot.PendingApprovals) == 0 && len(snapshot.ApprovalHistory) == 1
	})

	close(fakeHarness.WaitBlock)
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
	if len(fakeHarness.Decisions) != 1 {
		t.Fatalf("approval decisions = %+v, want exactly 1", fakeHarness.Decisions)
	}
}

func TestServiceTimesOutPendingApprovalAndFailsRun(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgentTypes[0].ApprovalPolicy = "manual"
	cfg.AgentTypes[0].ApprovalTimeout = config.Duration{Duration: 80 * time.Millisecond}
	repoURL := createGitRepo(t)

	fakeTracker := &testutil.FakeTracker{
		Issues: singleIssue(cfg, repoURL, "gitlab:team/project#57", "team/project#57"),
	}
	fakeHarness := &testutil.FakeHarness{
		WaitBlock:  make(chan struct{}),
		ApprovalCh: make(chan harness.ApprovalRequest, 1),
	}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun != nil
	})

	runID := svc.Snapshot().ActiveRun.ID
	fakeHarness.ApprovalCh <- harness.ApprovalRequest{
		RequestID:      "req-timeout",
		RunID:          runID,
		ToolName:       "shell",
		ToolInput:      "rm -rf /tmp/demo",
		ApprovalPolicy: "manual",
		RequestedAt:    time.Now().Add(-time.Second),
	}

	waitFor(t, 2*time.Second, func() bool {
		snapshot := svc.Snapshot()
		return len(fakeHarness.StopCalls) == 1 && snapshot.ActiveRun == nil && len(snapshot.PendingApprovals) == 0 && len(snapshot.ApprovalHistory) == 1
	})

	snapshot := svc.Snapshot()
	if got := snapshot.ApprovalHistory[0].Outcome; got != "timed_out" {
		t.Fatalf("approval outcome = %q, want timed_out", got)
	}
	if got := snapshot.ApprovalHistory[0].Reason; got != "approval timeout" {
		t.Fatalf("approval reason = %q, want approval timeout", got)
	}
	if len(fakeHarness.StopCalls) != 1 || fakeHarness.StopCalls[0] != runID {
		t.Fatalf("stop calls = %+v, want [%s]", fakeHarness.StopCalls, runID)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceTracksAndResolvesMessageRequests(t *testing.T) {
	cfg := testConfig(t)
	repoURL := createGitRepo(t)

	fakeTracker := &testutil.FakeTracker{
		Issues: singleIssue(cfg, repoURL, "gitlab:team/project#57", "team/project#57"),
	}
	fakeHarness := &testutil.FakeHarness{
		WaitBlock: make(chan struct{}),
		MessageCh: make(chan harness.MessageRequest, 1),
	}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun != nil
	})

	runID := svc.Snapshot().ActiveRun.ID
	fakeHarness.MessageCh <- harness.MessageRequest{
		RequestID:   "msg-1",
		RunID:       runID,
		Summary:     "Need clarification",
		Body:        "Should I update the API contract or only the UI copy?",
		RequestedAt: time.Now().Add(-time.Minute),
	}

	waitFor(t, 2*time.Second, func() bool {
		snapshot := svc.Snapshot()
		return len(snapshot.PendingMessages) == 1 && snapshot.ActiveRun != nil && snapshot.ActiveRun.Status == domain.RunStatusAwaiting
	})

	if err := svc.ResolveMessage("msg-1", "Update the API contract too.", "test"); err != nil {
		t.Fatalf("resolve message: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		snapshot := svc.Snapshot()
		return len(snapshot.PendingMessages) == 0 && len(snapshot.MessageHistory) == 1 && snapshot.ActiveRun != nil && snapshot.ActiveRun.Status == domain.RunStatusActive
	})

	close(fakeHarness.WaitBlock)
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
	if len(fakeHarness.Replies) != 1 || fakeHarness.Replies[0].Reply != "Update the API contract too." {
		t.Fatalf("message replies = %+v", fakeHarness.Replies)
	}
}

func TestServiceRestoresApprovalHistoryAsStaleAfterRestart(t *testing.T) {
	root := t.TempDir()
	cfg := testConfigWithRoot(t, root)
	now := time.Now().UTC().Round(time.Second)
	store := state.NewStore(cfg.State.Dir)
	if err := store.Save(state.Snapshot{
		RetryQueue: map[string]state.RetryEntry{},
		Finished:   map[string]state.TerminalIssue{},
		ActiveRun: &state.PersistedRun{
			RunID:          "run-restore",
			IssueID:        "gitlab:team/project#57",
			Identifier:     "team/project#57",
			Status:         domain.RunStatusAwaiting,
			Attempt:        0,
			StartedAt:      now,
			LastActivityAt: now,
			IssueUpdatedAt: now,
		},
		PendingApprovals: []state.PersistedApprovalRequest{
			{
				RequestID:       "req-stale",
				RunID:           "run-restore",
				IssueID:         "gitlab:team/project#57",
				IssueIdentifier: "team/project#57",
				AgentName:       "coder",
				ToolName:        "shell",
				ToolInput:       "dangerous command",
				ApprovalPolicy:  "manual",
				RequestedAt:     now,
				Resolvable:      true,
			},
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:    &testutil.FakeTracker{},
		Harness:    &testutil.FakeHarness{},
		Workspace:  workspace.NewManager(cfg.Workspace.Root),
		StateStore: store,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	snapshot := svc.Snapshot()
	if len(snapshot.PendingApprovals) != 0 {
		t.Fatalf("pending approvals = %d, want 0", len(snapshot.PendingApprovals))
	}
	if len(snapshot.ApprovalHistory) != 1 {
		t.Fatalf("approval history = %d, want 1", len(snapshot.ApprovalHistory))
	}
	if snapshot.ApprovalHistory[0].Outcome != "stale_restart" {
		t.Fatalf("approval outcome = %q", snapshot.ApprovalHistory[0].Outcome)
	}
}

func TestServiceFailsRecoveredRunWhenApprovalAlreadyTimedOut(t *testing.T) {
	root := t.TempDir()
	cfg := testConfigWithRoot(t, root)
	cfg.AgentTypes[0].ApprovalTimeout = config.Duration{Duration: time.Second}
	now := time.Now().UTC().Round(time.Second)
	store := state.NewStore(cfg.State.Dir)
	if err := store.Save(state.Snapshot{
		RetryQueue: map[string]state.RetryEntry{},
		Finished:   map[string]state.TerminalIssue{},
		ActiveRun: &state.PersistedRun{
			RunID:          "run-timeout",
			IssueID:        "gitlab:team/project#88",
			Identifier:     "team/project#88",
			Status:         domain.RunStatusAwaiting,
			Attempt:        1,
			StartedAt:      now.Add(-2 * time.Hour),
			LastActivityAt: now.Add(-2 * time.Hour),
			IssueUpdatedAt: now.Add(-2 * time.Hour),
		},
		PendingApprovals: []state.PersistedApprovalRequest{
			{
				RequestID:       "req-timeout",
				RunID:           "run-timeout",
				IssueID:         "gitlab:team/project#88",
				IssueIdentifier: "team/project#88",
				AgentName:       "coder",
				ToolName:        "shell",
				ToolInput:       "dangerous command",
				ApprovalPolicy:  "manual",
				RequestedAt:     now.Add(-2 * time.Minute),
				Resolvable:      true,
			},
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:    &testutil.FakeTracker{},
		Harness:    &testutil.FakeHarness{},
		Workspace:  workspace.NewManager(cfg.Workspace.Root),
		StateStore: store,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	snapshot := svc.Snapshot()
	if snapshot.ActiveRun != nil {
		t.Fatalf("active run = %+v, want nil", snapshot.ActiveRun)
	}
	if len(snapshot.Retries) != 0 {
		t.Fatalf("retries = %d, want 0", len(snapshot.Retries))
	}
	if len(snapshot.PendingApprovals) != 0 {
		t.Fatalf("pending approvals = %d, want 0", len(snapshot.PendingApprovals))
	}
	if len(snapshot.ApprovalHistory) != 1 {
		t.Fatalf("approval history = %d, want 1", len(snapshot.ApprovalHistory))
	}
	if got := snapshot.ApprovalHistory[0].Outcome; got != "timed_out" {
		t.Fatalf("approval outcome = %q, want timed_out", got)
	}

	persisted, err := store.Load()
	if err != nil {
		t.Fatalf("load persisted state: %v", err)
	}
	finished, ok := persisted.Finished["gitlab:team/project#88"]
	if !ok {
		t.Fatal("expected finished issue after approval timeout recovery")
	}
	if finished.Status != domain.RunStatusFailed {
		t.Fatalf("finished status = %s, want %s", finished.Status, domain.RunStatusFailed)
	}
	if !strings.Contains(finished.Error, "approval timeout") {
		t.Fatalf("finished error = %q, want approval timeout", finished.Error)
	}
}

func TestServiceStartsWithEmptyRecoveryStateWhenRunsFileIsCorrupt(t *testing.T) {
	root := t.TempDir()
	cfg := testConfigWithRoot(t, root)
	if err := os.MkdirAll(cfg.State.Dir, 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	runsPath := filepath.Join(cfg.State.Dir, "runs.json")
	if err := os.WriteFile(runsPath, []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:    &testutil.FakeTracker{},
		Harness:    &testutil.FakeHarness{},
		Workspace:  workspace.NewManager(cfg.Workspace.Root),
		StateStore: state.NewStore(cfg.State.Dir),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	snapshot := svc.Snapshot()
	if snapshot.ActiveRun != nil {
		t.Fatalf("active run = %+v, want nil", snapshot.ActiveRun)
	}
	if len(snapshot.Retries) != 0 || len(snapshot.PendingApprovals) != 0 {
		t.Fatalf("snapshot = %+v, want empty recovery state", snapshot)
	}

	archived, err := filepath.Glob(runsPath + ".corrupt.*")
	if err != nil {
		t.Fatalf("glob corrupt archive: %v", err)
	}
	if len(archived) != 1 {
		t.Fatalf("archived runs files = %v, want 1", archived)
	}
	if _, statErr := os.Stat(runsPath); !os.IsNotExist(statErr) {
		t.Fatalf("runs.json stat error = %v, want not exists", statErr)
	}
}

func TestServiceStopsRunWhenTrackerMarksIssueDone(t *testing.T) {
	cfg := testConfig(t)
	repoURL := createGitRepo(t)

	fakeTracker := &testutil.FakeTracker{
		Issues: singleIssue(cfg, repoURL, "gitlab:team/project#58", "team/project#58"),
	}
	fakeHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun != nil
	})

	if err := fakeTracker.AddLifecycleLabel(context.Background(), "gitlab:team/project#58", trackerbase.LifecycleLabelDone); err != nil {
		t.Fatalf("mark done: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StopCalls) == 1 && svc.Snapshot().ActiveRun == nil
	})

	stored := waitForPersistedState(t, cfg.State.Dir, 2*time.Second, func(snapshot state.Snapshot) bool {
		return snapshot.Finished["gitlab:team/project#58"].Status == domain.RunStatusDone
	})
	if stored.Finished["gitlab:team/project#58"].Status != domain.RunStatusDone {
		t.Fatalf("finished status = %q", stored.Finished["gitlab:team/project#58"].Status)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceCompletesRunWhenLifecycleSyncFails(t *testing.T) {
	cfg := testConfig(t)
	repoURL := createGitRepo(t)

	tracker := &failingLifecycleTracker{
		FakeTracker: &testutil.FakeTracker{
			Issues: singleIssue(cfg, repoURL, "gitlab:team/project#59", "team/project#59"),
		},
		addErr:     errors.New("add label failed"),
		removeErr:  errors.New("remove label failed"),
		commentErr: errors.New("comment failed"),
	}
	fakeHarness := &testutil.FakeHarness{}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   tracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		snapshot := svc.Snapshot()
		return len(fakeHarness.StartedRuns) == 1 && snapshot.ActiveRun == nil
	})

	snapshot := svc.Snapshot()
	if snapshot.ClaimedCount != 0 {
		t.Fatalf("claimed count = %d, want 0", snapshot.ClaimedCount)
	}

	stored := waitForPersistedState(t, cfg.State.Dir, 2*time.Second, func(snapshot state.Snapshot) bool {
		finished, ok := snapshot.Finished["gitlab:team/project#59"]
		return ok && finished.Status == domain.RunStatusDone && len(snapshot.RetryQueue) == 0
	})
	finished, ok := stored.Finished["gitlab:team/project#59"]
	if !ok {
		t.Fatal("expected finished issue despite lifecycle sync failures")
	}
	if finished.Status != domain.RunStatusDone {
		t.Fatalf("finished status = %q, want %q", finished.Status, domain.RunStatusDone)
	}
	if len(stored.RetryQueue) != 0 {
		t.Fatalf("retry queue = %+v, want empty", stored.RetryQueue)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceAppliesGlobalLifecycleDefaultsWhenSourceHooksAreUnset(t *testing.T) {
	cfg := testConfig(t)
	cfg.Defaults.LabelPrefix = "maestro"
	cfg.Defaults.OnComplete = &config.LifecycleTransition{
		AddLabels:    []string{"maestro:review"},
		RemoveLabels: []string{"maestro:coding"},
	}
	repoURL := createGitRepo(t)

	fakeTracker := &testutil.FakeTracker{
		Issues: []domain.Issue{
			{
				ID:          "gitlab:team/project#59a",
				Identifier:  "team/project#59a",
				Title:       "Add feature",
				State:       "todo",
				Labels:      []string{"maestro:coding"},
				SourceName:  cfg.Sources[0].Name,
				TrackerKind: "gitlab",
				Meta: map[string]string{
					"repo_url": repoURL,
				},
			},
		},
	}
	fakeHarness := &testutil.FakeHarness{}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun == nil
	})

	issue, err := fakeTracker.Get(context.Background(), "gitlab:team/project#59a")
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}
	if slicesContains(issue.Labels, "maestro:active") {
		t.Fatalf("labels = %v, active label should be removed on completion", issue.Labels)
	}
	if slicesContains(issue.Labels, "maestro:coding") {
		t.Fatalf("labels = %v, coding label should be removed by global on_complete", issue.Labels)
	}
	if !slicesContains(issue.Labels, "maestro:review") {
		t.Fatalf("labels = %v, review label should be added by global on_complete", issue.Labels)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceCodexContinuationStopsWhenActiveLabelIsRemoved(t *testing.T) {
	cfg := testConfig(t)
	cfg.Defaults.LabelPrefix = "maestro"
	cfg.Sources[0].Filter = config.FilterConfig{Labels: []string{"maestro:coding"}}
	cfg.AgentTypes[0].Harness = "codex"
	cfg.AgentTypes[0].Codex = &config.CodexConfig{MaxTurns: 3}
	repoURL := createGitRepo(t)

	fakeTracker := &testutil.FakeTracker{
		Issues: []domain.Issue{
			{
				ID:          "gitlab:team/project#59b",
				Identifier:  "team/project#59b",
				Title:       "Continue work",
				State:       "todo",
				Labels:      []string{"maestro:coding"},
				SourceName:  cfg.Sources[0].Name,
				TrackerKind: "gitlab",
				Meta: map[string]string{
					"repo_url": repoURL,
				},
			},
		},
	}
	fakeHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun != nil
	})

	started := fakeHarness.StartedRuns[0]
	if started.ContinuationFunc == nil {
		t.Fatal("continuation func = nil, want codex continuation func")
	}

	prompt, cont, err := started.ContinuationFunc(context.Background(), 1)
	if err != nil {
		t.Fatalf("continuation while active: %v", err)
	}
	if !cont {
		t.Fatal("continuation while active = false, want true")
	}
	if !strings.Contains(prompt, "Continuation turn 2 of 3") {
		t.Fatalf("continuation prompt = %q, want turn prompt", prompt)
	}

	if err := fakeTracker.RemoveLifecycleLabel(context.Background(), "gitlab:team/project#59b", "maestro:active"); err != nil {
		t.Fatalf("remove active label: %v", err)
	}

	_, cont, err = started.ContinuationFunc(context.Background(), 1)
	if err != nil {
		t.Fatalf("continuation after active removed: %v", err)
	}
	if cont {
		t.Fatal("continuation after active removed = true, want false")
	}

	close(fakeHarness.WaitBlock)
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceSchedulesRetryWhenLifecycleSyncFails(t *testing.T) {
	cfg := testConfig(t)
	cfg.State.RetryBase = config.Duration{Duration: time.Hour}
	cfg.State.MaxRetryBackoff = config.Duration{Duration: time.Hour}
	repoURL := createGitRepo(t)

	tracker := &failingLifecycleTracker{
		FakeTracker: &testutil.FakeTracker{
			Issues: singleIssue(cfg, repoURL, "gitlab:team/project#60", "team/project#60"),
		},
		addErr:     errors.New("add label failed"),
		removeErr:  errors.New("remove label failed"),
		commentErr: errors.New("comment failed"),
	}
	fakeHarness := &testutil.FakeHarness{
		WaitErr: errors.New("boom"),
	}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   tracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		snapshot := svc.Snapshot()
		return len(fakeHarness.StartedRuns) == 1 && snapshot.ActiveRun == nil && snapshot.RetryCount == 1
	})

	snapshot := svc.Snapshot()
	if snapshot.ClaimedCount != 0 {
		t.Fatalf("claimed count = %d, want 0", snapshot.ClaimedCount)
	}
	if len(snapshot.Retries) != 1 {
		t.Fatalf("retries = %+v, want 1 entry", snapshot.Retries)
	}
	if snapshot.Retries[0].Attempt != 1 {
		t.Fatalf("retry attempt = %d, want 1", snapshot.Retries[0].Attempt)
	}

	stored := waitForPersistedState(t, cfg.State.Dir, 2*time.Second, func(snapshot state.Snapshot) bool {
		retry, ok := snapshot.RetryQueue["gitlab:team/project#60"]
		return ok && retry.Attempt == 1 && len(snapshot.Finished) == 0
	})
	retry, ok := stored.RetryQueue["gitlab:team/project#60"]
	if !ok {
		t.Fatal("expected persisted retry entry despite lifecycle sync failures")
	}
	if retry.Attempt != 1 {
		t.Fatalf("retry attempt = %d, want 1", retry.Attempt)
	}
	if len(stored.Finished) != 0 {
		t.Fatalf("finished = %+v, want empty", stored.Finished)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceStopsStalledRunAndQueuesRetry(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgentTypes[0].StallTimeout = config.Duration{Duration: 50 * time.Millisecond}
	repoURL := createGitRepo(t)

	fakeTracker := &testutil.FakeTracker{
		Issues: singleIssue(cfg, repoURL, "gitlab:team/project#59", "team/project#59"),
	}
	fakeHarness := &testutil.FakeHarness{WaitBlock: make(chan struct{})}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun != nil
	})
	waitFor(t, 2*time.Second, func() bool {
		snapshot := svc.Snapshot()
		return len(fakeHarness.StopCalls) == 1 && snapshot.ActiveRun == nil && snapshot.RetryCount == 1
	})

	stored := waitForPersistedState(t, cfg.State.Dir, 2*time.Second, func(snapshot state.Snapshot) bool {
		retry := snapshot.RetryQueue["gitlab:team/project#59"]
		return retry.IssueID != ""
	})
	retry := stored.RetryQueue["gitlab:team/project#59"]
	if retry.IssueID == "" {
		t.Fatalf("expected retry entry for stalled run, got %+v", stored.RetryQueue)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func TestServiceRunsConfiguredHooks(t *testing.T) {
	cfg := testConfig(t)
	cfg.Hooks.Timeout = config.Duration{Duration: 5 * time.Second}
	repoURL := createGitRepo(t)
	hookDir := t.TempDir()
	afterCreateMarker := filepath.Join(hookDir, "after_create.marker")
	beforeRunMarker := filepath.Join(hookDir, "before_run.marker")
	afterRunMarker := filepath.Join(hookDir, "after_run.marker")
	cfg.Hooks.AfterCreate = hookTouchCommand(afterCreateMarker)
	cfg.Hooks.BeforeRun = hookTouchCommand(beforeRunMarker)
	cfg.Hooks.AfterRun = hookTouchCommand(afterRunMarker)

	fakeTracker := &testutil.FakeTracker{
		Issues: singleIssue(cfg, repoURL, "gitlab:team/project#60", "team/project#60"),
	}
	fakeHarness := &testutil.FakeHarness{}

	svc, err := orchestrator.NewServiceWithDeps(cfg, testLogger(), orchestrator.Dependencies{
		Tracker:   fakeTracker,
		Harness:   fakeHarness,
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Run(ctx)
	}()

	waitFor(t, 2*time.Second, func() bool {
		return len(fakeHarness.StartedRuns) == 1 && svc.Snapshot().ActiveRun == nil
	})

	for _, marker := range []string{afterCreateMarker, beforeRunMarker, afterRunMarker} {
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("expected hook marker %s: %v", marker, err)
		}
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("run service: %v", err)
	}
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return testConfigWithRoot(t, t.TempDir())
}

func testConfigWithRoot(t *testing.T, root string) *config.Config {
	t.Helper()

	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("Issue {{.Issue.Identifier}} attempt {{.Attempt}}"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	return &config.Config{
		User: config.UserConfig{Name: "TJ", GitLabUsername: "tjohnson"},
		Defaults: config.DefaultsConfig{
			PollInterval:        config.Duration{Duration: 20 * time.Millisecond},
			MaxConcurrentGlobal: 1,
			StallTimeout:        config.Duration{Duration: time.Minute},
		},
		Sources: []config.SourceConfig{
			{
				Name:         "platform-dev",
				Tracker:      "gitlab",
				AgentType:    "code-pr",
				PollInterval: config.Duration{Duration: 20 * time.Millisecond},
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:            "code-pr",
				InstanceName:    "coder",
				Harness:         "claude-code",
				Workspace:       "git-clone",
				Prompt:          promptPath,
				ApprovalPolicy:  "auto",
				ApprovalTimeout: config.Duration{Duration: 24 * time.Hour},
				MaxConcurrent:   1,
				StallTimeout:    config.Duration{Duration: time.Minute},
			},
		},
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

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func slicesContains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, timeout time.Duration, check func() bool) {
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

func waitForPersistedState(t *testing.T, dir string, timeout time.Duration, check func(state.Snapshot) bool) state.Snapshot {
	t.Helper()

	store := state.NewStore(dir)
	deadline := time.Now().Add(timeout)
	var last state.Snapshot
	var lastErr error
	for time.Now().Before(deadline) {
		snapshot, err := store.Load()
		if err == nil {
			last = snapshot
			if check(snapshot) {
				return snapshot
			}
		} else {
			lastErr = err
		}
		time.Sleep(10 * time.Millisecond)
	}

	if lastErr != nil {
		t.Fatalf("load persisted state: %v", lastErr)
	}
	t.Fatalf("persisted state did not satisfy condition before timeout: %+v", last)
	return state.Snapshot{}
}

func createGitRepo(t *testing.T) string {
	t.Helper()

	return createGitRepoWithFiles(t, map[string]string{
		"README.md": "hello\n",
	})
}

func createGitRepoWithFiles(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	for path, contents := range files {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	runGit(t, root, "init")
	runGit(t, root, "add", ".")
	runGit(t, root, "-c", "user.name=Test User", "-c", "user.email=test@example.com", "commit", "-m", "init")
	bare := filepath.Join(t.TempDir(), "repo.git")
	runGit(t, root, "clone", "--bare", root, bare)
	return bare
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, string(output))
	}
}

func singleIssue(cfg *config.Config, repoURL string, id string, identifier string) []domain.Issue {
	return []domain.Issue{
		{
			ID:          id,
			Identifier:  identifier,
			Title:       "Test issue",
			SourceName:  cfg.Sources[0].Name,
			TrackerKind: "gitlab",
			Meta: map[string]string{
				"repo_url": repoURL,
			},
		},
	}
}

func hookTouchCommand(path string) string {
	if runtime.GOOS == "windows" {
		return "type nul > \"" + path + "\""
	}
	return "touch \"" + path + "\""
}

type blockingLifecycleTracker struct {
	*testutil.FakeTracker
	blockLabel string
	started    chan struct{}
	release    chan struct{}

	triggered bool
}

func (b *blockingLifecycleTracker) AddLifecycleLabel(ctx context.Context, issueID string, label string) error {
	if label == b.blockLabel && !b.triggered {
		b.triggered = true
		close(b.started)
		<-b.release
	}
	return b.FakeTracker.AddLifecycleLabel(ctx, issueID, label)
}

type failingLifecycleTracker struct {
	*testutil.FakeTracker
	addErr     error
	removeErr  error
	commentErr error
}

func (f *failingLifecycleTracker) AddLifecycleLabel(ctx context.Context, issueID string, label string) error {
	if f.addErr != nil {
		return f.addErr
	}
	return f.FakeTracker.AddLifecycleLabel(ctx, issueID, label)
}

func (f *failingLifecycleTracker) RemoveLifecycleLabel(ctx context.Context, issueID string, label string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	return f.FakeTracker.RemoveLifecycleLabel(ctx, issueID, label)
}

func (f *failingLifecycleTracker) PostOperationalComment(ctx context.Context, issueID string, body string) error {
	if f.commentErr != nil {
		return f.commentErr
	}
	return f.FakeTracker.PostOperationalComment(ctx, issueID, body)
}

type blockingApproveHarness struct {
	*testutil.FakeHarness
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingApproveHarness) Approve(ctx context.Context, decision harness.ApprovalDecision) error {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return b.FakeHarness.Approve(ctx, decision)
}
