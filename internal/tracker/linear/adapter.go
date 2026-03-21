package linear

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
	trackerbase "github.com/tjohnson/maestro/internal/tracker"
)

const issuesQuery = `
query($projectId: ID!, $after: String) {
  issues(first: 50, after: $after, filter: { project: { id: { eq: $projectId } } }) {
    nodes {
      id
      identifier
      title
      description
      url
      createdAt
      updatedAt
      labels { nodes { name } }
      state { name type }
      assignee { name email }
      project { id name }
      team { id key name }
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}`

const projectLookupQuery = `
query($name: String!) {
  projects(first: 1, filter: { name: { eq: $name } }) {
    nodes {
      id
      name
    }
  }
}`

const issueQuery = `
query($id: String!) {
  issue(id: $id) {
    id
    identifier
    title
    description
    url
    createdAt
    updatedAt
    labels { nodes { name } }
    state { name type }
    assignee { name email }
    project { id name }
    team { id key name }
  }
}`

const issueLabelsQuery = `
query($teamId: ID!, $after: String) {
  issueLabels(first: 50, after: $after, filter: { team: { id: { eq: $teamId } } }) {
    nodes {
      id
      name
    }
    pageInfo {
      hasNextPage
      endCursor
    }
  }
}`

const commentCreateMutation = `
mutation($issueId: String!, $body: String!) {
  commentCreate(input: { issueId: $issueId, body: $body }) {
    success
  }
}`

const issueLabelCreateMutation = `
mutation($teamId: String!, $name: String!, $color: String!) {
  issueLabelCreate(input: { teamId: $teamId, name: $name, color: $color }) {
    success
    issueLabel {
      id
      name
    }
  }
}`

const issueAddLabelMutation = `
mutation($issueId: String!, $labelId: String!) {
  issueAddLabel(id: $issueId, labelId: $labelId) {
    success
  }
}`

const issueRemoveLabelMutation = `
mutation($issueId: String!, $labelId: String!) {
  issueRemoveLabel(id: $issueId, labelId: $labelId) {
    success
  }
}`

type Adapter struct {
	source config.SourceConfig
	client *Client
	mu     sync.Mutex
	labels map[string]string

	projectID       string
	projectResolved bool
}

type issuesResponse struct {
	Issues struct {
		Nodes    []issueNode `json:"nodes"`
		PageInfo pageInfo    `json:"pageInfo"`
	} `json:"issues"`
}

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type issueNode struct {
	ID          string `json:"id"`
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	Labels      struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	State struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"state"`
	Assignee *struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"assignee"`
	Project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	Team struct {
		ID   string `json:"id"`
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"team"`
}

type issueResponse struct {
	Issue *issueNode `json:"issue"`
}

type issueLabelsResponse struct {
	IssueLabels struct {
		Nodes []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"nodes"`
		PageInfo pageInfo `json:"pageInfo"`
	} `json:"issueLabels"`
}

func NewAdapter(source config.SourceConfig) (*Adapter, error) {
	client, err := NewClient(source.Connection.BaseURL, source.Connection.Token)
	if err != nil {
		return nil, err
	}

	return &Adapter{source: source, client: client, labels: map[string]string{}}, nil
}

func (a *Adapter) Kind() string {
	return "linear"
}

func (a *Adapter) Poll(ctx context.Context) ([]domain.Issue, error) {
	projectID, err := a.resolveProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var out []domain.Issue
	var after *string

	for {
		variables := map[string]any{
			"projectId": projectID,
			"after":     nil,
		}
		if after != nil {
			variables["after"] = *after
		}

		var resp issuesResponse
		if err := a.client.query(ctx, issuesQuery, variables, &resp); err != nil {
			return nil, err
		}

		for _, item := range resp.Issues.Nodes {
			issue := a.normalizeIssue(item)
			if trackerbase.IsCandidateWithPrefix(issue, a.source.Filter, a.source.LabelPrefix) {
				out = append(out, issue)
			}
		}

		if !resp.Issues.PageInfo.HasNextPage || strings.TrimSpace(resp.Issues.PageInfo.EndCursor) == "" {
			break
		}
		cursor := resp.Issues.PageInfo.EndCursor
		after = &cursor
	}

	return out, nil
}

func (a *Adapter) resolveProjectID(ctx context.Context) (string, error) {
	a.mu.Lock()
	if a.projectResolved {
		projectID := a.projectID
		a.mu.Unlock()
		return projectID, nil
	}
	a.mu.Unlock()

	project := strings.TrimSpace(a.source.Connection.Project)
	if project == "" {
		return "", fmt.Errorf("linear source %q missing connection.project", a.source.Name)
	}

	var resp struct {
		Projects struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		} `json:"projects"`
	}
	if err := a.client.query(ctx, projectLookupQuery, map[string]any{"name": project}, &resp); err == nil {
		if len(resp.Projects.Nodes) > 0 && strings.TrimSpace(resp.Projects.Nodes[0].ID) != "" {
			project = resp.Projects.Nodes[0].ID
		}
	}

	a.mu.Lock()
	a.projectID = project
	a.projectResolved = true
	a.mu.Unlock()
	return project, nil
}

func (a *Adapter) Get(ctx context.Context, issueID string) (domain.Issue, error) {
	externalID, err := parseLinearIssueID(issueID)
	if err != nil {
		return domain.Issue{}, err
	}

	var resp issueResponse
	if err := a.client.query(ctx, issueQuery, map[string]any{"id": externalID}, &resp); err != nil {
		return domain.Issue{}, err
	}
	if resp.Issue == nil {
		return domain.Issue{}, fmt.Errorf("linear issue %q not found", issueID)
	}
	return a.normalizeIssue(*resp.Issue), nil
}

