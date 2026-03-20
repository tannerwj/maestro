package harness

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrApprovalsUnsupported = errors.New("approvals unsupported")
var ErrMessagesUnsupported = errors.New("messages unsupported")

type RunConfig struct {
	RunID          string
	Prompt         string
	Workdir        string
	ApprovalPolicy string
	Env            map[string]string
	Stdout         io.Writer
	Stderr         io.Writer

	// Harness configuration
	Model     string
	Reasoning string
	MaxTurns  int
	ExtraArgs []string

	// Codex-specific
	ThreadSandbox     string
	TurnSandboxPolicy map[string]any

	// Multi-turn continuation
	ContinuationFunc func(ctx context.Context, turnNumber int) (prompt string, cont bool, err error)
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

type MessageRequest struct {
	RequestID   string
	RunID       string
	Kind        string
	Summary     string
	Body        string
	RequestedAt time.Time
}

type MessageReply struct {
	RequestID string
	Kind      string
	Reply     string
	RepliedAt time.Time
}

type Harness interface {
	Kind() string
	Start(ctx context.Context, cfg RunConfig) (ActiveRun, error)
	Stop(ctx context.Context, runID string) error
	Approvals() <-chan ApprovalRequest
	Approve(ctx context.Context, decision ApprovalDecision) error
	Messages() <-chan MessageRequest
	Reply(ctx context.Context, reply MessageReply) error
}
