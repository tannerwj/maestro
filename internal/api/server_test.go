package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/ops"
	"github.com/tjohnson/maestro/internal/orchestrator"
)

func newLocalRequest(method string, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Host = "127.0.0.1:8742"
	return request
}

type fakeRuntime struct {
	snapshot  orchestrator.Snapshot
	decisions []string
	replies   []string
	stops     []string
	polls     []string
	pollErr   error
}

func (f *fakeRuntime) Snapshot() orchestrator.Snapshot {
	return f.snapshot
}

func (f *fakeRuntime) ResolveApproval(requestID string, decision string) error {
	f.decisions = append(f.decisions, requestID+":"+decision)
	return nil
}

func (f *fakeRuntime) ResolveMessage(requestID string, reply string, resolvedVia string) error {
	f.replies = append(f.replies, requestID+":"+reply+":"+resolvedVia)
	return nil
}

func (f *fakeRuntime) StopRun(runID string, reason string) error {
	f.stops = append(f.stops, runID+":"+reason)
	return nil
}

func (f *fakeRuntime) RequestForcePoll(sourceName string) (orchestrator.ForcePollResult, error) {
	if f.pollErr != nil {
		return orchestrator.ForcePollResult{}, f.pollErr
	}
	f.polls = append(f.polls, sourceName)
	scope := "all"
	results := []orchestrator.ForcePollSourceResult{}
	if sourceName != "" {
		scope = "source"
		results = append(results, orchestrator.ForcePollSourceResult{
			Source: sourceName,
			Status: orchestrator.ForcePollQueued,
		})
	} else {
		for _, summary := range f.snapshot.SourceSummaries {
			results = append(results, orchestrator.ForcePollSourceResult{
				Source: summary.Name,
				Status: orchestrator.ForcePollQueued,
			})
		}
	}
	return orchestrator.ForcePollResult{Scope: scope, Results: results}, nil
}

func authorizeRequest(server *Server, request *http.Request) {
	request.Header.Set("Authorization", "Bearer "+server.apiKey)
}

