package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/harness"
	"github.com/tjohnson/maestro/internal/orchestrator"
)

type snapshotProvider interface {
	Snapshot() orchestrator.Snapshot
	ResolveApproval(requestID string, decision string) error
	ResolveMessage(requestID string, reply string, resolvedVia string) error
}

type tickMsg time.Time

type focusPane string
type runSortMode string
type retrySortMode string
type quickFilterMode string

const (
	focusSources   focusPane = "sources"
	focusRuns      focusPane = "runs"
	focusMessages  focusPane = "messages"
	focusRetries   focusPane = "retries"
	focusApprovals focusPane = "approvals"

	runSortStallRisk     runSortMode     = "stall-risk"
	runSortOldest        runSortMode     = "oldest"
	runSortNewest        runSortMode     = "newest"
	runSortApprovalFirst runSortMode     = "approval-first"
	retrySortDueSoonest  retrySortMode   = "due-soonest"
	retrySortOverdue     retrySortMode   = "overdue-first"
	retrySortAttempts    retrySortMode   = "highest-attempt"
	quickFilterAll       quickFilterMode = "all"
	quickFilterAttention quickFilterMode = "attention"
	quickFilterAwaiting  quickFilterMode = "awaiting-approval"
)

type Model struct {
	service          snapshotProvider
	snapshot         orchestrator.Snapshot
	selectedApproval int
	selectedRetry    int
	selectedRun      int
	selectedSource   int
	selectedMessage  int
	notice           string
	searchMode       bool
	searchQuery      string
	replyMode        bool
	replyInput       string
	groupFilter      string
	focus            focusPane
	runSort          runSortMode
	retrySort        retrySortMode
	compact          bool
	quickFilter      quickFilterMode
}

func NewModel(service snapshotProvider) Model {
	model := Model{
		service:     service,
		snapshot:    service.Snapshot(),
		focus:       focusSources,
		runSort:     runSortStallRisk,
		retrySort:   retrySortDueSoonest,
		quickFilter: quickFilterAll,
	}
	model.normalizeFocus()
	return model
}

func (m Model) Init() tea.Cmd {
	return tickCmd()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.replyMode {
			switch msg.String() {
			case "esc":
				m.replyMode = false
				m.replyInput = ""
				return m, nil
			case "enter":
				pending := m.filteredPendingMessages()
				if len(pending) == 0 {
					m.replyMode = false
					m.replyInput = ""
					return m, nil
				}
				reply := strings.TrimSpace(m.replyInput)
				if reply == "" {
					m.notice = "message reply cannot be empty"
					return m, nil
				}
				err := m.service.ResolveMessage(pending[m.selectedMessage].RequestID, reply, "tui")
				if err != nil {
					m.notice = "message reply failed: " + err.Error()
				} else {
					m.notice = "message reply sent"
					m.replyMode = false
					m.replyInput = ""
				}
				m.snapshot = m.service.Snapshot()
				m.clampSelection()
				return m, nil
			case "backspace":
				runes := []rune(m.replyInput)
				if len(runes) > 0 {
					m.replyInput = string(runes[:len(runes)-1])
				}
				return m, nil
			default:
				if len(msg.Runes) > 0 && !msg.Alt && !msg.Paste {
					m.replyInput += string(msg.Runes)
				}
				return m, nil
			}
		}
		if m.searchMode {
			switch msg.String() {
			case "esc":
				m.searchMode = false
				if strings.TrimSpace(m.searchQuery) == "" {
					m.searchQuery = ""
				}
				return m, nil
			case "enter":
				m.searchMode = false
				return m, nil
			case "backspace":
				if len(m.searchQuery) > 0 {
					m.searchQuery = string([]rune(m.searchQuery)[:len([]rune(m.searchQuery))-1])
				}
				return m, nil
			default:
				if len(msg.Runes) > 0 && !msg.Alt && !msg.Paste {
					m.searchQuery += string(msg.Runes)
				}
				return m, nil
			}
		}
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab":
			m.focus = m.nextFocus()
			m.clampSelection()
			return m, nil
		case "/":
			m.searchMode = true
			return m, nil
		case "f":
			m.groupFilter = nextGroupFilter(m.snapshot.SourceSummaries, m.groupFilter)
			return m, nil
		case "c":
			m.groupFilter = ""
			m.searchQuery = ""
			m.searchMode = false
			m.quickFilter = quickFilterAll
			return m, nil
		case "o":
			m.runSort = m.runSort.next()
			m.clampSelection()
			return m, nil
		case "O":
			m.retrySort = m.retrySort.next()
			m.clampSelection()
			return m, nil
		case "v":
			m.compact = !m.compact
			return m, nil
		case "u":
			m.quickFilter = m.quickFilter.toggle(quickFilterAttention)
			m.clampSelection()
			return m, nil
		case "w":
			m.quickFilter = m.quickFilter.toggle(quickFilterAwaiting)
			m.clampSelection()
			return m, nil
		case "j", "down":
			switch m.focus {
			case focusSources:
				sources := m.filteredSourceSummaries()
				if len(sources) > 0 && m.selectedSource < len(sources)-1 {
					m.selectedSource++
				}
			case focusApprovals:
				pending := m.filteredPendingApprovals()
				if len(pending) > 0 && m.selectedApproval < len(pending)-1 {
					m.selectedApproval++
				}
			case focusMessages:
				pending := m.filteredPendingMessages()
				if len(pending) > 0 && m.selectedMessage < len(pending)-1 {
					m.selectedMessage++
				}
			case focusRetries:
				retries := m.filteredRetries()
				if len(retries) > 0 && m.selectedRetry < len(retries)-1 {
					m.selectedRetry++
				}
			default:
				runs := m.filteredActiveRuns()
				if len(runs) > 0 && m.selectedRun < len(runs)-1 {
					m.selectedRun++
				}
			}
		case "k", "up":
			switch m.focus {
			case focusSources:
				if m.selectedSource > 0 {
					m.selectedSource--
				}
			case focusApprovals:
				if m.selectedApproval > 0 {
					m.selectedApproval--
				}
			case focusMessages:
				if m.selectedMessage > 0 {
					m.selectedMessage--
				}
			case focusRetries:
				if m.selectedRetry > 0 {
					m.selectedRetry--
				}
			default:
				if m.selectedRun > 0 {
					m.selectedRun--
				}
			}
		case "a":
			pending := m.filteredPendingApprovals()
			if len(pending) > 0 {
				err := m.service.ResolveApproval(pending[m.selectedApproval].RequestID, harness.DecisionApprove)
				if err != nil {
					m.notice = "approval failed: " + err.Error()
				} else {
					m.notice = "approval sent"
				}
				m.snapshot = m.service.Snapshot()
				m.clampSelection()
			}
		case "r":
			pending := m.filteredPendingApprovals()
			if len(pending) > 0 {
				err := m.service.ResolveApproval(pending[m.selectedApproval].RequestID, harness.DecisionReject)
				if err != nil {
					m.notice = "rejection failed: " + err.Error()
				} else {
					m.notice = "rejection sent"
				}
				m.snapshot = m.service.Snapshot()
				m.clampSelection()
			}
		case "s":
			if m.focus == focusMessages {
				pending := m.filteredPendingMessages()
				if len(pending) > 0 {
					err := m.service.ResolveMessage(pending[m.selectedMessage].RequestID, "start", "tui")
					if err != nil {
						m.notice = "message reply failed: " + err.Error()
					} else {
						m.notice = "start reply sent"
					}
					m.snapshot = m.service.Snapshot()
					m.clampSelection()
				}
			}
		case "e":
			if m.focus == focusMessages && len(m.filteredPendingMessages()) > 0 {
				m.replyMode = true
				m.replyInput = ""
				return m, nil
			}
		}
	case tickMsg:
		m.snapshot = m.service.Snapshot()
		m.normalizeFocus()
		m.clampSelection()
		return m, tickCmd()
	}
	return m, nil
}

