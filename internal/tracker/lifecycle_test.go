package tracker

import (
	"testing"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
)

func TestReservedLifecycleLabelsBlockCandidateIntake(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
	}{
		{name: "active", labels: []string{LifecycleLabelActive}},
		{name: "retry", labels: []string{LifecycleLabelRetry}},
		{name: "done", labels: []string{LifecycleLabelDone}},
		{name: "failed", labels: []string{LifecycleLabelFailed}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issue := domain.Issue{
				Labels: test.labels,
				State:  "todo",
			}

			if IsCandidate(issue, config.FilterConfig{States: []string{"todo"}}) {
				t.Fatalf("issue with labels %v should not be a candidate", test.labels)
			}
		})
	}
}

func TestLifecycleLabelsDoNotParticipateInUserLabelMatching(t *testing.T) {
	issue := domain.Issue{
		Labels: []string{"agent:ready", LifecycleLabelDone},
		State:  "todo",
	}

	if !MatchesFilter(issue, config.FilterConfig{
		Labels: []string{"agent:ready"},
		States: []string{"todo"},
	}) {
		t.Fatal("expected user labels to match after stripping lifecycle labels")
	}
}

func TestCustomPrefixedRoutingLabelsRemainVisibleToFilters(t *testing.T) {
	issue := domain.Issue{
		Labels: []string{"orch:coding"},
		State:  "todo",
	}

	if !IsCandidateWithPrefix(issue, config.FilterConfig{
		Labels: []string{"orch:coding"},
		States: []string{"todo"},
	}, "orch") {
		t.Fatal("expected custom routing label to remain visible to filters")
	}
}

func TestCustomPrefixedActiveLabelBlocksCandidateIntake(t *testing.T) {
	issue := domain.Issue{
		Labels: []string{"orch:coding", "orch:active"},
		State:  "todo",
	}

	if IsCandidateWithPrefix(issue, config.FilterConfig{
		Labels: []string{"orch:coding"},
		States: []string{"todo"},
	}, "orch") {
		t.Fatal("expected orch:active to block intake while preserving routing label")
	}
}
