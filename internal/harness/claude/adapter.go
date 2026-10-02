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

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/harness"
)

type Adapter struct {
	binary string
	runner harness.ProcessRunner

	mu        sync.Mutex
	procs     map[string]*claudeRun
	approvals chan harness.ApprovalRequest

	supportMu            sync.Mutex
	resumeSupportChecked bool
	resumeSupported      bool
}

type claudeRun struct {
	runID          string
	prompt         string
	hostWorkdir    string
	workdir        string
	lineageKey     string
	env            map[string]string
	stdout         io.Writer
	stderr         io.Writer
	approvalPolicy string

	// Harness config
	model            string
	reasoning        string
	maxTurns         int
	extraArgs        []string
	continuationFunc func(ctx context.Context, turnNumber int) (prompt string, cont bool, err error)
	executionMetaFn  func(harness.ExecutionMetadata)

	ctx    context.Context
	cancel context.CancelFunc

	cmdMu sync.Mutex
	cmd   *exec.Cmd

	sessionID       string
	metrics         domain.RunMetrics
	metricsCallback func(domain.RunMetrics)
	turnCallback    func(int, int)
	decisionCh      chan harness.ApprovalDecision
	doneCh          chan error
}

type activeRun struct {
	runID string
	wait  func() error
}

type turnOutcome struct {
	sessionID string
	approval  *harness.ApprovalRequest
}

type streamEvent struct {
	Type              string         `json:"type"`
	Subtype           string         `json:"subtype"`
	Result            string         `json:"result"`
	SessionID         string         `json:"session_id"`
	DurationMS        *int64         `json:"duration_ms,omitempty"`
	TotalCostUSD      *float64       `json:"total_cost_usd,omitempty"`
	Usage             *claudeUsage   `json:"usage,omitempty"`
	ModelUsage        claudeModelMap `json:"modelUsage,omitempty"`
	Message           *streamMessage `json:"message,omitempty"`
	PermissionDenials []struct {
		ToolName  string          `json:"tool_name"`
		ToolUseID string          `json:"tool_use_id"`
		ToolInput json.RawMessage `json:"tool_input"`
	} `json:"permission_denials"`
}

type streamMessage struct {
	Content []streamContent `json:"content"`
	Usage   *claudeUsage    `json:"usage,omitempty"`
}

type streamContent struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type claudeModelMap map[string]claudeModelUsage

type claudeModelUsage struct {
	InputTokens              *int64   `json:"inputTokens,omitempty"`
	OutputTokens             *int64   `json:"outputTokens,omitempty"`
	CostUSD                  *float64 `json:"costUSD,omitempty"`
	CacheReadInputTokens     *int64   `json:"cacheReadInputTokens,omitempty"`
	CacheCreationInputTokens *int64   `json:"cacheCreationInputTokens,omitempty"`
}

type claudeUsage struct {
	InputTokens  *int64 `json:"input_tokens,omitempty"`
	OutputTokens *int64 `json:"output_tokens,omitempty"`
}

type Option func(*adapterOptions)

type adapterOptions struct {
	runner harness.ProcessRunner
}

func WithProcessRunner(runner harness.ProcessRunner) Option {
	return func(opts *adapterOptions) {
		opts.runner = runner
	}
}

func NewAdapter(options ...Option) (*Adapter, error) {
	opts := adapterOptions{}
	for _, option := range options {
		option(&opts)
	}
	if opts.runner == nil {
		runner, err := harness.NewProcessRunner(nil)
		if err != nil {
			return nil, err
		}
		opts.runner = runner
	}

	binary, err := opts.runner.ResolveBinary("claude")
	if err != nil {
		return nil, err
	}

	return &Adapter{
		binary:    binary,
		runner:    opts.runner,
		procs:     map[string]*claudeRun{},
		approvals: make(chan harness.ApprovalRequest, 32),
	}, nil
}

func (a *Adapter) Kind() string {
	return "claude-code"
}

