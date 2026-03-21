package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/harness"
	"github.com/tjohnson/maestro/internal/orchestrator"
	"gopkg.in/yaml.v3"
)

type DemoRuntime struct {
	mu sync.RWMutex

	startedAt       time.Time
	sources         []orchestrator.SourceSummary
	runs            []domain.AgentRun
	runOutputs      map[string]orchestrator.RunOutputView
	retries         []orchestrator.RetryView
	approvals       []orchestrator.ApprovalView
	messages        []orchestrator.MessageView
	approvalHistory []orchestrator.ApprovalHistoryEntry
	messageHistory  []orchestrator.MessageHistoryEntry
	events          []orchestrator.Event
	tick            int
}

func DemoConfig(host string, port int) (*config.Config, error) {
	dir, err := os.MkdirTemp("", "maestro-demo-config.")
	if err != nil {
		return nil, err
	}

	agentRoot := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentRoot, 0o755); err != nil {
		return nil, err
	}

	if err := writeDemoPack(agentRoot, "platform-pack", config.AgentPackConfig{
		Name:           "platform-pack",
		Description:    "Repository-focused backend agent for platform service changes.",
		InstanceName:   "platform-coder",
		Harness:        "claude-code",
		Workspace:      "git-clone",
		Prompt:         "prompt.md",
		ApprovalPolicy: "auto",
		MaxConcurrent:  2,
		Tools:          []string{"go test", "make", "git"},
		Skills:         []string{"api compatibility", "narrow diffs", "verification before handoff"},
		ContextFiles:   []string{"context.md"},
	},
		"You are platform-coder. Deliver the scoped platform change, run the narrowest relevant verification, and summarize the result clearly for the operator.\n",
		"- Prefer Go tests and existing Make targets.\n- Preserve API compatibility unless the issue explicitly says otherwise.\n"); err != nil {
		return nil, err
	}
	if err := writeDemoPack(agentRoot, "growth-pack", config.AgentPackConfig{
		Name:           "growth-pack",
		Description:    "Experiment-focused product agent for GitLab epic child issues.",
		InstanceName:   "growth-coder",
		Harness:        "codex",
		Workspace:      "git-clone",
		Prompt:         "prompt.md",
		ApprovalPolicy: "manual",
		MaxConcurrent:  2,
		Tools:          []string{"npm test", "playwright", "git"},
		Skills:         []string{"responsive polish", "copy refinement", "design QA"},
		ContextFiles:   []string{"context.md"},
	},
		"You are growth-coder. Improve the growth/web experience with minimal UI risk and protect mobile polish while shipping the experiment.\n",
		"- Validate visual changes with the available preview workflow.\n- Keep copy and spacing changes easy to review.\n"); err != nil {
		return nil, err
	}
	if err := writeDemoPack(agentRoot, "triage-pack", config.AgentPackConfig{
		Name:           "triage-pack",
		Description:    "Intake and clustering agent for design and ops issue streams.",
		InstanceName:   "triage",
		Harness:        "claude-code",
		Workspace:      "git-clone",
		Prompt:         "prompt.md",
		ApprovalPolicy: "auto",
		MaxConcurrent:  2,
		Tools:          []string{"tracker search", "notes", "git"},
		Skills:         []string{"report clustering", "operator summaries", "scope triage"},
		ContextFiles:   []string{"context.md"},
	},
		"You are triage. Cluster related reports, extract the operator signal, and recommend the next action without over-committing engineering work.\n",
		"- Prefer short, precise summaries.\n- Separate symptoms, root-cause hints, and recommended next actions.\n"); err != nil {
		return nil, err
	}

	cfg := &config.Config{
		AgentPacksDir: agentRoot,
		User: config.UserConfig{
			Name:           "Demo Operator",
			GitLabUsername: "demo-operator",
			LinearUsername: "demo-operator",
		},
		Defaults: config.DefaultsConfig{
			MaxConcurrentGlobal: 3,
		},
		Sources: []config.SourceConfig{
			{
				Name:         "gitlab-platform",
				DisplayGroup: "GitLab",
				Tags:         []string{"platform", "prod"},
				Tracker:      "gitlab",
				Connection: config.SourceConnection{
					BaseURL:  "https://gitlab.example.com",
					TokenEnv: "GITLAB_TOKEN",
					Project:  "platform/app",
				},
				Filter: config.FilterConfig{
					Labels: []string{"agent:ready"},
				},
				AgentType: "platform-coder",
			},
			{
				Name:         "gitlab-epic-growth",
				DisplayGroup: "GitLab",
				Tags:         []string{"epic", "growth"},
				Tracker:      "gitlab-epic",
				Connection: config.SourceConnection{
					BaseURL:  "https://gitlab.example.com",
					TokenEnv: "GITLAB_TOKEN",
					Group:    "growth",
				},
				Repo: "growth/web",
				EpicFilter: config.FilterConfig{
					IIDs: []int{17},
				},
				IssueFilter: config.FilterConfig{
					Labels: []string{"agent:ready"},
				},
				AgentType: "growth-coder",
			},
			{
				Name:         "linear-design",
				DisplayGroup: "Linear",
				Tags:         []string{"design", "ux"},
				Tracker:      "linear",
				Connection: config.SourceConnection{
					BaseURL:  "https://api.linear.app/graphql",
					TokenEnv: "LINEAR_API_KEY",
					Project:  "Design Ops",
					Team:     "DES",
				},
				Repo: "design/system",
				Filter: config.FilterConfig{
					Labels: []string{"agent:ready"},
				},
				AgentType: "triage",
			},
		},
		AgentTypes: []config.AgentTypeConfig{
			{
				Name:      "platform-coder",
				AgentPack: "platform-pack",
			},
			{
				Name:      "growth-coder",
				AgentPack: "growth-pack",
			},
			{
				Name:      "triage",
				AgentPack: "triage-pack",
			},
		},
		Workspace: config.WorkspaceConfig{
			Root: "/tmp/maestro-demo/workspaces",
		},
		State: config.StateConfig{
			Dir: "/tmp/maestro-demo/state",
		},
		Logging: config.LoggingConfig{
			Level:    "info",
			Dir:      "/tmp/maestro-demo/logs",
			MaxFiles: 10,
		},
		Server: config.ServerConfig{
			Enabled: true,
			Host:    host,
			Port:    port,
		},
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	configPath := dir + "/maestro.yaml"
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		return nil, err
	}
	restoreEnv := setDemoValidationEnv(
		map[string]string{
			"GITLAB_TOKEN":   "demo-gitlab-token",
			"LINEAR_API_KEY": "demo-linear-token",
		},
	)
	defer restoreEnv()

	loaded, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	loaded.Server = cfg.Server
	return loaded, nil
}

