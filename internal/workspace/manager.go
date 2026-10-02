package workspace

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/redact"
)

type Prepared struct {
	Path   string
	Branch string
}

type Manager struct {
	root        string
	gitLabHost  string
	gitLabToken string
}

func NewManager(root string) *Manager {
	return &Manager{root: root}
}

func (m *Manager) WithGitLabAuth(baseURL string, token string) *Manager {
	clone := *m
	clone.gitLabToken = strings.TrimSpace(token)
	if parsed, err := url.Parse(strings.TrimSpace(baseURL)); err == nil {
		clone.gitLabHost = strings.TrimSpace(parsed.Host)
	}
	return &clone
}

func (m *Manager) Prepare(ctx context.Context, issue domain.Issue, agentName string) (Prepared, error) {
	return m.PrepareClone(ctx, issue, agentName)
}

func (m *Manager) PrepareClone(ctx context.Context, issue domain.Issue, agentName string) (Prepared, error) {
	prepared, err := m.PreviewClone(issue, agentName)
	if err != nil {
		return Prepared{}, err
	}
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return Prepared{}, err
	}
	repoURL, err := cloneRepoURL(issue)
	if err != nil {
		return Prepared{}, err
	}

	// Reuse existing workspace if it contains a valid git repo.
	if isGitRepo(prepared.Path) {
		if !isGitRepoHealthy(ctx, prepared.Path) {
			// Repo is corrupt — safe to remove and re-clone.
			_ = os.RemoveAll(prepared.Path)
		} else if err := reuseWorkspace(ctx, prepared.Path, prepared.Branch, repoURL, m.gitLabHost, m.gitLabToken); err != nil {
			// Repo is healthy but fetch/checkout failed (network, auth, conflict).
			// Preserve the workspace and return the error — don't destroy local work.
			return Prepared{}, fmt.Errorf("reuse workspace %s: %w", prepared.Path, err)
		} else {
			return prepared, nil
		}
	}

	if err := resetWorkspacePath(prepared.Path); err != nil {
		return Prepared{}, err
	}
	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(prepared.Path)
		}
	}()

	if err := cloneRepo(ctx, repoURL, m.gitLabHost, m.gitLabToken, prepared.Path); err != nil {
		return Prepared{}, err
	}

	if err := checkoutOrCreateBranch(ctx, prepared.Path, prepared.Branch); err != nil {
		return Prepared{}, err
	}

	cleanupOnError = false
	return prepared, nil
}

// checkoutOrCreateBranch checks out an existing branch (local or remote-tracking)
// or creates a new one. This handles the case where a different Maestro instance
// already pushed work on this branch.
func checkoutOrCreateBranch(ctx context.Context, path string, branch string) error {
	// Try checking out an existing local or remote-tracking branch.
	if err := runGit(ctx, path, "checkout", branch); err == nil {
		return nil
	}
	// Branch doesn't exist — create it.
	return runGit(ctx, path, "checkout", "-b", branch)
}

func isGitRepo(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && info.IsDir()
}

func isGitRepoHealthy(ctx context.Context, path string) bool {
	return runGit(ctx, path, "rev-parse", "--git-dir") == nil
}

func reuseWorkspace(ctx context.Context, path string, branch string, repoURL string, gitLabHost string, gitLabToken string) error {
	remoteURL, err := configuredOriginURL(ctx, path)
	if err != nil {
		return fmt.Errorf("read existing workspace origin: %w", err)
	}
	if remoteURL != repoURL {
		return fmt.Errorf("existing workspace origin does not match requested repository: %q", path)
	}
	// Fetch latest from origin, with auth only for matching GitLab HTTPS remotes.
	if err := fetchWorkspaceOrigin(ctx, path, gitLabHost, gitLabToken); err != nil {
		return err
	}
	return checkoutOrCreateBranch(ctx, path, branch)
}

func configuredOriginURL(ctx context.Context, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "config", "--get", "remote.origin.url")
	cmd.Dir = path
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git config remote.origin.url: %w: %s", err, redact.String(strings.TrimSpace(string(output))))
	}
	return strings.TrimSpace(string(output)), nil
}

func (m *Manager) PrepareEmpty(issue domain.Issue) (Prepared, error) {
	prepared, err := m.PreviewEmpty(issue)
	if err != nil {
		return Prepared{}, err
	}
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return Prepared{}, err
	}
	if err := resetWorkspacePath(prepared.Path); err != nil {
		return Prepared{}, err
	}
	if err := os.MkdirAll(prepared.Path, 0o755); err != nil {
		return Prepared{}, err
	}

	return prepared, nil
}

func (m *Manager) PreviewClone(issue domain.Issue, agentName string) (Prepared, error) {
	if _, err := cloneRepoURL(issue); err != nil {
		return Prepared{}, err
	}
	path, err := m.workspacePath(issue.Identifier)
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{
		Path:   path,
		Branch: BranchName(agentName, issue.Identifier),
	}, nil
}

