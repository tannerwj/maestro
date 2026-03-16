package tracker

import (
	"context"

	"github.com/tjohnson/maestro/internal/domain"
)

type Tracker interface {
	Kind() string
	Poll(ctx context.Context) ([]domain.Issue, error)
	Get(ctx context.Context, issueID string) (domain.Issue, error)
	PostOperationalComment(ctx context.Context, issueID string, body string) error
	AddLifecycleLabel(ctx context.Context, issueID string, label string) error
	RemoveLifecycleLabel(ctx context.Context, issueID string, label string) error
}
