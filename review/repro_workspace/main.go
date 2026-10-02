// Run with: go run ./review/repro_workspace
// Uses two disposable local Git repositories. No network access is required.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/workspace"
)

func main() {
	root, err := os.MkdirTemp("", "maestro-workspace-repro-")
	check(err)
	defer os.RemoveAll(root)
	first := makeRepo(root, "first")
	second := makeRepo(root, "second")
	manager := workspace.NewManager(filepath.Join(root, "workspaces"))
	ctx := context.Background()
	a, err := manager.PrepareClone(ctx, issue("team/repo#1", first), "reviewer")
	check(err)
	b, err := manager.PrepareClone(ctx, issue("team_repo#1", second), "reviewer")
	check(err)
	got := run(b.Path, "git", "remote", "get-url", "origin")
	if a.Path == b.Path || strings.TrimSpace(got) != second {
		panic("workspace collision or wrong origin")
	}
	if _, err := manager.PrepareClone(ctx, issue("team/repo#1", second), "reviewer"); err == nil {
		panic("workspace origin mismatch was accepted")
	}
	firstOrigin := run(a.Path, "git", "remote", "get-url", "origin")
	if strings.TrimSpace(firstOrigin) != first {
		panic("origin mismatch destroyed existing workspace")
	}
	fmt.Printf("distinct_workspaces=true requested_second_origin=true origin_mismatch_rejected=true\n")
}

func issue(identifier, repo string) domain.Issue {
	return domain.Issue{Identifier: identifier, Meta: map[string]string{"repo_url": repo}}
}

func makeRepo(root, name string) string {
	source := filepath.Join(root, name+"-source")
	check(os.MkdirAll(source, 0o700))
	run(source, "git", "init", "-q")
	check(os.WriteFile(filepath.Join(source, "marker.txt"), []byte(name+"\n"), 0o600))
	run(source, "git", "add", "marker.txt")
	run(source, "git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture")
	bare := filepath.Join(root, name+".git")
	run(root, "git", "clone", "-q", "--bare", source, bare)
	return bare
}

func run(dir, binary string, args ...string) string {
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("%s failed: %v: %s", binary, err, strings.TrimSpace(string(output))))
	}
	return string(output)
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
