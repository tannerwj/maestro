package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/tjohnson/maestro/internal/harness"
)

type Adapter struct {
	binary string

	mu        sync.Mutex
	procs     map[string]*claudeRun
	approvals chan harness.ApprovalRequest
}

type claudeRun struct {
	runID          string
	prompt         string
	workdir        string
	env            map[string]string
	stdout         io.Writer
	stderr         io.Writer
	approvalPolicy string

	// Harness config
	model     string
	reasoning string
	extraArgs []string

	ctx    context.Context
	cancel context.CancelFunc

	cmdMu sync.Mutex
	cmd   *exec.Cmd

	decisionCh chan harness.ApprovalDecision
	doneCh     chan error
}

type activeRun struct {
	runID string
	wait  func() error
}

type streamEvent struct {
	Type              string `json:"type"`
	Subtype           string `json:"subtype"`
	Result            string `json:"result"`
	PermissionDenials []struct {
		ToolName  string          `json:"tool_name"`
		ToolUseID string          `json:"tool_use_id"`
		ToolInput json.RawMessage `json:"tool_input"`
	} `json:"permission_denials"`
}

func NewAdapter() (*Adapter, error) {
	binary, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("find claude executable: %w", err)
	}

	return &Adapter{
		binary:    binary,
		procs:     map[string]*claudeRun{},
		approvals: make(chan harness.ApprovalRequest, 32),
	}, nil
}

func (a *Adapter) Kind() string {
	return "claude-code"
}

func (a *Adapter) Start(ctx context.Context, cfg harness.RunConfig) (harness.ActiveRun, error) {
	runCtx, cancel := context.WithCancel(ctx)
	run := &claudeRun{
		runID:          cfg.RunID,
		prompt:         cfg.Prompt,
		workdir:        cfg.Workdir,
		env:            cfg.Env,
		stdout:         writerOrDiscard(cfg.Stdout),
		stderr:         writerOrDiscard(cfg.Stderr),
		approvalPolicy: cfg.ApprovalPolicy,
		model:          cfg.Model,
		reasoning:      cfg.Reasoning,
		extraArgs:      cfg.ExtraArgs,
		ctx:            runCtx,
		cancel:         cancel,
		decisionCh:     make(chan harness.ApprovalDecision, 1),
		doneCh:         make(chan error, 1),
	}

	a.mu.Lock()
	a.procs[cfg.RunID] = run
	a.mu.Unlock()

	go run.execute(a.binary, a.approvals)

	return &activeRun{
		runID: cfg.RunID,
		wait: func() error {
			err := <-run.doneCh
			a.mu.Lock()
			delete(a.procs, cfg.RunID)
			a.mu.Unlock()
			return err
		},
	}, nil
}

func (a *Adapter) Stop(ctx context.Context, runID string) error {
	a.mu.Lock()
	run, ok := a.procs[runID]
	a.mu.Unlock()
	if !ok {
		return nil
	}
	run.stop()
	return nil
}

func (a *Adapter) Approvals() <-chan harness.ApprovalRequest {
	return a.approvals
}

func (a *Adapter) Approve(ctx context.Context, decision harness.ApprovalDecision) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, run := range a.procs {
		if err := run.approve(decision); err == nil {
			return nil
		}
	}
	return fmt.Errorf("approval request %q not found", decision.RequestID)
}

func (a *Adapter) Messages() <-chan harness.MessageRequest {
	return nil
}

func (a *Adapter) Reply(ctx context.Context, reply harness.MessageReply) error {
	return harness.ErrMessagesUnsupported
}

func (r *activeRun) RunID() string {
	return r.runID
}

func (r *activeRun) Wait() error {
	return r.wait()
}

func (r *claudeRun) execute(binary string, approvals chan<- harness.ApprovalRequest) {
	switch r.approvalPolicy {
	case "manual", "destructive-only":
		request, err := r.runDetection(binary)
		if err != nil {
			r.finish(err)
			return
		}
		if request == nil {
			r.finish(nil)
			return
		}

		select {
		case approvals <- *request:
		case <-r.ctx.Done():
			r.finish(r.ctx.Err())
			return
		}

		var decision harness.ApprovalDecision
		select {
		case decision = <-r.decisionCh:
		case <-r.ctx.Done():
			r.finish(r.ctx.Err())
			return
		}
		if decision.Decision != "approve" {
			r.finish(fmt.Errorf("approval rejected"))
			return
		}

		r.finish(r.runPermissive(binary, "bypassPermissions"))
	default:
		r.finish(r.runPermissive(binary, "bypassPermissions"))
	}
}

