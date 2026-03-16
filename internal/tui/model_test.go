package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/orchestrator"
)

type staticSnapshotProvider struct {
	snapshot orchestrator.Snapshot
}

func (s staticSnapshotProvider) Snapshot() orchestrator.Snapshot {
	return s.snapshot
}

func (s staticSnapshotProvider) ResolveApproval(requestID string, decision string) error {
	return nil
}

func TestViewGroupsSourcesAndShowsTags(t *testing.T) {
	snapshot := orchestrator.Snapshot{
		SourceName: "epic-a, project-a, linear-a",
		SourceSummaries: []orchestrator.SourceSummary{
			{Name: "project-a", DisplayGroup: "Delivery", Tracker: "gitlab", Tags: []string{"backend", "prod"}, LastPollAt: time.Date(2026, 3, 16, 10, 0, 0, 0, time.UTC)},
			{Name: "epic-a", DisplayGroup: "Planning", Tracker: "gitlab-epic", Tags: []string{"platform"}, LastPollAt: time.Date(2026, 3, 16, 10, 0, 1, 0, time.UTC)},
			{Name: "linear-a", Tracker: "linear", Tags: []string{"triage"}, LastPollAt: time.Date(2026, 3, 16, 10, 0, 2, 0, time.UTC)},
		},
	}
	model := Model{
		service:  staticSnapshotProvider{snapshot: snapshot},
		snapshot: snapshot,
	}

	view := model.View()
	for _, want := range []string{
		"Source status:",
		"  Delivery:",
		"    project-a [gitlab] tags=backend,prod",
		"  Planning:",
		"    epic-a [gitlab-epic] tags=platform",
		"  linear:",
		"    linear-a [linear] tags=triage",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestViewAppliesGroupFilterAndSearch(t *testing.T) {
	snapshot := orchestrator.Snapshot{
		SourceName: "epic-a, project-a, linear-a",
		SourceSummaries: []orchestrator.SourceSummary{
			{Name: "project-a", DisplayGroup: "Delivery", Tracker: "gitlab", Tags: []string{"backend"}},
			{Name: "epic-a", DisplayGroup: "Planning", Tracker: "gitlab-epic", Tags: []string{"platform"}},
		},
		ActiveRuns: []domain.AgentRun{
			{AgentName: "coder", SourceName: "project-a", Issue: domain.Issue{Identifier: "team/project#1", Title: "Backend work"}},
			{AgentName: "triage", SourceName: "epic-a", Issue: domain.Issue{Identifier: "team/project#2", Title: "Platform work"}},
		},
	}
	model := Model{
		service:     staticSnapshotProvider{snapshot: snapshot},
		snapshot:    snapshot,
		groupFilter: "Planning",
		searchQuery: "platform",
	}

	view := model.View()
	if strings.Contains(view, "project-a [gitlab]") {
		t.Fatalf("expected filtered view to hide project-a:\n%s", view)
	}
	if !strings.Contains(view, "epic-a [gitlab-epic] tags=platform") {
		t.Fatalf("expected filtered view to show epic-a:\n%s", view)
	}
	if !strings.Contains(view, "Filters: group=Planning search=platform") {
		t.Fatalf("expected filter summary in view:\n%s", view)
	}
}

func TestUpdateCyclesGroupFilterAndSearchMode(t *testing.T) {
	snapshot := orchestrator.Snapshot{
		SourceSummaries: []orchestrator.SourceSummary{
			{Name: "project-a", DisplayGroup: "Delivery", Tracker: "gitlab"},
			{Name: "epic-a", DisplayGroup: "Planning", Tracker: "gitlab-epic"},
		},
	}
	model := NewModel(staticSnapshotProvider{snapshot: snapshot})

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	gotModel := updated.(Model)
	if gotModel.groupFilter != "Delivery" {
		t.Fatalf("group filter after first cycle = %q", gotModel.groupFilter)
	}

	updated, _ = gotModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	gotModel = updated.(Model)
	if !gotModel.searchMode {
		t.Fatal("expected search mode to be enabled")
	}

	updated, _ = gotModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	gotModel = updated.(Model)
	if gotModel.searchQuery != "a" {
		t.Fatalf("search query = %q", gotModel.searchQuery)
	}
}
