package gitlab

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/domain"
	trackerbase "github.com/tjohnson/maestro/internal/tracker"
)

type Adapter struct {
	source  config.SourceConfig
	client  *Client
	project projectResponse
}

type projectResponse struct {
	ID                int    `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	WebURL            string `json:"web_url"`
	SSHURLToRepo      string `json:"ssh_url_to_repo"`
	HTTPURLToRepo     string `json:"http_url_to_repo"`
}

type issueResponse struct {
	ID          int           `json:"id"`
	IID         int           `json:"iid"`
	ProjectID   int           `json:"project_id"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	State       string        `json:"state"`
	WebURL      string        `json:"web_url"`
	Labels      []string      `json:"labels"`
	Author      userResponse  `json:"author"`
	Assignee    *userResponse `json:"assignee"`
	CreatedAt   string        `json:"created_at"`
	UpdatedAt   string        `json:"updated_at"`
	References  struct {
		Full string `json:"full"`
	} `json:"references"`
	Epic *struct {
		ID  int `json:"id"`
		IID int `json:"iid"`
	} `json:"epic"`
}

type epicResponse struct {
	ID          int          `json:"id"`
	IID         int          `json:"iid"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	State       string       `json:"state"`
	WebURL      string       `json:"web_url"`
	Labels      []string     `json:"labels"`
	Author      userResponse `json:"author"`
	CreatedAt   string       `json:"created_at"`
	UpdatedAt   string       `json:"updated_at"`
}

type userResponse struct {
	Username string `json:"username"`
}

type gitLabIssueRef struct {
	Project string
	IID     int
}

type gitLabEpicIssueRef struct {
	Group    string
	EpicIID  int
	EpicID   int
	Project  string
	IssueIID int
}

func NewAdapter(source config.SourceConfig) (*Adapter, error) {
	client, err := NewClient(source.Connection.BaseURL, source.Connection.Token)
	if err != nil {
		return nil, err
	}

	return &Adapter{source: source, client: client}, nil
}

func (a *Adapter) Kind() string {
	return "gitlab"
}

func (a *Adapter) Poll(ctx context.Context) ([]domain.Issue, error) {
	if a.epicMode() {
		return a.pollEpicIssues(ctx)
	}
	return a.pollProjectIssues(ctx)
}

func (a *Adapter) Get(ctx context.Context, issueID string) (domain.Issue, error) {
	if a.epicMode() {
		return a.getEpicIssue(ctx, issueID)
	}
	return a.getProjectIssue(ctx, issueID)
}

func (a *Adapter) PostOperationalComment(ctx context.Context, issueID string, body string) error {
	if a.epicMode() {
		ref, err := parseGitLabEpicIssueRef(issueID)
		if err != nil {
			return err
		}
		return a.postIssueComment(ctx, gitLabIssueRef{Project: ref.Project, IID: ref.IssueIID}, body)
	}

	ref, err := parseGitLabIssueRef(issueID)
	if err != nil {
		return err
	}
	return a.postIssueComment(ctx, ref, body)
}

func (a *Adapter) AddLifecycleLabel(ctx context.Context, issueID string, label string) error {
	return a.updateLifecycleLabel(ctx, issueID, label, "")
}

func (a *Adapter) RemoveLifecycleLabel(ctx context.Context, issueID string, label string) error {
	return a.updateLifecycleLabel(ctx, issueID, "", label)
}

// UpdateIssueState changes the GitLab issue state. Recognized values are
// "close"/"closed" and "reopen"/"open"/"opened"; other values are ignored.
func (a *Adapter) UpdateIssueState(ctx context.Context, issueID string, stateName string) error {
	event := ""
	switch strings.ToLower(strings.TrimSpace(stateName)) {
	case "close", "closed":
		event = "close"
	case "reopen", "open", "opened":
		event = "reopen"
	default:
		return nil
	}

	form := url.Values{}
	form.Set("state_event", event)

	if a.epicMode() {
		ref, err := parseGitLabEpicIssueRef(issueID)
		if err != nil {
			return err
		}
		_, err = a.client.putForm(ctx, fmt.Sprintf("/api/v4/projects/%s/issues/%d", url.PathEscape(ref.Project), ref.IssueIID), form, nil)
		return err
	}

	ref, err := parseGitLabIssueRef(issueID)
	if err != nil {
		return err
	}
	_, err = a.client.putForm(ctx, fmt.Sprintf("/api/v4/projects/%s/issues/%d", url.PathEscape(ref.Project), ref.IID), form, nil)
	return err
}

func (a *Adapter) pollProjectIssues(ctx context.Context) ([]domain.Issue, error) {
	if err := a.ensureProject(ctx); err != nil {
		return nil, err
	}

	filter := a.source.EffectiveIssueFilter()
	var out []domain.Issue
	page := 1
	for {
		query := url.Values{}
		query.Set("per_page", "100")
		query.Set("page", strconv.Itoa(page))
		query.Set("state", issueAPIState(filter.States))
		if len(filter.Labels) > 0 {
			query.Set("labels", strings.Join(filter.Labels, ","))
		}
		if assignee := strings.TrimSpace(filter.Assignee); assignee != "" {
			query.Set("assignee_username", assignee)
		}

		var payload []issueResponse
		resp, err := a.client.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%s/issues", url.PathEscape(a.source.Connection.Project)), query, &payload)
		if err != nil {
			return nil, err
		}

		for _, item := range payload {
			issue := a.normalizeProjectIssue(item)
			if trackerbase.IsCandidateWithPrefix(issue, filter, a.source.LabelPrefix) {
				out = append(out, issue)
			}
		}

		if resp.Header.Get("X-Next-Page") == "" {
			break
		}
		page++
	}

	return out, nil
}

func (a *Adapter) pollEpicIssues(ctx context.Context) ([]domain.Issue, error) {
	epics, err := a.pollCandidateEpics(ctx)
	if err != nil {
		return nil, err
	}
	if len(epics) == 0 {
		return nil, nil
	}

	groupIssues, err := a.pollGroupIssues(ctx)
	if err != nil {
		return nil, err
	}

	epicsByID := make(map[int]epicResponse, len(epics))
	for _, epic := range epics {
		epicsByID[epic.ID] = epic
	}

	out := make([]domain.Issue, 0, len(groupIssues))
	for _, item := range groupIssues {
		if item.Epic == nil {
			continue
		}
		epic, ok := epicsByID[item.Epic.ID]
		if !ok {
			continue
		}
		if !a.matchesEpicIssueFilter(item) {
			continue
		}
		issue, ok := a.normalizeEpicIssue(item, epic)
		if !ok {
			continue
		}
		if trackerbase.IsCandidateWithPrefix(issue, epicIssueDisplayFilter(a.source), a.source.LabelPrefix) {
			out = append(out, issue)
		}
	}
	return out, nil
}

func (a *Adapter) pollCandidateEpics(ctx context.Context) ([]epicResponse, error) {
	filter := a.source.EffectiveEpicFilter()
	var out []epicResponse
	page := 1

	for {
		query := url.Values{}
		query.Set("per_page", "100")
		query.Set("page", strconv.Itoa(page))
		query.Set("state", "opened")
		if len(filter.Labels) > 0 {
			query.Set("labels", strings.Join(filter.Labels, ","))
		}

		var payload []epicResponse
		resp, err := a.client.getJSON(ctx, fmt.Sprintf("/api/v4/groups/%s/epics", url.PathEscape(a.source.Connection.GroupPath())), query, &payload)
		if err != nil {
			return nil, err
		}

		for _, item := range payload {
			if trackerbase.HasBlockingLifecycleLabelWithPrefix(normalizeLabels(item.Labels), a.source.LabelPrefix) {
				continue
			}
			if a.matchesEpicFilter(item) {
				out = append(out, item)
			}
		}

		if resp.Header.Get("X-Next-Page") == "" {
			break
		}
		page++
	}

	return out, nil
}

func (a *Adapter) pollGroupIssues(ctx context.Context) ([]issueResponse, error) {
	filter := a.source.EffectiveIssueFilter()
	var out []issueResponse
	page := 1

	for {
		query := url.Values{}
		query.Set("per_page", "100")
		query.Set("page", strconv.Itoa(page))
		query.Set("state", issueAPIState(filter.States))
		if len(filter.Labels) > 0 {
			query.Set("labels", strings.Join(filter.Labels, ","))
		}
		if assignee := strings.TrimSpace(filter.Assignee); assignee != "" {
			query.Set("assignee_username", assignee)
		}

		var payload []issueResponse
		resp, err := a.client.getJSON(ctx, fmt.Sprintf("/api/v4/groups/%s/issues", url.PathEscape(a.source.Connection.GroupPath())), query, &payload)
		if err != nil {
			return nil, err
		}

		out = append(out, payload...)
		if resp.Header.Get("X-Next-Page") == "" {
			break
		}
		page++
	}

	return out, nil
}

func (a *Adapter) getProjectIssue(ctx context.Context, issueID string) (domain.Issue, error) {
	if err := a.ensureProject(ctx); err != nil {
		return domain.Issue{}, err
	}

	ref, err := parseGitLabIssueRef(issueID)
	if err != nil {
		return domain.Issue{}, err
	}

	payload, err := a.fetchIssue(ctx, ref.Project, ref.IID)
	if err != nil {
		return domain.Issue{}, err
	}
	return a.normalizeProjectIssue(payload), nil
}

func (a *Adapter) getEpicIssue(ctx context.Context, issueID string) (domain.Issue, error) {
	ref, err := parseGitLabEpicIssueRef(issueID)
	if err != nil {
		return domain.Issue{}, err
	}

	item, err := a.fetchIssue(ctx, ref.Project, ref.IssueIID)
	if err != nil {
		return domain.Issue{}, err
	}
	epic, err := a.fetchEpic(ctx, ref.Group, ref.EpicIID)
	if err != nil {
		return domain.Issue{}, err
	}
	issue, ok := a.normalizeEpicIssue(item, epic)
	if !ok {
		return domain.Issue{}, fmt.Errorf("gitlab epic issue %q missing project reference", issueID)
	}
	return issue, nil
}

func (a *Adapter) fetchIssue(ctx context.Context, project string, iid int) (issueResponse, error) {
	var payload issueResponse
	if _, err := a.client.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%s/issues/%d", url.PathEscape(project), iid), nil, &payload); err != nil {
		return issueResponse{}, err
	}
	return payload, nil
}

func (a *Adapter) fetchEpic(ctx context.Context, group string, iid int) (epicResponse, error) {
	var payload epicResponse
	if _, err := a.client.getJSON(ctx, fmt.Sprintf("/api/v4/groups/%s/epics/%d", url.PathEscape(group), iid), nil, &payload); err != nil {
		return epicResponse{}, err
	}
	return payload, nil
}

func (a *Adapter) postIssueComment(ctx context.Context, ref gitLabIssueRef, body string) error {
	form := url.Values{}
	form.Set("body", body)
	_, err := a.client.postForm(ctx, fmt.Sprintf("/api/v4/projects/%s/issues/%d/notes", url.PathEscape(ref.Project), ref.IID), form, nil)
	return err
}

func (a *Adapter) ensureProject(ctx context.Context) error {
	if a.project.ID != 0 {
		return nil
	}

	var project projectResponse
	if _, err := a.client.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%s", url.PathEscape(a.source.Connection.Project)), nil, &project); err != nil {
		return err
	}

	a.project = project
	return nil
}

func (a *Adapter) normalizeProjectIssue(item issueResponse) domain.Issue {
	project := a.source.Connection.Project
	if issueProject := issueProjectPath(item); issueProject != "" {
		project = issueProject
	}

	return domain.Issue{
		ID:          formatGitLabIssueID(project, item.IID),
		ExternalID:  strconv.Itoa(item.ID),
		Identifier:  fmt.Sprintf("%s#%d", project, item.IID),
		TrackerKind: "gitlab",
		SourceName:  a.source.Name,
		Title:       item.Title,
		Description: item.Description,
		State:       normalizeState(item.State),
		Labels:      normalizeLabels(item.Labels),
		Assignee:    issueAssignee(item),
		URL:         item.WebURL,
		CreatedAt:   trackerbase.ParseTime(item.CreatedAt),
		UpdatedAt:   trackerbase.ParseTime(item.UpdatedAt),
		Meta: map[string]string{
			"project":         project,
			"repo_url":        chooseRepoURL(a.project),
			"gitlab_base_url": a.source.Connection.BaseURL,
			"gitlab_scope":    "project-issue",
			"state_type":      strings.ToLower(strings.TrimSpace(item.State)),
		},
	}
}

func (a *Adapter) normalizeEpicIssue(item issueResponse, epic epicResponse) (domain.Issue, bool) {
	project := issueProjectPath(item)
	if project == "" {
		return domain.Issue{}, false
	}

	labels := normalizeLabels(item.Labels)
	labels = append(labels, normalizeLabels(epic.Labels)...)
	labels = uniqueStrings(labels)

	ref := gitLabEpicIssueRef{
		Group:    a.source.Connection.GroupPath(),
		EpicIID:  epic.IID,
		EpicID:   epic.ID,
		Project:  project,
		IssueIID: item.IID,
	}

	return domain.Issue{
		ID:          formatGitLabEpicIssueID(ref),
		ExternalID:  strconv.Itoa(item.ID),
		Identifier:  fmt.Sprintf("%s#%d", project, item.IID),
		TrackerKind: "gitlab",
		SourceName:  a.source.Name,
		Title:       item.Title,
		Description: item.Description,
		State:       normalizeState(item.State),
		Labels:      labels,
		Assignee:    issueAssignee(item),
		URL:         item.WebURL,
		CreatedAt:   trackerbase.ParseTime(item.CreatedAt),
		UpdatedAt:   trackerbase.ParseTime(item.UpdatedAt),
		Meta: map[string]string{
			"project":           project,
			"project_id":        strconv.Itoa(item.ProjectID),
			"repo_url":          resolveSourceRepoURL(a.source.Connection.BaseURL, a.source.Repo),
			"gitlab_base_url":   a.source.Connection.BaseURL,
			"gitlab_scope":      "epic-issue",
			"state_type":        strings.ToLower(strings.TrimSpace(item.State)),
			"bucket_state_type": strings.ToLower(strings.TrimSpace(epic.State)),
			"epic_group":        ref.Group,
			"epic_iid":          strconv.Itoa(epic.IID),
			"epic_id":           strconv.Itoa(epic.ID),
			"epic_title":        epic.Title,
			"epic_url":          epic.WebURL,
		},
	}, true
}

func (a *Adapter) matchesEpicFilter(epic epicResponse) bool {
	filter := a.source.EffectiveEpicFilter()
	if isZeroGitLabFilter(filter) {
		return true
	}
	if len(filter.IIDs) > 0 {
		matched := false
		for _, iid := range filter.IIDs {
			if epic.IID == iid {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	issue := domain.Issue{
		State:  normalizeState(epic.State),
		Labels: normalizeLabels(epic.Labels),
	}
	return trackerbase.MatchesFilterWithPrefix(issue, filter, a.source.LabelPrefix) &&
		!trackerbase.HasBlockingLifecycleLabelWithPrefix(issue.Labels, a.source.LabelPrefix)
}

func (a *Adapter) matchesEpicIssueFilter(item issueResponse) bool {
	filter := a.source.EffectiveIssueFilter()
	if isZeroGitLabFilter(filter) {
		return true
	}
	issue := domain.Issue{
		State:    normalizeState(item.State),
		Labels:   normalizeLabels(item.Labels),
		Assignee: issueAssignee(item),
	}
	return trackerbase.IsCandidateWithPrefix(issue, filter, a.source.LabelPrefix)
}

func epicIssueDisplayFilter(source config.SourceConfig) config.FilterConfig {
	return source.EffectiveIssueFilter()
}

func isZeroGitLabFilter(filter config.FilterConfig) bool {
	return len(filter.Labels) == 0 && len(filter.IIDs) == 0 && len(filter.States) == 0 && strings.TrimSpace(filter.Assignee) == ""
}

func issueAssignee(item issueResponse) string {
	if item.Assignee == nil {
		return ""
	}
	return item.Assignee.Username
}

func issueProjectPath(item issueResponse) string {
	full := strings.TrimSpace(item.References.Full)
	hash := strings.LastIndex(full, "#")
	if hash == -1 {
		return ""
	}
	return full[:hash]
}

func normalizeLabels(labels []string) []string {
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		out = append(out, strings.ToLower(label))
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeState(state string) string {
	state = strings.ToLower(strings.TrimSpace(state))
	if state == "opened" {
		return "open"
	}
	return state
}

func chooseRepoURL(project projectResponse) string {
	if strings.TrimSpace(project.HTTPURLToRepo) != "" {
		return project.HTTPURLToRepo
	}
	if strings.TrimSpace(project.SSHURLToRepo) != "" {
		return project.SSHURLToRepo
	}
	return ""
}

func resolveSourceRepoURL(baseURL string, repo string) string {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return ""
	}
	if strings.HasPrefix(repo, "/") || strings.HasPrefix(repo, ".") {
		return repo
	}
	parsed, err := url.Parse(repo)
	if err == nil && (parsed.Scheme != "" || parsed.Host != "") {
		return repo
	}

	root, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || root.Scheme == "" || root.Host == "" {
		return repo
	}

	path := "/" + strings.TrimPrefix(strings.TrimSpace(repo), "/")
	if !strings.HasSuffix(path, ".git") {
		path += ".git"
	}
	root.Path = path
	root.RawPath = ""
	root.RawQuery = ""
	root.Fragment = ""
	return root.String()
}


func formatGitLabIssueID(project string, iid int) string {
	return fmt.Sprintf("gitlab:%s#%d", project, iid)
}

func parseGitLabIssueIID(issueID string) (int, error) {
	ref, err := parseGitLabIssueRef(issueID)
	if err != nil {
		return 0, err
	}
	return ref.IID, nil
}

func parseGitLabIssueRef(issueID string) (gitLabIssueRef, error) {
	trimmed := strings.TrimSpace(issueID)
	if !strings.HasPrefix(trimmed, "gitlab:") {
		return gitLabIssueRef{}, fmt.Errorf("invalid gitlab issue id %q", issueID)
	}
	payload := strings.TrimPrefix(trimmed, "gitlab:")
	hash := strings.LastIndex(payload, "#")
	if hash == -1 || hash == len(payload)-1 {
		return gitLabIssueRef{}, fmt.Errorf("invalid gitlab issue id %q", issueID)
	}
	iid, err := strconv.Atoi(payload[hash+1:])
	if err != nil {
		return gitLabIssueRef{}, fmt.Errorf("invalid gitlab issue id %q", issueID)
	}
	return gitLabIssueRef{Project: payload[:hash], IID: iid}, nil
}

func formatGitLabEpicIssueID(ref gitLabEpicIssueRef) string {
	return fmt.Sprintf("gitlab-epic:%s&%d:%d|%s#%d", ref.Group, ref.EpicIID, ref.EpicID, ref.Project, ref.IssueIID)
}

func parseGitLabEpicRef(issueID string) (group string, iid int, epicID int, err error) {
	ref, err := parseGitLabEpicIssueRef(issueID)
	if err != nil {
		return "", 0, 0, err
	}
	return ref.Group, ref.EpicIID, ref.EpicID, nil
}

func parseGitLabEpicIssueRef(issueID string) (gitLabEpicIssueRef, error) {
	trimmed := strings.TrimSpace(issueID)
	if !strings.HasPrefix(trimmed, "gitlab-epic:") {
		return gitLabEpicIssueRef{}, fmt.Errorf("invalid gitlab epic issue id %q", issueID)
	}
	payload := strings.TrimPrefix(trimmed, "gitlab-epic:")
	parts := strings.SplitN(payload, "|", 2)
	if len(parts) != 2 {
		return gitLabEpicIssueRef{}, fmt.Errorf("invalid gitlab epic issue id %q", issueID)
	}

	epicPart := parts[0]
	colon := strings.LastIndex(epicPart, ":")
	if colon == -1 || colon == len(epicPart)-1 {
		return gitLabEpicIssueRef{}, fmt.Errorf("invalid gitlab epic issue id %q", issueID)
	}
	epicID, err := strconv.Atoi(epicPart[colon+1:])
	if err != nil {
		return gitLabEpicIssueRef{}, fmt.Errorf("invalid gitlab epic issue id %q", issueID)
	}
	amp := strings.LastIndex(epicPart[:colon], "&")
	if amp == -1 || amp == len(epicPart[:colon])-1 {
		return gitLabEpicIssueRef{}, fmt.Errorf("invalid gitlab epic issue id %q", issueID)
	}
	epicIID, err := strconv.Atoi(epicPart[amp+1 : colon])
	if err != nil {
		return gitLabEpicIssueRef{}, fmt.Errorf("invalid gitlab epic issue id %q", issueID)
	}
	group := epicPart[:amp]

	issueRef, err := parseGitLabIssueRef("gitlab:" + parts[1])
	if err != nil {
		return gitLabEpicIssueRef{}, fmt.Errorf("invalid gitlab epic issue id %q", issueID)
	}

	return gitLabEpicIssueRef{
		Group:    group,
		EpicIID:  epicIID,
		EpicID:   epicID,
		Project:  issueRef.Project,
		IssueIID: issueRef.IID,
	}, nil
}

func (a *Adapter) updateLifecycleLabel(ctx context.Context, issueID string, add string, remove string) error {
	form := url.Values{}
	if strings.TrimSpace(add) != "" {
		form.Set("add_labels", add)
	}
	if strings.TrimSpace(remove) != "" {
		form.Set("remove_labels", remove)
	}

	if a.epicMode() {
		ref, err := parseGitLabEpicIssueRef(issueID)
		if err != nil {
			return err
		}
		_, err = a.client.putForm(ctx, fmt.Sprintf("/api/v4/projects/%s/issues/%d", url.PathEscape(ref.Project), ref.IssueIID), form, nil)
		return err
	}

	ref, err := parseGitLabIssueRef(issueID)
	if err != nil {
		return err
	}
	_, err = a.client.putForm(ctx, fmt.Sprintf("/api/v4/projects/%s/issues/%d", url.PathEscape(ref.Project), ref.IID), form, nil)
	return err
}

func (a *Adapter) epicMode() bool {
	return a.source.Tracker == "gitlab-epic"
}

func issueAPIState(states []string) string {
	if len(states) == 0 {
		return "opened"
	}

	seenOpen := false
	seenClosed := false
	for _, state := range states {
		switch normalizeState(state) {
		case "open":
			seenOpen = true
		case "closed":
			seenClosed = true
		}
	}
	switch {
	case seenOpen && seenClosed:
		return "all"
	case seenClosed:
		return "closed"
	default:
		return "opened"
	}
}