func setDemoValidationEnv(values map[string]string) func() {
	original := map[string]*string{}
	for key, value := range values {
		current, ok := os.LookupEnv(key)
		if ok {
			copyValue := current
			original[key] = &copyValue
			continue
		}
		original[key] = nil
		if strings.TrimSpace(value) != "" {
			_ = os.Setenv(key, value)
		}
	}
	return func() {
		for key, value := range original {
			if value == nil {
				_ = os.Unsetenv(key)
				continue
			}
			_ = os.Setenv(key, *value)
		}
	}
}

func writeDemoPack(root string, name string, pack config.AgentPackConfig, promptBody string, contextBody string) error {
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := yaml.Marshal(pack)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), raw, 0o600); err != nil {
		return err
	}
	promptPath := filepath.Join(dir, "prompt.md")
	contextPath := filepath.Join(dir, "context.md")
	if err := os.WriteFile(promptPath, []byte(promptBody), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(contextPath, []byte(contextBody), 0o600); err != nil {
		return err
	}
	return nil
}

func NewDemoRuntime() *DemoRuntime {
	now := time.Now().UTC()

	run1 := domain.AgentRun{
		ID:             "run-platform-42",
		AgentName:      "platform-coder",
		AgentType:      "platform-coder",
		SourceName:     "gitlab-platform",
		HarnessKind:    "claude-code",
		WorkspacePath:  "/tmp/maestro-demo/workspaces/platform_app_42",
		Status:         domain.RunStatusAwaiting,
		Attempt:        1,
		ApprovalPolicy: "auto",
		ApprovalState:  domain.ApprovalStateApproved,
		StartedAt:      now.Add(-14 * time.Minute),
		LastActivityAt: now.Add(-15 * time.Second),
		Issue: domain.Issue{
			ID:          "gl-project-42",
			Identifier:  "platform/app#42",
			TrackerKind: "gitlab",
			SourceName:  "gitlab-platform",
			Title:       "Ship audit log export endpoint",
			Description: "Add paginated export support and keep the existing auth contract.",
			State:       "opened",
			Labels:      []string{"agent:ready", "backend"},
			Assignee:    "demo-operator",
			URL:         "https://gitlab.example.com/platform/app/-/issues/42",
			UpdatedAt:   now.Add(-2 * time.Minute),
		},
	}
	run2 := domain.AgentRun{
		ID:             "run-epic-17",
		AgentName:      "growth-coder",
		AgentType:      "growth-coder",
		SourceName:     "gitlab-epic-growth",
		HarnessKind:    "codex",
		WorkspacePath:  "/tmp/maestro-demo/workspaces/growth_web_17",
		Status:         domain.RunStatusAwaiting,
		Attempt:        2,
		ApprovalPolicy: "manual",
		ApprovalState:  domain.ApprovalStateAwaiting,
		StartedAt:      now.Add(-9 * time.Minute),
		LastActivityAt: now.Add(-90 * time.Second),
		Issue: domain.Issue{
			ID:          "gl-epic-17",
			Identifier:  "growth/web#17",
			TrackerKind: "gitlab-epic",
			SourceName:  "gitlab-epic-growth",
			Title:       "Polish onboarding banner experiment",
			Description: "Tighten copy, improve CTA placement, and preserve mobile spacing.",
			State:       "opened",
			Labels:      []string{"agent:ready", "frontend"},
			Assignee:    "demo-operator",
			URL:         "https://gitlab.example.com/growth/web/-/issues/17",
			UpdatedAt:   now.Add(-3 * time.Minute),
		},
	}
	run3 := domain.AgentRun{
		ID:             "run-linear-96",
		AgentName:      "triage",
		AgentType:      "triage",
		SourceName:     "linear-design",
		HarnessKind:    "claude-code",
		WorkspacePath:  "/tmp/maestro-demo/workspaces/DES-96",
		Status:         domain.RunStatusActive,
		Attempt:        1,
		ApprovalPolicy: "auto",
		ApprovalState:  domain.ApprovalStateApproved,
		StartedAt:      now.Add(-6 * time.Minute),
		LastActivityAt: now.Add(-20 * time.Second),
		Issue: domain.Issue{
			ID:          "linear-DES-96",
			Identifier:  "DES-96",
			TrackerKind: "linear",
			SourceName:  "linear-design",
			Title:       "Triage dark mode nav spacing reports",
			Description: "Collect repro steps, cluster duplicates, and propose next actions.",
			State:       "started",
			Labels:      []string{"agent:ready", "triage"},
			Assignee:    "demo-operator",
			URL:         "https://linear.app/demo/issue/DES-96",
			UpdatedAt:   now.Add(-4 * time.Minute),
		},
	}

	demo := &DemoRuntime{
		startedAt: now,
		sources: []orchestrator.SourceSummary{
			{Name: "gitlab-platform", DisplayGroup: "GitLab", Tags: []string{"platform", "prod"}, Tracker: "gitlab", LastPollAt: now.Add(-12 * time.Second), LastPollCount: 2, ClaimedCount: 1, ActiveRunCount: 1, PendingMessages: 1},
			{Name: "gitlab-epic-growth", DisplayGroup: "GitLab", Tags: []string{"epic", "growth"}, Tracker: "gitlab-epic", LastPollAt: now.Add(-9 * time.Second), LastPollCount: 3, ClaimedCount: 1, ActiveRunCount: 1, PendingApprovals: 1},
			{Name: "linear-design", DisplayGroup: "Linear", Tags: []string{"design", "ux"}, Tracker: "linear", LastPollAt: now.Add(-7 * time.Second), LastPollCount: 1, ClaimedCount: 1, ActiveRunCount: 1, RetryCount: 1},
		},
		runs: []domain.AgentRun{run1, run2, run3},
		runOutputs: map[string]orchestrator.RunOutputView{
			run1.ID: {
				RunID:           run1.ID,
				SourceName:      run1.SourceName,
				IssueIdentifier: run1.Issue.Identifier,
				StdoutTail:      "Workspace prepared\nWaiting for operator guidance before work begins\n",
				UpdatedAt:       now.Add(-15 * time.Second),
			},
			run2.ID: {
				RunID:           run2.ID,
				SourceName:      run2.SourceName,
				IssueIdentifier: run2.Issue.Identifier,
				StdoutTail:      "Prepared diff for onboarding banner refresh\nWaiting for manual approval to write snapshot changes\n",
				StderrTail:      "approval required: write /tmp/maestro-demo/workspaces/growth_web_17/src/banner.tsx\n",
				UpdatedAt:       now.Add(-90 * time.Second),
			},
			run3.ID: {
				RunID:           run3.ID,
				SourceName:      run3.SourceName,
				IssueIdentifier: run3.Issue.Identifier,
				StdoutTail:      "Collected 6 linked reports\nClustered into 2 spacing buckets\nDrafting operator summary\n",
				UpdatedAt:       now.Add(-20 * time.Second),
			},
		},
		retries: []orchestrator.RetryView{
			{
				IssueID:         "linear-DES-88",
				IssueIdentifier: "DES-88",
				SourceName:      "linear-design",
				Attempt:         2,
				DueAt:           now.Add(3 * time.Minute),
				Error:           "tracker writeback failed: upstream timeout",
			},
		},
		approvals: []orchestrator.ApprovalView{
			{
				RequestID:       "approval-growth-17",
				RunID:           run2.ID,
				IssueID:         run2.Issue.ID,
				IssueIdentifier: run2.Issue.Identifier,
				AgentName:       run2.AgentName,
				ToolName:        "write_file",
				ToolInput:       "Update onboarding banner copy and CTA spacing in src/banner.tsx",
				ApprovalPolicy:  "manual",
				RequestedAt:     now.Add(-95 * time.Second),
				Resolvable:      true,
			},
		},
		messages: []orchestrator.MessageView{
			{
				RequestID:       "control-before-work:run-platform-42",
				RunID:           run1.ID,
				IssueID:         run1.Issue.ID,
				IssueIdentifier: run1.Issue.Identifier,
				SourceName:      run1.SourceName,
				AgentName:       run1.AgentName,
				Kind:            "before_work",
				Summary:         "Before work: platform/app#42",
				Body:            "Review platform/app#42 before work begins. Reply with any operator instructions or simply say start.",
				RequestedAt:     now.Add(-45 * time.Second),
				Resolvable:      true,
			},
		},
		approvalHistory: []orchestrator.ApprovalHistoryEntry{
			{
				RequestID:       "approval-platform-41",
				RunID:           "run-platform-41",
				IssueID:         "gl-project-41",
				IssueIdentifier: "platform/app#41",
				AgentName:       "platform-coder",
				ToolName:        "bash",
				ApprovalPolicy:  "destructive-only",
				Decision:        "approve",
				Reason:          "migration cleanup is expected in this run",
				RequestedAt:     now.Add(-32 * time.Minute),
				DecidedAt:       now.Add(-31 * time.Minute),
				Outcome:         "applied",
			},
		},
		events: []orchestrator.Event{
			{Time: now.Add(-2 * time.Minute), Level: "INFO", Source: "gitlab-platform", RunID: run1.ID, Issue: run1.Issue.Identifier, Message: "workspace prepared and branch created"},
			{Time: now.Add(-95 * time.Second), Level: "WARN", Source: "gitlab-epic-growth", RunID: run2.ID, Issue: run2.Issue.Identifier, Message: "manual approval requested for write_file"},
			{Time: now.Add(-50 * time.Second), Level: "INFO", Source: "linear-design", RunID: run3.ID, Issue: run3.Issue.Identifier, Message: "triage summary in progress"},
			{Time: now.Add(-20 * time.Second), Level: "WARN", Source: "linear-design", Issue: "DES-88", Message: "retry scheduled after tracker timeout"},
		},
	}
	return demo
}