func TestStatusEndpointReturnsSnapshotAndConfig(t *testing.T) {
	runtime := &fakeRuntime{
		snapshot: orchestrator.Snapshot{
			SourceName: "gitlab-a, linear-a",
			ActiveRuns: []domain.AgentRun{
				{ID: "run-1", AgentName: "coder", SourceName: "gitlab-a", Issue: domain.Issue{Identifier: "team/project#1"}},
			},
			PendingApprovals: []orchestrator.ApprovalView{
				{RequestID: "approval-1", IssueIdentifier: "team/project#1", ToolName: "Write"},
			},
		},
	}
	cfg := &config.Config{
		Server: config.ServerConfig{Enabled: true, Host: "127.0.0.1", Port: 8742},
		Workspace: config.WorkspaceConfig{
			Root: "/tmp/workspaces",
		},
		State: config.StateConfig{
			Dir: "/tmp/state",
		},
		Logging: config.LoggingConfig{
			Dir:      "/tmp/logs",
			MaxFiles: 20,
		},
		Sources: []config.SourceConfig{
			{Name: "gitlab-a", Tracker: "gitlab"},
			{Name: "linear-a", Tracker: "linear"},
		},
		AgentTypes: []config.AgentTypeConfig{
			{Name: "code-pr", Harness: "claude-code", Workspace: "git-clone", ApprovalPolicy: "auto", Prompt: "/tmp/prompt.md"},
		},
	}

	server := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime)
	recorder := httptest.NewRecorder()
	request := newLocalRequest(http.MethodGet, "/api/v1/status", nil)
	authorizeRequest(server, request)

	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", recorder.Code)
	}

	var payload struct {
		GeneratedAt time.Time      `json:"generated_at"`
		Config      map[string]any `json:"config"`
		Snapshot    map[string]any `json:"snapshot"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload.GeneratedAt.IsZero() {
		t.Fatal("expected generated_at")
	}
	activeRuns, ok := payload.Snapshot["active_runs"].([]any)
	if !ok || len(activeRuns) != 1 {
		t.Fatalf("active runs = %#v, want 1 item", payload.Snapshot["active_runs"])
	}
	sourceSummaries, ok := payload.Snapshot["source_summaries"].([]any)
	if ok && len(sourceSummaries) > 0 {
		summary, ok := sourceSummaries[0].(map[string]any)
		if !ok {
			t.Fatalf("source summary = %#v, want object", sourceSummaries[0])
		}
		if _, ok := summary["max_active_runs"]; !ok {
			t.Fatalf("source summary missing max_active_runs: %#v", summary)
		}
	}
	approvals, ok := payload.Snapshot["pending_approvals"].([]any)
	if !ok || len(approvals) != 1 {
		t.Fatalf("approvals = %#v, want 1 item", payload.Snapshot["pending_approvals"])
	}
	if _, ok := payload.Config["sources"]; !ok {
		t.Fatalf("config response missing sources: %v", payload.Config)
	}
}

func TestStatusEndpointEncodesRichSnapshotFields(t *testing.T) {
	now := time.Unix(1710000000, 0).UTC()
	tokensIn := int64(120)
	tokensOut := int64(30)
	totalTokens := int64(150)
	durationMS := int64(5000)
	limit := int64(5000)
	remaining := int64(4200)
	runtime := &fakeRuntime{
		snapshot: orchestrator.Snapshot{
			SourceName:    "gitlab-a",
			SourceTracker: "gitlab",
			LastPollAt:    now,
			LastPollCount: 3,
			ClaimedCount:  1,
			RetryCount:    1,
			InstanceMetrics: domain.RunMetrics{
				TokensIn:    &tokensIn,
				TokensOut:   &tokensOut,
				TotalTokens: &totalTokens,
			},
			HarnessMetrics: []orchestrator.MetricBreakdown{
				{
					Name: "claude-code",
					Metrics: domain.RunMetrics{
						TokensIn:    &tokensIn,
						TokensOut:   &tokensOut,
						TotalTokens: &totalTokens,
					},
				},
			},
			PendingApprovals: []orchestrator.ApprovalView{
				{
					RequestID:       "approval-1",
					RunID:           "run-1",
					IssueID:         "123",
					IssueIdentifier: "team/project#1",
					AgentName:       "coder",
					ToolName:        "Bash",
					ToolInput:       `{"command":"git status"}`,
					ApprovalPolicy:  "manual",
					RequestedAt:     now,
					Resolvable:      true,
				},
			},
			PendingMessages: []orchestrator.MessageView{
				{
					RequestID:       "message-1",
					RunID:           "run-1",
					IssueID:         "123",
					IssueIdentifier: "team/project#1",
					SourceName:      "gitlab-a",
					AgentName:       "coder",
					Kind:            "before_work_review",
					Summary:         "Need review",
					Body:            "Proceed?",
					RequestedAt:     now,
					Resolvable:      true,
				},
			},
			Retries: []orchestrator.RetryView{
				{
					IssueID:         "124",
					IssueIdentifier: "team/project#2",
					SourceName:      "gitlab-a",
					Attempt:         2,
					DueAt:           now.Add(time.Minute),
					Error:           "temporary failure",
				},
			},
			ApprovalHistory: []orchestrator.ApprovalHistoryEntry{
				{
					RequestID:       "approval-0",
					RunID:           "run-0",
					IssueID:         "122",
					IssueIdentifier: "team/project#0",
					AgentName:       "coder",
					ToolName:        "Edit",
					ApprovalPolicy:  "manual",
					Decision:        "approve",
					Reason:          "ok",
					RequestedAt:     now.Add(-2 * time.Minute),
					DecidedAt:       now.Add(-time.Minute),
					Outcome:         "resolved",
				},
			},
			MessageHistory: []orchestrator.MessageHistoryEntry{
				{
					RequestID:       "message-0",
					RunID:           "run-0",
					IssueID:         "122",
					IssueIdentifier: "team/project#0",
					SourceName:      "gitlab-a",
					AgentName:       "coder",
					Kind:            "before_work_review",
					Summary:         "Need review",
					Body:            "Proceed?",
					Reply:           "yes",
					ResolvedVia:     "web",
					RequestedAt:     now.Add(-2 * time.Minute),
					RepliedAt:       now.Add(-time.Minute),
					Outcome:         "resolved",
				},
			},
			ActiveRun: &domain.AgentRun{
				ID:             "run-1",
				AgentName:      "coder",
				AgentType:      "code-pr",
				SourceName:     "gitlab-a",
				HarnessKind:    "claude-code",
				WorkspacePath:  "/tmp/workspace",
				Status:         domain.RunStatusActive,
				Attempt:        1,
				ApprovalPolicy: "manual",
				ApprovalState:  domain.ApprovalStateAwaiting,
				StartedAt:      now,
				LastActivityAt: now.Add(30 * time.Second),
				Metrics: domain.RunMetrics{
					TokensIn:    &tokensIn,
					TokensOut:   &tokensOut,
					TotalTokens: &totalTokens,
					DurationMS:  &durationMS,
				},
				Issue: domain.Issue{
					ID:         "123",
					Identifier: "team/project#1",
					Title:      "Fix bug",
					URL:        "https://example.com/issues/123",
					State:      "opened",
					Labels:     []string{"bug", "urgent"},
					UpdatedAt:  now,
				},
			},
			ActiveRuns: []domain.AgentRun{
				{
					ID:             "run-1",
					AgentName:      "coder",
					AgentType:      "code-pr",
					SourceName:     "gitlab-a",
					HarnessKind:    "claude-code",
					WorkspacePath:  "/tmp/workspace",
					Status:         domain.RunStatusActive,
					Attempt:        1,
					ApprovalPolicy: "manual",
					ApprovalState:  domain.ApprovalStateAwaiting,
					StartedAt:      now,
					LastActivityAt: now.Add(30 * time.Second),
					Metrics: domain.RunMetrics{
						TokensIn:    &tokensIn,
						TokensOut:   &tokensOut,
						TotalTokens: &totalTokens,
						DurationMS:  &durationMS,
					},
					Issue: domain.Issue{
						ID:         "123",
						Identifier: "team/project#1",
						Title:      "Fix bug",
						URL:        "https://example.com/issues/123",
						State:      "opened",
						Labels:     []string{"bug", "urgent"},
						UpdatedAt:  now,
					},
				},
			},
			RunOutputs: []orchestrator.RunOutputView{
				{
					RunID:           "run-1",
					SourceName:      "gitlab-a",
					IssueIdentifier: "team/project#1",
					StdoutTail:      "hello",
					StderrTail:      "warn",
					UpdatedAt:       now,
				},
			},
			SourceSummaries: []orchestrator.SourceSummary{
				{
					Name:         "gitlab-a",
					DisplayGroup: "Core",
					Tags:         []string{"prod"},
					Tracker:      "gitlab",
					Execution: &orchestrator.ExecutionSummary{
						Mode:              "docker",
						Image:             "maestro-agent:latest",
						Network:           "bridge",
						NetworkPolicyMode: "allowlist",
						NetworkAllow:      []string{"api.openai.com", "*.anthropic.com"},
						CPUs:              2,
						Memory:            "4g",
						PIDsLimit:         256,
						AuthSource:        "env",
						SecurityPreset:    "locked-down",
						EnvCount:          2,
						SecretMountCount:  1,
						ToolMountCount:    1,
					},
					RateLimit: &domain.TrackerRateLimit{
						Limit:     &limit,
						Remaining: &remaining,
						ResetAt:   now.Add(time.Hour),
					},
					LastPollAt:             now,
					LastPollCount:          3,
					ClaimedCount:           1,
					RetryCount:             1,
					ActiveRunCount:         1,
					MaxActiveRuns:          3,
					AgentMaxConcurrent:     4,
					GlobalMaxConcurrent:    10,
					EffectiveMaxConcurrent: 3,
					Metrics: domain.RunMetrics{
						TokensIn:    &tokensIn,
						TokensOut:   &tokensOut,
						TotalTokens: &totalTokens,
					},
					PendingApprovals: 1,
					PendingMessages:  1,
				},
			},
			RecentEvents: []orchestrator.Event{
				{
					Time:    now,
					Level:   "info",
					Source:  "gitlab-a",
					RunID:   "run-1",
					Issue:   "team/project#1",
					Message: "dispatched",
				},
			},
		},
	}
	cfg := &config.Config{
		Server: config.ServerConfig{Enabled: true, Host: "127.0.0.1", Port: 8742},
	}
	server := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime)

	recorder := httptest.NewRecorder()
	request := newLocalRequest(http.MethodGet, "/api/v1/status", nil)
	authorizeRequest(server, request)
	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", recorder.Code)
	}

	body := recorder.Body.String()
	for _, want := range []string{
		`"source_tracker": "gitlab"`,
		`"tool_input": "{\"command\":\"git status\"}"`,
		`"resolved_via": "web"`,
		`"workspace_path": "/tmp/workspace"`,
		`"tokens_in": 120`,
		`"total_tokens": 150`,
		`"instance_metrics": {`,
		`"harness_metrics": [`,
		`"name": "claude-code"`,
		`"remaining": 4200`,
		`"mode": "docker"`,
		`"image": "maestro-agent:latest"`,
		`"network_policy_mode": "allowlist"`,
		`"api.openai.com"`,
		`"auth_source": "env"`,
		`"security_preset": "locked-down"`,
		`"env_count": 2`,
		`"secret_mount_count": 1`,
		`"tool_mount_count": 1`,
		`"stdout_tail": "hello"`,
		`"display_group": "Core"`,
		`"agent_max_concurrent": 4`,
		`"global_max_concurrent": 10`,
		`"effective_max_concurrent": 3`,
		`"metrics": {`,
		`"message": "dispatched"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("status body missing %q:\n%s", want, body)
		}
	}
}

func TestApprovalActionEndpointResolvesDecision(t *testing.T) {
	runtime := &fakeRuntime{}
	cfg := &config.Config{
		Server: config.ServerConfig{Enabled: true, Host: "127.0.0.1", Port: 8742},
	}
	server := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime)

	recorder := httptest.NewRecorder()
	request := newLocalRequest(http.MethodPost, "/api/v1/approvals/approval-1/approve", nil)
	authorizeRequest(server, request)
	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", recorder.Code)
	}
	if got := strings.Join(runtime.decisions, ","); got != "approval-1:approve" {
		t.Fatalf("decisions = %q", got)
	}
}

