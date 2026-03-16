package workspace_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/workspace"
)

func TestPrepareClonesRepositoryAndCreatesBranch(t *testing.T) {
	repoURL := createSeedRepo(t)
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(root, "workspaces"))

	prepared, err := manager.Prepare(context.Background(), domain.Issue{
		Identifier: "team/project#42",
		Meta: map[string]string{
			"repo_url": repoURL,
		},
	}, "coder")
	if err != nil {
		t.Fatalf("prepare workspace: %v", err)
	}

	head := gitOutput(t, prepared.Path, "branch", "--show-current")
	if head != "maestro/coder/team_project_42" {
		t.Fatalf("branch = %q", head)
	}

	if _, err := os.Stat(filepath.Join(prepared.Path, "README.md")); err != nil {
		t.Fatalf("expected cloned repo contents: %v", err)
	}
}

func createSeedRepo(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	runGitForWorkspace(t, root, "init")
	runGitForWorkspace(t, root, "add", "README.md")
	runGitForWorkspace(t, root, "-c", "user.name=Test User", "-c", "user.email=test@example.com", "commit", "-m", "init")

	bare := filepath.Join(t.TempDir(), "repo.git")
	runGitForWorkspace(t, root, "clone", "--bare", root, bare)
	return bare
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, string(output))
	}
	return strings.TrimSpace(string(output))
}

func runGitForWorkspace(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, string(output))
	}
}
