package workspace_test

import (
	"testing"

	"github.com/tjohnson/maestro/internal/workspace"
)

func TestWorkspaceKey(t *testing.T) {
	got := workspace.WorkspaceKey("team/project#42")
	if got != "team_project_42" {
		t.Fatalf("workspace key = %q", got)
	}
}

func TestBranchName(t *testing.T) {
	got := workspace.BranchName("coder", "team/project#42")
	if got != "maestro/coder/team_project_42" {
		t.Fatalf("branch name = %q", got)
	}
}