func (a *Adapter) PostOperationalComment(ctx context.Context, issueID string, body string) error {
	externalID, err := parseLinearIssueID(issueID)
	if err != nil {
		return err
	}

	return a.client.query(ctx, commentCreateMutation, map[string]any{
		"issueId": externalID,
		"body":    body,
	}, nil)
}

func (a *Adapter) AddLifecycleLabel(ctx context.Context, issueID string, label string) error {
	return a.updateLifecycleLabel(ctx, issueID, label, issueAddLabelMutation)
}

func (a *Adapter) RemoveLifecycleLabel(ctx context.Context, issueID string, label string) error {
	return a.updateLifecycleLabel(ctx, issueID, label, issueRemoveLabelMutation)
}

// UpdateIssueState is a no-op for Linear; changing workflow states requires
// looking up team-specific workflow state IDs via GraphQL which is deferred.
func (a *Adapter) UpdateIssueState(_ context.Context, _ string, _ string) error {
	return nil
}

func (a *Adapter) normalizeIssue(item issueNode) domain.Issue {
	labels := make([]string, 0, len(item.Labels.Nodes))
	for _, label := range item.Labels.Nodes {
		labels = append(labels, strings.ToLower(label.Name))
	}

	var assignee string
	if item.Assignee != nil {
		switch {
		case strings.TrimSpace(item.Assignee.Email) != "":
			assignee = item.Assignee.Email
		default:
			assignee = item.Assignee.Name
		}
	}

	return domain.Issue{
		ID:          "linear:" + item.ID,
		ExternalID:  item.ID,
		Identifier:  item.Identifier,
		TrackerKind: "linear",
		SourceName:  a.source.Name,
		Title:       item.Title,
		Description: item.Description,
		State:       strings.ToLower(item.State.Name),
		Labels:      labels,
		Assignee:    assignee,
		URL:         item.URL,
		CreatedAt:   trackerbase.ParseTime(item.CreatedAt),
		UpdatedAt:   trackerbase.ParseTime(item.UpdatedAt),
		Meta: map[string]string{
			"project":    item.Project.ID,
			"repo_url":   a.source.Repo,
			"team_id":    item.Team.ID,
			"team_key":   item.Team.Key,
			"state_type": strings.ToLower(strings.TrimSpace(item.State.Type)),
		},
	}
}


func parseLinearIssueID(issueID string) (string, error) {
	trimmed := strings.TrimSpace(issueID)
	if !strings.HasPrefix(trimmed, "linear:") {
		return "", fmt.Errorf("invalid linear issue id %q", issueID)
	}
	return strings.TrimPrefix(trimmed, "linear:"), nil
}

func (a *Adapter) updateLifecycleLabel(ctx context.Context, issueID string, label string, mutation string) error {
	externalID, err := parseLinearIssueID(issueID)
	if err != nil {
		return err
	}

	labelID, err := a.ensureLabelID(ctx, issueID, label)
	if err != nil {
		return err
	}

	return a.client.query(ctx, mutation, map[string]any{
		"issueId": externalID,
		"labelId": labelID,
	}, nil)
}

func (a *Adapter) ensureLabelID(ctx context.Context, issueID string, label string) (string, error) {
	issue, err := a.Get(ctx, issueID)
	if err != nil {
		return "", err
	}
	teamID := strings.TrimSpace(issue.Meta["team_id"])
	if teamID == "" {
		return "", fmt.Errorf("linear issue %q missing team metadata", issueID)
	}

	cacheKey := teamID + ":" + strings.ToLower(strings.TrimSpace(label))
	a.mu.Lock()
	if labelID, ok := a.labels[cacheKey]; ok {
		a.mu.Unlock()
		return labelID, nil
	}
	a.mu.Unlock()

	labelID, err := a.lookupLabelID(ctx, teamID, label)
	if err != nil {
		return "", err
	}
	if labelID == "" {
		labelID, err = a.createLabel(ctx, teamID, label)
		if err != nil {
			return "", err
		}
	}

	a.mu.Lock()
	a.labels[cacheKey] = labelID
	a.mu.Unlock()
	return labelID, nil
}

func (a *Adapter) lookupLabelID(ctx context.Context, teamID string, label string) (string, error) {
	var after *string
	for {
		variables := map[string]any{
			"teamId": teamID,
			"after":  nil,
		}
		if after != nil {
			variables["after"] = *after
		}

		var resp issueLabelsResponse
		if err := a.client.query(ctx, issueLabelsQuery, variables, &resp); err != nil {
			return "", err
		}
		for _, node := range resp.IssueLabels.Nodes {
			if strings.EqualFold(node.Name, label) {
				return node.ID, nil
			}
		}
		if !resp.IssueLabels.PageInfo.HasNextPage || strings.TrimSpace(resp.IssueLabels.PageInfo.EndCursor) == "" {
			return "", nil
		}
		cursor := resp.IssueLabels.PageInfo.EndCursor
		after = &cursor
	}
}

func (a *Adapter) createLabel(ctx context.Context, teamID string, label string) (string, error) {
	var resp struct {
		IssueLabelCreate struct {
			IssueLabel struct {
				ID string `json:"id"`
			} `json:"issueLabel"`
		} `json:"issueLabelCreate"`
	}
	if err := a.client.query(ctx, issueLabelCreateMutation, map[string]any{
		"teamId": teamID,
		"name":   label,
		"color":  "#6b7280",
	}, &resp); err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.IssueLabelCreate.IssueLabel.ID) == "" {
		return "", fmt.Errorf("linear label create returned empty id for %q", label)
	}
	return resp.IssueLabelCreate.IssueLabel.ID, nil
}