func (m Model) View() string {
	var b strings.Builder
	filteredSources := m.filteredSourceSummaries()
	filteredApprovals := m.filteredPendingApprovals()
	filteredMessages := m.filteredPendingMessages()
	filteredRetries := m.filteredRetries()
	filteredActiveRuns := m.filteredActiveRuns()
	filteredHistory := m.filteredApprovalHistory()
	filteredEvents := m.filteredEvents()
	selectedRunEvents := m.selectedRunEvents(filteredActiveRuns)
	selectedSourceEvents := m.selectedSourceEvents(filteredSources)
	selectedRunOutput := m.selectedRunOutput(filteredActiveRuns)
	b.WriteString("Maestro MVP\n\n")
	b.WriteString(fmt.Sprintf(
		"Overview: sources=%d active=%d approvals=%d messages=%d retries=%d focus=%s run-sort=%s retry-sort=%s view=%s quick=%s\n",
		len(filteredSources),
		len(filteredActiveRuns),
		len(filteredApprovals),
		len(filteredMessages),
		len(filteredRetries),
		m.focus,
		m.runSort,
		m.retrySort,
		compactLabel(m.compact),
		m.quickFilter,
	))
	if m.snapshot.SourceName != "" {
		b.WriteString(fmt.Sprintf("Snapshot source: %s\n", m.snapshot.SourceName))
	}
	if !m.snapshot.LastPollAt.IsZero() {
		b.WriteString(fmt.Sprintf("Last poll: %s (%d issues)\n", m.snapshot.LastPollAt.Format(time.RFC3339), m.snapshot.LastPollCount))
	}
	b.WriteString(fmt.Sprintf("Claimed issues: %d\n\n", m.snapshot.ClaimedCount))
	if m.notice != "" {
		b.WriteString(fmt.Sprintf("Notice: %s\n\n", m.notice))
	}
	if filters := m.filterSummary(); filters != "" {
		b.WriteString(fmt.Sprintf("Filters: %s\n\n", filters))
	}
	if len(filteredSources) > 0 {
		b.WriteString("Source status:\n")
		selectedSourceName := m.selectedSourceName(filteredSources)
		for _, group := range groupSourceSummaries(filteredSources) {
			b.WriteString(fmt.Sprintf("  %s:\n", group.Name))
			for _, summary := range group.Sources {
				marker := " "
				if summary.Name == selectedSourceName && m.focus == focusSources {
					marker = ">"
				}
				health := sourceHealth(summary, m.snapshot.RecentEvents)
				lastPoll := "never"
				if !summary.LastPollAt.IsZero() {
					lastPoll = summary.LastPollAt.Format("15:04:05")
				}
				tagText := ""
				if len(summary.Tags) > 0 {
					tagText = fmt.Sprintf(" tags=%s", strings.Join(summary.Tags, ","))
				}
				if m.compact {
					b.WriteString(fmt.Sprintf("   %s [%s] %s [%s] c=%d a=%d r=%d p=%d m=%d last=%s%s\n",
						marker,
						health,
						summary.Name,
						summary.Tracker,
						summary.ClaimedCount,
						summary.ActiveRunCount,
						summary.RetryCount,
						summary.PendingApprovals,
						summary.PendingMessages,
						lastPoll,
						tagText,
					))
					continue
				}
				b.WriteString(fmt.Sprintf("   %s [%s] %s [%s]%s polled=%d last=%s claimed=%d active=%d retry=%d approvals=%d messages=%d\n",
					marker,
					health,
					summary.Name,
					summary.Tracker,
					tagText,
					summary.LastPollCount,
					lastPoll,
					summary.ClaimedCount,
					summary.ActiveRunCount,
					summary.RetryCount,
					summary.PendingApprovals,
					summary.PendingMessages,
				))
			}
		}
		selected := filteredSources[m.selectedSource]
		b.WriteString("\nSelected source:\n")
		b.WriteString(fmt.Sprintf("  Name: %s\n", selected.Name))
		b.WriteString(fmt.Sprintf("  Health: %s\n", sourceHealth(selected, m.snapshot.RecentEvents)))
		b.WriteString(fmt.Sprintf("  Tracker: %s\n", selected.Tracker))
		group := selected.DisplayGroup
		if strings.TrimSpace(group) == "" {
			group = selected.Tracker
		}
		b.WriteString(fmt.Sprintf("  Group: %s\n", group))
		if len(selected.Tags) > 0 {
			b.WriteString(fmt.Sprintf("  Tags: %s\n", strings.Join(selected.Tags, ", ")))
		}
		if !selected.LastPollAt.IsZero() {
			b.WriteString(fmt.Sprintf("  Last poll: %s\n", selected.LastPollAt.Format(time.RFC3339)))
		}
		b.WriteString(fmt.Sprintf("  Last poll count: %d\n", selected.LastPollCount))
		b.WriteString(fmt.Sprintf("  Claimed: %d\n", selected.ClaimedCount))
		b.WriteString(fmt.Sprintf("  Active runs: %d\n", selected.ActiveRunCount))
		b.WriteString(fmt.Sprintf("  Retries: %d\n", selected.RetryCount))
		b.WriteString(fmt.Sprintf("  Pending approvals: %d\n", selected.PendingApprovals))
		b.WriteString(fmt.Sprintf("  Pending messages: %d\n", selected.PendingMessages))
		b.WriteString(fmt.Sprintf("  Visible active runs: %d\n", countRunsForSource(filteredActiveRuns, selected.Name)))
		b.WriteString(fmt.Sprintf("  Visible retries: %d\n", countRetriesForSource(filteredRetries, selected.Name)))
		b.WriteString("\nSelected source events:\n")
		if len(selectedSourceEvents) == 0 {
			b.WriteString("  none\n")
		} else {
			for _, event := range selectedSourceEvents {
				context := eventContextSummary(event)
				if context != "" {
					b.WriteString(fmt.Sprintf("  [%s] %s %s %s\n", event.Level, event.Time.Format("15:04:05"), context, event.Message))
					continue
				}
				b.WriteString(fmt.Sprintf("  [%s] %s %s\n", event.Level, event.Time.Format("15:04:05"), event.Message))
			}
		}
		b.WriteString("\n")
	}
	if len(filteredMessages) > 0 {
		b.WriteString("Pending messages:\n")
		for i, message := range filteredMessages {
			marker := " "
			if i == m.selectedMessage && m.focus == focusMessages {
				marker = ">"
			}
			b.WriteString(fmt.Sprintf(" %s %s on %s [%s] %s ago\n", marker, messageLabel(message.Kind), message.IssueIdentifier, message.AgentName, timeAgo(message.RequestedAt)))
		}
		selected := filteredMessages[m.selectedMessage]
		b.WriteString("\nSelected message:\n")
		b.WriteString(fmt.Sprintf("  Request: %s\n", selected.RequestID))
		b.WriteString(fmt.Sprintf("  Kind: %s\n", messageLabel(selected.Kind)))
		b.WriteString(fmt.Sprintf("  Run: %s\n", selected.RunID))
		if selected.AgentName != "" {
			b.WriteString(fmt.Sprintf("  Agent: %s\n", selected.AgentName))
		}
		if selected.IssueIdentifier != "" {
			b.WriteString(fmt.Sprintf("  Issue: %s\n", selected.IssueIdentifier))
		}
		if selected.Summary != "" {
			b.WriteString(fmt.Sprintf("  Summary: %s\n", selected.Summary))
		}
		if selected.Body != "" {
			b.WriteString(fmt.Sprintf("  Details:\n%s\n", indentBlock(strings.TrimSpace(selected.Body), "    ")))
		}
		if m.replyMode && m.focus == focusMessages {
			b.WriteString(fmt.Sprintf("  Reply: %s_\n", m.replyInput))
		}
		b.WriteString("\n")
	}
	if len(filteredApprovals) > 0 {
		b.WriteString("Pending approvals:\n")
		for i, approval := range filteredApprovals {
			marker := " "
			if i == m.selectedApproval && m.focus == focusApprovals {
				marker = ">"
			}
			b.WriteString(fmt.Sprintf(" %s %s on %s [%s] %s ago\n", marker, approval.ToolName, approval.IssueIdentifier, approval.ApprovalPolicy, timeAgo(approval.RequestedAt)))
		}
		if len(filteredApprovals) > 0 {
			selected := filteredApprovals[m.selectedApproval]
			b.WriteString("\nSelected approval:\n")
			b.WriteString(fmt.Sprintf("  Request: %s\n", selected.RequestID))
			b.WriteString(fmt.Sprintf("  Run: %s\n", selected.RunID))
			if selected.AgentName != "" {
				b.WriteString(fmt.Sprintf("  Agent: %s\n", selected.AgentName))
			}
			if selected.IssueIdentifier != "" {
				b.WriteString(fmt.Sprintf("  Issue: %s\n", selected.IssueIdentifier))
			}
			b.WriteString(fmt.Sprintf("  Policy: %s\n", selected.ApprovalPolicy))
			if selected.ToolInput != "" {
				b.WriteString(fmt.Sprintf("  Details:\n%s\n", indentBlock(strings.TrimSpace(selected.ToolInput), "    ")))
			}
			b.WriteString("\n")
		}
	}

	if len(filteredRetries) == 0 {
		b.WriteString("Retries: none\n")
	} else {
		b.WriteString(fmt.Sprintf("Retries (sort=%s):\n", m.retrySort))
		for i, retry := range filteredRetries {
			marker := " "
			if i == m.selectedRetry && m.focus == focusRetries {
				marker = ">"
			}
			if m.compact {
				b.WriteString(fmt.Sprintf(" %s %s src=%s attempt=%d due=%s\n", marker, retry.IssueIdentifier, retry.SourceName, retry.Attempt, dueIn(retry.DueAt)))
				continue
			}
			b.WriteString(fmt.Sprintf(" %s %s attempt=%d due=%s\n", marker, retry.IssueIdentifier, retry.Attempt, retry.DueAt.Format(time.RFC3339)))
		}
		selected := filteredRetries[m.selectedRetry]
		b.WriteString("\nSelected retry:\n")
		b.WriteString(fmt.Sprintf("  Source: %s\n", selected.SourceName))
		b.WriteString(fmt.Sprintf("  Issue: %s\n", selected.IssueIdentifier))
		b.WriteString(fmt.Sprintf("  Attempt: %d\n", selected.Attempt))
		b.WriteString(fmt.Sprintf("  Due: %s (%s)\n", selected.DueAt.Format(time.RFC3339), dueIn(selected.DueAt)))
		if selected.Error != "" {
			b.WriteString(fmt.Sprintf("  Error: %s\n", selected.Error))
		}
		b.WriteString("\n")
	}

	if len(filteredActiveRuns) == 0 {
		b.WriteString("Active runs: none\n")
	} else {
		b.WriteString(fmt.Sprintf("Active runs (sort=%s):\n", m.runSort))
		for i, run := range filteredActiveRuns {
			marker := " "
			if i == m.selectedRun && m.focus == focusRuns {
				marker = ">"
			}
			title := strings.TrimSpace(run.Issue.Title)
			if title == "" {
				title = "(untitled)"
			}
			if m.compact {
				b.WriteString(fmt.Sprintf(" %s [%s] %s on %s src=%s idle=%s\n",
					marker,
					runStatusBadge(run),
					run.AgentName,
					run.Issue.Identifier,
					run.SourceName,
					runIdle(run),
				))
				continue
			}
			b.WriteString(fmt.Sprintf(" %s [%s] %s on %s [%s]\n", marker, runStatusBadge(run), run.AgentName, run.Issue.Identifier, run.Status))
			b.WriteString(fmt.Sprintf("    Source: %s | Title: %s\n", run.SourceName, title))
			b.WriteString(fmt.Sprintf("    Approval: %s/%s | Idle: %s\n", run.ApprovalPolicy, run.ApprovalState, runIdle(run)))
			if run.WorkspacePath != "" {
				b.WriteString(fmt.Sprintf("    Workspace: %s\n", run.WorkspacePath))
			}
			if run.Error != "" {
				b.WriteString(fmt.Sprintf("    Error: %s\n", run.Error))
			}
		}
		selected := filteredActiveRuns[m.selectedRun]
		b.WriteString("\nSelected run:\n")
		b.WriteString(fmt.Sprintf("  Run: %s\n", selected.ID))
		b.WriteString(fmt.Sprintf("  Agent: %s (%s)\n", selected.AgentName, selected.AgentType))
		b.WriteString(fmt.Sprintf("  Harness: %s\n", selected.HarnessKind))
		b.WriteString(fmt.Sprintf("  Source: %s\n", selected.SourceName))
		b.WriteString(fmt.Sprintf("  Issue: %s\n", selected.Issue.Identifier))
		if strings.TrimSpace(selected.Issue.Title) != "" {
			b.WriteString(fmt.Sprintf("  Title: %s\n", selected.Issue.Title))
		}
		if strings.TrimSpace(selected.Issue.URL) != "" {
			b.WriteString(fmt.Sprintf("  URL: %s\n", selected.Issue.URL))
		}
		b.WriteString(fmt.Sprintf("  Status: %s\n", selected.Status))
		b.WriteString(fmt.Sprintf("  Attempt: %d\n", selected.Attempt))
		b.WriteString(fmt.Sprintf("  Approval: %s/%s\n", selected.ApprovalPolicy, selected.ApprovalState))
		if !selected.StartedAt.IsZero() {
			b.WriteString(fmt.Sprintf("  Started: %s (%s ago)\n", selected.StartedAt.Format(time.RFC3339), timeAgo(selected.StartedAt)))
		}
		if !selected.LastActivityAt.IsZero() {
			b.WriteString(fmt.Sprintf("  Last activity: %s (%s ago)\n", selected.LastActivityAt.Format(time.RFC3339), timeAgo(selected.LastActivityAt)))
		}
		if !selected.CompletedAt.IsZero() {
			b.WriteString(fmt.Sprintf("  Completed: %s\n", selected.CompletedAt.Format(time.RFC3339)))
		}
		if selected.WorkspacePath != "" {
			b.WriteString(fmt.Sprintf("  Workspace: %s\n", selected.WorkspacePath))
		}
		if selected.Error != "" {
			b.WriteString(fmt.Sprintf("  Error: %s\n", selected.Error))
		}
		b.WriteString("\nSelected run output:\n")
		if strings.TrimSpace(selectedRunOutput.StdoutTail) == "" && strings.TrimSpace(selectedRunOutput.StderrTail) == "" {
			b.WriteString("  none\n")
		} else {
			if strings.TrimSpace(selectedRunOutput.StdoutTail) != "" {
				b.WriteString("  Stdout:\n")
				b.WriteString(indentBlock(strings.TrimSpace(selectedRunOutput.StdoutTail), "    "))
				b.WriteString("\n")
			}
			if strings.TrimSpace(selectedRunOutput.StderrTail) != "" {
				b.WriteString("  Stderr:\n")
				b.WriteString(indentBlock(strings.TrimSpace(selectedRunOutput.StderrTail), "    "))
				b.WriteString("\n")
			}
		}
		b.WriteString("\nSelected run events:\n")
		if len(selectedRunEvents) == 0 {
			b.WriteString("  none\n")
		} else {
			for _, event := range selectedRunEvents {
				b.WriteString(fmt.Sprintf("  [%s] %s %s\n", event.Level, event.Time.Format("15:04:05"), event.Message))
			}
		}
	}

	b.WriteString("\nApproval history:\n")
	if len(filteredHistory) == 0 {
		b.WriteString("  none\n")
	} else {
		for _, entry := range filteredHistory {
			b.WriteString(fmt.Sprintf("  %s %s on %s (%s)\n", entry.Decision, entry.ToolName, entry.IssueIdentifier, entry.Outcome))
		}
	}

	b.WriteString("\nMessage history:\n")
	historyCount := 0
	for _, entry := range m.snapshot.MessageHistory {
		if !m.matchesSearch(entry.AgentName, entry.IssueIdentifier, entry.Summary, entry.Reply, entry.Outcome) {
			continue
		}
		via := strings.TrimSpace(entry.ResolvedVia)
		if via == "" {
			via = "operator"
		}
		b.WriteString(fmt.Sprintf("  %s on %s (%s via %s)\n", messageLabel(entry.Kind), entry.IssueIdentifier, entry.Outcome, via))
		historyCount++
	}
	if historyCount == 0 {
		b.WriteString("  none\n")
	}

	b.WriteString("\nRecent events:\n")
	if len(filteredEvents) == 0 {
		b.WriteString("  none\n")
	} else {
		for _, event := range filteredEvents {
			context := eventContextSummary(event)
			if context != "" {
				b.WriteString(fmt.Sprintf("  [%s] %s %s %s\n", event.Level, event.Time.Format("15:04:05"), context, event.Message))
				continue
			}
			b.WriteString(fmt.Sprintf("  [%s] %s %s\n", event.Level, event.Time.Format("15:04:05"), event.Message))
		}
	}

	if m.searchMode {
		b.WriteString(fmt.Sprintf("\nSearch: %s_\n", m.searchQuery))
	}
	b.WriteString("\nKeys: tab change focus, j/k move, a approve, r reject, e reply to message, s send start, / search, f cycle group, u attention filter, w awaiting filter, c clear filters, o run sort, O retry sort, v compact, q quit.\n")
	return b.String()
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m runSortMode) next() runSortMode {
	order := []runSortMode{runSortStallRisk, runSortApprovalFirst, runSortOldest, runSortNewest}
	for i, item := range order {
		if item == m {
			return order[(i+1)%len(order)]
		}
	}
	return order[0]
}