func (m *Manager) PreviewEmpty(issue domain.Issue) (Prepared, error) {
	path, err := m.workspacePath(issue.Identifier)
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{Path: path}, nil
}

func cloneRepoURL(issue domain.Issue) (string, error) {
	repoURL := issue.Meta["repo_url"]
	if strings.TrimSpace(repoURL) == "" {
		return "", fmt.Errorf("issue %q missing repo_url metadata", issue.Identifier)
	}
	return repoURL, nil
}

func (m *Manager) PopulateHarnessConfig(workspacePath string, claudeDir string, codexDir string) error {
	if err := copyOptionalDirIfMissing(claudeDir, filepath.Join(workspacePath, ".claude")); err != nil {
		return err
	}
	if err := copyOptionalDirIfMissing(codexDir, filepath.Join(workspacePath, ".codex")); err != nil {
		return err
	}
	return nil
}

func WorkspaceKey(identifier string) string {
	var b strings.Builder
	for _, c := range []byte(identifier) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "_%02X", c)
		}
	}
	return b.String()
}

func BranchName(agentName string, issueIdentifier string) string {
	return fmt.Sprintf("maestro/%s/%s", WorkspaceKey(agentName), WorkspaceKey(issueIdentifier))
}

func (m *Manager) workspacePath(identifier string) (string, error) {
	key := WorkspaceKey(identifier)
	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("issue identifier is required for workspace path")
	}
	return filepath.Join(m.root, key), nil
}

func resetWorkspacePath(path string) error {
	if _, err := os.Stat(path); err == nil {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove existing workspace %s: %w", path, err)
		}
	}
	return nil
}

func copyOptionalDirIfMissing(source string, destination string) error {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	// Skip if destination already exists (workspace reuse).
	if _, err := os.Stat(destination); err == nil {
		return nil
	}
	return copyOptionalDir(source, destination)
}

func copyOptionalDir(source string, destination string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil
	}
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("stat source dir %s: %w", source, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("source dir %s must not be a symlink", source)
	}
	if !info.IsDir() {
		return fmt.Errorf("source dir %s must be a directory", source)
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("destination %s already exists", destination)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat destination %s: %w", destination, err)
	}
	if err := os.MkdirAll(destination, info.Mode().Perm()); err != nil {
		return fmt.Errorf("create destination dir %s: %w", destination, err)
	}
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("source path %s must not be a symlink", path)
		}
		switch {
		case entry.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode().IsRegular():
			return copyFile(path, target, info.Mode().Perm())
		default:
			return fmt.Errorf("source path %s has unsupported file type", path)
		}
	}); err != nil {
		_ = os.RemoveAll(destination)
		return err
	}
	return nil
}

func copyFile(source string, destination string, mode fs.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source file %s: %w", source, err)
	}
	defer input.Close()

	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create destination file %s: %w", destination, err)
	}
	defer output.Close()

	if _, err := io.Copy(output, input); err != nil {
		return fmt.Errorf("copy file %s: %w", source, err)
	}
	return nil
}

func fetchWorkspaceOrigin(ctx context.Context, path string, gitLabHost string, gitLabToken string) error {
	remoteURL, err := gitRemoteURL(ctx, path, "origin")
	if err != nil {
		return err
	}
	return runGitWithAuth(ctx, path, remoteURL, gitLabHost, gitLabToken, "fetch", "origin", "--prune")
}

func gitRemoteURL(ctx context.Context, path string, remote string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", remote)
	cmd.Dir = path
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf(
			"git remote get-url %s: %w: %s",
			redact.String(remote),
			err,
			redact.String(strings.TrimSpace(string(output))),
		)
	}
	return strings.TrimSpace(string(output)), nil
}

func gitLabAuthEnv(repoURL string, gitLabHost string, gitLabToken string) []string {
	if strings.TrimSpace(gitLabToken) == "" || strings.TrimSpace(gitLabHost) == "" {
		return nil
	}
	repoEndpoint, err := url.Parse(strings.TrimSpace(repoURL))
	if err != nil {
		return nil
	}
	if !strings.EqualFold(repoEndpoint.Scheme, "https") || !strings.EqualFold(repoEndpoint.Host, strings.TrimSpace(gitLabHost)) {
		return nil
	}
	auth := base64.StdEncoding.EncodeToString([]byte("oauth2:" + gitLabToken))
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + auth,
	}
}

func runGitWithAuth(ctx context.Context, dir string, repoURL string, gitLabHost string, gitLabToken string, args ...string) error {
	return runGitWithEnv(ctx, dir, gitLabAuthEnv(repoURL, gitLabHost, gitLabToken), args...)
}

func cloneRepo(ctx context.Context, repoURL string, gitLabHost string, gitLabToken string, path string) error {
	return runGitWithAuth(ctx, "", repoURL, gitLabHost, gitLabToken, "clone", repoURL, path)
}