func (d *DemoRuntime) Start(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.advance()
			}
		}
	}()
}

func (d *DemoRuntime) Snapshot() orchestrator.Snapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()

	sourceSummaries := append([]orchestrator.SourceSummary(nil), d.sources...)
	runs := append([]domain.AgentRun(nil), d.runs...)
	retries := append([]orchestrator.RetryView(nil), d.retries...)
	approvals := append([]orchestrator.ApprovalView(nil), d.approvals...)
	history := append([]orchestrator.ApprovalHistoryEntry(nil), d.approvalHistory...)
	events := append([]orchestrator.Event(nil), d.events...)

	runOutputs := make([]orchestrator.RunOutputView, 0, len(d.runOutputs))
	for _, output := range d.runOutputs {
		runOutputs = append(runOutputs, output)
	}
	sort.Slice(runOutputs, func(i, j int) bool {
		if runOutputs[i].UpdatedAt.Equal(runOutputs[j].UpdatedAt) {
			return runOutputs[i].RunID < runOutputs[j].RunID
		}
		return runOutputs[i].UpdatedAt.After(runOutputs[j].UpdatedAt)
	})
	sort.Slice(retries, func(i, j int) bool {
		return retries[i].DueAt.Before(retries[j].DueAt)
	})

	return orchestrator.Snapshot{
		SourceName:       "demo",
		SourceTracker:    "mixed",
		LastPollAt:       latestPollAt(sourceSummaries),
		LastPollCount:    len(sourceSummaries),
		ClaimedCount:     len(runs),
		RetryCount:       len(retries),
		PendingApprovals: approvals,
		PendingMessages:  append([]orchestrator.MessageView(nil), d.messages...),
		Retries:          retries,
		ApprovalHistory:  history,
		MessageHistory:   append([]orchestrator.MessageHistoryEntry(nil), d.messageHistory...),
		ActiveRuns:       runs,
		RunOutputs:       runOutputs,
		SourceSummaries:  sourceSummaries,
		RecentEvents:     events,
	}
}

