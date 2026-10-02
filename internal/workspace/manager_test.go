package workspace_test

import (
	"path/filepath"
	"testing"

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/workspace"
)

func TestWorkspaceKey(t *testing.T) {
	got := workspace.WorkspaceKey("team/project#42")
	if got != "team_2Fproject_2342" {
		t.Fatalf("workspace key = %q", got)
	}
}

func TestBranchName(t *testing.T) {
	got := workspace.BranchName("coder", "team/project#42")
	if got != "maestro/coder/team_2Fproject_2342" {
		t.Fatalf("branch name = %q", got)
	}
}

func TestManagerPreviewCloneRequiresRepoURL(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	mgr := workspace.NewManager(root)

	_, err := mgr.PreviewClone(domain.Issue{Identifier: "team/project#42"}, "coder")
	if err == nil {
		t.Fatal("expected missing repo_url error")
	}
}

func TestManagerPreviewClone(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	mgr := workspace.NewManager(root)

	got, err := mgr.PreviewClone(domain.Issue{
		Identifier: "team/project#42",
		Meta: map[string]string{
			"repo_url": "https://example.com/repo.git",
		},
	}, "coder")
	if err != nil {
		t.Fatalf("preview clone: %v", err)
	}
	if got.Path != filepath.Join(root, "team_2Fproject_2342") {
		t.Fatalf("preview path = %q, want %q", got.Path, filepath.Join(root, "team_2Fproject_2342"))
	}
	if got.Branch != "maestro/coder/team_2Fproject_2342" {
		t.Fatalf("preview branch = %q", got.Branch)
	}
}

func TestManagerPreviewEmpty(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspaces")
	mgr := workspace.NewManager(root)

	got, err := mgr.PreviewEmpty(domain.Issue{Identifier: "OPS-42"})
	if err != nil {
		t.Fatalf("preview empty: %v", err)
	}
	if got.Path != filepath.Join(root, "OPS-42") {
		t.Fatalf("preview path = %q, want %q", got.Path, filepath.Join(root, "OPS-42"))
	}
	if got.Branch != "" {
		t.Fatalf("preview branch = %q, want empty", got.Branch)
	}
}