func TestRunStopEndpointStopsRun(t *testing.T) {
	runtime := &fakeRuntime{}
	cfg := &config.Config{
		Server: config.ServerConfig{Enabled: true, Host: "127.0.0.1", Port: 8742},
	}
	server := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime)

	recorder := httptest.NewRecorder()
	request := newLocalRequest(http.MethodPost, "/api/v1/runs/run-1/stop", nil)
	authorizeRequest(server, request)
	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", recorder.Code)
	}
	if got := strings.Join(runtime.stops, ","); !strings.Contains(got, "run-1:stopped from the web console") {
		t.Fatalf("stops = %q", got)
	}
}

func TestForcePollEndpointRequestsAllSources(t *testing.T) {
	runtime := &fakeRuntime{
		snapshot: orchestrator.Snapshot{
			SourceSummaries: []orchestrator.SourceSummary{
				{Name: "gitlab-a", Tracker: "gitlab"},
				{Name: "linear-a", Tracker: "linear"},
			},
		},
	}
	cfg := &config.Config{
		Server: config.ServerConfig{Enabled: true, Host: "127.0.0.1", Port: 8742},
	}
	server := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime)

	recorder := httptest.NewRecorder()
	request := newLocalRequest(http.MethodPost, "/api/v1/poll", nil)
	authorizeRequest(server, request)
	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", recorder.Code)
	}
	if got := strings.Join(runtime.polls, ","); got != "" {
		t.Fatalf("poll requests = %q, want all-sources request", got)
	}
	body := recorder.Body.String()
	for _, want := range []string{`"scope": "all"`, `"source": "gitlab-a"`, `"source": "linear-a"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("force poll body missing %q:\n%s", want, body)
		}
	}
}

func TestSourceForcePollEndpointRequestsSingleSource(t *testing.T) {
	runtime := &fakeRuntime{}
	cfg := &config.Config{
		Server: config.ServerConfig{Enabled: true, Host: "127.0.0.1", Port: 8742},
	}
	server := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime)

	recorder := httptest.NewRecorder()
	request := newLocalRequest(http.MethodPost, "/api/v1/sources/gitlab-a/poll", nil)
	authorizeRequest(server, request)
	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", recorder.Code)
	}
	if got := strings.Join(runtime.polls, ","); got != "gitlab-a" {
		t.Fatalf("poll requests = %q, want gitlab-a", got)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"requested_source": "gitlab-a"`) {
		t.Fatalf("force poll body missing requested source:\n%s", body)
	}
}