func (m retrySortMode) next() retrySortMode {
	order := []retrySortMode{retrySortDueSoonest, retrySortOverdue, retrySortAttempts}
	for i, item := range order {
		if item == m {
			return order[(i+1)%len(order)]
		}
	}
	return order[0]
}

func (m quickFilterMode) toggle(target quickFilterMode) quickFilterMode {
	if m == target {
		return quickFilterAll
	}
	return target
}

func (m *Model) clampSelection() {
	m.normalizeFocus()
	sources := m.filteredSourceSummaries()
	pending := m.filteredPendingApprovals()
	messages := m.filteredPendingMessages()
	retries := m.filteredRetries()
	runs := m.filteredActiveRuns()
	if len(sources) == 0 {
		m.selectedSource = 0
	} else {
		if m.selectedSource >= len(sources) {
			m.selectedSource = len(sources) - 1
		}
		if m.selectedSource < 0 {
			m.selectedSource = 0
		}
	}
	if len(pending) == 0 {
		m.selectedApproval = 0
	} else {
		if m.selectedApproval >= len(pending) {
			m.selectedApproval = len(pending) - 1
		}
		if m.selectedApproval < 0 {
			m.selectedApproval = 0
		}
	}
	if len(messages) == 0 {
		m.selectedMessage = 0
	} else {
		if m.selectedMessage >= len(messages) {
			m.selectedMessage = len(messages) - 1
		}
		if m.selectedMessage < 0 {
			m.selectedMessage = 0
		}
	}
	if len(runs) == 0 {
		m.selectedRun = 0
	} else {
		if m.selectedRun >= len(runs) {
			m.selectedRun = len(runs) - 1
		}
		if m.selectedRun < 0 {
			m.selectedRun = 0
		}
	}
	if len(retries) == 0 {
		m.selectedRetry = 0
	} else {
		if m.selectedRetry >= len(retries) {
			m.selectedRetry = len(retries) - 1
		}
		if m.selectedRetry < 0 {
			m.selectedRetry = 0
		}
	}
}