func (d *DemoRuntime) ResolveApproval(requestID string, decision string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	index := -1
	var approval orchestrator.ApprovalView
	for i, candidate := range d.approvals {
		if candidate.RequestID == requestID {
			index = i
			approval = candidate
			break
		}
	}
	if index == -1 {
		return fmt.Errorf("approval %q not found", requestID)
	}

	now := time.Now().UTC()
	d.approvals = append(d.approvals[:index], d.approvals[index+1:]...)
	outcome := "rejected"
	if decision == harness.DecisionApprove {
		outcome = "applied"
	}
	d.approvalHistory = append([]orchestrator.ApprovalHistoryEntry{{
		RequestID:       approval.RequestID,
		RunID:           approval.RunID,
		IssueID:         approval.IssueID,
		IssueIdentifier: approval.IssueIdentifier,
		AgentName:       approval.AgentName,
		ToolName:        approval.ToolName,
		ApprovalPolicy:  approval.ApprovalPolicy,
		Decision:        decision,
		RequestedAt:     approval.RequestedAt,
		DecidedAt:       now,
		Outcome:         outcome,
	}}, d.approvalHistory...)
	if len(d.approvalHistory) > 10 {
		d.approvalHistory = d.approvalHistory[:10]
	}

	for i := range d.runs {
		if d.runs[i].ID != approval.RunID {
			continue
		}
		d.runs[i].LastActivityAt = now
		switch decision {
		case harness.DecisionApprove:
			d.runs[i].Status = domain.RunStatusActive
			d.runs[i].ApprovalState = domain.ApprovalStateApproved
			d.appendOutputLocked(d.runs[i].ID, "Approval granted, applying banner changes\n")
			d.appendEventLocked(orchestrator.Event{Time: now, Level: "INFO", Source: d.runs[i].SourceName, RunID: d.runs[i].ID, Issue: d.runs[i].Issue.Identifier, Message: "manual approval granted"})
		default:
			d.runs[i].Status = domain.RunStatusFailed
			d.runs[i].ApprovalState = domain.ApprovalStateRejected
			d.runs[i].CompletedAt = now
			d.runs[i].Error = "operator rejected approval request"
			d.appendOutputLocked(d.runs[i].ID, "Approval rejected, stopping run\n")
			d.appendEventLocked(orchestrator.Event{Time: now, Level: "ERROR", Source: d.runs[i].SourceName, RunID: d.runs[i].ID, Issue: d.runs[i].Issue.Identifier, Message: "manual approval rejected"})
		}
		break
	}
	d.refreshSourceCountsLocked()
	return nil
}

