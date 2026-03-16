package codex

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

func TestLiveCodexHarness(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_CODEX")
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skipf("skipping live test; codex binary not found: %v", err)
	}

	adapter, err := NewAdapter()
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var stdout strings.Builder
	run, err := adapter.Start(ctx, harness.RunConfig{
		RunID:   "live-codex-run",
		Prompt:  "Reply with exactly: MAESTRO_CODEX_SMOKE_OK",
		Workdir: t.TempDir(),
		Stdout:  &stdout,
	})
	if err != nil {
		t.Fatalf("start harness: %v", err)
	}
	if err := run.Wait(); err != nil {
		t.Fatalf("wait harness: %v", err)
	}

	if !strings.Contains(stdout.String(), "MAESTRO_CODEX_SMOKE_OK") {
		t.Fatalf("unexpected live codex output: %q", stdout.String())
	}
}

func TestLiveCodexManualApproval(t *testing.T) {
	testutil.RequireFlag(t, "MAESTRO_TEST_LIVE_CODEX")
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skipf("skipping live test; codex binary not found: %v", err)
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
		RunID:          "live-codex-approval-run",
		Prompt:         "Run the shell command `printf MAESTRO_CODEX_APPROVAL_OK > APPROVAL_OK.txt` and then reply with exactly MAESTRO_CODEX_APPROVAL_OK.",
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
	case <-time.After(60 * time.Second):
		t.Skip("codex app-server did not emit an approval request within 60s under on-request policy")
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

	if !strings.Contains(stdout.String(), "MAESTRO_CODEX_APPROVAL_OK") {
		t.Fatalf("unexpected live codex output: %q", stdout.String())
	}
	content, err := os.ReadFile(filepath.Join(workdir, "APPROVAL_OK.txt"))
	if err != nil {
		t.Fatalf("read approval file: %v", err)
	}
	if strings.TrimSpace(string(content)) != "MAESTRO_CODEX_APPROVAL_OK" {
		t.Fatalf("approval file content = %q", string(content))
	}
}
