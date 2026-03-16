package domain

import "time"

// Issue is the normalized work item shape used by the orchestrator.
type Issue struct {
	ID          string
	ExternalID  string
	Identifier  string
	TrackerKind string
	SourceName  string
	Title       string
	Description string
	Priority    *int
	State       string
	Labels      []string
	Assignee    string
	URL         string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Meta        map[string]string
}