func (d *DemoRuntime) ResolveMessage(requestID string, reply string, resolvedVia string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	index := -1
	var request orchestrator.MessageView
	for i, candidate := range d.messages {
		if candidate.RequestID == requestID {
			index = i
			request = candidate
			break
		}
	}
	if index == -1 {
		return fmt.Errorf("message %q not found", requestID)
	}

	now := time.Now().UTC()
	d.messages = append(d.messages[:index], d.messages[index+1:]...)
	d.messageHistory = append([]orchestrator.MessageHistoryEntry{{
		RequestID:       request.RequestID,
		RunID:           request.RunID,
		IssueID:         request.IssueID,
		IssueIdentifier: request.IssueIdentifier,
		SourceName:      request.SourceName,
		AgentName:       request.AgentName,
		Kind:            request.Kind,
		Summary:         request.Summary,
		Body:            request.Body,
		Reply:           reply,
		ResolvedVia:     resolvedVia,
		RequestedAt:     request.RequestedAt,
		RepliedAt:       now,
		Outcome:         "resolved",
	}}, d.messageHistory...)
	if len(d.messageHistory) > 10 {
		d.messageHistory = d.messageHistory[:10]
	}
	for i := range d.runs {
		if d.runs[i].ID != request.RunID {
			continue
		}
		d.runs[i].Status = domain.RunStatusActive
		d.runs[i].LastActivityAt = now
		d.appendOutputLocked(d.runs[i].ID, fmt.Sprintf("Operator guidance received: %s\n", reply))
		break
	}
	d.appendEventLocked(orchestrator.Event{Time: now, Level: "INFO", Source: sourceNameForRun(d.runs, request.RunID), RunID: request.RunID, Issue: request.IssueIdentifier, Message: "operator message reply received"})
	d.refreshSourceCountsLocked()
	return nil
}