func TestResourceEndpointsReturnCollections(t *testing.T) {
	runtime := &fakeRuntime{
		snapshot: orchestrator.Snapshot{
			SourceSummaries: []orchestrator.SourceSummary{
				{Name: "gitlab-a", Tracker: "gitlab", ActiveRunCount: 1},
			},
			ActiveRuns: []domain.AgentRun{
				{ID: "run-1", AgentName: "coder", SourceName: "gitlab-a", Issue: domain.Issue{Identifier: "team/project#1"}},
			},
			RunOutputs: []orchestrator.RunOutputView{
				{RunID: "run-1", SourceName: "gitlab-a", IssueIdentifier: "team/project#1", StdoutTail: "hello"},
			},
			Retries: []orchestrator.RetryView{
				{IssueIdentifier: "team/project#2", SourceName: "gitlab-a", Attempt: 2},
			},
			RecentEvents: []orchestrator.Event{
				{Level: "INFO", Source: "gitlab-a", Message: "polled"},
			},
			PendingApprovals: []orchestrator.ApprovalView{
				{RequestID: "approval-1", IssueIdentifier: "team/project#1", ToolName: "Write"},
			},
			PendingMessages: []orchestrator.MessageView{
				{RequestID: "message-1", IssueIdentifier: "team/project#1", Kind: "before_work", Summary: "Before work: team/project#1", Body: "Reply with start", Resolvable: true},
			},
		},
	}
	cfg := &config.Config{
		Server: config.ServerConfig{Enabled: true, Host: "127.0.0.1", Port: 8742},
	}
	server := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime)

	for _, test := range []struct {
		path       string
		wantCount  string
		wantPieces []string
	}{
		{path: "/api/v1/sources", wantCount: `"count": 1`, wantPieces: []string{`"name": "gitlab-a"`}},
		{path: "/api/v1/runs", wantCount: `"count": 1`, wantPieces: []string{`"agent_name": "coder"`, `"outputs": [`}},
		{path: "/api/v1/retries", wantCount: `"count": 1`, wantPieces: []string{`"issue_identifier": "team/project#2"`}},
		{path: "/api/v1/events", wantCount: `"count": 1`, wantPieces: []string{`"message": "polled"`}},
		{path: "/api/v1/approvals", wantCount: `"count": 1`, wantPieces: []string{`"request_id": "approval-1"`}},
		{path: "/api/v1/messages", wantCount: `"count": 1`, wantPieces: []string{`"request_id": "message-1"`, `"kind": "before_work"`}},
	} {
		recorder := httptest.NewRecorder()
		request := newLocalRequest(http.MethodGet, test.path, nil)
		authorizeRequest(server, request)
		server.httpServer.Handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status code = %d, want 200", test.path, recorder.Code)
		}
		body := recorder.Body.String()
		if !strings.Contains(body, test.wantCount) {
			t.Fatalf("%s missing count in body:\n%s", test.path, body)
		}
		for _, want := range test.wantPieces {
			if !strings.Contains(body, want) {
				t.Fatalf("%s missing %q:\n%s", test.path, want, body)
			}
		}
	}
}

func TestMessageReplyEndpointResolvesReply(t *testing.T) {
	runtime := &fakeRuntime{}
	cfg := &config.Config{
		Server: config.ServerConfig{Enabled: true, Host: "127.0.0.1", Port: 8742},
	}
	server := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime)

	recorder := httptest.NewRecorder()
	request := newLocalRequest(http.MethodPost, "/api/v1/messages/message-1/reply", strings.NewReader(`{"reply":"start"}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeRequest(server, request)
	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", recorder.Code)
	}
	if got := strings.Join(runtime.replies, ","); got != "message-1:start:web" {
		t.Fatalf("replies = %q", got)
	}
}

func TestMessageReplyEndpointRejectsEmptyReply(t *testing.T) {
	runtime := &fakeRuntime{}
	cfg := &config.Config{
		Server: config.ServerConfig{Enabled: true, Host: "127.0.0.1", Port: 8742},
	}
	server := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime)

	recorder := httptest.NewRecorder()
	request := newLocalRequest(http.MethodPost, "/api/v1/messages/message-1/reply", strings.NewReader(`{"reply":"   "}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeRequest(server, request)
	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400", recorder.Code)
	}
}

