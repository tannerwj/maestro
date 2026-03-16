package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/orchestrator"
)

type snapshotProvider interface {
	Snapshot() orchestrator.Snapshot
	ResolveApproval(requestID string, decision string) error
}

type tickMsg time.Time

type Model struct {
	service          snapshotProvider
	snapshot         orchestrator.Snapshot
	selectedApproval int
	notice           string
	searchMode       bool
	searchQuery      string
	groupFilter      string
}

func NewModel(service snapshotProvider) Model {
	return Model{service: service, snapshot: service.Snapshot()}
}

func (m Model) Init() tea.Cmd {
	return tickCmd()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
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
			return m, nil
		case "j", "down":
			pending := m.filteredPendingApprovals()
			if len(pending) > 0 && m.selectedApproval < len(pending)-1 {
				m.selectedApproval++
			}
		case "k", "up":
			if m.selectedApproval > 0 {
				m.selectedApproval--
			}
		case "a":
			pending := m.filteredPendingApprovals()
			if len(pending) > 0 {
				err := m.service.ResolveApproval(pending[m.selectedApproval].RequestID, "approve")
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
				err := m.service.ResolveApproval(pending[m.selectedApproval].RequestID, "reject")
				if err != nil {
					m.notice = "rejection failed: " + err.Error()
				} else {
					m.notice = "rejection sent"
				}
				m.snapshot = m.service.Snapshot()
				m.clampSelection()
			}
		}
	case tickMsg:
		m.snapshot = m.service.Snapshot()
		m.clampSelection()
		return m, tickCmd()
	}
	return m, nil
}

func (m Model) View() string {
	var b strings.Builder
	filteredSources := m.filteredSourceSummaries()
	filteredApprovals := m.filteredPendingApprovals()
	filteredActiveRuns := m.filteredActiveRuns()
	filteredHistory := m.filteredApprovalHistory()
	filteredEvents := m.filteredEvents()
	b.WriteString("Maestro MVP\n\n")
	b.WriteString(fmt.Sprintf("Sources: %s\n", m.snapshot.SourceName))
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
		for _, group := range groupSourceSummaries(filteredSources) {
			b.WriteString(fmt.Sprintf("  %s:\n", group.Name))
			for _, summary := range group.Sources {
				lastPoll := "never"
				if !summary.LastPollAt.IsZero() {
					lastPoll = summary.LastPollAt.Format("15:04:05")
				}
				tagText := ""
				if len(summary.Tags) > 0 {
					tagText = fmt.Sprintf(" tags=%s", strings.Join(summary.Tags, ","))
				}
				b.WriteString(fmt.Sprintf("    %s [%s]%s polled=%d last=%s claimed=%d active=%d retry=%d approvals=%d\n",
					summary.Name,
					summary.Tracker,
					tagText,
					summary.LastPollCount,
					lastPoll,
					summary.ClaimedCount,
					summary.ActiveRunCount,
					summary.RetryCount,
					summary.PendingApprovals,
				))
			}
		}
		b.WriteString("\n")
	}
	if len(filteredApprovals) > 0 {
		b.WriteString("Pending approvals:\n")
		for i, approval := range filteredApprovals {
			marker := " "
			if i == m.selectedApproval {
				marker = ">"
			}
			b.WriteString(fmt.Sprintf(" %s %s on %s [%s] %s ago\n", marker, approval.ToolName, approval.IssueIdentifier, approval.ApprovalPolicy, timeAgo(approval.RequestedAt)))
		}
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

	if len(filteredActiveRuns) == 0 {
		b.WriteString("Active runs: none\n")
	} else {
		b.WriteString("Active runs:\n")
		for _, run := range filteredActiveRuns {
			b.WriteString(fmt.Sprintf("  - %s on %s [%s]\n", run.AgentName, run.Issue.Identifier, run.Status))
			b.WriteString(fmt.Sprintf("    Approval: %s/%s\n", run.ApprovalPolicy, run.ApprovalState))
			if run.WorkspacePath != "" {
				b.WriteString(fmt.Sprintf("    Workspace: %s\n", run.WorkspacePath))
			}
			if run.Error != "" {
				b.WriteString(fmt.Sprintf("    Error: %s\n", run.Error))
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

	b.WriteString("\nRecent events:\n")
	if len(filteredEvents) == 0 {
		b.WriteString("  none\n")
	} else {
		for _, event := range filteredEvents {
			b.WriteString(fmt.Sprintf("  [%s] %s %s\n", event.Level, event.Time.Format("15:04:05"), event.Message))
		}
	}

	if m.searchMode {
		b.WriteString(fmt.Sprintf("\nSearch: %s_\n", m.searchQuery))
	}
	b.WriteString("\nKeys: j/k select approval, a approve, r reject, / search, f cycle group, c clear filters, q quit.\n")
	return b.String()
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *Model) clampSelection() {
	pending := m.filteredPendingApprovals()
	if len(pending) == 0 {
		m.selectedApproval = 0
		return
	}
	if m.selectedApproval >= len(pending) {
		m.selectedApproval = len(pending) - 1
	}
	if m.selectedApproval < 0 {
		m.selectedApproval = 0
	}
}

func (m Model) filteredSourceSummaries() []orchestrator.SourceSummary {
	out := make([]orchestrator.SourceSummary, 0, len(m.snapshot.SourceSummaries))
	for _, summary := range m.snapshot.SourceSummaries {
		if !m.matchesSourceGroup(summary) || !m.matchesSourceSearch(summary) {
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
		if !m.matchesSearch(run.SourceName, run.AgentName, run.Issue.Identifier, run.Issue.Title, run.Error) {
			continue
		}
		out = append(out, run)
	}
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
	return strings.Join(parts, " ")
}

func indentBlock(raw string, prefix string) string {
	lines := strings.Split(raw, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
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