func (m Model) filteredSourceSummaries() []orchestrator.SourceSummary {
	out := make([]orchestrator.SourceSummary, 0, len(m.snapshot.SourceSummaries))
	for _, summary := range m.snapshot.SourceSummaries {
		if !m.matchesSourceGroup(summary) || !m.matchesSourceSearch(summary) || !m.matchesQuickFilterSource(summary) {
			continue
		}
		out = append(out, summary)
	}
	return out
}

func (m Model) filteredActiveRuns() []domain.AgentRun {
	visibleSources := m.visibleSourceNames()
	out := make([]domain.AgentRun, 0, len(m.snapshot.ActiveRuns))
	for _, run := range m.snapshot.ActiveRuns {
		if len(visibleSources) > 0 {
			if _, ok := visibleSources[run.SourceName]; !ok {
				continue
			}
		} else if m.groupFilter != "" {
			continue
		}
		if !m.matchesQuickFilterRun(run) {
			continue
		}
		if !m.matchesSearch(run.SourceName, run.AgentName, run.Issue.Identifier, run.Issue.Title, run.Error) {
			continue
		}
		out = append(out, run)
	}
	sortActiveRuns(out, m.runSort)
	return out
}

func (m Model) filteredPendingApprovals() []orchestrator.ApprovalView {
	out := make([]orchestrator.ApprovalView, 0, len(m.snapshot.PendingApprovals))
	for _, approval := range m.snapshot.PendingApprovals {
		if !m.matchesSearch(approval.AgentName, approval.IssueIdentifier, approval.ToolName, approval.ApprovalPolicy, approval.ToolInput) {
			continue
		}
		out = append(out, approval)
	}
	return out
}