func (r *claudeRun) runDetection(binary string) (*harness.ApprovalRequest, error) {
	args := []string{
		"--print",
		"--verbose",
		"--output-format", "stream-json",
		"--permission-mode", "default",
		"--add-dir", r.workdir,
	}
	if r.model != "" {
		args = append(args, "--model", r.model)
	}
	if r.reasoning != "" {
		args = append(args, "--config", "model_reasoning_effort="+r.reasoning)
	}
	args = append(args, r.extraArgs...)
	cmd := exec.CommandContext(r.ctx, binary, args...)
	cmd.Dir = r.workdir
	cmd.Stdin = strings.NewReader(r.prompt)
	cmd.Env = harness.MergeEnv(r.env)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	r.setCmd(cmd)
	defer r.clearCmd(cmd)

	go func() {
		_, _ = io.Copy(r.stderr, stderr)
	}()

	var request *harness.ApprovalRequest
	var resultText string
	decoder := json.NewDecoder(stdout)
	for {
		var event streamEvent
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, err
		}
		if event.Type == "result" {
			if len(event.PermissionDenials) > 0 {
				request = buildApprovalRequest(r.runID, r.approvalPolicy, event.PermissionDenials[0])
			}
			resultText = event.Result
		}
	}

	waitErr := cmd.Wait()
	if request != nil {
		return request, nil
	}
	if waitErr != nil {
		return nil, waitErr
	}
	if strings.TrimSpace(resultText) != "" {
		_, _ = io.WriteString(r.stdout, resultText)
	}
	return nil, nil
}

func (r *claudeRun) runPermissive(binary string, permissionMode string) error {
	args := []string{
		"--print",
		"--verbose",
		"--output-format", "stream-json",
		"--permission-mode", permissionMode,
		"--add-dir", r.workdir,
	}
	if r.model != "" {
		args = append(args, "--model", r.model)
	}
	if r.reasoning != "" {
		args = append(args, "--config", "model_reasoning_effort="+r.reasoning)
	}
	args = append(args, r.extraArgs...)
	cmd := exec.CommandContext(r.ctx, binary, args...)
	cmd.Dir = r.workdir
	cmd.Stdin = strings.NewReader(r.prompt)
	cmd.Env = harness.MergeEnv(r.env)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}
	r.setCmd(cmd)
	defer r.clearCmd(cmd)

	go func() {
		_, _ = io.Copy(r.stderr, stderr)
	}()

	decoder := json.NewDecoder(stdout)
	for {
		var event streamEvent
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
		r.writeStreamEvent(event)
	}

	return cmd.Wait()
}

func (r *claudeRun) approve(decision harness.ApprovalDecision) error {
	select {
	case r.decisionCh <- decision:
		return nil
	default:
		return fmt.Errorf("approval request %q not pending", decision.RequestID)
	}
}

func (r *claudeRun) stop() {
	r.cancel()

	r.cmdMu.Lock()
	defer r.cmdMu.Unlock()
	if r.cmd == nil || r.cmd.Process == nil {
		return
	}
	if err := r.cmd.Process.Signal(os.Interrupt); err == nil {
		return
	}
	_ = r.cmd.Process.Kill()
}

func (r *claudeRun) finish(err error) {
	select {
	case r.doneCh <- err:
	default:
	}
}

func (r *claudeRun) setCmd(cmd *exec.Cmd) {
	r.cmdMu.Lock()
	r.cmd = cmd
	r.cmdMu.Unlock()
}

func (r *claudeRun) clearCmd(cmd *exec.Cmd) {
	r.cmdMu.Lock()
	if r.cmd == cmd {
		r.cmd = nil
	}
	r.cmdMu.Unlock()
}

func buildApprovalRequest(runID string, approvalPolicy string, denial struct {
	ToolName  string          `json:"tool_name"`
	ToolUseID string          `json:"tool_use_id"`
	ToolInput json.RawMessage `json:"tool_input"`
}) *harness.ApprovalRequest {
	toolName := strings.TrimSpace(denial.ToolName)
	if toolName == "" {
		toolName = "unknown"
	}
	return &harness.ApprovalRequest{
		RequestID:      fmt.Sprintf("%s:%s", runID, denial.ToolUseID),
		RunID:          runID,
		ToolName:       toolName,
		ToolInput:      strings.TrimSpace(string(denial.ToolInput)),
		ApprovalPolicy: approvalPolicy,
		RequestedAt:    time.Now(),
	}
}

func writerOrDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

func (r *claudeRun) writeStreamEvent(event streamEvent) {
	if strings.TrimSpace(event.Result) != "" {
		_, _ = io.WriteString(r.stdout, event.Result)
		if !strings.HasSuffix(event.Result, "\n") {
			_, _ = io.WriteString(r.stdout, "\n")
		}
		return
	}

	switch event.Type {
	case "assistant":
		_, _ = io.WriteString(r.stdout, "[claude assistant event]\n")
	case "tool_use":
		_, _ = io.WriteString(r.stdout, "[claude tool use]\n")
	case "tool_result":
		_, _ = io.WriteString(r.stdout, "[claude tool result]\n")
	}
}