func (a *Adapter) Start(ctx context.Context, cfg harness.RunConfig) (harness.ActiveRun, error) {
	if cfg.MaxTurns > 1 && cfg.ContinuationFunc != nil {
		supported, err := a.supportsResume()
		if err != nil {
			return nil, fmt.Errorf("detect claude resume support: %w", err)
		}
		if !supported {
			return nil, fmt.Errorf("claude CLI does not support multi-turn session resume")
		}
	}

	maxTurns := cfg.MaxTurns
	if maxTurns < 1 {
		maxTurns = 1
	}

	runCtx, cancel := context.WithCancel(ctx)
	run := &claudeRun{
		runID:            cfg.RunID,
		prompt:           cfg.Prompt,
		hostWorkdir:      cfg.Workdir,
		workdir:          a.runner.VisibleWorkdir(cfg.Workdir),
		lineageKey:       cfg.LineageKey,
		env:              cfg.Env,
		stdout:           harness.WriterOrDiscard(cfg.Stdout),
		stderr:           harness.WriterOrDiscard(cfg.Stderr),
		approvalPolicy:   cfg.ApprovalPolicy,
		model:            cfg.Model,
		reasoning:        cfg.Reasoning,
		maxTurns:         maxTurns,
		extraArgs:        cfg.ExtraArgs,
		continuationFunc: cfg.ContinuationFunc,
		executionMetaFn:  cfg.ExecutionMetadataCallback,
		ctx:              runCtx,
		cancel:           cancel,
		metricsCallback:  cfg.MetricsCallback,
		turnCallback:     cfg.TurnCallback,
		decisionCh:       make(chan harness.ApprovalDecision, 1),
		doneCh:           make(chan error, 1),
	}

	a.mu.Lock()
	a.procs[cfg.RunID] = run
	a.mu.Unlock()

	go run.execute(a.binary, a.runner, a.approvals)

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

func (r *claudeRun) execute(binary string, runner harness.ProcessRunner, approvals chan<- harness.ApprovalRequest) {
	currentTurn := 1
	r.publishTurn(currentTurn)
	prompt := r.prompt
	for {
		sessionID, err := r.runTurn(binary, runner, prompt, r.sessionID, approvals)
		if err != nil {
			r.finish(err)
			return
		}
		if sessionID != "" {
			r.sessionID = sessionID
		}
		if r.maxTurns <= 1 || currentTurn >= r.maxTurns || r.continuationFunc == nil {
			r.finish(nil)
			return
		}

		nextPrompt, cont, err := r.continuationFunc(r.ctx, currentTurn)
		if err != nil {
			r.finish(fmt.Errorf("continuation check: %w", err))
			return
		}
		if !cont {
			r.finish(nil)
			return
		}
		if strings.TrimSpace(r.sessionID) == "" {
			r.finish(fmt.Errorf("claude continuation requires session_id from prior turn"))
			return
		}

		prompt = nextPrompt
		currentTurn++
		r.publishTurn(currentTurn)
	}
}

func (r *claudeRun) publishTurn(currentTurn int) {
	if r.turnCallback == nil {
		return
	}
	r.turnCallback(currentTurn, r.maxTurns)
}

func (a *Adapter) supportsResume() (bool, error) {
	a.supportMu.Lock()
	if a.resumeSupportChecked {
		supported := a.resumeSupported
		a.supportMu.Unlock()
		return supported, nil
	}
	a.supportMu.Unlock()

	cmd, err := a.runner.CommandContext(context.Background(), harness.ProcessSpec{
		Binary: a.binary,
		Args:   []string{"--help"},
	})
	if err != nil {
		return false, err
	}
	out, err := cmd.CombinedOutput()
	supported := strings.Contains(string(out), "--resume")
	if err != nil && !supported {
		return false, err
	}

	a.supportMu.Lock()
	a.resumeSupportChecked = true
	a.resumeSupported = supported
	a.supportMu.Unlock()
	return supported, nil
}

func (r *claudeRun) runTurn(binary string, runner harness.ProcessRunner, prompt string, resumeSessionID string, approvals chan<- harness.ApprovalRequest) (string, error) {
	switch r.approvalPolicy {
	case "manual":
		return r.runManualTurn(binary, runner, prompt, resumeSessionID, approvals)
	default:
		outcome, err := r.runCommand(binary, runner, prompt, resumeSessionID, "bypassPermissions", false)
		return outcome.sessionID, err
	}
}

func (r *claudeRun) runManualTurn(binary string, runner harness.ProcessRunner, prompt string, resumeSessionID string, approvals chan<- harness.ApprovalRequest) (string, error) {
	outcome, err := r.runCommand(binary, runner, prompt, resumeSessionID, "default", true)
	if err != nil {
		return outcome.sessionID, err
	}
	if outcome.approval == nil {
		return outcome.sessionID, nil
	}

	select {
	case approvals <- *outcome.approval:
	case <-r.ctx.Done():
		return outcome.sessionID, r.ctx.Err()
	}

	var decision harness.ApprovalDecision
	select {
	case decision = <-r.decisionCh:
	case <-r.ctx.Done():
		return outcome.sessionID, r.ctx.Err()
	}
	if decision.Decision != harness.DecisionApprove {
		return outcome.sessionID, fmt.Errorf("approval rejected")
	}

	outcome, err = r.runCommand(binary, runner, prompt, resumeSessionID, "bypassPermissions", false)
	return outcome.sessionID, err
}

func (r *claudeRun) runCommand(binary string, runner harness.ProcessRunner, prompt string, resumeSessionID string, permissionMode string, detectApproval bool) (turnOutcome, error) {
	outcome := turnOutcome{}
	args := []string{
		"--print",
		"--verbose",
		"--output-format", "stream-json",
		"--permission-mode", permissionMode,
	}
	if strings.TrimSpace(resumeSessionID) != "" {
		args = append(args, "--resume", resumeSessionID)
	}
	args = append(args, "--add-dir", r.workdir)
	if r.model != "" {
		args = append(args, "--model", r.model)
	}
	if r.reasoning != "" {
		args = append(args, "--effort", r.reasoning)
	}
	args = append(args, r.extraArgs...)
	lifecycle := &harness.ProcessLifecycle{}
	cmd, err := runner.CommandContext(r.ctx, harness.ProcessSpec{
		RunID:      r.runID,
		LineageKey: r.lineageKey,
		Binary:     binary,
		Args:       args,
		Workdir:    r.hostWorkdir,
		Env:        r.env,
		Lifecycle:  lifecycle,
	})
	if err != nil {
		return outcome, err
	}
	if r.executionMetaFn != nil {
		r.executionMetaFn(lifecycle.Metadata)
	}
	cmd.Stdin = strings.NewReader(prompt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return outcome, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return outcome, err
	}
	if err := cmd.Start(); err != nil {
		if lifecycle.Release != nil {
			_ = lifecycle.Release(context.Background(), err)
		}
		return outcome, err
	}
	r.setCmd(cmd)
	defer r.clearCmd(cmd)
	defer func() {
		if lifecycle.Release != nil {
			_ = lifecycle.Release(context.Background(), nil)
		}
	}()

	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(r.stderr, stderr)
		close(stderrDone)
	}()

	var resultText string
	decoder := json.NewDecoder(stdout)
	for {
		var event streamEvent
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			_ = cmd.Process.Kill()
			<-stderrDone
			_ = cmd.Wait()
			return outcome, err
		}
		if strings.TrimSpace(event.SessionID) != "" {
			outcome.sessionID = event.SessionID
		}
		if event.Type == "result" {
			if detectApproval && len(event.PermissionDenials) > 0 {
				outcome.approval = buildApprovalRequest(r.runID, r.approvalPolicy, event.PermissionDenials[0])
			}
			r.publishMetrics(claudeMetricsFromResultEvent(event))
			resultText = event.Result
			if detectApproval {
				continue
			}
		}
		r.writeStreamEvent(event)
	}

	<-stderrDone
	waitErr := cmd.Wait()
	if outcome.approval != nil {
		return outcome, nil
	}
	if waitErr != nil {
		return outcome, waitErr
	}
	if detectApproval && strings.TrimSpace(resultText) != "" {
		_, _ = io.WriteString(r.stdout, resultText)
	}
	return outcome, nil
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

