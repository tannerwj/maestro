package workspace_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestPrepareCloneFailureRemovesStaleWorkspace(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(root, "workspaces"))
	issue := domain.Issue{
		Identifier: "team/project#43",
		Meta: map[string]string{
			"repo_url": filepath.Join(root, "missing.git"),
		},
	}

	stalePath := filepath.Join(root, "workspaces", workspace.WorkspaceKey(issue.Identifier))
	if err := os.MkdirAll(stalePath, 0o755); err != nil {
		t.Fatalf("mkdir stale workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stalePath, "stale.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	_, err := manager.Prepare(context.Background(), issue, "coder")
	if err == nil {
		t.Fatal("expected prepare failure")
	}

	if _, statErr := os.Stat(filepath.Join(stalePath, "stale.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("stale file stat error = %v, want not exists", statErr)
	}
	if _, statErr := os.Stat(stalePath); !os.IsNotExist(statErr) {
		t.Fatalf("workspace path stat error = %v, want not exists", statErr)
	}
}

func TestPrepareBranchFailureCleansUpClonedWorkspace(t *testing.T) {
	repoURL := createSeedRepo(t)
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(root, "workspaces"))
	issue := domain.Issue{
		Identifier: "team/project#44",
		Meta: map[string]string{
			"repo_url": repoURL,
		},
	}

	_, err := manager.Prepare(context.Background(), issue, "")
	if err == nil {
		t.Fatal("expected prepare failure")
	}

	workspacePath := filepath.Join(root, "workspaces", workspace.WorkspaceKey(issue.Identifier))
	if _, statErr := os.Stat(workspacePath); !os.IsNotExist(statErr) {
		t.Fatalf("workspace path stat error = %v, want not exists", statErr)
	}
}

func TestPrepareEmptyCreatesCleanWorkspace(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(root, "workspaces"))
	issue := domain.Issue{Identifier: "OPS-42"}

	first, err := manager.PrepareEmpty(issue)
	if err != nil {
		t.Fatalf("prepare empty workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(first.Path, "stale.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	second, err := manager.PrepareEmpty(issue)
	if err != nil {
		t.Fatalf("prepare empty workspace again: %v", err)
	}
	if second.Path != first.Path {
		t.Fatalf("workspace path = %q, want %q", second.Path, first.Path)
	}

	entries, err := os.ReadDir(second.Path)
	if err != nil {
		t.Fatalf("read workspace: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("workspace entries = %d, want 0", len(entries))
	}
}

func TestPopulateHarnessConfigCopiesClaudeAndCodexDirs(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(root, "workspaces"))
	prepared, err := manager.PrepareEmpty(domain.Issue{Identifier: "OPS-99"})
	if err != nil {
		t.Fatalf("prepare empty workspace: %v", err)
	}

	packRoot := filepath.Join(root, "pack")
	claudeDir := filepath.Join(packRoot, "claude")
	codexDir := filepath.Join(packRoot, "codex")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(codexDir, "skills"), 0o755); err != nil {
		t.Fatalf("mkdir codex skills dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte("claude"), 0o644); err != nil {
		t.Fatalf("write claude file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "skills", "skill.md"), []byte("codex"), 0o644); err != nil {
		t.Fatalf("write codex file: %v", err)
	}

	if err := manager.PopulateHarnessConfig(prepared.Path, claudeDir, codexDir); err != nil {
		t.Fatalf("populate harness config: %v", err)
	}

	if _, err := os.Stat(filepath.Join(prepared.Path, ".claude", "CLAUDE.md")); err != nil {
		t.Fatalf("expected .claude file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(prepared.Path, ".codex", "skills", "skill.md")); err != nil {
		t.Fatalf("expected .codex file: %v", err)
	}
}

func TestPopulateHarnessConfigRejectsExistingDestination(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(root, "workspaces"))
	prepared, err := manager.PrepareEmpty(domain.Issue{Identifier: "OPS-100"})
	if err != nil {
		t.Fatalf("prepare empty workspace: %v", err)
	}

	claudeDir := filepath.Join(root, "pack", "claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte("claude"), 0o644); err != nil {
		t.Fatalf("write claude file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(prepared.Path, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir destination: %v", err)
	}

	err = manager.PopulateHarnessConfig(prepared.Path, claudeDir, "")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("populate error = %v, want existing destination error", err)
	}
}

func TestPopulateHarnessConfigMissingDirsIsNoOp(t *testing.T) {
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(root, "workspaces"))
	prepared, err := manager.PrepareEmpty(domain.Issue{Identifier: "OPS-102"})
	if err != nil {
		t.Fatalf("prepare empty workspace: %v", err)
	}

	if err := manager.PopulateHarnessConfig(prepared.Path, "", ""); err != nil {
		t.Fatalf("populate harness config: %v", err)
	}

	entries, err := os.ReadDir(prepared.Path)
	if err != nil {
		t.Fatalf("read workspace: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("workspace entries = %d, want 0", len(entries))
	}
}

func TestPopulateHarnessConfigRejectsSymlinkSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not reliable on windows test environments")
	}

	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(root, "workspaces"))
	prepared, err := manager.PrepareEmpty(domain.Issue{Identifier: "OPS-101"})
	if err != nil {
		t.Fatalf("prepare empty workspace: %v", err)
	}

	sourceRoot := filepath.Join(root, "real-claude")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		t.Fatalf("mkdir source root: %v", err)
	}
	linkPath := filepath.Join(root, "pack", "claude")
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatalf("mkdir pack dir: %v", err)
	}
	if err := os.Symlink(sourceRoot, linkPath); err != nil {
		t.Fatalf("symlink claude dir: %v", err)
	}

	err = manager.PopulateHarnessConfig(prepared.Path, linkPath, "")
	if err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("populate error = %v, want symlink error", err)
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
