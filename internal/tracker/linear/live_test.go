package linear

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/testutil"
	trackerbase "github.com/tjohnson/maestro/internal/tracker"
)

func TestLiveLinearPollsConfiguredProject(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_LINEAR")
	env := testutil.RequireEnv(
		t,
		"MAESTRO_TEST_LINEAR_TOKEN",
		"MAESTRO_TEST_LINEAR_PROJECT",
	)

	adapter, err := NewAdapter(config.SourceConfig{
		Name:      "live-linear",
		Tracker:   "linear",
		AgentType: "code-pr",
		Repo:      "https://gitlab.example.com/team/maestro-testbed.git",
		Connection: config.SourceConnection{
			Token:   env["MAESTRO_TEST_LINEAR_TOKEN"],
			Project: env["MAESTRO_TEST_LINEAR_PROJECT"],
		},
		Filter: config.FilterConfig{
			States: []string{"Todo"},
		},
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	issues, err := adapter.Poll(ctx)
	if err != nil {
		t.Fatalf("poll live linear: %v", err)
	}
	if len(issues) == 0 {
		t.Fatalf("expected at least one issue for project=%s", env["MAESTRO_TEST_LINEAR_PROJECT"])
	}

	issue := issues[0]
	if issue.TrackerKind != "linear" {
		t.Fatalf("tracker kind = %q", issue.TrackerKind)
	}
	if !strings.HasPrefix(issue.Identifier, "TAN-") {
		t.Fatalf("identifier = %q", issue.Identifier)
	}
}

func TestLiveLinearLifecycleWriteback(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_LINEAR")
	env := testutil.RequireEnv(
		t,
		"MAESTRO_TEST_LINEAR_TOKEN",
		"MAESTRO_TEST_LINEAR_PROJECT",
	)

	adapter, err := NewAdapter(config.SourceConfig{
		Name:      "live-linear",
		Tracker:   "linear",
		AgentType: "code-pr",
		Repo:      "https://gitlab.example.com/team/maestro-testbed.git",
		Connection: config.SourceConnection{
			Token:   env["MAESTRO_TEST_LINEAR_TOKEN"],
			Project: env["MAESTRO_TEST_LINEAR_PROJECT"],
		},
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	issues, err := adapter.Poll(ctx)
	if err != nil {
		t.Fatalf("poll live linear: %v", err)
	}
	if len(issues) == 0 {
		t.Fatalf("expected at least one issue for project=%s", env["MAESTRO_TEST_LINEAR_PROJECT"])
	}

	issue := issues[0]
	cleanupLabels := []string{
		trackerbase.LifecycleLabelActive,
		trackerbase.LifecycleLabelRetry,
		trackerbase.LifecycleLabelDone,
		trackerbase.LifecycleLabelFailed,
	}
	for _, label := range cleanupLabels {
		_ = adapter.RemoveLifecycleLabel(ctx, issue.ID, label)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		for _, label := range cleanupLabels {
			_ = adapter.RemoveLifecycleLabel(cleanupCtx, issue.ID, label)
		}
	})

	comment := "Maestro live writeback test."
	if err := adapter.PostOperationalComment(ctx, issue.ID, comment); err != nil {
		t.Fatalf("post operational comment: %v", err)
	}

	if err := adapter.AddLifecycleLabel(ctx, issue.ID, trackerbase.LifecycleLabelActive); err != nil {
		t.Fatalf("add lifecycle label: %v", err)
	}

	updated, err := adapter.Get(ctx, issue.ID)
	if err != nil {
		t.Fatalf("get issue after add: %v", err)
	}
	if !hasLabel(updated.Labels, trackerbase.LifecycleLabelActive) {
		t.Fatalf("expected %q on issue %s, labels=%v", trackerbase.LifecycleLabelActive, updated.Identifier, updated.Labels)
	}

	if err := adapter.RemoveLifecycleLabel(ctx, issue.ID, trackerbase.LifecycleLabelActive); err != nil {
		t.Fatalf("remove lifecycle label: %v", err)
	}

	updated, err = adapter.Get(ctx, issue.ID)
	if err != nil {
		t.Fatalf("get issue after remove: %v", err)
	}
	if hasLabel(updated.Labels, trackerbase.LifecycleLabelActive) {
		t.Fatalf("expected %q removed from issue %s, labels=%v", trackerbase.LifecycleLabelActive, updated.Identifier, updated.Labels)
	}
}

func hasLabel(labels []string, want string) bool {
	for _, label := range labels {
		if strings.EqualFold(strings.TrimSpace(label), want) {
			return true
		}
	}
	return false
}
