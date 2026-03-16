package tracker

import (
	"strings"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
)

const (
	LifecycleLabelActive = "maestro:active"
	LifecycleLabelRetry  = "maestro:retry"
	LifecycleLabelDone   = "maestro:done"
	LifecycleLabelFailed = "maestro:failed"
)

func HasBlockingLifecycleLabel(labels []string) bool {
	return LifecycleLabelState(labels) != ""
}

func LifecycleLabelState(labels []string) string {
	seen := map[string]bool{}
	for _, label := range labels {
		switch normalized := strings.ToLower(strings.TrimSpace(label)); normalized {
		case LifecycleLabelActive, LifecycleLabelRetry, LifecycleLabelDone, LifecycleLabelFailed:
			seen[normalized] = true
		}
	}
	for _, candidate := range []string{LifecycleLabelDone, LifecycleLabelFailed, LifecycleLabelRetry, LifecycleLabelActive} {
		if seen[candidate] {
			return candidate
		}
	}
	return ""
}

func StripLifecycleLabels(labels []string) []string {
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		normalized := strings.ToLower(strings.TrimSpace(label))
		switch normalized {
		case LifecycleLabelActive, LifecycleLabelRetry, LifecycleLabelDone, LifecycleLabelFailed:
			continue
		default:
			out = append(out, normalized)
		}
	}
	return out
}

func MatchesFilter(issue domain.Issue, filter config.FilterConfig) bool {
	labels := StripLifecycleLabels(issue.Labels)

	if len(filter.Labels) > 0 {
		set := make(map[string]struct{}, len(labels))
		for _, label := range labels {
			set[strings.ToLower(label)] = struct{}{}
		}
		for _, want := range filter.Labels {
			if _, ok := set[strings.ToLower(strings.TrimSpace(want))]; !ok {
				return false
			}
		}
	}

	if len(filter.States) > 0 {
		match := false
		for _, state := range filter.States {
			if strings.EqualFold(issue.State, state) {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}

	if assignee := strings.TrimSpace(filter.Assignee); assignee != "" && !strings.EqualFold(issue.Assignee, assignee) {
		return false
	}

	return true
}

func IsCandidate(issue domain.Issue, filter config.FilterConfig) bool {
	return MatchesFilter(issue, filter) && !HasBlockingLifecycleLabel(issue.Labels)
}

func IsTerminal(issue domain.Issue) bool {
	if stateType := strings.ToLower(strings.TrimSpace(issue.Meta["bucket_state_type"])); stateType != "" {
		switch stateType {
		case "closed", "completed", "canceled", "cancelled":
			return true
		}
	}
	if stateType := strings.ToLower(strings.TrimSpace(issue.Meta["state_type"])); stateType != "" {
		switch stateType {
		case "completed", "canceled", "cancelled":
			return true
		}
	}

	switch strings.ToLower(strings.TrimSpace(issue.State)) {
	case "closed", "done", "completed", "canceled", "cancelled", "resolved":
		return true
	default:
		return false
	}
}
