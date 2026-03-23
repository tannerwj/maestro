package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tjohnson/maestro/internal/domain"
)

const currentVersion = 3
const backupCount = 2

type CorruptStateError struct {
	Path         string
	ArchivedPath string
	Err          error
}

func (e *CorruptStateError) Error() string {
	if e == nil {
		return ""
	}
	if e.ArchivedPath != "" {
		return fmt.Sprintf("state file %s is unreadable or invalid; archived at %s: %v", e.Path, e.ArchivedPath, e.Err)
	}
	return fmt.Sprintf("state file %s is unreadable or invalid: %v", e.Path, e.Err)
}

func (e *CorruptStateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

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
	WorkspacePath  string    `json:"workspace_path,omitempty"`
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

type PersistedMessageRequest struct {
	RequestID       string    `json:"request_id"`
	RunID           string    `json:"run_id"`
	IssueID         string    `json:"issue_id,omitempty"`
	IssueIdentifier string    `json:"issue_identifier,omitempty"`
	SourceName      string    `json:"source_name,omitempty"`
	AgentName       string    `json:"agent_name,omitempty"`
	Kind            string    `json:"kind,omitempty"`
	Summary         string    `json:"summary,omitempty"`
	Body            string    `json:"body,omitempty"`
	RequestedAt     time.Time `json:"requested_at"`
	Resolvable      bool      `json:"resolvable,omitempty"`
}

type PersistedMessageReply struct {
	RequestID       string    `json:"request_id"`
	RunID           string    `json:"run_id"`
	IssueID         string    `json:"issue_id,omitempty"`
	IssueIdentifier string    `json:"issue_identifier,omitempty"`
	SourceName      string    `json:"source_name,omitempty"`
	AgentName       string    `json:"agent_name,omitempty"`
	Kind            string    `json:"kind,omitempty"`
	Summary         string    `json:"summary,omitempty"`
	Body            string    `json:"body,omitempty"`
	Reply           string    `json:"reply,omitempty"`
	ResolvedVia     string    `json:"resolved_via,omitempty"`
	RequestedAt     time.Time `json:"requested_at,omitempty"`
	RepliedAt       time.Time `json:"replied_at"`
	Outcome         string    `json:"outcome,omitempty"`
}

type Snapshot struct {
	Version          int                         `json:"version"`
	Finished         map[string]TerminalIssue    `json:"finished"`
	RetryQueue       map[string]RetryEntry       `json:"retry_queue"`
	ActiveRun        *PersistedRun               `json:"active_run,omitempty"`
	PendingApprovals []PersistedApprovalRequest  `json:"pending_approvals,omitempty"`
	ApprovalHistory  []PersistedApprovalDecision `json:"approval_history,omitempty"`
	PendingMessages  []PersistedMessageRequest   `json:"pending_messages,omitempty"`
	MessageHistory   []PersistedMessageReply     `json:"message_history,omitempty"`
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

func (s *Store) Dir() string {
	return filepath.Dir(s.path)
}

func (s *Store) Load() (Snapshot, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptySnapshot(), nil
		}
		return emptySnapshot(), &CorruptStateError{Path: s.path, Err: err}
	}

	snapshot := emptySnapshot()
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		archivedPath, archiveErr := s.archiveCorruptFile()
		if archiveErr != nil {
			return emptySnapshot(), &CorruptStateError{
				Path: s.path,
				Err:  fmt.Errorf("decode state: %w; archive corrupt file: %v", err, archiveErr),
			}
		}
		return emptySnapshot(), &CorruptStateError{
			Path:         s.path,
			ArchivedPath: archivedPath,
			Err:          fmt.Errorf("decode state: %w", err),
		}
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
	if snapshot.PendingMessages == nil {
		snapshot.PendingMessages = []PersistedMessageRequest{}
	}
	if snapshot.MessageHistory == nil {
		snapshot.MessageHistory = []PersistedMessageReply{}
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
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := s.rotateBackups(); err != nil {
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
		PendingMessages:  []PersistedMessageRequest{},
		MessageHistory:   []PersistedMessageReply{},
	}
}

func (s *Store) rotateBackups() error {
	if _, err := os.Stat(s.path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	lastBackup := s.backupPath(backupCount)
	if err := os.Remove(lastBackup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for i := backupCount - 1; i >= 1; i-- {
		current := s.backupPath(i)
		next := s.backupPath(i + 1)
		if err := os.Rename(current, next); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return copyFile(s.path, s.backupPath(1))
}

func (s *Store) archiveCorruptFile() (string, error) {
	archivedPath := fmt.Sprintf("%s.corrupt.%d", s.path, time.Now().UTC().UnixNano())
	if err := os.Rename(s.path, archivedPath); err != nil {
		return "", err
	}
	return archivedPath, nil
}

func (s *Store) backupPath(index int) string {
	return fmt.Sprintf("%s.%d", s.path, index)
}

func copyFile(source string, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()

	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer output.Close()

	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	return output.Close()
}