func TestDashboardServesHTML(t *testing.T) {
	runtime := &fakeRuntime{}
	cfg := &config.Config{
		Server: config.ServerConfig{Enabled: true, Host: "127.0.0.1", Port: 8742},
	}
	server := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime)

	recorder := httptest.NewRecorder()
	request := newLocalRequest(http.MethodGet, "/", nil)
	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	for _, want := range []string{"<title>Maestro</title>", "<div id=\"root\"></div>", "/assets/", "index-"} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, body)
		}
	}
}

func TestStreamEndpointEmitsInitialUpdate(t *testing.T) {
	runtime := &fakeRuntime{
		snapshot: orchestrator.Snapshot{
			SourceSummaries: []orchestrator.SourceSummary{{Name: "gitlab-a", Tracker: "gitlab"}},
			ActiveRuns: []domain.AgentRun{
				{ID: "run-1", AgentName: "coder", SourceName: "gitlab-a", Issue: domain.Issue{Identifier: "team/project#1"}},
			},
			PendingApprovals: []orchestrator.ApprovalView{
				{RequestID: "approval-1", IssueIdentifier: "team/project#1", ToolName: "Write"},
			},
		},
	}
	cfg := &config.Config{
		Server: config.ServerConfig{Enabled: true, Host: "127.0.0.1", Port: 8742},
	}
	server := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime)
	testServer := httptest.NewServer(server.httpServer.Handler)
	defer testServer.Close()

	resp, err := http.Get(testServer.URL + "/api/v1/stream?api_key=" + server.apiKey)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("content-type = %q", got)
	}

	reader := bufio.NewReader(resp.Body)
	line1, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read event line: %v", err)
	}
	line2, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read data line: %v", err)
	}
	if !strings.HasPrefix(line1, "event: update") {
		t.Fatalf("unexpected first line: %q", line1)
	}
	if !strings.HasPrefix(line2, "data: ") {
		t.Fatalf("unexpected second line: %q", line2)
	}
	raw := strings.TrimPrefix(strings.TrimSpace(line2), "data: ")
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	snapshot, ok := payload["snapshot"].(map[string]any)
	if !ok {
		t.Fatalf("snapshot payload missing: %#v", payload)
	}
	if got := int(snapshot["source_count"].(float64)); got != 1 {
		t.Fatalf("source_count = %d, want 1", got)
	}
	if got := int(snapshot["active_run_count"].(float64)); got != 1 {
		t.Fatalf("active_run_count = %d, want 1", got)
	}
	if got := int(snapshot["approval_count"].(float64)); got != 1 {
		t.Fatalf("approval_count = %d, want 1", got)
	}
}

func TestRunShutsDownWithActiveStreamClient(t *testing.T) {
	runtime := &fakeRuntime{
		snapshot: orchestrator.Snapshot{
			SourceSummaries: []orchestrator.SourceSummary{{Name: "gitlab-a", Tracker: "gitlab"}},
			ActiveRuns: []domain.AgentRun{
				{ID: "run-1", AgentName: "coder", SourceName: "gitlab-a", Issue: domain.Issue{Identifier: "team/project#1"}},
			},
		},
	}
	server := New(&config.Config{Server: config.ServerConfig{Enabled: true, Host: "127.0.0.1", Port: freeTCPPort(t)}}, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime)

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(runCtx)
	}()

	baseURL := "http://" + server.Addr()
	waitForHTTPHealthy(t, baseURL+"/healthz")

	resp, err := http.Get(baseURL + "/api/v1/stream")
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read stream event line: %v", err)
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read stream data line: %v", err)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down promptly with active stream client")
	}
}

