package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjohnson/maestro/internal/harness"
)

func TestAdapterStartAndWaitWithStubAppServer(t *testing.T) {
	tmp := t.TempDir()
	binary := filepath.Join(tmp, "codex")
	script := `#!/bin/sh
if [ "$1" = "app-server" ]; then
  while IFS= read -r line; do
    case "$line" in
      *'"method":"initialize"'*)
        printf '%s\n' '{"id":1,"result":{"userAgent":"stub"}}'
        ;;
      *'"method":"initialized"'*)
        ;;
      *'"method":"thread/start"'*)
        printf '%s\n' '{"id":2,"result":{"approvalPolicy":"never","cwd":"/tmp","model":"gpt-5","modelProvider":"openai","sandbox":{"type":"dangerFullAccess"},"thread":{"id":"thread-1","cliVersion":"0.0.0","createdAt":0,"cwd":"/tmp","ephemeral":true,"modelProvider":"openai","preview":"","source":"appServer","status":{"type":"idle"},"turns":[],"updatedAt":0}}}'
        ;;
      *'"method":"turn/start"'*)
        printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-1","items":[],"status":"inProgress"}}}'
        printf '%s\n' '{"method":"item/agentMessage/delta","params":{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1","delta":"CODEX_OK"}}'
        printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","items":[],"status":"completed"}}}'
        exit 0
        ;;
    esac
  done
  exit 0
fi
echo "unexpected args: $@" >&2
exit 1
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub codex: %v", err)
	}

	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))

	adapter, err := NewAdapter()
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	var stdout strings.Builder
	run, err := adapter.Start(context.Background(), harness.RunConfig{
		RunID:   "run-1",
		Prompt:  "hello world",
		Workdir: tmp,
		Stdout:  &stdout,
	})
	if err != nil {
		t.Fatalf("start harness: %v", err)
	}

	if err := run.Wait(); err != nil {
		t.Fatalf("wait harness: %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "CODEX_OK") {
		t.Fatalf("stdout = %q", got)
	}
}

func TestAdapterApprovalFlowWithStubAppServer(t *testing.T) {
	tmp := t.TempDir()
	binary := filepath.Join(tmp, "codex")
	script := `#!/bin/sh
if [ "$1" = "app-server" ]; then
  while IFS= read -r line; do
    case "$line" in
      *'"method":"initialize"'*)
        printf '%s\n' '{"id":1,"result":{"userAgent":"stub"}}'
        ;;
      *'"method":"initialized"'*)
        ;;
      *'"method":"thread/start"'*)
        printf '%s\n' '{"id":2,"result":{"approvalPolicy":"on-request","cwd":"/tmp","model":"gpt-5","modelProvider":"openai","sandbox":{"type":"dangerFullAccess"},"thread":{"id":"thread-1","cliVersion":"0.0.0","createdAt":0,"cwd":"/tmp","ephemeral":true,"modelProvider":"openai","preview":"","source":"appServer","status":{"type":"idle"},"turns":[],"updatedAt":0}}}'
        ;;
      *'"method":"turn/start"'*)
        printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-1","items":[],"status":"inProgress"}}}'
        printf '%s\n' '{"id":40,"method":"item/fileChange/requestApproval","params":{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1","reason":"need to edit PROBE.txt"}}'
        read -r response
        case "$response" in
          *'"decision":"accept"'*)
            printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","items":[],"status":"completed"}}}'
            exit 0
            ;;
          *)
            printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","items":[],"status":"failed","error":{"message":"approval denied"}}}}'
            exit 0
            ;;
        esac
        ;;
    esac
  done
  exit 0
fi
echo "unexpected args: $@" >&2
exit 1
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub codex: %v", err)
	}

	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))

	adapter, err := NewAdapter()
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	run, err := adapter.Start(context.Background(), harness.RunConfig{
		RunID:          "run-approval",
		Prompt:         "edit a file",
		Workdir:        tmp,
		ApprovalPolicy: "manual",
	})
	if err != nil {
		t.Fatalf("start harness: %v", err)
	}

	select {
	case request := <-adapter.Approvals():
		if request.ToolName != "file-change" {
			t.Fatalf("tool name = %q, want file-change", request.ToolName)
		}
		if !strings.Contains(request.ToolInput, "PROBE.txt") {
			t.Fatalf("tool input = %q", request.ToolInput)
		}
		if err := adapter.Approve(context.Background(), harness.ApprovalDecision{
			RequestID: request.RequestID,
			Decision:  "approve",
		}); err != nil {
			t.Fatalf("approve request: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval request")
	}

	if err := run.Wait(); err != nil {
		t.Fatalf("wait harness: %v", err)
	}
}

func TestCodexApprovalPolicyAndSandboxSelection(t *testing.T) {
	if got := codexApprovalPolicy("auto"); got != "never" {
		t.Fatalf("auto approval policy = %q, want never", got)
	}
	if got := codexApprovalPolicy("manual"); got != "on-request" {
		t.Fatalf("manual approval policy = %q, want on-request", got)
	}
	if got := codexSandboxMode("auto"); got != "danger-full-access" {
		t.Fatalf("auto sandbox mode = %q, want danger-full-access", got)
	}
	if got := codexSandboxMode("manual"); got != "workspace-write" {
		t.Fatalf("manual sandbox mode = %q, want workspace-write", got)
	}

	policy := codexSandboxPolicy("manual", "/tmp/work")
	if got := policy["type"]; got != "workspaceWrite" {
		t.Fatalf("manual sandbox policy type = %v, want workspaceWrite", got)
	}
	roots, ok := policy["writableRoots"].([]string)
	if !ok || len(roots) != 1 || roots[0] != "/tmp/work" {
		t.Fatalf("manual writableRoots = %#v", policy["writableRoots"])
	}
}