func (m Model) filteredPendingMessages() []orchestrator.MessageView {
	out := make([]orchestrator.MessageView, 0, len(m.snapshot.PendingMessages))
	visibleSources := m.visibleSourceNames()
	for _, message := range m.snapshot.PendingMessages {
		if len(visibleSources) > 0 {
			if sourceName, ok := sourceNameForMessage(m.snapshot.ActiveRuns, message); ok {
				if _, visible := visibleSources[sourceName]; !visible {
					continue
				}
			}
		} else if m.groupFilter != "" {
			continue
		}
		if !m.matchesQuickFilterMessage(message) {
			continue
		}
		if !m.matchesSearch(message.AgentName, message.IssueIdentifier, message.Summary, message.Body, message.Kind) {
			continue
		}
		out = append(out, message)
	}
	return out
}

func (m Model) filteredRetries() []orchestrator.RetryView {
	out := make([]orchestrator.RetryView, 0, len(m.snapshot.Retries))
	visibleSources := m.visibleSourceNames()
	for _, retry := range m.snapshot.Retries {
		if len(visibleSources) > 0 {
			if _, ok := visibleSources[retry.SourceName]; !ok {
				continue
			}
		} else if m.groupFilter != "" {
			continue
		}
		if !m.matchesQuickFilterRetry(retry) {
			continue
		}
		if !m.matchesSearch(retry.SourceName, retry.IssueIdentifier, retry.Error) {
			continue
		}
		out = append(out, retry)
	}
	sortRetries(out, m.retrySort)
	return out
}

