package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tjohnson/maestro/internal/harness"
)

type Adapter struct {
	binary string

	mu        sync.Mutex
	procs     map[string]*codexRun
	approvals chan harness.ApprovalRequest
}

type codexRun struct {
	runID          string
	approvalPolicy string
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	stdout         io.ReadCloser

	writeMu sync.Mutex

	reqMu   sync.Mutex
	nextID  int64
	pending map[int64]chan rpcResponse

	approvalMu      sync.Mutex
	pendingApproval map[string]pendingApproval

	doneCh    chan error
	processCh chan error
	stopOnce  sync.Once
}

type pendingApproval struct {
	approved rpcEnvelope
	rejected rpcEnvelope
}

type activeRun struct {
	runID string
	wait  func() error
}

type rpcResponse struct {
	Result json.RawMessage
	Error  *rpcError
}

type rpcEnvelope struct {
	ID     *int64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializeResponse struct {
	UserAgent string `json:"userAgent"`
}

type threadStartResponse struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type turnStartResponse struct {
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
}

type turnCompletedNotification struct {
	ThreadID string `json:"threadId"`
	Turn     struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"turn"`
}

type agentMessageDeltaNotification struct {
	Delta string `json:"delta"`
}

func NewAdapter() (*Adapter, error) {
	binary, err := exec.LookPath("codex")
	if err != nil {
		return nil, fmt.Errorf("find codex executable: %w", err)
	}

	return &Adapter{
		binary:    binary,
		procs:     map[string]*codexRun{},
		approvals: make(chan harness.ApprovalRequest, 32),
	}, nil
}

func (a *Adapter) Kind() string {
	return "codex"
}

func (a *Adapter) Start(ctx context.Context, cfg harness.RunConfig) (harness.ActiveRun, error) {
	cmd := exec.CommandContext(ctx, a.binary, "app-server", "--listen", "stdio://")
	cmd.Dir = cfg.Workdir
	cmd.Env = mergeEnv(cfg.Env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	run := &codexRun{
		runID:           cfg.RunID,
		approvalPolicy:  cfg.ApprovalPolicy,
		cmd:             cmd,
		stdin:           stdin,
		stdout:          stdout,
		pending:         map[int64]chan rpcResponse{},
		pendingApproval: map[string]pendingApproval{},
		doneCh:          make(chan error, 1),
		processCh:       make(chan error, 1),
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.procs[cfg.RunID] = run
	a.mu.Unlock()

	go func() {
		_, _ = io.Copy(writerOrDiscard(cfg.Stderr), stderr)
	}()
	go run.readLoop(a.approvals, writerOrDiscard(cfg.Stdout))
	go func() {
		err := cmd.Wait()
		run.processCh <- err
	}()

	if err := run.initialize(); err != nil {
		a.cleanup(cfg.RunID)
		return nil, err
	}
	threadID, err := run.startThread(cfg.Workdir)
	if err != nil {
		a.cleanup(cfg.RunID)
		return nil, err
	}
	if _, err := run.startTurn(threadID, cfg.Workdir, cfg.Prompt); err != nil {
		a.cleanup(cfg.RunID)
		return nil, err
	}

	return &activeRun{
		runID: cfg.RunID,
		wait: func() error {
			err := <-run.doneCh
			run.stop()
			procErr := <-run.processCh
			a.cleanup(cfg.RunID)
			if err != nil {
				return err
			}
			if procErr != nil && !isExpectedStopError(procErr) {
				return procErr
			}
			return nil
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

func (r *activeRun) RunID() string {
	return r.runID
}

func (r *activeRun) Wait() error {
	return r.wait()
}

func (a *Adapter) cleanup(runID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.procs, runID)
}

func (r *codexRun) initialize() error {
	var resp initializeResponse
	if err := r.request("initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "maestro",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}, &resp); err != nil {
		return fmt.Errorf("initialize codex app-server: %w", err)
	}

	if err := r.notify("initialized", nil); err != nil {
		return fmt.Errorf("notify initialized: %w", err)
	}
	return nil
}

func (r *codexRun) startThread(cwd string) (string, error) {
	var resp threadStartResponse
	if err := r.request("thread/start", map[string]any{
		"cwd":            cwd,
		"ephemeral":      true,
		"approvalPolicy": codexApprovalPolicy(r.approvalPolicy),
		"personality":    "pragmatic",
		"sandbox":        codexSandboxMode(r.approvalPolicy),
	}, &resp); err != nil {
		return "", fmt.Errorf("thread/start: %w", err)
	}
	if resp.Thread.ID == "" {
		return "", fmt.Errorf("thread/start returned empty thread id")
	}
	return resp.Thread.ID, nil
}

func (r *codexRun) startTurn(threadID string, cwd string, prompt string) (string, error) {
	var resp turnStartResponse
	if err := r.request("turn/start", map[string]any{
		"threadId":       threadID,
		"cwd":            cwd,
		"approvalPolicy": codexApprovalPolicy(r.approvalPolicy),
		"sandboxPolicy":  codexSandboxPolicy(r.approvalPolicy, cwd),
		"input": []map[string]any{
			{
				"type": "text",
				"text": prompt,
			},
		},
	}, &resp); err != nil {
		return "", fmt.Errorf("turn/start: %w", err)
	}
	if resp.Turn.ID == "" {
		return "", fmt.Errorf("turn/start returned empty turn id")
	}
	return resp.Turn.ID, nil
}

func (r *codexRun) readLoop(approvalCh chan<- harness.ApprovalRequest, stdout io.Writer) {
	decoder := json.NewDecoder(bufio.NewReader(r.stdout))
	for {
		var msg rpcEnvelope
		if err := decoder.Decode(&msg); err != nil {
			if err != io.EOF {
				r.finish(fmt.Errorf("decode codex app-server message: %w", err))
			}
			return
		}

		switch {
		case msg.ID != nil && msg.Method == "":
			r.resolve(*msg.ID, rpcResponse{Result: msg.Result, Error: msg.Error})
		case msg.ID != nil && msg.Method != "":
			r.handleServerRequest(*msg.ID, msg.Method, msg.Params, approvalCh)
		case msg.Method != "":
			r.handleNotification(msg.Method, msg.Params, stdout)
		}
	}
}

func (r *codexRun) handleNotification(method string, params json.RawMessage, stdout io.Writer) {
	switch method {
	case "item/agentMessage/delta":
		var msg agentMessageDeltaNotification
		if err := json.Unmarshal(params, &msg); err == nil && msg.Delta != "" {
			_, _ = io.WriteString(stdout, msg.Delta)
		}
	case "turn/completed":
		var msg turnCompletedNotification
		if err := json.Unmarshal(params, &msg); err != nil {
			r.finish(fmt.Errorf("decode turn/completed: %w", err))
			return
		}
		switch msg.Turn.Status {
		case "completed":
			r.finish(nil)
		case "failed":
			if msg.Turn.Error != nil && msg.Turn.Error.Message != "" {
				r.finish(fmt.Errorf("codex turn failed: %s", msg.Turn.Error.Message))
				return
			}
			r.finish(fmt.Errorf("codex turn failed"))
		case "interrupted":
			r.finish(fmt.Errorf("codex turn interrupted"))
		}
	}
}

func (r *codexRun) handleServerRequest(id int64, method string, params json.RawMessage, approvalCh chan<- harness.ApprovalRequest) {
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval":
		requestID, approved, rejected, approval, err := buildApprovalRequest(r.runID, r.approvalPolicy, id, method, params)
		if err != nil {
			_ = r.write(rpcEnvelope{
				ID: &id,
				Error: &rpcError{
					Code:    -32602,
					Message: err.Error(),
				},
			})
			return
		}

		r.approvalMu.Lock()
		r.pendingApproval[requestID] = pendingApproval{
			approved: approved,
			rejected: rejected,
		}
		r.approvalMu.Unlock()

		approvalCh <- approval
		return
	}
	_ = r.write(rpcEnvelope{
		ID: &id,
		Error: &rpcError{
			Code:    -32601,
			Message: fmt.Sprintf("method %s is unsupported in maestro mvp", method),
		},
	})
}

func (r *codexRun) finish(err error) {
	select {
	case r.doneCh <- err:
	default:
	}
}

func (r *codexRun) approve(decision harness.ApprovalDecision) error {
	r.approvalMu.Lock()
	pending, ok := r.pendingApproval[decision.RequestID]
	if ok {
		delete(r.pendingApproval, decision.RequestID)
	}
	r.approvalMu.Unlock()
	if !ok {
		return fmt.Errorf("approval request %q not found", decision.RequestID)
	}

	if decision.Decision == "approve" {
		return r.write(pending.approved)
	}
	return r.write(pending.rejected)
}

func (r *codexRun) request(method string, params any, out any) error {
	id := atomic.AddInt64(&r.nextID, 1)
	respCh := make(chan rpcResponse, 1)

	r.reqMu.Lock()
	r.pending[id] = respCh
	r.reqMu.Unlock()

	if err := r.write(rpcEnvelope{
		ID:     &id,
		Method: method,
		Params: mustMarshalRaw(params),
	}); err != nil {
		r.reqMu.Lock()
		delete(r.pending, id)
		r.reqMu.Unlock()
		return err
	}

	resp := <-respCh
	if resp.Error != nil {
		return fmt.Errorf("%s", resp.Error.Message)
	}
	if out != nil {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return err
		}
	}
	return nil
}

func (r *codexRun) notify(method string, params any) error {
	msg := rpcEnvelope{Method: method}
	if params != nil {
		msg.Params = mustMarshalRaw(params)
	}
	return r.write(msg)
}

func (r *codexRun) write(msg rpcEnvelope) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	encoder := json.NewEncoder(r.stdin)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(msg)
}

func (r *codexRun) resolve(id int64, resp rpcResponse) {
	r.reqMu.Lock()
	ch, ok := r.pending[id]
	if ok {
		delete(r.pending, id)
	}
	r.reqMu.Unlock()
	if ok {
		ch <- resp
	}
}

func (r *codexRun) stop() {
	r.stopOnce.Do(func() {
		if r.stdin != nil {
			_ = r.stdin.Close()
		}
		if r.cmd != nil && r.cmd.Process != nil {
			if err := r.cmd.Process.Signal(os.Interrupt); err != nil {
				_ = r.cmd.Process.Kill()
			}
		}
	})
}

func mustMarshalRaw(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

func codexApprovalPolicy(policy string) string {
	switch policy {
	case "manual", "destructive-only":
		return "on-request"
	default:
		return "never"
	}
}

func codexSandboxMode(policy string) string {
	switch policy {
	case "manual", "destructive-only":
		return "workspace-write"
	default:
		return "danger-full-access"
	}
}

func codexSandboxPolicy(policy string, cwd string) map[string]any {
	switch policy {
	case "manual", "destructive-only":
		return map[string]any{
			"type":          "workspaceWrite",
			"writableRoots": []string{cwd},
			"networkAccess": false,
		}
	default:
		return map[string]any{
			"type": "dangerFullAccess",
		}
	}
}

func buildApprovalRequest(runID string, approvalPolicy string, rpcID int64, method string, params json.RawMessage) (string, rpcEnvelope, rpcEnvelope, harness.ApprovalRequest, error) {
	requestID := fmt.Sprintf("%s:%d", runID, rpcID)
	approval := harness.ApprovalRequest{
		RequestID:      requestID,
		RunID:          runID,
		ApprovalPolicy: approvalPolicy,
		RequestedAt:    time.Now(),
	}

	switch method {
	case "item/commandExecution/requestApproval":
		var payload struct {
			Command string `json:"command"`
			Reason  string `json:"reason"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return "", rpcEnvelope{}, rpcEnvelope{}, harness.ApprovalRequest{}, err
		}
		approval.ToolName = "command"
		approval.ToolInput = strings.TrimSpace(payload.Command + "\n" + payload.Reason)
		return requestID, rpcEnvelope{
				ID:     &rpcID,
				Result: mustMarshalRaw(map[string]any{"decision": "approved"}),
			}, rpcEnvelope{
				ID:     &rpcID,
				Result: mustMarshalRaw(map[string]any{"decision": "denied"}),
			}, approval, nil
	case "item/fileChange/requestApproval":
		var payload struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return "", rpcEnvelope{}, rpcEnvelope{}, harness.ApprovalRequest{}, err
		}
		approval.ToolName = "file-change"
		approval.ToolInput = payload.Reason
		return requestID, rpcEnvelope{
				ID:     &rpcID,
				Result: mustMarshalRaw(map[string]any{"decision": "accept"}),
			}, rpcEnvelope{
				ID:     &rpcID,
				Result: mustMarshalRaw(map[string]any{"decision": "decline"}),
			}, approval, nil
	case "item/permissions/requestApproval":
		var payload struct {
			Permissions map[string]any `json:"permissions"`
			Reason      string         `json:"reason"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			return "", rpcEnvelope{}, rpcEnvelope{}, harness.ApprovalRequest{}, err
		}
		approval.ToolName = "permissions"
		approval.ToolInput = payload.Reason
		return requestID, rpcEnvelope{
				ID:     &rpcID,
				Result: mustMarshalRaw(map[string]any{"permissions": payload.Permissions, "scope": "turn"}),
			}, rpcEnvelope{
				ID:     &rpcID,
				Result: mustMarshalRaw(map[string]any{"permissions": map[string]any{}, "scope": "turn"}),
			}, approval, nil
	default:
		return "", rpcEnvelope{}, rpcEnvelope{}, harness.ApprovalRequest{}, fmt.Errorf("unsupported approval method %s", method)
	}
}

func writerOrDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

func mergeEnv(extra map[string]string) []string {
	env := append([]string{}, os.Environ()...)
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	return env
}

func isExpectedStopError(err error) bool {
	if err == nil {
		return false
	}
	return err.Error() == "signal: interrupt" || err.Error() == "signal: killed"
}
