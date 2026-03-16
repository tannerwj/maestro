package claude

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjohnson/maestro/internal/harness"
	"github.com/tjohnson/maestro/internal/testutil"
)

func TestLiveClaudeHarness(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_CLAUDE")
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("skipping live test; claude binary not found: %v", err)
	}

	adapter, err := NewAdapter()
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var stdout strings.Builder
	run, err := adapter.Start(ctx, harness.RunConfig{
		RunID:   "live-run",
		Prompt:  "Reply with exactly: MAESTRO_CLAUDE_SMOKE_OK",
		Workdir: t.TempDir(),
		Stdout:  &stdout,
	})
	if err != nil {
		t.Fatalf("start harness: %v", err)
	}
	if err := run.Wait(); err != nil {
		t.Fatalf("wait harness: %v", err)
	}

	if !strings.Contains(stdout.String(), "MAESTRO_CLAUDE_SMOKE_OK") {
		t.Fatalf("unexpected live claude output: %q", stdout.String())
	}
}

func TestLiveClaudeManualApproval(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_CLAUDE")
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("skipping live test; claude binary not found: %v", err)
	}

	adapter, err := NewAdapter()
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	workdir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var stdout strings.Builder
	run, err := adapter.Start(ctx, harness.RunConfig{
		RunID:          "live-claude-approval-run",
		Prompt:         "Create a file named APPROVAL_OK.txt containing exactly MAESTRO_CLAUDE_APPROVAL_OK and then reply with exactly MAESTRO_CLAUDE_APPROVAL_OK.",
		Workdir:        workdir,
		ApprovalPolicy: "manual",
		Stdout:         &stdout,
	})
	if err != nil {
		t.Fatalf("start harness: %v", err)
	}

	var approval harness.ApprovalRequest
	select {
	case approval = <-adapter.Approvals():
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for claude approval request")
	}
	if approval.ToolName != "Write" {
		t.Fatalf("tool name = %q, want Write", approval.ToolName)
	}
	if err := adapter.Approve(ctx, harness.ApprovalDecision{
		RequestID: approval.RequestID,
		Decision:  "approve",
	}); err != nil {
		t.Fatalf("approve request: %v", err)
	}

	if err := run.Wait(); err != nil {
		t.Fatalf("wait harness: %v", err)
	}
	if !strings.Contains(stdout.String(), "MAESTRO_CLAUDE_APPROVAL_OK") {
		t.Fatalf("unexpected live claude output: %q", stdout.String())
	}
	content, err := os.ReadFile(filepath.Join(workdir, "APPROVAL_OK.txt"))
	if err != nil {
		t.Fatalf("read approval file: %v", err)
	}
	if strings.TrimSpace(string(content)) != "MAESTRO_CLAUDE_APPROVAL_OK" {
		t.Fatalf("approval file content = %q", string(content))
	}
}
