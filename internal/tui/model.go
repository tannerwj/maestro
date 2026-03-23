package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/harness"
	"github.com/tjohnson/maestro/internal/orchestrator"
)

// ---------------------------------------------------------------------------
// Lipgloss styles
// ---------------------------------------------------------------------------

var (
	colorBorder      = lipgloss.Color("240")
	colorTitle       = lipgloss.Color("255")
	colorGreen       = lipgloss.Color("82")
	colorYellow      = lipgloss.Color("220")
	colorRed         = lipgloss.Color("196")
	colorDimGrey     = lipgloss.Color("245")
	colorCyan        = lipgloss.Color("87")
	colorMutedText   = lipgloss.Color("241")
	colorWhite       = lipgloss.Color("255")
	colorFocusBorder = lipgloss.Color("87")

	styleTitle  = lipgloss.NewStyle().Bold(true).Foreground(colorTitle)
	styleDim    = lipgloss.NewStyle().Foreground(colorMutedText)
	styleGreen  = lipgloss.NewStyle().Foreground(colorGreen)
	styleYellow = lipgloss.NewStyle().Foreground(colorYellow)
	styleRed    = lipgloss.NewStyle().Foreground(colorRed)
	styleGrey   = lipgloss.NewStyle().Foreground(colorDimGrey)
	styleCyan   = lipgloss.NewStyle().Foreground(colorCyan)

	maxVisibleEvents = 5
)

func panelStyle(width int, focused bool) lipgloss.Style {
	borderColor := colorBorder
	if focused {
		borderColor = colorFocusBorder
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(width - 2). // -2 for border chars
		PaddingLeft(1).
		PaddingRight(1)
}

func panelTitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(colorTitle)
}

// statusStyle returns the appropriate color style for a status string.
func statusStyle(status string) lipgloss.Style {
	switch status {
	case "OK", "RUN", "DONE":
		return styleGreen
	case "WAIT", "RETRY":
		return styleYellow
	case "ERROR", "FAIL":
		return styleRed
	case "IDLE", "PREP":
		return styleGrey
	default:
		return styleDim
	}
}

// ---------------------------------------------------------------------------
// Types and constants (unchanged)
// ---------------------------------------------------------------------------

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

type ModelOption func(*Model)

func WithWebURL(url string) ModelOption {
	return func(m *Model) { m.webURL = url }
}

func WithPollInterval(d time.Duration) ModelOption {
	return func(m *Model) { m.pollInterval = d }
}

func WithShutdown(fn func() error) ModelOption {
	return func(m *Model) { m.shutdown = fn }
}

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
	width            int
	height           int
	webURL           string
	pollInterval     time.Duration
	shutdown         func() error
	shuttingDown     bool
	shutdownComplete bool
	shutdownErr      string
}

type shutdownFinishedMsg struct {
	err error
}

type shutdownExitMsg struct{}

func NewModel(service snapshotProvider, opts ...ModelOption) Model {
	model := Model{
		service:     service,
		snapshot:    service.Snapshot(),
		focus:       focusSources,
		runSort:     runSortStallRisk,
		retrySort:   retrySortDueSoonest,
		quickFilter: quickFilterAll,
		width:       80,
		height:      24,
	}
	for _, opt := range opts {
		opt(&model)
	}
	model.normalizeFocus()
	return model
}

