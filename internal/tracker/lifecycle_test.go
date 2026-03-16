package tracker

import (
	"testing"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
)

func TestDoneAndFailedLifecycleLabelsBlockCandidateIntake(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
	}{
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