func (d *DemoRuntime) StopRun(runID string, reason string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	for i := range d.runs {
		if d.runs[i].ID != runID {
			continue
		}
		d.runs[i].Status = domain.RunStatusFailed
		d.runs[i].LastActivityAt = time.Now().UTC()
		d.appendEventLocked(orchestrator.Event{
			Time:    time.Now().UTC(),
			Level:   "WARN",
			Source:  d.runs[i].SourceName,
			RunID:   runID,
			Issue:   d.runs[i].Issue.Identifier,
			Message: fmt.Sprintf("run stopped by operator: %s", reason),
		})
		d.appendOutputLocked(runID, fmt.Sprintf("Run stopped by operator: %s\n", reason))
		return nil
	}
	return fmt.Errorf("run %q not found", runID)
}

func (d *DemoRuntime) advance() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.tick++
	now := time.Now().UTC()

	for i := range d.runs {
		if d.runs[i].Status != domain.RunStatusActive {
			continue
		}
		d.runs[i].LastActivityAt = now
		switch d.runs[i].ID {
		case "run-platform-42":
			d.appendOutputLocked(d.runs[i].ID, fmt.Sprintf("tick %d: synced export job metrics\n", d.tick))
		case "run-linear-96":
			d.appendOutputLocked(d.runs[i].ID, fmt.Sprintf("tick %d: refined grouping notes for nav spacing reports\n", d.tick))
		case "run-epic-17":
			d.appendOutputLocked(d.runs[i].ID, fmt.Sprintf("tick %d: preview build and screenshot review completed\n", d.tick))
		}
	}

	if d.tick%2 == 0 {
		d.appendEventLocked(orchestrator.Event{
			Time:    now,
			Level:   "INFO",
			Source:  "gitlab-platform",
			RunID:   "run-platform-42",
			Issue:   "platform/app#42",
			Message: "captured fresh run output from platform export task",
		})
	}
	if d.tick%3 == 0 && len(d.retries) > 0 {
		d.retries[0].DueAt = now.Add(2 * time.Minute)
		d.appendEventLocked(orchestrator.Event{
			Time:    now,
			Level:   "WARN",
			Source:  d.retries[0].SourceName,
			Issue:   d.retries[0].IssueIdentifier,
			Message: "retry backoff recalculated after transient tracker error",
		})
	}

	for i := range d.sources {
		offset := time.Duration(i+1) * time.Second
		d.sources[i].LastPollAt = now.Add(-offset)
	}
	d.refreshSourceCountsLocked()
}

