package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/tjohnson/maestro/internal/domain"
)

const currentVersion = 2

type TerminalIssue struct {
	IssueID        string           `json:"issue_id"`
	Identifier     string           `json:"identifier"`
	Status         domain.RunStatus `json:"status"`
	Attempt        int              `json:"attempt"`
	IssueUpdatedAt time.Time        `json:"issue_updated_at,omitempty"`
	FinishedAt     time.Time        `json:"finished_at"`
	Error          string           `json:"error,omitempty"`
}

type RetryEntry struct {
	IssueID        string    `json:"issue_id"`
	Identifier     string    `json:"identifier"`
	Attempt        int       `json:"attempt"`
	DueAt          time.Time `json:"due_at"`
	Error          string    `json:"error,omitempty"`
	IssueUpdatedAt time.Time `json:"issue_updated_at,omitempty"`
}

type PersistedRun struct {
	RunID          string           `json:"run_id"`
	IssueID        string           `json:"issue_id"`
	Identifier     string           `json:"identifier"`
	Status         domain.RunStatus `json:"status"`
	Attempt        int              `json:"attempt"`
	WorkspacePath  string           `json:"workspace_path,omitempty"`
	StartedAt      time.Time        `json:"started_at"`
	LastActivityAt time.Time        `json:"last_activity_at,omitempty"`
	IssueUpdatedAt time.Time        `json:"issue_updated_at,omitempty"`
}

type PersistedApprovalRequest struct {
	RequestID       string    `json:"request_id"`
	RunID           string    `json:"run_id"`
	IssueID         string    `json:"issue_id,omitempty"`
	IssueIdentifier string    `json:"issue_identifier,omitempty"`
	AgentName       string    `json:"agent_name,omitempty"`
	ToolName        string    `json:"tool_name"`
	ToolInput       string    `json:"tool_input,omitempty"`
	ApprovalPolicy  string    `json:"approval_policy,omitempty"`
	RequestedAt     time.Time `json:"requested_at"`
	Resolvable      bool      `json:"resolvable,omitempty"`
}

type PersistedApprovalDecision struct {
	RequestID       string    `json:"request_id"`
	RunID           string    `json:"run_id"`
	IssueID         string    `json:"issue_id,omitempty"`
	IssueIdentifier string    `json:"issue_identifier,omitempty"`
	AgentName       string    `json:"agent_name,omitempty"`
	ToolName        string    `json:"tool_name,omitempty"`
	ApprovalPolicy  string    `json:"approval_policy,omitempty"`
	Decision        string    `json:"decision"`
	Reason          string    `json:"reason,omitempty"`
	RequestedAt     time.Time `json:"requested_at,omitempty"`
	DecidedAt       time.Time `json:"decided_at"`
	Outcome         string    `json:"outcome,omitempty"`
}

type Snapshot struct {
	Version          int                         `json:"version"`
	Finished         map[string]TerminalIssue    `json:"finished"`
	RetryQueue       map[string]RetryEntry       `json:"retry_queue"`
	ActiveRun        *PersistedRun               `json:"active_run,omitempty"`
	PendingApprovals []PersistedApprovalRequest  `json:"pending_approvals,omitempty"`
	ApprovalHistory  []PersistedApprovalDecision `json:"approval_history,omitempty"`
}

type Store struct {
	path string
}

func NewStore(dir string) *Store {
	return &Store{path: filepath.Join(dir, "runs.json")}
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) Load() (Snapshot, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptySnapshot(), nil
		}
		return Snapshot{}, err
	}

	snapshot := emptySnapshot()
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return Snapshot{}, err
	}
	if snapshot.Version == 0 {
		snapshot.Version = currentVersion
	}
	if snapshot.Finished == nil {
		snapshot.Finished = map[string]TerminalIssue{}
	}
	if snapshot.RetryQueue == nil {
		snapshot.RetryQueue = map[string]RetryEntry{}
	}
	return snapshot, nil
}

func (s *Store) Save(snapshot Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	snapshot.Version = currentVersion
	if snapshot.Finished == nil {
		snapshot.Finished = map[string]TerminalIssue{}
	}
	if snapshot.RetryQueue == nil {
		snapshot.RetryQueue = map[string]RetryEntry{}
	}
	if snapshot.PendingApprovals == nil {
		snapshot.PendingApprovals = []PersistedApprovalRequest{}
	}
	if snapshot.ApprovalHistory == nil {
		snapshot.ApprovalHistory = []PersistedApprovalDecision{}
	}

	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.path), "runs-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	cleanup := func() {
		_ = os.Remove(tmpPath)
	}
	defer cleanup()

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return err
	}

	return nil
}

func emptySnapshot() Snapshot {
	return Snapshot{
		Version:          currentVersion,
		Finished:         map[string]TerminalIssue{},
		RetryQueue:       map[string]RetryEntry{},
		PendingApprovals: []PersistedApprovalRequest{},
		ApprovalHistory:  []PersistedApprovalDecision{},
	}
}
