package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/testutil"
	"github.com/tjohnson/maestro/internal/workspace"
)

type blockingPollTracker struct{}

func (blockingPollTracker) Kind() string { return "blocking" }

func (blockingPollTracker) Poll(ctx context.Context) ([]domain.Issue, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingPollTracker) Get(ctx context.Context, issueID string) (domain.Issue, error) {
	return domain.Issue{}, errors.New("not implemented")
}

func (blockingPollTracker) PostOperationalComment(ctx context.Context, issueID string, body string) error {
	return nil
}

func (blockingPollTracker) AddLifecycleLabel(ctx context.Context, issueID string, label string) error {
	return nil
}

func (blockingPollTracker) RemoveLifecycleLabel(ctx context.Context, issueID string, label string) error {
	return nil
}

func (blockingPollTracker) UpdateIssueState(ctx context.Context, issueID string, stateName string) error {
	return nil
}

func TestTickBoundsPollWithTimeout(t *testing.T) {
	oldTimeout := pollRequestTimeout
	pollRequestTimeout = 50 * time.Millisecond
	defer func() { pollRequestTimeout = oldTimeout }()

	root := t.TempDir()
	promptPath := filepath.Join(root, "prompt.md")
	cfg := &config.Config{
		Defaults: config.DefaultsConfig{
			PollInterval:        config.Duration{Duration: 20 * time.Millisecond},
			MaxConcurrentGlobal: 1,
			StallTimeout:        config.Duration{Duration: time.Minute},
		},
		Sources: []config.SourceConfig{{
			Name:         "platform-dev",
			Tracker:      "gitlab",
			AgentType:    "code-pr",
			PollInterval: config.Duration{Duration: 20 * time.Millisecond},
		}},
		AgentTypes: []config.AgentTypeConfig{{
			Name:            "code-pr",
			Harness:         "claude-code",
			Workspace:       "git-clone",
			Prompt:          promptPath,
			ApprovalPolicy:  "auto",
			ApprovalTimeout: config.Duration{Duration: 24 * time.Hour},
			MaxConcurrent:   1,
			StallTimeout:    config.Duration{Duration: time.Minute},
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

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := NewServiceWithDeps(cfg, logger, Dependencies{
		Tracker:   blockingPollTracker{},
		Harness:   &testutil.FakeHarness{},
		Workspace: workspace.NewManager(cfg.Workspace.Root),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	start := time.Now()
	err = svc.tick(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("tick error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("tick took %s, want bounded timeout", elapsed)
	}
}
