package workspace

import (
	"context"
	"encoding/base64"
	"fmt"
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
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return Prepared{}, err
	}

	repoURL := issue.Meta["repo_url"]
	if strings.TrimSpace(repoURL) == "" {
		return Prepared{}, fmt.Errorf("issue %q missing repo_url metadata", issue.Identifier)
	}

	key := WorkspaceKey(issue.Identifier)
	path := filepath.Join(m.root, key)
	if _, err := os.Stat(path); err == nil {
		if err := os.RemoveAll(path); err != nil {
			return Prepared{}, fmt.Errorf("remove existing workspace %s: %w", path, err)
		}
	}

	if err := cloneRepo(ctx, repoURL, m.gitLabHost, m.gitLabToken, path); err != nil {
		return Prepared{}, err
	}

	branch := BranchName(agentName, issue.Identifier)
	if err := runGit(ctx, path, "checkout", "-b", branch); err != nil {
		return Prepared{}, err
	}

	return Prepared{Path: path, Branch: branch}, nil
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