func TestPackSavePersistsFiles(t *testing.T) {
	root := t.TempDir()
	packsDir := filepath.Join(root, "agents", "code-pr")
	if err := os.MkdirAll(packsDir, 0o755); err != nil {
		t.Fatalf("mkdir packs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packsDir, "agent.yaml"), []byte(strings.TrimSpace(`
name: code-pr
description: Original description
harness: claude-code
workspace: git-clone
prompt: prompt.md
approval_policy: auto
max_concurrent: 1
tools:
  - git
skills:
  - narrow diffs
context_files:
  - context.md
`)), 0o600); err != nil {
		t.Fatalf("write pack yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packsDir, "prompt.md"), []byte("Old prompt"), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packsDir, "context.md"), []byte("Old context"), 0o600); err != nil {
		t.Fatalf("write context: %v", err)
	}

	configPath := filepath.Join(root, "maestro.yaml")
	if err := os.WriteFile(configPath, []byte(strings.TrimSpace(`
agent_packs_dir: ./agents
defaults:
  max_concurrent_global: 1
sources:
  - name: demo
    tracker: gitlab
    connection:
      base_url: https://gitlab.example.com
      project: team/project
    filter:
      labels:
        - agent:ready
    agent_type: code-pr
agent_types:
  - name: code-pr
    agent_pack: code-pr
workspace:
  root: ./workspaces
state:
  dir: ./state
logging:
  dir: ./logs
  max_files: 5
server:
  enabled: true
  host: 127.0.0.1
  port: 8742
`)), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	server := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), &fakeRuntime{})

	body := []byte(`{"original_name":"code-pr","name":"code-pr","description":"Updated description","instance_name":"code-pr","harness":"claude-code","workspace":"git-clone","approval_policy":"auto","max_concurrent":2,"tools":["git","make"],"skills":["narrow diffs","verification"],"env_keys":[],"prompt_body":"Updated prompt","context_body":"Updated context"}`)
	recorder := httptest.NewRecorder()
	request := newLocalRequest(http.MethodPost, "/api/v1/packs/save", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	authorizeRequest(server, request)
	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	promptRaw, err := os.ReadFile(filepath.Join(packsDir, "prompt.md"))
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	if got := strings.TrimSpace(string(promptRaw)); got != "Updated prompt" {
		t.Fatalf("prompt = %q", got)
	}
	contextRaw, err := os.ReadFile(filepath.Join(packsDir, "context.md"))
	if err != nil {
		t.Fatalf("read context: %v", err)
	}
	if got := strings.TrimSpace(string(contextRaw)); got != "Updated context" {
		t.Fatalf("context = %q", got)
	}
	agentRaw, err := os.ReadFile(filepath.Join(packsDir, "agent.yaml"))
	if err != nil {
		t.Fatalf("read agent yaml: %v", err)
	}
	if !strings.Contains(string(agentRaw), "description: Updated description") {
		t.Fatalf("agent yaml missing updated description:\n%s", string(agentRaw))
	}
}

func TestPackSaveRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "maestro.yaml")
	if err := os.WriteFile(configPath, []byte(strings.TrimSpace(`
agent_packs_dir: ./agents
defaults:
  max_concurrent_global: 1
sources:
  - name: demo
    tracker: gitlab
    connection:
      base_url: https://gitlab.example.com
      project: team/project
    filter:
      labels:
        - agent:ready
    agent_type: code-pr
agent_types:
  - name: code-pr
    harness: claude-code
    workspace: git-clone
    prompt: ./prompt.md
    approval_policy: auto
    max_concurrent: 1
workspace:
  root: ./workspaces
state:
  dir: ./state
logging:
  dir: ./logs
  max_files: 5
server:
  enabled: true
  host: 127.0.0.1
  port: 8742
`)), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "prompt.md"), []byte("prompt"), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	server := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), &fakeRuntime{})

	body := []byte(`{"name":"../../escape","description":"Updated description","instance_name":"escape","harness":"claude-code","workspace":"git-clone","approval_policy":"auto","max_concurrent":1,"tools":[],"skills":[],"env_keys":[],"prompt_body":"Updated prompt","context_body":"Updated context"}`)
	recorder := httptest.NewRecorder()
	request := newLocalRequest(http.MethodPost, "/api/v1/packs/save", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "must stay within the agent packs directory") {
		t.Fatalf("expected traversal error, got %s", recorder.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "escape", "agent.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected no escaped pack write, stat err = %v", err)
	}
}

func TestConfigRawValidateAndSaveEndpoints(t *testing.T) {
	configPath, raw := testConfigFile(t)
	cfg := &config.Config{
		ConfigPath: configPath,
		ConfigDir:  filepath.Dir(configPath),
		Server:     config.ServerConfig{Enabled: true, Host: "127.0.0.1", Port: 8742},
		Workspace:  config.WorkspaceConfig{Root: "/tmp/workspaces"},
		State:      config.StateConfig{Dir: "/tmp/state"},
		Logging:    config.LoggingConfig{Dir: "/tmp/logs", MaxFiles: 20},
		Sources:    []config.SourceConfig{{Name: "gitlab-a", Tracker: "gitlab", AgentType: "code-pr"}},
		AgentTypes: []config.AgentTypeConfig{{Name: "code-pr", Harness: "claude-code", Workspace: "git-clone", ApprovalPolicy: "auto", Prompt: "/tmp/prompt.md"}},
	}
	server := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), &fakeRuntime{})

	recorder := httptest.NewRecorder()
	request := newLocalRequest(http.MethodGet, "/api/v1/config/raw", nil)
	authorizeRequest(server, request)
	server.httpServer.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("config raw status code = %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "sources:") {
		t.Fatalf("config raw missing yaml: %s", recorder.Body.String())
	}

	validateRecorder := httptest.NewRecorder()
	validateRequest := newLocalRequest(http.MethodPost, "/api/v1/config/validate", strings.NewReader(`{"yaml":`+strconvQuote(raw)+`}`))
	validateRequest.Header.Set("Content-Type", "application/json")
	authorizeRequest(server, validateRequest)
	server.httpServer.Handler.ServeHTTP(validateRecorder, validateRequest)
	if validateRecorder.Code != http.StatusOK {
		t.Fatalf("config validate status code = %d, want 200", validateRecorder.Code)
	}
	if !strings.Contains(validateRecorder.Body.String(), `"ok": true`) {
		t.Fatalf("config validate missing ok response: %s", validateRecorder.Body.String())
	}

	updated := strings.Replace(raw, "gitlab-a", "gitlab-b", 1)
	saveRecorder := httptest.NewRecorder()
	saveRequest := newLocalRequest(http.MethodPost, "/api/v1/config/save", strings.NewReader(`{"yaml":`+strconvQuote(updated)+`}`))
	saveRequest.Header.Set("Content-Type", "application/json")
	authorizeRequest(server, saveRequest)
	server.httpServer.Handler.ServeHTTP(saveRecorder, saveRequest)
	if saveRecorder.Code != http.StatusOK {
		t.Fatalf("config save status code = %d, want 200", saveRecorder.Code)
	}
	savedRaw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(savedRaw), "gitlab-b") {
		t.Fatalf("saved config missing updated source name: %s", string(savedRaw))
	}

	dryRunRecorder := httptest.NewRecorder()
	dryRunRequest := newLocalRequest(http.MethodPost, "/api/v1/config/dry-run", strings.NewReader(`{"yaml":`+strconvQuote(strings.Replace(updated, "gitlab-b", "gitlab-c", 1))+`}`))
	dryRunRequest.Header.Set("Content-Type", "application/json")
	authorizeRequest(server, dryRunRequest)
	server.httpServer.Handler.ServeHTTP(dryRunRecorder, dryRunRequest)
	if dryRunRecorder.Code != http.StatusOK {
		t.Fatalf("config dry-run status code = %d, want 200", dryRunRecorder.Code)
	}
	if !strings.Contains(dryRunRecorder.Body.String(), `"diff":`) {
		t.Fatalf("config dry-run missing diff: %s", dryRunRecorder.Body.String())
	}

	backupsRecorder := httptest.NewRecorder()
	backupsRequest := newLocalRequest(http.MethodGet, "/api/v1/config/backups", nil)
	authorizeRequest(server, backupsRequest)
	server.httpServer.Handler.ServeHTTP(backupsRecorder, backupsRequest)
	if backupsRecorder.Code != http.StatusOK {
		t.Fatalf("config backups status code = %d, want 200", backupsRecorder.Code)
	}
	if !strings.Contains(backupsRecorder.Body.String(), `"count": 1`) {
		t.Fatalf("config backups missing count: %s", backupsRecorder.Body.String())
	}

	backupEntries, err := os.ReadDir(filepath.Dir(configPath))
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	backupName := ""
	for _, entry := range backupEntries {
		if strings.HasPrefix(entry.Name(), filepath.Base(configPath)+".bak.") {
			backupName = entry.Name()
			break
		}
	}
	if backupName == "" {
		t.Fatal("expected backup file to exist after save")
	}
	backupDetailRecorder := httptest.NewRecorder()
	backupDetailRequest := newLocalRequest(http.MethodGet, "/api/v1/config/backups/"+backupName, nil)
	authorizeRequest(server, backupDetailRequest)
	server.httpServer.Handler.ServeHTTP(backupDetailRecorder, backupDetailRequest)
	if backupDetailRecorder.Code != http.StatusOK {
		t.Fatalf("config backup detail status code = %d, want 200", backupDetailRecorder.Code)
	}
	if !strings.Contains(backupDetailRecorder.Body.String(), `"backup"`) {
		t.Fatalf("config backup detail missing payload: %s", backupDetailRecorder.Body.String())
	}

	restoreRecorder := httptest.NewRecorder()
	restoreRequest := newLocalRequest(http.MethodPost, "/api/v1/config/backups/"+backupName, nil)
	authorizeRequest(server, restoreRequest)
	server.httpServer.Handler.ServeHTTP(restoreRecorder, restoreRequest)
	if restoreRecorder.Code != http.StatusOK {
		t.Fatalf("config restore status code = %d, want 200", restoreRecorder.Code)
	}
	restoredRaw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read restored config: %v", err)
	}
	if !strings.Contains(string(restoredRaw), "gitlab-a") {
		t.Fatalf("restore did not bring back original config: %s", string(restoredRaw))
	}

	createBackupRecorder := httptest.NewRecorder()
	createBackupRequest := newLocalRequest(http.MethodPost, "/api/v1/config/backups/create", nil)
	authorizeRequest(server, createBackupRequest)
	server.httpServer.Handler.ServeHTTP(createBackupRecorder, createBackupRequest)
	if createBackupRecorder.Code != http.StatusOK {
		t.Fatalf("config backup create status code = %d, want 200", createBackupRecorder.Code)
	}
	if !strings.Contains(createBackupRecorder.Body.String(), `"ok": true`) {
		t.Fatalf("config backup create missing ok response: %s", createBackupRecorder.Body.String())
	}

	invalidSaveRecorder := httptest.NewRecorder()
	invalidSaveRequest := newLocalRequest(http.MethodPost, "/api/v1/config/save", strings.NewReader(`{"yaml":"sources:\n  - name: broken\n    tracker: gitlab\n"}`))
	invalidSaveRequest.Header.Set("Content-Type", "application/json")
	authorizeRequest(server, invalidSaveRequest)
	server.httpServer.Handler.ServeHTTP(invalidSaveRecorder, invalidSaveRequest)
	if invalidSaveRecorder.Code != http.StatusOK {
		t.Fatalf("invalid config save status code = %d, want 200", invalidSaveRecorder.Code)
	}
	if !strings.Contains(invalidSaveRecorder.Body.String(), `"ok": false`) {
		t.Fatalf("invalid config save missing failed response: %s", invalidSaveRecorder.Body.String())
	}
	postInvalidRaw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after invalid save: %v", err)
	}
	if string(postInvalidRaw) != string(restoredRaw) {
		t.Fatalf("invalid save should not modify config\nwant:\n%s\n\ngot:\n%s", string(restoredRaw), string(postInvalidRaw))
	}
}

func TestConfigValidateRejectsBrokenYAML(t *testing.T) {
	server := New(&config.Config{Server: config.ServerConfig{Enabled: true, Host: "127.0.0.1", Port: 8742}}, slog.New(slog.NewTextHandler(io.Discard, nil)), &fakeRuntime{})
	recorder := httptest.NewRecorder()
	request := newLocalRequest(http.MethodPost, "/api/v1/config/validate", strings.NewReader(`{"yaml":"sources:\n  - name: bad\n    tracker: gitlab\n"}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeRequest(server, request)
	server.httpServer.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("broken validate status code = %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"ok": false`) {
		t.Fatalf("expected failed validation response: %s", recorder.Body.String())
	}
}

func TestConfigValidateUsesInjectedTokenLookupWithoutMutatingEnv(t *testing.T) {
	_ = os.Unsetenv("MISSING_GITLAB_TOKEN")
	root := t.TempDir()
	configPath := filepath.Join(root, "maestro.yaml")
	promptPath := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("prompt"), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	server := New(&config.Config{
		Server: config.ServerConfig{Enabled: true, Host: "127.0.0.1", Port: 8742},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), &fakeRuntime{})
	server.configSummary.ConfigPath = configPath
	server.configSummary.Sources = []ops.ConfigSourceSummary{
		{Name: "gitlab-a", TokenEnv: "MISSING_GITLAB_TOKEN"},
	}

	raw := `defaults:
  poll_interval: 30s
  stall_timeout: 10m
  max_concurrent_global: 1
user:
  name: Demo Operator
sources:
  - name: gitlab-a
    tracker: gitlab
    connection:
      base_url: https://gitlab.example.com
      token_env: MISSING_GITLAB_TOKEN
      project: team/project
    filter:
      labels: ["agent:ready"]
    agent_type: code-pr
agent_types:
  - name: code-pr
    harness: claude-code
    workspace: git-clone
    prompt: ./prompt.md
    approval_policy: auto
    max_concurrent: 1
workspace:
  root: ./workspaces
state:
  dir: ./state
logging:
  dir: ./logs
`

	recorder := httptest.NewRecorder()
	request := newLocalRequest(http.MethodPost, "/api/v1/config/validate", strings.NewReader(`{"yaml":`+strconvQuote(raw)+`}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeRequest(server, request)
	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"ok": true`) {
		t.Fatalf("expected successful validation response: %s", recorder.Body.String())
	}
	if value := os.Getenv("MISSING_GITLAB_TOKEN"); value != "" {
		t.Fatalf("token env mutated to %q, want empty", value)
	}
}

func TestAPIEndpointsRequireAuthorization(t *testing.T) {
	server := New(&config.Config{Server: config.ServerConfig{Enabled: true, Host: "0.0.0.0", Port: 8742}}, slog.New(slog.NewTextHandler(io.Discard, nil)), &fakeRuntime{})

	recorder := httptest.NewRecorder()
	request := newLocalRequest(http.MethodGet, "/api/v1/status", nil)
	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status code = %d, want 401", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "unauthorized") {
		t.Fatalf("expected unauthorized body, got %s", recorder.Body.String())
	}
}

func TestLoopbackAPIAllowsUnauthenticatedRequests(t *testing.T) {
	server := New(&config.Config{Server: config.ServerConfig{Enabled: true, Host: "127.0.0.1", Port: 8742}}, slog.New(slog.NewTextHandler(io.Discard, nil)), &fakeRuntime{})

	recorder := httptest.NewRecorder()
	request := newLocalRequest(http.MethodGet, "/api/v1/status", nil)
	server.httpServer.Handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", recorder.Code)
	}
}

func testConfigFile(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GITLAB_TOKEN", "test-token")
	promptPath := filepath.Join(dir, "prompt.md")
	if err := os.WriteFile(promptPath, []byte("demo prompt"), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	raw := strings.TrimSpace(`
defaults:
  poll_interval: 30s
  stall_timeout: 10m
  max_concurrent_global: 1
user:
  name: Demo Operator
sources:
  - name: gitlab-a
    tracker: gitlab
    connection:
      base_url: https://gitlab.example.com
      token_env: GITLAB_TOKEN
      project: team/project
    filter:
      labels: ["agent:ready"]
    agent_type: code-pr
agent_types:
  - name: code-pr
    harness: claude-code
    workspace: git-clone
    prompt: `+promptPath+`
    approval_policy: auto
    max_concurrent: 1
workspace:
  root: `+filepath.Join(dir, "workspaces")+`
state:
  dir: `+filepath.Join(dir, "state")+`
logging:
  dir: `+filepath.Join(dir, "logs")+`
`) + "\n"
	configPath := filepath.Join(dir, "maestro.yaml")
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath, raw
}

func strconvQuote(input string) string {
	raw, _ := json.Marshal(input)
	return string(raw)
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitForHTTPHealthy(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			lastErr = errors.New(resp.Status)
		} else {
			lastErr = err
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("server never became healthy: %v", lastErr)
}
