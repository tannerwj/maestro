package workspace

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/tjohnson/maestro/internal/domain"
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
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return Prepared{}, err
	}

	path, err := m.workspacePath(issue.Identifier)
	if err != nil {
		return Prepared{}, err
	}

	repoURL := issue.Meta["repo_url"]
	if strings.TrimSpace(repoURL) == "" {
		return Prepared{}, fmt.Errorf("issue %q missing repo_url metadata", issue.Identifier)
	}

	if err := resetWorkspacePath(path); err != nil {
		return Prepared{}, err
	}
	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(path)
		}
	}()

	if err := cloneRepo(ctx, repoURL, m.gitLabHost, m.gitLabToken, path); err != nil {
		return Prepared{}, err
	}

	branch := BranchName(agentName, issue.Identifier)
	if err := runGit(ctx, path, "checkout", "-b", branch); err != nil {
		return Prepared{}, err
	}

	cleanupOnError = false
	return Prepared{Path: path, Branch: branch}, nil
}

func (m *Manager) PrepareEmpty(issue domain.Issue) (Prepared, error) {
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return Prepared{}, err
	}

	path, err := m.workspacePath(issue.Identifier)
	if err != nil {
		return Prepared{}, err
	}
	if err := resetWorkspacePath(path); err != nil {
		return Prepared{}, err
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return Prepared{}, err
	}

	return Prepared{Path: path}, nil
}

func (m *Manager) PopulateHarnessConfig(workspacePath string, claudeDir string, codexDir string) error {
	if err := copyOptionalDir(claudeDir, filepath.Join(workspacePath, ".claude")); err != nil {
		return err
	}
	if err := copyOptionalDir(codexDir, filepath.Join(workspacePath, ".codex")); err != nil {
		return err
	}
	return nil
}

func WorkspaceKey(identifier string) string {
	var b strings.Builder
	for _, r := range identifier {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
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

func cloneRepo(ctx context.Context, repoURL string, gitLabHost string, gitLabToken string, path string) error {
	if strings.TrimSpace(gitLabToken) == "" || !strings.HasPrefix(repoURL, "http") {
		return runGit(ctx, "", "clone", repoURL, path)
	}
	repoEndpoint, err := url.Parse(repoURL)
	if err != nil || !strings.EqualFold(repoEndpoint.Host, gitLabHost) {
		return runGit(ctx, "", "clone", repoURL, path)
	}

	auth := base64.StdEncoding.EncodeToString([]byte("oauth2:" + gitLabToken))
	return runGit(
		ctx,
		"",
		"-c",
		"http.extraHeader=Authorization: Basic "+auth,
		"clone",
		repoURL,
		path,
	)
}
