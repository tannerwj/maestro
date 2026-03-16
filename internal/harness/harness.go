package harness

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrApprovalsUnsupported = errors.New("approvals unsupported")

type RunConfig struct {
	RunID          string
	Prompt         string
	Workdir        string
	ApprovalPolicy string
	Env            map[string]string
	Stdout         io.Writer
	Stderr         io.Writer
}

type ActiveRun interface {
	RunID() string
	Wait() error
}

type ApprovalRequest struct {
	RequestID      string
	RunID          string
	ToolName       string
	ToolInput      string
	ApprovalPolicy string
	RequestedAt    time.Time
}

type ApprovalDecision struct {
	RequestID string
	Decision  string
	Reason    string
	TimedOut  bool
}

type Harness interface {
	Kind() string
	Start(ctx context.Context, cfg RunConfig) (ActiveRun, error)
	Stop(ctx context.Context, runID string) error
	Approvals() <-chan ApprovalRequest
	Approve(ctx context.Context, decision ApprovalDecision) error
}