func (d *DemoRuntime) refreshSourceCountsLocked() {
	activeCounts := map[string]int{}
	approvalCounts := map[string]int{}
	messageCounts := map[string]int{}
	retryCounts := map[string]int{}
	claimedCounts := map[string]int{}

	for _, run := range d.runs {
		switch run.Status {
		case domain.RunStatusActive, domain.RunStatusPreparing, domain.RunStatusAwaiting:
			activeCounts[run.SourceName]++
			claimedCounts[run.SourceName]++
		}
	}
	for _, approval := range d.approvals {
		sourceName := sourceNameForRun(d.runs, approval.RunID)
		if sourceName != "" {
			approvalCounts[sourceName]++
		}
	}
	for _, message := range d.messages {
		sourceName := sourceNameForRun(d.runs, message.RunID)
		if sourceName != "" {
			messageCounts[sourceName]++
		}
	}
	for _, retry := range d.retries {
		retryCounts[retry.SourceName]++
	}
	for i := range d.sources {
		d.sources[i].ActiveRunCount = activeCounts[d.sources[i].Name]
		d.sources[i].ClaimedCount = claimedCounts[d.sources[i].Name]
		d.sources[i].PendingApprovals = approvalCounts[d.sources[i].Name]
		d.sources[i].PendingMessages = messageCounts[d.sources[i].Name]
		d.sources[i].RetryCount = retryCounts[d.sources[i].Name]
	}
}

func (d *DemoRuntime) appendOutputLocked(runID string, line string) {
	output := d.runOutputs[runID]
	output.StdoutTail += line
	lines := trimLines(output.StdoutTail, 8)
	output.StdoutTail = lines
	output.UpdatedAt = time.Now().UTC()
	d.runOutputs[runID] = output
}

func (d *DemoRuntime) appendEventLocked(event orchestrator.Event) {
	d.events = append(d.events, event)
	if len(d.events) > 25 {
		d.events = d.events[len(d.events)-25:]
	}
}

func trimLines(input string, keep int) string {
	lines := make([]string, 0, keep)
	start := 0
	for i := 0; i < len(input); i++ {
		if input[i] != '\n' {
			continue
		}
		lines = append(lines, input[start:i+1])
		start = i + 1
	}
	if start < len(input) {
		lines = append(lines, input[start:])
	}
	if len(lines) <= keep {
		return input
	}
	return concatLines(lines[len(lines)-keep:])
}

func concatLines(lines []string) string {
	out := ""
	for _, line := range lines {
		out += line
	}
	return out
}

func latestPollAt(summaries []orchestrator.SourceSummary) time.Time {
	var latest time.Time
	for _, summary := range summaries {
		if summary.LastPollAt.After(latest) {
			latest = summary.LastPollAt
		}
	}
	return latest
}

func sourceNameForRun(runs []domain.AgentRun, runID string) string {
	for _, run := range runs {
		if run.ID == runID {
			return run.SourceName
		}
	}
	return ""
}