func (r *claudeRun) publishMetrics(metrics domain.RunMetrics) {
	if r.metricsCallback == nil {
		return
	}
	if metrics.TokensIn == nil && metrics.TokensOut == nil && metrics.TotalTokens == nil && metrics.CostUSD == nil && metrics.DurationMS == nil {
		return
	}
	r.metrics = accumulateRunMetrics(r.metrics, metrics)
	r.metricsCallback(r.metrics)
}

func claudeMetricsFromResultEvent(event streamEvent) domain.RunMetrics {
	metrics := domain.RunMetrics{UpdatedAt: time.Now()}
	var tokensIn int64
	var tokensOut int64
	var tokensSeen bool
	for _, usage := range event.ModelUsage {
		if usage.InputTokens != nil {
			tokensIn += *usage.InputTokens
			tokensSeen = true
		}
		if usage.OutputTokens != nil {
			tokensOut += *usage.OutputTokens
			tokensSeen = true
		}
	}
	if tokensSeen {
		metrics.TokensIn = &tokensIn
		metrics.TokensOut = &tokensOut
	} else if event.Usage != nil {
		metrics.TokensIn = cloneInt64(event.Usage.InputTokens)
		metrics.TokensOut = cloneInt64(event.Usage.OutputTokens)
	}
	if event.TotalCostUSD != nil {
		metrics.CostUSD = cloneFloat64(event.TotalCostUSD)
	} else {
		var cost float64
		var hasCost bool
		for _, usage := range event.ModelUsage {
			if usage.CostUSD != nil {
				cost += *usage.CostUSD
				hasCost = true
			}
		}
		if hasCost {
			metrics.CostUSD = &cost
		}
	}
	metrics.DurationMS = cloneInt64(event.DurationMS)
	return domain.DeriveRunMetrics(metrics, time.Time{}, time.Time{}, time.Now())
}

