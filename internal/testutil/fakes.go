package testutil

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/harness"
)

type FakeTracker struct {
	Issues []domain.Issue
	Err    error

	mu sync.Mutex
}

func (f *FakeTracker) Kind() string { return "fake" }

func (f *FakeTracker) Poll(ctx context.Context) ([]domain.Issue, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	out := append([]domain.Issue(nil), f.Issues...)
	return out, nil
}

func (f *FakeTracker) Get(ctx context.Context, issueID string) (domain.Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, issue := range f.Issues {
		if issue.ID == issueID {
			return issue, nil
		}
	}
	return domain.Issue{}, errors.New("not found")
}

func (f *FakeTracker) PostOperationalComment(ctx context.Context, issueID string, body string) error {
	f.bumpUpdatedAt(issueID)
	return nil
}

func (f *FakeTracker) AddLifecycleLabel(ctx context.Context, issueID string, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := range f.Issues {
		if f.Issues[i].ID != issueID {
			continue
		}
		for _, existing := range f.Issues[i].Labels {
			if existing == label {
				f.Issues[i].UpdatedAt = nextUpdatedAt(f.Issues[i].UpdatedAt)
				return nil
			}
		}
		f.Issues[i].Labels = append(f.Issues[i].Labels, label)
		f.Issues[i].UpdatedAt = nextUpdatedAt(f.Issues[i].UpdatedAt)
		return nil
	}
	return errors.New("not found")
}

func (f *FakeTracker) RemoveLifecycleLabel(ctx context.Context, issueID string, label string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := range f.Issues {
		if f.Issues[i].ID != issueID {
			continue
		}
		filtered := f.Issues[i].Labels[:0]
		for _, existing := range f.Issues[i].Labels {
			if existing != label {
				filtered = append(filtered, existing)
			}
		}
		f.Issues[i].Labels = filtered
		f.Issues[i].UpdatedAt = nextUpdatedAt(f.Issues[i].UpdatedAt)
		return nil
	}
	return errors.New("not found")
}

func (f *FakeTracker) bumpUpdatedAt(issueID string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := range f.Issues {
		if f.Issues[i].ID == issueID {
			f.Issues[i].UpdatedAt = nextUpdatedAt(f.Issues[i].UpdatedAt)
			return
		}
	}
}

func (f *FakeTracker) SetIssueState(issueID string, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := range f.Issues {
		if f.Issues[i].ID == issueID {
			f.Issues[i].State = state
			f.Issues[i].UpdatedAt = nextUpdatedAt(f.Issues[i].UpdatedAt)
			return
		}
	}
}

func nextUpdatedAt(current time.Time) time.Time {
	if current.IsZero() {
		return time.Now().UTC()
	}
	return current.Add(time.Second)
}

type FakeHarness struct {
	KindValue   string
	StartErr    error
	WaitErr     error
	WaitErrs    []error
	StartedRuns []harness.RunConfig
	StopCalls   []string
	WaitBlock   chan struct{}
	ApprovalCh  chan harness.ApprovalRequest
	ApproveErr  error
	Decisions   []harness.ApprovalDecision

	mu sync.Mutex
}

type fakeActiveRun struct {
	runID string
	wait  func() error
}

func (f *FakeHarness) Kind() string {
	if f.KindValue == "" {
		return "fake-harness"
	}
	return f.KindValue
}

func (f *FakeHarness) Start(ctx context.Context, cfg harness.RunConfig) (harness.ActiveRun, error) {
	f.mu.Lock()
	f.StartedRuns = append(f.StartedRuns, cfg)
	runIndex := len(f.StartedRuns) - 1
	f.mu.Unlock()

	if cfg.Stdout != nil {
		_, _ = io.WriteString(cfg.Stdout, "fake stdout")
	}
	if cfg.Stderr != nil {
		_, _ = io.WriteString(cfg.Stderr, "fake stderr")
	}
	if f.StartErr != nil {
		return nil, f.StartErr
	}

	return &fakeActiveRun{
		runID: cfg.RunID,
		wait: func() error {
			if f.WaitBlock != nil {
				<-f.WaitBlock
			}
			if runIndex < len(f.WaitErrs) {
				return f.WaitErrs[runIndex]
			}
			return f.WaitErr
		},
	}, nil
}

func (f *FakeHarness) Stop(ctx context.Context, runID string) error {
	f.mu.Lock()
	f.StopCalls = append(f.StopCalls, runID)
	if f.WaitBlock != nil {
		select {
		case <-f.WaitBlock:
		default:
			close(f.WaitBlock)
		}
	}
	f.mu.Unlock()
	return nil
}

func (f *FakeHarness) Approvals() <-chan harness.ApprovalRequest {
	return f.ApprovalCh
}

func (f *FakeHarness) Approve(ctx context.Context, decision harness.ApprovalDecision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Decisions = append(f.Decisions, decision)
	if f.ApproveErr != nil {
		return f.ApproveErr
	}
	if f.ApprovalCh == nil {
		return harness.ErrApprovalsUnsupported
	}
	return nil
}

func (r *fakeActiveRun) RunID() string {
	return r.runID
}

func (r *fakeActiveRun) Wait() error {
	return r.wait()
}