func (m Model) filteredApprovalHistory() []orchestrator.ApprovalHistoryEntry {
	out := make([]orchestrator.ApprovalHistoryEntry, 0, len(m.snapshot.ApprovalHistory))
	for _, entry := range m.snapshot.ApprovalHistory {
		if !m.matchesSearch(entry.AgentName, entry.IssueIdentifier, entry.ToolName, entry.Decision, entry.Outcome) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func (m Model) filteredEvents() []orchestrator.Event {
	if strings.TrimSpace(m.searchQuery) == "" {
		return m.snapshot.RecentEvents
	}
	out := make([]orchestrator.Event, 0, len(m.snapshot.RecentEvents))
	for _, event := range m.snapshot.RecentEvents {
		if m.matchesSearch(event.Level, event.Message) {
			out = append(out, event)
		}
	}
	return out
}

func (m Model) selectedRunEvents(runs []domain.AgentRun) []orchestrator.Event {
	if len(runs) == 0 || m.selectedRun >= len(runs) {
		return nil
	}
	selected := runs[m.selectedRun]
	out := make([]orchestrator.Event, 0, len(m.snapshot.RecentEvents))
	for _, event := range m.snapshot.RecentEvents {
		if event.RunID != "" && event.RunID == selected.ID {
			out = append(out, event)
			continue
		}
		if event.Issue != "" && event.Issue == selected.Issue.Identifier {
			out = append(out, event)
			continue
		}
	}
	return out
}

func (m Model) selectedRunOutput(runs []domain.AgentRun) orchestrator.RunOutputView {
	if len(runs) == 0 || m.selectedRun >= len(runs) {
		return orchestrator.RunOutputView{}
	}
	selected := runs[m.selectedRun]
	for _, output := range m.snapshot.RunOutputs {
		if output.RunID == selected.ID {
			return output
		}
	}
	return orchestrator.RunOutputView{}
}

func (m Model) selectedSourceEvents(summaries []orchestrator.SourceSummary) []orchestrator.Event {
	if len(summaries) == 0 || m.selectedSource >= len(summaries) {
		return nil
	}
	selected := summaries[m.selectedSource]
	out := make([]orchestrator.Event, 0, len(m.snapshot.RecentEvents))
	for _, event := range m.snapshot.RecentEvents {
		if event.Source == selected.Name {
			out = append(out, event)
		}
	}
	return out
}

func (m Model) selectedSourceName(summaries []orchestrator.SourceSummary) string {
	if len(summaries) == 0 || m.selectedSource >= len(summaries) {
		return ""
	}
	return summaries[m.selectedSource].Name
}

func (m Model) visibleSourceNames() map[string]struct{} {
	visible := map[string]struct{}{}
	for _, summary := range m.filteredSourceSummaries() {
		visible[summary.Name] = struct{}{}
	}
	return visible
}

func (m Model) matchesSourceGroup(summary orchestrator.SourceSummary) bool {
	if strings.TrimSpace(m.groupFilter) == "" {
		return true
	}
	group := strings.TrimSpace(summary.DisplayGroup)
	if group == "" {
		group = summary.Tracker
	}
	return strings.EqualFold(group, m.groupFilter)
}

func (m Model) matchesSourceSearch(summary orchestrator.SourceSummary) bool {
	parts := []string{summary.Name, summary.DisplayGroup, summary.Tracker}
	parts = append(parts, summary.Tags...)
	return m.matchesSearch(parts...)
}

func (m Model) matchesQuickFilterSource(summary orchestrator.SourceSummary) bool {
	switch m.quickFilter {
	case quickFilterAttention:
		health := sourceHealth(summary, m.snapshot.RecentEvents)
		return health == "ERROR" || health == "RETRY" || health == "WARN" || health == "WAIT"
	case quickFilterAwaiting:
		return summary.PendingApprovals > 0 || summary.PendingMessages > 0
	default:
		return true
	}
}

func (m Model) matchesQuickFilterRun(run domain.AgentRun) bool {
	switch m.quickFilter {
	case quickFilterAttention:
		return run.ApprovalState == domain.ApprovalStateAwaiting || run.Status == domain.RunStatusAwaiting || strings.TrimSpace(run.Error) != ""
	case quickFilterAwaiting:
		return run.ApprovalState == domain.ApprovalStateAwaiting || run.Status == domain.RunStatusAwaiting
	default:
		return true
	}
}

func (m Model) matchesQuickFilterRetry(retry orchestrator.RetryView) bool {
	switch m.quickFilter {
	case quickFilterAwaiting:
		return false
	default:
		return true
	}
}

func (m Model) matchesQuickFilterMessage(message orchestrator.MessageView) bool {
	switch m.quickFilter {
	case quickFilterAttention, quickFilterAwaiting:
		return true
	default:
		return true
	}
}

func (m Model) matchesSearch(parts ...string) bool {
	query := strings.ToLower(strings.TrimSpace(m.searchQuery))
	if query == "" {
		return true
	}
	for _, part := range parts {
		if strings.Contains(strings.ToLower(part), query) {
			return true
		}
	}
	return false
}

func (m Model) filterSummary() string {
	parts := make([]string, 0, 2)
	if strings.TrimSpace(m.groupFilter) != "" {
		parts = append(parts, "group="+m.groupFilter)
	}
	if strings.TrimSpace(m.searchQuery) != "" {
		parts = append(parts, "search="+m.searchQuery)
	}
	if m.quickFilter != quickFilterAll {
		parts = append(parts, "quick="+string(m.quickFilter))
	}
	return strings.Join(parts, " ")
}

func (m *Model) normalizeFocus() {
	hasSources := len(m.filteredSourceSummaries()) > 0
	hasRuns := len(m.filteredActiveRuns()) > 0
	hasMessages := len(m.filteredPendingMessages()) > 0
	hasRetries := len(m.filteredRetries()) > 0
	hasApprovals := len(m.filteredPendingApprovals()) > 0
	switch {
	case m.focus == focusSources && hasSources:
		return
	case m.focus == focusMessages && hasMessages:
		return
	case m.focus == focusApprovals && hasApprovals:
		return
	case m.focus == focusRetries && hasRetries:
		return
	case m.focus == focusRuns && hasRuns:
		return
	case hasSources:
		m.focus = focusSources
	case hasRuns:
		m.focus = focusRuns
	case hasMessages:
		m.focus = focusMessages
	case hasRetries:
		m.focus = focusRetries
	case hasApprovals:
		m.focus = focusApprovals
	default:
		m.focus = focusRuns
	}
}

func (m Model) nextFocus() focusPane {
	hasSources := len(m.filteredSourceSummaries()) > 0
	hasRuns := len(m.filteredActiveRuns()) > 0
	hasMessages := len(m.filteredPendingMessages()) > 0
	hasRetries := len(m.filteredRetries()) > 0
	hasApprovals := len(m.filteredPendingApprovals()) > 0
	options := make([]focusPane, 0, 5)
	if hasSources {
		options = append(options, focusSources)
	}
	if hasRuns {
		options = append(options, focusRuns)
	}
	if hasMessages {
		options = append(options, focusMessages)
	}
	if hasRetries {
		options = append(options, focusRetries)
	}
	if hasApprovals {
		options = append(options, focusApprovals)
	}
	if len(options) == 0 {
		return focusRuns
	}
	for i, option := range options {
		if option == m.focus {
			return options[(i+1)%len(options)]
		}
	}
	return options[0]
}

func indentBlock(raw string, prefix string) string {
	lines := strings.Split(raw, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func compactLabel(compact bool) string {
	if compact {
		return "compact"
	}
	return "expanded"
}

func eventContextSummary(event orchestrator.Event) string {
	parts := make([]string, 0, 3)
	if strings.TrimSpace(event.Source) != "" {
		parts = append(parts, event.Source)
	}
	if strings.TrimSpace(event.Issue) != "" {
		parts = append(parts, event.Issue)
	}
	if strings.TrimSpace(event.RunID) != "" {
		parts = append(parts, "run="+event.RunID)
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func timeAgo(ts time.Time) string {
	if ts.IsZero() {
		return "unknown"
	}
	delta := time.Since(ts).Round(time.Second)
	if delta < time.Second {
		return "just now"
	}
	return delta.String()
}

func runIdle(run domain.AgentRun) string {
	if !run.LastActivityAt.IsZero() {
		return timeAgo(run.LastActivityAt)
	}
	if !run.StartedAt.IsZero() {
		return timeAgo(run.StartedAt)
	}
	return "unknown"
}

func runStatusBadge(run domain.AgentRun) string {
	switch {
	case run.ApprovalState == domain.ApprovalStateAwaiting || run.Status == domain.RunStatusAwaiting:
		return "WAIT"
	case run.Status == domain.RunStatusFailed:
		return "FAIL"
	case run.Status == domain.RunStatusDone:
		return "DONE"
	case run.Status == domain.RunStatusPreparing:
		return "PREP"
	default:
		return "RUN"
	}
}

func dueIn(ts time.Time) string {
	if ts.IsZero() {
		return "unknown"
	}
	delta := time.Until(ts).Round(time.Second)
	switch {
	case delta > 0:
		return "in " + delta.String()
	case delta < 0:
		return fmt.Sprintf("%s ago", (-delta).String())
	default:
		return "now"
	}
}

func messageLabel(kind string) string {
	switch strings.TrimSpace(kind) {
	case "before_work":
		return "before_work gate"
	case "", "agent_message":
		return "agent message"
	default:
		return kind
	}
}

func countRunsForSource(runs []domain.AgentRun, sourceName string) int {
	count := 0
	for _, run := range runs {
		if run.SourceName == sourceName {
			count++
		}
	}
	return count
}

func sourceHealth(summary orchestrator.SourceSummary, events []orchestrator.Event) string {
	switch {
	case sourceHasEventLevel(summary.Name, events, "ERROR"):
		return "ERROR"
	case summary.RetryCount > 0:
		return "RETRY"
	case summary.PendingApprovals > 0 || summary.PendingMessages > 0:
		return "WAIT"
	case summary.ActiveRunCount > 0:
		return "RUN"
	case sourceHasEventLevel(summary.Name, events, "WARN"):
		return "WARN"
	case !summary.LastPollAt.IsZero():
		return "OK"
	default:
		return "IDLE"
	}
}

func sourceHasEventLevel(sourceName string, events []orchestrator.Event, level string) bool {
	for _, event := range events {
		if event.Source == sourceName && strings.EqualFold(event.Level, level) {
			return true
		}
	}
	return false
}

func sortActiveRuns(runs []domain.AgentRun, mode runSortMode) {
	slices.SortFunc(runs, func(a, b domain.AgentRun) int {
		switch mode {
		case runSortNewest:
			return compareTime(b.StartedAt, a.StartedAt, a.Issue.Identifier, b.Issue.Identifier)
		case runSortApprovalFirst:
			aAwait := a.ApprovalState == domain.ApprovalStateAwaiting || a.Status == domain.RunStatusAwaiting
			bAwait := b.ApprovalState == domain.ApprovalStateAwaiting || b.Status == domain.RunStatusAwaiting
			if aAwait != bAwait {
				if aAwait {
					return -1
				}
				return 1
			}
			return compareTime(a.StartedAt, b.StartedAt, a.Issue.Identifier, b.Issue.Identifier)
		case runSortOldest:
			return compareTime(a.StartedAt, b.StartedAt, a.Issue.Identifier, b.Issue.Identifier)
		default:
			aTime := a.LastActivityAt
			if aTime.IsZero() {
				aTime = a.StartedAt
			}
			bTime := b.LastActivityAt
			if bTime.IsZero() {
				bTime = b.StartedAt
			}
			return compareTime(aTime, bTime, a.Issue.Identifier, b.Issue.Identifier)
		}
	})
}

func sortRetries(retries []orchestrator.RetryView, mode retrySortMode) {
	now := time.Now()
	slices.SortFunc(retries, func(a, b orchestrator.RetryView) int {
		switch mode {
		case retrySortAttempts:
			if a.Attempt != b.Attempt {
				if a.Attempt > b.Attempt {
					return -1
				}
				return 1
			}
			return compareTime(a.DueAt, b.DueAt, a.IssueIdentifier, b.IssueIdentifier)
		case retrySortOverdue:
			aOverdue := a.DueAt.Before(now)
			bOverdue := b.DueAt.Before(now)
			if aOverdue != bOverdue {
				if aOverdue {
					return -1
				}
				return 1
			}
			return compareTime(a.DueAt, b.DueAt, a.IssueIdentifier, b.IssueIdentifier)
		default:
			return compareTime(a.DueAt, b.DueAt, a.IssueIdentifier, b.IssueIdentifier)
		}
	})
}

func compareTime(a time.Time, b time.Time, aID string, bID string) int {
	switch {
	case a.Before(b):
		return -1
	case a.After(b):
		return 1
	default:
		return strings.Compare(aID, bID)
	}
}

func countRetriesForSource(retries []orchestrator.RetryView, sourceName string) int {
	count := 0
	for _, retry := range retries {
		if retry.SourceName == sourceName {
			count++
		}
	}
	return count
}

func sourceNameForMessage(runs []domain.AgentRun, message orchestrator.MessageView) (string, bool) {
	for _, run := range runs {
		if run.ID == message.RunID {
			return run.SourceName, true
		}
	}
	return "", false
}

type sourceSummaryGroup struct {
	Name    string
	Sources []orchestrator.SourceSummary
}

func groupSourceSummaries(summaries []orchestrator.SourceSummary) []sourceSummaryGroup {
	grouped := map[string][]orchestrator.SourceSummary{}
	order := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		name := strings.TrimSpace(summary.DisplayGroup)
		if name == "" {
			name = summary.Tracker
		}
		if _, exists := grouped[name]; !exists {
			order = append(order, name)
		}
		grouped[name] = append(grouped[name], summary)
	}
	slices.Sort(order)
	result := make([]sourceSummaryGroup, 0, len(order))
	for _, name := range order {
		items := append([]orchestrator.SourceSummary(nil), grouped[name]...)
		slices.SortFunc(items, func(a, b orchestrator.SourceSummary) int {
			return strings.Compare(a.Name, b.Name)
		})
		result = append(result, sourceSummaryGroup{Name: name, Sources: items})
	}
	return result
}

func nextGroupFilter(summaries []orchestrator.SourceSummary, current string) string {
	options := []string{""}
	seen := map[string]struct{}{}
	for _, summary := range summaries {
		name := strings.TrimSpace(summary.DisplayGroup)
		if name == "" {
			name = summary.Tracker
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		options = append(options, name)
	}
	slices.Sort(options[1:])
	currentKey := strings.ToLower(strings.TrimSpace(current))
	for i, option := range options {
		if strings.ToLower(option) == currentKey {
			return options[(i+1)%len(options)]
		}
	}
	return options[0]
}
