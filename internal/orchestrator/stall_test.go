package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/testutil"
)

func TestReconcileStalledRunStopsTimedOutActiveRun(t *testing.T) {
	fakeHarness := &testutil.FakeHarness{}
	svc := newStallTestService(fakeHarness, 50*time.Millisecond)
	svc.activeRun = &domain.AgentRun{
		ID:             "run-1",
		SourceName:     "test-source",
		Status:         domain.RunStatusActive,
		LastActivityAt: time.Now().Add(-time.Second),
	}

	svc.reconcileStalledRun(context.Background())

	stop, ok := svc.pendingStops["run-1"]
	if !ok {
		t.Fatal("expected pending stop for stalled run")
	}
	if stop.Status != domain.RunStatusFailed || !stop.Retry {
		t.Fatalf("pending stop = %+v, want failed retry stop", stop)
	}
	if len(fakeHarness.StopCalls) != 1 || fakeHarness.StopCalls[0] != "run-1" {
		t.Fatalf("stop calls = %+v, want [run-1]", fakeHarness.StopCalls)
	}
}

func TestReconcileStalledRunIgnoresRecentActivity(t *testing.T) {
	fakeHarness := &testutil.FakeHarness{}
	svc := newStallTestService(fakeHarness, time.Minute)
	svc.activeRun = &domain.AgentRun{
		ID:             "run-1",
		SourceName:     "test-source",
		Status:         domain.RunStatusActive,
		LastActivityAt: time.Now(),
	}

	svc.reconcileStalledRun(context.Background())

	if len(fakeHarness.StopCalls) != 0 {
		t.Fatalf("stop calls = %+v, want none", fakeHarness.StopCalls)
	}
	if len(svc.pendingStops) != 0 {
		t.Fatalf("pending stops = %+v, want none", svc.pendingStops)
	}
}

func TestReconcileStalledRunIgnoresNonStallStatuses(t *testing.T) {
	fakeHarness := &testutil.FakeHarness{}
	svc := newStallTestService(fakeHarness, 50*time.Millisecond)
	svc.activeRun = &domain.AgentRun{
		ID:             "run-1",
		SourceName:     "test-source",
		Status:         domain.RunStatusDone,
		LastActivityAt: time.Now().Add(-time.Second),
	}

	svc.reconcileStalledRun(context.Background())

	if len(fakeHarness.StopCalls) != 0 {
		t.Fatalf("stop calls = %+v, want none", fakeHarness.StopCalls)
	}
	if len(svc.pendingStops) != 0 {
		t.Fatalf("pending stops = %+v, want none", svc.pendingStops)
	}
}

func TestReconcileStalledRunRespectsSourceOverride(t *testing.T) {
	fakeHarness := &testutil.FakeHarness{}
	svc := newStallTestService(fakeHarness, 50*time.Millisecond)
	// Source sets a longer stall timeout that should override the agent default.
	svc.source.StallTimeout = config.Duration{Duration: time.Hour}
	svc.activeRun = &domain.AgentRun{
		ID:             "run-1",
		SourceName:     "test-source",
		Status:         domain.RunStatusActive,
		LastActivityAt: time.Now().Add(-time.Second),
	}

	svc.reconcileStalledRun(context.Background())

	if len(fakeHarness.StopCalls) != 0 {
		t.Fatalf("stop calls = %+v, want none (source stall_timeout should keep run alive)", fakeHarness.StopCalls)
	}
	if len(svc.pendingStops) != 0 {
		t.Fatalf("pending stops = %+v, want none", svc.pendingStops)
	}
}

func newStallTestService(fakeHarness *testutil.FakeHarness, stallTimeout time.Duration) *Service {
	return &Service{
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		source:       config.SourceConfig{Name: "test-source"},
		agent:        config.AgentTypeConfig{StallTimeout: config.Duration{Duration: stallTimeout}},
		harness:      fakeHarness,
		pendingStops: map[string]pendingStop{},
	}
}