func (m Model) Init() tea.Cmd {
	return tickCmd()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case shutdownFinishedMsg:
		if msg.err != nil {
			m.shutdownErr = msg.err.Error()
			m.notice = "shutdown warning: " + msg.err.Error()
		}
		m.shutdownComplete = true
		return m, shutdownExitCmd()
	case shutdownExitMsg:
		return m, tea.Quit
	case tea.KeyMsg:
		if m.shuttingDown {
			return m, nil
		}
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
			if m.shutdown != nil {
				m.shuttingDown = true
				m.notice = ""
				return m, shutdownCmd(m.shutdown)
			}
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

// ---------------------------------------------------------------------------
// View — lipgloss-powered dashboard
// ---------------------------------------------------------------------------

func (m Model) View() string {
	if m.shuttingDown {
		return m.renderShutdownView()
	}

	w := m.width
	if w < 40 {
		w = 40
	}

	filteredSources := m.filteredSourceSummaries()
	filteredApprovals := m.filteredPendingApprovals()
	filteredMessages := m.filteredPendingMessages()
	filteredRetries := m.filteredRetries()
	filteredActiveRuns := m.filteredActiveRuns()
	filteredEvents := m.filteredEvents()

	sections := make([]string, 0, 10)

	// --- Header panel ---
	sections = append(sections, m.renderHeader(w, filteredSources, filteredActiveRuns, filteredRetries))

	// --- Notice ---
	if m.notice != "" {
		notice := styleYellow.Render("  " + m.notice)
		sections = append(sections, notice)
	}

	// --- Filters bar ---
	if filters := m.filterSummary(); filters != "" {
		sections = append(sections, styleDim.Render("  Filters: "+filters))
	}

	// --- Sources panel ---
	if len(filteredSources) > 0 {
		sections = append(sections, m.renderSourcesPanel(w, filteredSources))

		// Source drill-down when focused
		if m.focus == focusSources {
			sections = append(sections, m.renderSourceDetail(w, filteredSources, filteredActiveRuns, filteredRetries))
		}
	}

	// --- Pending messages (only when present) ---
	if len(filteredMessages) > 0 {
		sections = append(sections, m.renderMessagesPanel(w, filteredMessages))
	}

	// --- Pending approvals (only when present) ---
	if len(filteredApprovals) > 0 {
		sections = append(sections, m.renderApprovalsPanel(w, filteredApprovals))
	}

	// --- Active Runs panel ---
	sections = append(sections, m.renderRunsPanel(w, filteredActiveRuns))

	// Run drill-down when focused
	if m.focus == focusRuns && len(filteredActiveRuns) > 0 {
		sections = append(sections, m.renderRunDetail(w, filteredActiveRuns))
	}

	// --- Retry Queue panel ---
	sections = append(sections, m.renderRetriesPanel(w, filteredRetries))

	// Retry drill-down when focused
	if m.focus == focusRetries && len(filteredRetries) > 0 {
		sections = append(sections, m.renderRetryDetail(w, filteredRetries))
	}

	// --- Events panel ---
	sections = append(sections, m.renderEventsPanel(w, filteredEvents))

	// --- Approval/message history (only when present) ---
	filteredHistory := m.filteredApprovalHistory()
	if len(filteredHistory) > 0 {
		sections = append(sections, m.renderApprovalHistory(w, filteredHistory))
	}
	if msgHistoryLines := m.renderMessageHistory(w); msgHistoryLines != "" {
		sections = append(sections, msgHistoryLines)
	}

	// --- Search bar ---
	if m.searchMode {
		searchLine := styleCyan.Render("  / ") + m.searchQuery + styleDim.Render("_")
		sections = append(sections, searchLine)
	}

	// --- Footer keybindings ---
	sections = append(sections, m.renderFooter())

	return lipgloss.JoinVertical(lipgloss.Left, sections...) + "\n"
}

func (m Model) renderShutdownView() string {
	w := m.width
	if w < 40 {
		w = 40
	}
	h := m.height
	if h < 8 {
		h = 8
	}

	var lines []string
	if m.shutdownComplete {
		lines = append(lines, panelTitleStyle().Render("Shutdown complete."))
		lines = append(lines, "")
		if strings.TrimSpace(m.shutdownErr) != "" {
			lines = append(lines, styleYellow.Render("Completed with warning: "+m.shutdownErr))
		} else {
			lines = append(lines, "Maestro has stopped all runtime components cleanly.")
		}
		lines = append(lines, "Returning to the terminal...")
	} else {
		lines = append(lines, panelTitleStyle().Render("Shutting down Maestro"))
		lines = append(lines, "")
		lines = append(lines, "Stopping runtime components and waiting for active work to exit cleanly.")
		lines = append(lines, "This can take a few seconds if an agent process is still shutting down.")
	}
	if !m.shutdownComplete && strings.TrimSpace(m.shutdownErr) != "" {
		lines = append(lines, "")
		lines = append(lines, styleYellow.Render("Warning: "+m.shutdownErr))
	}

	content := strings.Join(lines, "\n")
	panelWidth := w
	if panelWidth > 80 {
		panelWidth = 80
	}
	panel := panelStyle(panelWidth, true)
	body := panel.Render(content)

	return lipgloss.Place(
		w,
		h,
		lipgloss.Center,
		lipgloss.Center,
		body,
	)
}

// ---------------------------------------------------------------------------
// Render: Header
// ---------------------------------------------------------------------------

func (m Model) renderHeader(w int, sources []orchestrator.SourceSummary, runs []domain.AgentRun, retries []orchestrator.RetryView) string {
	totalSources := len(m.snapshot.SourceSummaries)
	activeSources := 0
	for _, s := range m.snapshot.SourceSummaries {
		if s.ActiveRunCount > 0 || !s.LastPollAt.IsZero() {
			activeSources++
		}
	}

	totalAgents := len(runs)
	retryCount := len(retries)

	line1Parts := []string{
		"Sources: " + styleGreen.Render(fmt.Sprintf("%d/%d", activeSources, totalSources)) + " active",
		"Agents: " + styleGreen.Render(fmt.Sprintf("%d", totalAgents)) + " running",
		"Retries: " + retryCountStyle(retryCount).Render(fmt.Sprintf("%d", retryCount)) + " queued",
	}
	line1 := strings.Join(line1Parts, "    ")

	line2Parts := make([]string, 0, 4)
	if !m.snapshot.LastPollAt.IsZero() && m.pollInterval > 0 {
		nextPoll := time.Until(m.snapshot.LastPollAt.Add(m.pollInterval)).Round(time.Second)
		if nextPoll < 0 {
			line2Parts = append(line2Parts, "Next poll: "+styleYellow.Render("now"))
		} else {
			line2Parts = append(line2Parts, "Next poll: "+styleDim.Render("~"+nextPoll.String()))
		}
	} else if !m.snapshot.LastPollAt.IsZero() {
		line2Parts = append(line2Parts, "Last poll: "+styleDim.Render(timeAgo(m.snapshot.LastPollAt)+" ago"))
	}
	if m.snapshot.ClaimedCount > 0 {
		line2Parts = append(line2Parts, "Claimed: "+styleGreen.Render(fmt.Sprintf("%d", m.snapshot.ClaimedCount)))
	}
	if m.webURL != "" {
		line2Parts = append(line2Parts, "Web: "+styleCyan.Render(m.webURL))
	}
	line2 := strings.Join(line2Parts, "    ")

	content := line1
	if line2 != "" {
		content += "\n" + line2
	}

	panel := panelStyle(w, false)
	title := panelTitleStyle().Render(" MAESTRO ")
	return panel.BorderTop(true).Render(title + "\n" + content)
}

func healthIcon(health string) string {
	switch health {
	case "OK":
		return styleGreen.Render("[OK]")
	case "RUN":
		return styleGreen.Render("[RUN]")
	case "WAIT":
		return styleYellow.Render("[WAIT]")
	case "RETRY":
		return styleYellow.Render("[RETRY]")
	case "ERROR":
		return styleRed.Render("[ERR]")
	case "FAIL":
		return styleRed.Render("[FAIL]")
	case "WARN":
		return styleYellow.Render("[WARN]")
	default:
		return styleGrey.Render("[IDLE]")
	}
}

func retryCountStyle(n int) lipgloss.Style {
	if n > 0 {
		return styleYellow
	}
	return styleGreen
}

// ---------------------------------------------------------------------------
// Render: Sources
// ---------------------------------------------------------------------------

func (m Model) renderSourcesPanel(w int, sources []orchestrator.SourceSummary) string {
	focused := m.focus == focusSources
	var rows []string

	selectedName := m.selectedSourceName(sources)
	for _, group := range groupSourceSummaries(sources) {
		for _, summary := range group.Sources {
			indicator := "  "
			if summary.Name == selectedName && focused {
				indicator = styleCyan.Render("▸ ")
			}

			health := sourceHealth(summary, m.snapshot.RecentEvents)
			healthStr := statusStyle(health).Render(padRight(health, 6))

			dot := styleGreen.Render("●")
			if health == "IDLE" {
				dot = styleDim.Render("○")
			} else if health == "ERROR" {
				dot = styleRed.Render("●")
			} else if health == "WAIT" || health == "RETRY" {
				dot = styleYellow.Render("●")
			}

			nameStr := lipgloss.NewStyle().Width(22).Render(summary.Name)
			trackerStr := styleDim.Render(padRight(summary.Tracker, 12))

			active := styleGreen.Render(fmt.Sprintf("%d", summary.ActiveRunCount)) + " active"
			retry := retryCountStyle(summary.RetryCount).Render(fmt.Sprintf("%d", summary.RetryCount)) + " retry"

			filterStr := ""
			if len(summary.FilterStates) > 0 {
				filterStr += styleDim.Render(" states:") + strings.Join(summary.FilterStates, ",")
			}
			if len(summary.FilterLabels) > 0 {
				filterStr += styleDim.Render(" labels:") + strings.Join(summary.FilterLabels, ",")
			}

			row := fmt.Sprintf("%s%s %s %s  %s  %s  %s%s",
				indicator, dot, nameStr, trackerStr, active, retry, healthStr, filterStr)
			rows = append(rows, row)
		}
	}

	content := strings.Join(rows, "\n")
	if content == "" {
		content = styleDim.Render("No sources")
	}

	panel := panelStyle(w, focused)
	title := panelTitleStyle().Render(" Sources ")
	return panel.Render(title + "\n" + content)
}

// ---------------------------------------------------------------------------
// Render: Source detail drill-down
// ---------------------------------------------------------------------------

func (m Model) renderSourceDetail(w int, sources []orchestrator.SourceSummary, runs []domain.AgentRun, retries []orchestrator.RetryView) string {
	if len(sources) == 0 || m.selectedSource >= len(sources) {
		return ""
	}
	selected := sources[m.selectedSource]
	selectedSourceEvts := m.selectedSourceEvents(sources)
	health := sourceHealth(selected, m.snapshot.RecentEvents)

	var lines []string

	// Compact summary line: name + health icon + tracker + counts
	parts := []string{
		selected.Name,
		healthIcon(health),
		styleDim.Render(selected.Tracker),
		styleDim.Render("active:") + styleGreen.Render(fmt.Sprintf("%d", selected.ActiveRunCount)),
		styleDim.Render("retry:") + retryCountStyle(selected.RetryCount).Render(fmt.Sprintf("%d", selected.RetryCount)),
		styleDim.Render("claimed:") + fmt.Sprintf("%d", selected.ClaimedCount),
	}
	if len(selected.Tags) > 0 {
		parts = append(parts, styleDim.Render("tags:")+strings.Join(selected.Tags, ","))
	}
	lines = append(lines, strings.Join(parts, "  "))
	if strings.TrimSpace(selected.ProjectURL) != "" {
		lines = append(lines, styleDim.Render("Project: ")+styleCyan.Render(selected.ProjectURL))
	}

	// Source events
	if len(selectedSourceEvts) > 0 {
		lines = append(lines, styleDim.Render("Events:"))
		evts := tailSlice(selectedSourceEvts, maxVisibleEvents)
		for _, event := range evts {
			lines = append(lines, renderEventLine(event))
		}
	}

	content := strings.Join(lines, "\n")
	panel := panelStyle(w, false).BorderForeground(colorBorder)
	return panel.Render(styleDim.Render(" Source Detail ") + "\n" + content)
}

// ---------------------------------------------------------------------------
// Render: Active Runs
// ---------------------------------------------------------------------------

func (m Model) renderRunsPanel(w int, runs []domain.AgentRun) string {
	focused := m.focus == focusRuns

	if len(runs) == 0 {
		panel := panelStyle(w, focused)
		title := panelTitleStyle().Render(" Active Runs ")
		return panel.Render(title + "\n" + styleDim.Render("No active runs"))
	}

	// Column widths
	colIssue := 18
	colAgent := 14
	colSource := 22
	colStatus := 6
	colAge := 10
	colIdle := 10

	header := styleDim.Render(
		"  " +
			padRight("ISSUE", colIssue) +
			padRight("AGENT", colAgent) +
			padRight("SOURCE", colSource) +
			padRight("STATUS", colStatus) +
			padRight("AGE", colAge) +
			padRight("IDLE", colIdle))
	divider := styleDim.Render("  " + strings.Repeat("\u2500", colIssue+colAgent+colSource+colStatus+colAge+colIdle))

	var rows []string
	rows = append(rows, header)
	rows = append(rows, divider)

	for i, run := range runs {
		indicator := "  "
		if i == m.selectedRun && focused {
			indicator = styleCyan.Render("▸ ")
		}

		badge := runStatusBadge(run)
		badgeStr := statusStyle(badge).Render(padRight(badge, colStatus))

		issue := padRight(run.Issue.Identifier, colIssue)
		agent := padRight(run.AgentName, colAgent)
		source := padRight(run.SourceName, colSource)
		age := padRight(timeAgo(run.StartedAt), colAge)
		idle := padRight(runIdle(run), colIdle)

		if m.compact {
			row := indicator + issue + agent + source + badgeStr + age + idle
			rows = append(rows, row)
		} else {
			row := indicator + issue + agent + source + badgeStr + age + idle
			rows = append(rows, row)
			// Extra detail line in expanded mode
			title := strings.TrimSpace(run.Issue.Title)
			if title == "" {
				title = "(untitled)"
			}
			detail := styleDim.Render("    " + title)
			if strings.TrimSpace(run.Issue.URL) != "" {
				detail += "  " + styleCyan.Render(run.Issue.URL)
			}
			if run.Error != "" {
				detail += "  " + styleRed.Render(run.Error)
			}
			rows = append(rows, detail)
		}
	}

	content := strings.Join(rows, "\n")
	panel := panelStyle(w, focused)
	title := panelTitleStyle().Render(" Active Runs ")
	sortLabel := styleDim.Render(" sort:" + string(m.runSort))
	return panel.Render(title + sortLabel + "\n" + content)
}

// ---------------------------------------------------------------------------
// Render: Run detail drill-down
// ---------------------------------------------------------------------------

func (m Model) renderRunDetail(w int, runs []domain.AgentRun) string {
	if len(runs) == 0 || m.selectedRun >= len(runs) {
		return ""
	}
	selected := runs[m.selectedRun]
	selectedRunEvts := m.selectedRunEvents(runs)
	selectedOutput := m.selectedRunOutput(runs)

	var lines []string

	// Issue + title on one line.
	issueLine := selected.Issue.Identifier
	if strings.TrimSpace(selected.Issue.Title) != "" {
		issueLine += styleDim.Render(" — ") + selected.Issue.Title
	}
	lines = append(lines, issueLine)
	if strings.TrimSpace(selected.Issue.URL) != "" {
		lines = append(lines, styleCyan.Render(selected.Issue.URL))
	}

	// Agent + harness + source on one line.
	agentLabel := selected.AgentName
	if selected.AgentType != "" && selected.AgentType != selected.AgentName {
		agentLabel += styleDim.Render(" (") + selected.AgentType + styleDim.Render(")")
	}
	lines = append(lines, styleDim.Render("Agent: ")+agentLabel+styleDim.Render("  Harness: ")+selected.HarnessKind+styleDim.Render("  Source: ")+selected.SourceName)

	// Status + attempt on one line.
	badge := runStatusBadge(selected)
	lines = append(lines, styleDim.Render("Status: ")+statusStyle(badge).Render(string(selected.Status))+styleDim.Render("  Attempt: ")+fmt.Sprintf("%d", selected.Attempt)+styleDim.Render("  Approval: ")+selected.ApprovalPolicy+"/"+string(selected.ApprovalState))

	// Started + last activity on one line.
	timeParts := make([]string, 0, 3)
	if !selected.StartedAt.IsZero() {
		timeParts = append(timeParts, styleDim.Render("Started: ")+timeAgo(selected.StartedAt)+" ago")
	}
	if !selected.LastActivityAt.IsZero() {
		timeParts = append(timeParts, styleDim.Render("Last activity: ")+timeAgo(selected.LastActivityAt)+" ago")
	}
	if !selected.CompletedAt.IsZero() {
		timeParts = append(timeParts, styleDim.Render("Completed: ")+selected.CompletedAt.Format(time.RFC3339))
	}
	if len(timeParts) > 0 {
		lines = append(lines, strings.Join(timeParts, "  "))
	}

	// Run ID + workspace.
	metaParts := []string{styleDim.Render("Run: ") + selected.ID}
	if selected.WorkspacePath != "" {
		metaParts = append(metaParts, styleDim.Render("Workspace: ")+selected.WorkspacePath)
	}
	lines = append(lines, strings.Join(metaParts, "  "))

	if selected.Error != "" {
		lines = append(lines, styleDim.Render("Error: ")+styleRed.Render(selected.Error))
	}

	// Output — word-wrapped and tailed to fit the panel.
	maxOutputLines := 12
	outputWidth := w - 8
	if outputWidth < 40 {
		outputWidth = 40
	}
	hasStdout := strings.TrimSpace(selectedOutput.StdoutTail) != ""
	hasStderr := strings.TrimSpace(selectedOutput.StderrTail) != ""
	if hasStdout || hasStderr {
		lines = append(lines, "")
		if hasStdout {
			lines = append(lines, styleDim.Render("Stdout:"))
			wrapped := wrapAndTail(strings.TrimSpace(selectedOutput.StdoutTail), outputWidth, maxOutputLines)
			lines = append(lines, indentBlock(wrapped, "  "))
		}
		if hasStderr {
			lines = append(lines, styleDim.Render("Stderr:"))
			wrapped := wrapAndTail(strings.TrimSpace(selectedOutput.StderrTail), outputWidth, maxOutputLines)
			lines = append(lines, indentBlock(wrapped, "  "))
		}
	}

	// Run events
	if len(selectedRunEvts) > 0 {
		lines = append(lines, "")
		lines = append(lines, styleDim.Render("Events:"))
		evts := tailSlice(selectedRunEvts, maxVisibleEvents)
		for _, event := range evts {
			lines = append(lines, renderEventLine(event))
		}
	}

	content := strings.Join(lines, "\n")
	panel := panelStyle(w, false).BorderForeground(colorBorder)
	title := panelTitleStyle().Render(" Run Detail ")
	return panel.Render(title + "\n" + content)
}

// ---------------------------------------------------------------------------
// Render: Retry Queue
// ---------------------------------------------------------------------------

func (m Model) renderRetriesPanel(w int, retries []orchestrator.RetryView) string {
	focused := m.focus == focusRetries

	sortLabel := styleDim.Render(" sort:" + string(m.retrySort))

	if len(retries) == 0 {
		panel := panelStyle(w, focused)
		title := panelTitleStyle().Render(" Retry Queue ")
		return panel.Render(title + sortLabel + "\n" + styleDim.Render("No queued retries"))
	}

	colIssue := 18
	colSource := 22
	colAttempt := 10
	colDue := 16
	colError := 30

	header := styleDim.Render(
		"  " +
			padRight("ISSUE", colIssue) +
			padRight("SOURCE", colSource) +
			padRight("ATTEMPT", colAttempt) +
			padRight("DUE", colDue) +
			padRight("ERROR", colError))
	divider := styleDim.Render("  " + strings.Repeat("\u2500", colIssue+colSource+colAttempt+colDue+colError))

	var rows []string
	rows = append(rows, header)
	rows = append(rows, divider)

	for i, retry := range retries {
		indicator := "  "
		if i == m.selectedRetry && focused {
			indicator = styleCyan.Render("▸ ")
		}

		issue := padRight(retry.IssueIdentifier, colIssue)
		source := padRight(retry.SourceName, colSource)
		attempt := styleYellow.Render(padRight(fmt.Sprintf("%d", retry.Attempt), colAttempt))
		due := padRight(dueIn(retry.DueAt), colDue)
		errStr := ""
		if retry.Error != "" {
			errStr = styleRed.Render(truncate(retry.Error, colError))
		}

		row := indicator + issue + source + attempt + due + errStr
		rows = append(rows, row)
	}

	content := strings.Join(rows, "\n")
	panel := panelStyle(w, focused)
	title := panelTitleStyle().Render(" Retry Queue ")
	return panel.Render(title + sortLabel + "\n" + content)
}

// ---------------------------------------------------------------------------
// Render: Retry detail drill-down
// ---------------------------------------------------------------------------

func (m Model) renderRetryDetail(w int, retries []orchestrator.RetryView) string {
	if len(retries) == 0 || m.selectedRetry >= len(retries) {
		return ""
	}
	selected := retries[m.selectedRetry]

	var lines []string
	lines = append(lines, styleDim.Render("Source: ")+selected.SourceName)
	lines = append(lines, styleDim.Render("Issue: ")+selected.IssueIdentifier)
	lines = append(lines, styleDim.Render("Attempt: ")+styleYellow.Render(fmt.Sprintf("%d", selected.Attempt)))
	lines = append(lines, styleDim.Render("Due: ")+selected.DueAt.Format(time.RFC3339)+styleDim.Render(" (")+dueIn(selected.DueAt)+styleDim.Render(")"))
	if selected.Error != "" {
		lines = append(lines, styleDim.Render("Error: ")+styleRed.Render(selected.Error))
	}

	content := strings.Join(lines, "\n")
	panel := panelStyle(w, false).BorderForeground(colorBorder)
	return panel.Render(styleDim.Render(" Retry Detail ") + "\n" + content)
}

// ---------------------------------------------------------------------------
// Render: Messages
// ---------------------------------------------------------------------------

func (m Model) renderMessagesPanel(w int, messages []orchestrator.MessageView) string {
	focused := m.focus == focusMessages

	var rows []string
	for i, message := range messages {
		indicator := "  "
		if i == m.selectedMessage && focused {
			indicator = styleCyan.Render("▸ ")
		}
		label := messageLabel(message.Kind)
		age := timeAgo(message.RequestedAt) + " ago"
		row := indicator + styleYellow.Render(label) + " on " + message.IssueIdentifier +
			styleDim.Render(" ["+message.AgentName+"] ") + styleDim.Render(age)
		rows = append(rows, row)
	}

	content := strings.Join(rows, "\n")

	// Selected message detail
	if focused && len(messages) > 0 && m.selectedMessage < len(messages) {
		selected := messages[m.selectedMessage]
		content += "\n"
		content += "\n" + styleDim.Render("Request: ") + selected.RequestID
		content += "\n" + styleDim.Render("Kind: ") + messageLabel(selected.Kind)
		content += "\n" + styleDim.Render("Run: ") + selected.RunID
		if selected.AgentName != "" {
			content += "\n" + styleDim.Render("Agent: ") + selected.AgentName
		}
		if selected.IssueIdentifier != "" {
			content += "\n" + styleDim.Render("Issue: ") + selected.IssueIdentifier
		}
		if selected.Summary != "" {
			content += "\n" + styleDim.Render("Summary: ") + selected.Summary
		}
		if selected.Body != "" {
			content += "\n" + styleDim.Render("Details:")
			content += "\n" + indentBlock(strings.TrimSpace(selected.Body), "  ")
		}
		if m.replyMode {
			content += "\n" + styleCyan.Render("Reply: ") + m.replyInput + styleDim.Render("_")
		}
	}

	panel := panelStyle(w, focused)
	title := panelTitleStyle().Render(" Pending Messages ")
	return panel.Render(title + "\n" + content)
}

// ---------------------------------------------------------------------------
// Render: Approvals
// ---------------------------------------------------------------------------

func (m Model) renderApprovalsPanel(w int, approvals []orchestrator.ApprovalView) string {
	focused := m.focus == focusApprovals

	var rows []string
	for i, approval := range approvals {
		indicator := "  "
		if i == m.selectedApproval && focused {
			indicator = styleCyan.Render("▸ ")
		}
		age := timeAgo(approval.RequestedAt) + " ago"
		row := indicator + styleYellow.Render(approval.ToolName) + " on " + approval.IssueIdentifier +
			styleDim.Render(" ["+approval.ApprovalPolicy+"] ") + styleDim.Render(age)
		rows = append(rows, row)
	}

	content := strings.Join(rows, "\n")

	// Selected approval detail
	if focused && len(approvals) > 0 && m.selectedApproval < len(approvals) {
		selected := approvals[m.selectedApproval]
		content += "\n"
		content += "\n" + styleDim.Render("Request: ") + selected.RequestID
		content += "\n" + styleDim.Render("Run: ") + selected.RunID
		if selected.AgentName != "" {
			content += "\n" + styleDim.Render("Agent: ") + selected.AgentName
		}
		if selected.IssueIdentifier != "" {
			content += "\n" + styleDim.Render("Issue: ") + selected.IssueIdentifier
		}
		content += "\n" + styleDim.Render("Policy: ") + selected.ApprovalPolicy
		if selected.ToolInput != "" {
			content += "\n" + styleDim.Render("Details:")
			content += "\n" + indentBlock(strings.TrimSpace(selected.ToolInput), "  ")
		}
	}

	panel := panelStyle(w, focused)
	title := panelTitleStyle().Render(" Pending Approvals ")
	return panel.Render(title + "\n" + content)
}

// ---------------------------------------------------------------------------
// Render: Events
// ---------------------------------------------------------------------------

func (m Model) renderEventsPanel(w int, events []orchestrator.Event) string {
	visible := tailSlice(events, maxVisibleEvents)
	var rows []string
	for _, event := range visible {
		rows = append(rows, renderEventLine(event))
	}

	content := strings.Join(rows, "\n")
	if content == "" {
		content = styleDim.Render("No recent events")
	}
	// Pad to minimum height so the panel doesn't start tiny.
	lines := strings.Count(content, "\n") + 1
	for lines < maxVisibleEvents {
		content += "\n"
		lines++
	}

	panel := panelStyle(w, false)
	title := panelTitleStyle().Render(" Events ")
	return panel.Render(title + "\n" + content)
}

func renderEventLine(event orchestrator.Event) string {
	timeStr := styleDim.Render(event.Time.Format("15:04"))

	var levelStr string
	switch strings.ToUpper(event.Level) {
	case "ERROR":
		levelStr = styleRed.Render("ERROR")
	case "WARN":
		levelStr = styleYellow.Render("WARN ")
	default:
		levelStr = styleDim.Render("INFO ")
	}

	context := eventContextSummary(event)
	msg := event.Message
	if context != "" {
		msg = styleDim.Render(context) + " " + msg
	}

	return "  " + timeStr + "  " + levelStr + "  " + msg
}

// ---------------------------------------------------------------------------
// Render: Approval history (only when present)
// ---------------------------------------------------------------------------

func (m Model) renderApprovalHistory(w int, history []orchestrator.ApprovalHistoryEntry) string {
	var rows []string
	for _, entry := range history {
		rows = append(rows, "  "+entry.Decision+" "+entry.ToolName+" on "+entry.IssueIdentifier+styleDim.Render(" ("+entry.Outcome+")"))
	}
	content := strings.Join(rows, "\n")
	panel := panelStyle(w, false)
	title := panelTitleStyle().Render(" Approval History ")
	return panel.Render(title + "\n" + content)
}

// ---------------------------------------------------------------------------
// Render: Message history (only when present)
// ---------------------------------------------------------------------------

func (m Model) renderMessageHistory(w int) string {
	var rows []string
	for _, entry := range m.snapshot.MessageHistory {
		if !m.matchesSearch(entry.AgentName, entry.IssueIdentifier, entry.Summary, entry.Reply, entry.Outcome) {
			continue
		}
		via := strings.TrimSpace(entry.ResolvedVia)
		if via == "" {
			via = "operator"
		}
		rows = append(rows, "  "+messageLabel(entry.Kind)+" on "+entry.IssueIdentifier+styleDim.Render(" ("+entry.Outcome+" via "+via+")"))
	}
	if len(rows) == 0 {
		return ""
	}
	content := strings.Join(rows, "\n")
	panel := panelStyle(w, false)
	title := panelTitleStyle().Render(" Message History ")
	return panel.Render(title + "\n" + content)
}

// ---------------------------------------------------------------------------
// Render: Footer
// ---------------------------------------------------------------------------

func (m Model) renderFooter() string {
	keys := []string{
		"tab", "focus",
		"j/k", "move",
		"v", "compact",
		"/", "search",
		"o", "sort runs",
		"O", "sort retries",
		"f", "group",
		"c", "clear",
		"q", "quit",
	}

	// Add context-specific keys
	if m.focus == focusApprovals {
		keys = append(keys, "a", "approve", "r", "reject")
	}
	if m.focus == focusMessages {
		keys = append(keys, "e", "reply", "s", "start")
	}

	var parts []string
	for i := 0; i+1 < len(keys); i += 2 {
		parts = append(parts, styleDim.Render(keys[i])+" "+styleDim.Render(keys[i+1]))
	}
	return " " + styleDim.Render(strings.Join(parts, styleDim.Render(" · ")))
}

// ---------------------------------------------------------------------------
// Helpers: string formatting
// ---------------------------------------------------------------------------

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func tailSlice[T any](items []T, n int) []T {
	if len(items) <= n {
		return items
	}
	return items[len(items)-n:]
}

// ---------------------------------------------------------------------------
// Tick command (unchanged)
// ---------------------------------------------------------------------------

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func shutdownCmd(fn func() error) tea.Cmd {
	return func() tea.Msg {
		return shutdownFinishedMsg{err: fn()}
	}
}

func shutdownExitCmd() tea.Cmd {
	return tea.Tick(350*time.Millisecond, func(time.Time) tea.Msg {
		return shutdownExitMsg{}
	})
}

// ---------------------------------------------------------------------------
// Sort mode cycling (unchanged)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Selection clamping (unchanged)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Filtering methods (unchanged)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Filter/search helpers (unchanged)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Focus management (unchanged)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Utility helpers (unchanged)
// ---------------------------------------------------------------------------

// wrapAndTail word-wraps text to width, then returns only the last maxLines lines.
func wrapAndTail(text string, width int, maxLines int) string {
	if width < 10 {
		width = 10
	}
	var wrapped []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, " \t")
		// Filter out noise lines from harness stream events.
		if strings.TrimSpace(line) == "[claude assistant event]" {
			continue
		}
		if len(line) <= width {
			wrapped = append(wrapped, line)
			continue
		}
		// Word-wrap long lines.
		for len(line) > 0 {
			if len(line) <= width {
				wrapped = append(wrapped, line)
				break
			}
			// Find last space before width.
			cut := strings.LastIndex(line[:width], " ")
			if cut <= 0 {
				cut = width // no space — hard break
			}
			wrapped = append(wrapped, line[:cut])
			line = strings.TrimLeft(line[cut:], " ")
		}
	}
	if len(wrapped) > maxLines {
		wrapped = wrapped[len(wrapped)-maxLines:]
	}
	return strings.Join(wrapped, "\n")
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
	parts := make([]string, 0, 2)
	if strings.TrimSpace(event.Source) != "" {
		parts = append(parts, event.Source)
	}
	if strings.TrimSpace(event.Issue) != "" {
		parts = append(parts, event.Issue)
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