func accumulateRunMetrics(base domain.RunMetrics, add domain.RunMetrics) domain.RunMetrics {
	base.TokensIn = sumInt64Ptrs(base.TokensIn, add.TokensIn)
	base.TokensOut = sumInt64Ptrs(base.TokensOut, add.TokensOut)
	base.TotalTokens = sumInt64Ptrs(base.TotalTokens, add.TotalTokens)
	base.DurationMS = sumInt64Ptrs(base.DurationMS, add.DurationMS)
	base.CostUSD = sumFloat64Ptrs(base.CostUSD, add.CostUSD)
	if !add.UpdatedAt.IsZero() {
		base.UpdatedAt = add.UpdatedAt
	}
	return domain.DeriveRunMetrics(base, time.Time{}, time.Time{}, time.Now())
}

func sumInt64Ptrs(current *int64, add *int64) *int64 {
	if add == nil {
		return cloneInt64(current)
	}
	if current == nil {
		return cloneInt64(add)
	}
	total := *current + *add
	return &total
}

func sumFloat64Ptrs(current *float64, add *float64) *float64 {
	if add == nil {
		return cloneFloat64(current)
	}
	if current == nil {
		return cloneFloat64(add)
	}
	total := *current + *add
	return &total
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
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

func (r *claudeRun) writeStreamEvent(event streamEvent) {
	if strings.TrimSpace(event.Result) != "" {
		_, _ = io.WriteString(r.stdout, event.Result)
		if !strings.HasSuffix(event.Result, "\n") {
			_, _ = io.WriteString(r.stdout, "\n")
		}
		return
	}

	if event.Message == nil {
		return
	}

	for _, block := range event.Message.Content {
		switch block.Type {
		case "tool_use":
			_, _ = fmt.Fprintf(r.stdout, "Using %s\n", toolUseSummary(block.Name, block.Input))
		case "text":
			if text := strings.TrimSpace(block.Text); text != "" {
				_, _ = io.WriteString(r.stdout, text)
				if !strings.HasSuffix(text, "\n") {
					_, _ = io.WriteString(r.stdout, "\n")
				}
			}
		}
	}
}

func toolUseSummary(name string, input json.RawMessage) string {
	if strings.HasPrefix(name, "mcp__") {
		return name
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil || len(fields) == 0 {
		return name
	}

	extract := func(key string) string {
		raw, ok := fields[key]
		if !ok {
			return ""
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return ""
		}
		return strings.TrimSpace(value)
	}

	formatters := map[string]func(func(string) string) string{
		"Bash": func(extract func(string) string) string {
			return firstNonEmpty(extract, "description", "command")
		},
		"Glob": func(extract func(string) string) string {
			return formatPatternPathDetail(extract, false)
		},
		"Grep": func(extract func(string) string) string {
			return formatPatternPathDetail(extract, true)
		},
	}

	detail := ""
	if format := formatters[name]; format != nil {
		detail = format(extract)
	} else {
		detail = firstNonEmpty(extract, "description", "file_path", "path", "url", "query", "pattern", "command")
	}

	if detail == "" {
		return name
	}
	return fmt.Sprintf("%s: %s", name, truncate(detail, 200))
}

func firstNonEmpty(extract func(string) string, keys ...string) string {
	for _, key := range keys {
		if value := extract(key); value != "" {
			return value
		}
	}
	return ""
}

func formatPatternPathDetail(extract func(string) string, quotePattern bool) string {
	pattern := extract("pattern")
	path := extract("path")
	if pattern == "" {
		return ""
	}
	if quotePattern {
		pattern = fmt.Sprintf("%q", pattern)
	}
	if path != "" {
		return fmt.Sprintf("%s in %s", pattern, path)
	}
	return pattern
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
