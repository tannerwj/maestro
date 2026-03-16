package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjohnson/maestro/internal/harness"
)

func TestAdapterStartAndWaitWithStubBinary(t *testing.T) {
	tmp := t.TempDir()
	binary := filepath.Join(tmp, "claude")
	argsPath := filepath.Join(tmp, "args.txt")
	script := "#!/bin/sh\nprintf '%s\n' \"$@\" > \"" + argsPath + "\"\nprintf 'prompt:'\ncat\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub claude: %v", err)
	}

	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))

	adapter, err := NewAdapter()
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	var stdout strings.Builder
	run, err := adapter.Start(context.Background(), harness.RunConfig{
		RunID:   "run-1",
		Prompt:  "hello world\n",
		Workdir: tmp,
		Stdout:  &stdout,
	})
	if err != nil {
		t.Fatalf("start harness: %v", err)
	}

	if err := run.Wait(); err != nil {
		t.Fatalf("wait harness: %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "hello world") {
		t.Fatalf("stdout = %q", got)
	}

	rawArgs, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	args := string(rawArgs)
	if !strings.Contains(args, "--permission-mode\nbypassPermissions") {
		t.Fatalf("args = %q, want bypassPermissions", args)
	}
	if !strings.Contains(args, "--add-dir\n"+tmp) {
		t.Fatalf("args = %q, want add-dir %s", args, tmp)
	}
}

func TestAdapterApprovalFlowWithStubBinary(t *testing.T) {
	tmp := t.TempDir()
	binary := filepath.Join(tmp, "claude")
	invocations := filepath.Join(tmp, "invocations.txt")
	script := `#!/bin/sh
printf '%s\n' "$@" >> "` + invocations + `"
printf '%s\n' "---" >> "` + invocations + `"
case "$*" in
  *"--output-format stream-json --permission-mode default"*)
    cat >/dev/null
    printf '%s\n' '{"type":"system","subtype":"init"}'
    printf '%s\n' '{"type":"result","subtype":"success","result":"approval pending","permission_denials":[{"tool_name":"Write","tool_use_id":"tool-1","tool_input":{"file_path":"` + tmp + `/APPROVAL.txt","content":"APPROVED"}}]}'
    ;;
  *"--permission-mode acceptEdits"*)
    cat >/dev/null
    printf 'APPROVED'
    printf '%s' 'APPROVED' > "` + tmp + `/APPROVAL.txt"
    ;;
  *)
    echo "unexpected args: $*" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub claude: %v", err)
	}

	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))

	adapter, err := NewAdapter()
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	var stdout strings.Builder
	run, err := adapter.Start(context.Background(), harness.RunConfig{
		RunID:          "run-approval",
		Prompt:         "please edit",
		Workdir:        tmp,
		ApprovalPolicy: "manual",
		Stdout:         &stdout,
	})
	if err != nil {
		t.Fatalf("start harness: %v", err)
	}

	select {
	case request := <-adapter.Approvals():
		if request.ToolName != "Write" {
			t.Fatalf("tool name = %q, want Write", request.ToolName)
		}
		if !strings.Contains(request.ToolInput, "APPROVAL.txt") {
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
	if got := stdout.String(); !strings.Contains(got, "APPROVED") {
		t.Fatalf("stdout = %q", got)
	}

	content, err := os.ReadFile(filepath.Join(tmp, "APPROVAL.txt"))
	if err != nil {
		t.Fatalf("read approval file: %v", err)
	}
	if strings.TrimSpace(string(content)) != "APPROVED" {
		t.Fatalf("approval file content = %q", string(content))
	}

	rawInvocations, err := os.ReadFile(invocations)
	if err != nil {
		t.Fatalf("read invocations: %v", err)
	}
	log := string(rawInvocations)
	if !strings.Contains(log, "--output-format\nstream-json") {
		t.Fatalf("invocations = %q, want stream-json detection pass", log)
	}
	if !strings.Contains(log, "--permission-mode\nacceptEdits") {
		t.Fatalf("invocations = %q, want acceptEdits rerun", log)
	}
}
