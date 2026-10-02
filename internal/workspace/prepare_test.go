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
	if head != "maestro/coder/team_2Fproject_2342" {
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

func TestPopulateHarnessConfigSkipsExistingDestination(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte("new-content"), 0o644); err != nil {
		t.Fatalf("write claude file: %v", err)
	}

	// Pre-create destination with existing content.
	existingClaude := filepath.Join(prepared.Path, ".claude")
	if err := os.MkdirAll(existingClaude, 0o755); err != nil {
		t.Fatalf("mkdir destination: %v", err)
	}
	if err := os.WriteFile(filepath.Join(existingClaude, "existing.md"), []byte("old"), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	// Should succeed without overwriting existing content.
	if err := manager.PopulateHarnessConfig(prepared.Path, claudeDir, ""); err != nil {
		t.Fatalf("populate harness config: %v", err)
	}

	// Existing content preserved.
	content, err := os.ReadFile(filepath.Join(existingClaude, "existing.md"))
	if err != nil || string(content) != "old" {
		t.Fatalf("existing file content = %q, err = %v; want 'old'", content, err)
	}

	// New content was NOT copied over (skip, not merge).
	if _, err := os.Stat(filepath.Join(existingClaude, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatalf("expected new file to NOT be copied into existing dir, stat err = %v", err)
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

func TestPrepareCloneReusesExistingWorkspace(t *testing.T) {
	repoURL := createSeedRepo(t)
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(root, "workspaces"))

	issue := domain.Issue{
		Identifier: "team/project#50",
		Meta:       map[string]string{"repo_url": repoURL},
	}

	// First prepare: fresh clone.
	first, err := manager.Prepare(context.Background(), issue, "coder")
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}

	// Simulate agent work: commit a file on the branch.
	runGitForWorkspace(t, first.Path, "-c", "user.name=Agent", "-c", "user.email=a@test.com",
		"commit", "--allow-empty", "-m", "agent work")
	firstHead := gitOutput(t, first.Path, "rev-parse", "HEAD")

	// Second prepare: should reuse workspace and preserve the commit.
	second, err := manager.Prepare(context.Background(), issue, "coder")
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}

	if second.Path != first.Path {
		t.Fatalf("workspace path changed: %q → %q", first.Path, second.Path)
	}

	secondHead := gitOutput(t, second.Path, "rev-parse", "HEAD")
	if secondHead != firstHead {
		t.Fatalf("HEAD changed on reuse: %q → %q (agent work lost)", firstHead, secondHead)
	}

	branch := gitOutput(t, second.Path, "branch", "--show-current")
	if branch != "maestro/coder/team_2Fproject_2350" {
		t.Fatalf("branch = %q after reuse", branch)
	}
}

func TestPrepareClonePreservesWorkspaceOnTransientReuseFailure(t *testing.T) {
	repoURL := createSeedRepo(t)
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(root, "workspaces"))

	issue := domain.Issue{
		Identifier: "team/project#52",
		Meta:       map[string]string{"repo_url": repoURL},
	}

	// First prepare: fresh clone.
	first, err := manager.Prepare(context.Background(), issue, "coder")
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}

	// Simulate agent work: commit a file on the branch.
	runGitForWorkspace(t, first.Path, "-c", "user.name=Agent", "-c", "user.email=a@test.com",
		"commit", "--allow-empty", "-m", "agent work")
	agentHead := gitOutput(t, first.Path, "rev-parse", "HEAD")

	// Break the remote so fetch fails (simulates network/auth failure).
	runGitForWorkspace(t, first.Path, "remote", "set-url", "origin", "https://invalid.example.com/broken.git")

	// Second prepare: should fail but NOT destroy the workspace.
	_, err = manager.Prepare(context.Background(), issue, "coder")
	if err == nil {
		t.Fatal("expected reuse failure due to broken remote")
	}
	if !strings.Contains(err.Error(), "reuse workspace") {
		t.Fatalf("expected reuse workspace error, got: %v", err)
	}

	// Workspace must still exist with the agent's commit intact.
	if _, statErr := os.Stat(filepath.Join(first.Path, ".git")); statErr != nil {
		t.Fatalf("workspace destroyed after transient failure: %v", statErr)
	}
	currentHead := gitOutput(t, first.Path, "rev-parse", "HEAD")
	if currentHead != agentHead {
		t.Fatalf("agent work lost: HEAD was %s, now %s", agentHead, currentHead)
	}
}

func TestPrepareCloneFallsBackOnCorruptGitRepo(t *testing.T) {
	repoURL := createSeedRepo(t)
	root := t.TempDir()
	manager := workspace.NewManager(filepath.Join(root, "workspaces"))

	issue := domain.Issue{
		Identifier: "team/project#51",
		Meta:       map[string]string{"repo_url": repoURL},
	}

	// Create a workspace with a broken .git directory.
	wsPath := filepath.Join(root, "workspaces", workspace.WorkspaceKey(issue.Identifier))
	if err := os.MkdirAll(filepath.Join(wsPath, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Prepare should fall back to fresh clone.
	prepared, err := manager.Prepare(context.Background(), issue, "coder")
	if err != nil {
		t.Fatalf("prepare after corrupt: %v", err)
	}

	branch := gitOutput(t, prepared.Path, "branch", "--show-current")
	if branch != "maestro/coder/team_2Fproject_2351" {
		t.Fatalf("branch = %q after fallback clone", branch)
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
