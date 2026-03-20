package tracker

import (
	"strings"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
)

// Default lifecycle label constants (prefix "maestro").
const (
	LifecycleLabelActive = "maestro:active"
	LifecycleLabelRetry  = "maestro:retry"
	LifecycleLabelDone   = "maestro:done"
	LifecycleLabelFailed = "maestro:failed"
)

// Lifecycle label suffixes.
const (
	LifecycleSuffixActive = "active"
	LifecycleSuffixDone   = "done"
	LifecycleSuffixFailed = "failed"
	LifecycleSuffixRetry  = "retry"
)

// LifecycleLabel constructs a lifecycle label from a prefix and suffix.
func LifecycleLabel(prefix, suffix string) string {
	return normalizeLifecyclePrefix(prefix) + ":" + suffix
}

// AllLifecycleLabels returns the four lifecycle labels for a given prefix.
func AllLifecycleLabels(prefix string) []string {
	return []string{
		LifecycleLabel(prefix, LifecycleSuffixActive),
		LifecycleLabel(prefix, LifecycleSuffixRetry),
		LifecycleLabel(prefix, LifecycleSuffixDone),
		LifecycleLabel(prefix, LifecycleSuffixFailed),
	}
}

func normalizeLifecyclePrefix(prefix string) string {
	if strings.TrimSpace(prefix) == "" {
		return "maestro"
	}
	return strings.TrimSpace(prefix)
}

func HasBlockingLifecycleLabel(labels []string) bool {
	return HasBlockingLifecycleLabelWithPrefix(labels, "maestro")
}

func HasBlockingLifecycleLabelWithPrefix(labels []string, prefix string) bool {
	return LifecycleLabelStateWithPrefix(labels, prefix) != ""
}

func LifecycleLabelState(labels []string) string {
	return LifecycleLabelStateWithPrefix(labels, "maestro")
}

func LifecycleLabelStateWithPrefix(labels []string, prefix string) string {
	prefix = normalizeLifecyclePrefix(prefix)
	active := LifecycleLabel(prefix, LifecycleSuffixActive)
	retry := LifecycleLabel(prefix, LifecycleSuffixRetry)
	done := LifecycleLabel(prefix, LifecycleSuffixDone)
	failed := LifecycleLabel(prefix, LifecycleSuffixFailed)

	seen := map[string]bool{}
	for _, label := range labels {
		normalized := strings.ToLower(strings.TrimSpace(label))
		switch normalized {
		case active, retry, done, failed:
			seen[normalized] = true
		}
	}
	for _, candidate := range []string{done, failed, retry, active} {
		if seen[candidate] {
			return candidate
		}
	}
	return ""
}

func StripLifecycleLabels(labels []string) []string {
	return StripLifecycleLabelsWithPrefix(labels, "maestro")
}

func StripLifecycleLabelsWithPrefix(labels []string, prefix string) []string {
	prefix = normalizeLifecyclePrefix(prefix)
	active := LifecycleLabel(prefix, LifecycleSuffixActive)
	retry := LifecycleLabel(prefix, LifecycleSuffixRetry)
	done := LifecycleLabel(prefix, LifecycleSuffixDone)
	failed := LifecycleLabel(prefix, LifecycleSuffixFailed)

	out := make([]string, 0, len(labels))
	for _, label := range labels {
		normalized := strings.ToLower(strings.TrimSpace(label))
		switch normalized {
		case active, retry, done, failed:
			continue
		default:
			out = append(out, normalized)
		}
	}
	return out
}

func MatchesFilter(issue domain.Issue, filter config.FilterConfig) bool {
	return MatchesFilterWithPrefix(issue, filter, "maestro")
}

func MatchesFilterWithPrefix(issue domain.Issue, filter config.FilterConfig, prefix string) bool {
	labels := StripLifecycleLabelsWithPrefix(issue.Labels, prefix)

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
	return IsCandidateWithPrefix(issue, filter, "maestro")
}

func IsCandidateWithPrefix(issue domain.Issue, filter config.FilterConfig, prefix string) bool {
	return MatchesFilterWithPrefix(issue, filter, prefix) && !HasBlockingLifecycleLabelWithPrefix(issue.Labels, prefix)
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
