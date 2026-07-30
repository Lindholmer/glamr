package provider

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"glamr/internal/graphql"
)

// GitLabGraphQLProvider implements the Provider interface using GitLab's GraphQL API
type GitLabGraphQLProvider struct {
	host     string
	repo     string // optional - if empty, fetches from all repos
	username string
}

// NewGitLabGraphQLProvider creates a new GitLab GraphQL provider
func NewGitLabGraphQLProvider(host, repo string) (*GitLabGraphQLProvider, error) {
	p := &GitLabGraphQLProvider{
		host: host,
		repo: repo,
	}

	// Get current user for filtering
	username, err := p.GetCurrentUser()
	if err != nil {
		return nil, fmt.Errorf("failed to get current user: %w", err)
	}
	p.username = username

	return p, nil
}

func (p *GitLabGraphQLProvider) Name() string {
	return "gitlab-graphql"
}

func (p *GitLabGraphQLProvider) GetCurrentUser() (string, error) {
	query := `{ currentUser { username } }`

	response, err := p.executeGraphQL(query, nil)
	if err != nil {
		return "", err
	}

	if response.Data.CurrentUser == nil {
		return "", fmt.Errorf("no current user found")
	}

	return response.Data.CurrentUser.Username, nil
}

func (p *GitLabGraphQLProvider) ListMRs(scope Scope) ([]MergeRequest, error) {
	query := p.getQueryForScope(scope)

	var allMRs []MergeRequest
	cursor := ""

	// Fetch all pages
	for {
		variables := map[string]interface{}{
			"first": 50,
		}
		if cursor != "" {
			variables["after"] = cursor
		}

		response, err := p.executeGraphQL(query, variables)
		if err != nil {
			return nil, err
		}

		if response.Data.CurrentUser == nil {
			break
		}

		// Get the appropriate connection based on scope
		var conn *graphql.MergeRequestConnection
		switch scope {
		case ScopeAuthored:
			conn = response.Data.CurrentUser.AuthoredMergeRequests
		case ScopeAssigned:
			conn = response.Data.CurrentUser.AssignedMergeRequests
		case ScopeReviewing:
			conn = response.Data.CurrentUser.ReviewRequestedMergeRequests
		}

		if conn == nil {
			break
		}

		// Convert GraphQL MRs to provider MRs
		for _, gqlMR := range conn.Nodes {
			mr := p.convertGraphQLMR(gqlMR)
			allMRs = append(allMRs, mr)
		}

		// Check if we need to fetch more pages
		if !conn.PageInfo.HasNextPage {
			break
		}
		cursor = conn.PageInfo.EndCursor
	}

	return allMRs, nil
}

func (p *GitLabGraphQLProvider) GetMR(id string) (*MergeRequest, error) {
	// Parse the ID to extract project path and IID
	// ID format could be "project/path!123" or just "123"
	parts := strings.Split(id, "!")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid MR ID format: %s (expected format: project/path!123)", id)
	}

	projectPath := parts[0]
	iid := parts[1]

	variables := map[string]interface{}{
		"projectPath": projectPath,
		"iid":         iid,
	}

	response, err := p.executeGraphQL(graphql.GetMRQuery, variables)
	if err != nil {
		return nil, err
	}

	if response.Data.Project == nil || response.Data.Project.MergeRequest == nil {
		return nil, fmt.Errorf("MR not found: %s", id)
	}

	mr := p.convertGraphQLMR(*response.Data.Project.MergeRequest)
	return &mr, nil
}

func (p *GitLabGraphQLProvider) ListRepos() ([]string, error) {
	// Use REST API for this as GraphQL pagination for projects is more complex
	// and we already have the REST implementation
	cmd := p.buildGlabCommand("api", "projects", "--paginate", "-X", "GET",
		"-f", "membership=true", "-f", "per_page=100", "-f", "simple=true")

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list repos: %w", err)
	}

	var projects []map[string]interface{}
	if err := json.Unmarshal(output, &projects); err != nil {
		return nil, fmt.Errorf("failed to parse repos: %w", err)
	}

	repos := make([]string, 0, len(projects))
	for _, proj := range projects {
		if pathWithNs, ok := proj["path_with_namespace"].(string); ok {
			repos = append(repos, pathWithNs)
		}
	}

	return repos, nil
}

func (p *GitLabGraphQLProvider) GetPipelineJobs(pipelineID string, repo string) ([]PipelineJob, error) {
	// Extract numeric ID from GraphQL global ID if needed
	// GraphQL IDs look like "gid://gitlab/Ci::Pipeline/123"
	numericID := extractNumericID(pipelineID)

	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/pipelines/%s/jobs", urlEncodedRepo, numericID))

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get pipeline jobs: %w", err)
	}

	var jobs []map[string]interface{}
	if err := json.Unmarshal(output, &jobs); err != nil {
		return nil, fmt.Errorf("failed to parse jobs: %w", err)
	}

	result := make([]PipelineJob, 0, len(jobs))
	for _, job := range jobs {
		result = append(result, PipelineJob{
			ID:     fmt.Sprintf("%.0f", job["id"].(float64)),
			Name:   job["name"].(string),
			Status: job["status"].(string),
			Stage:  job["stage"].(string),
		})
	}

	return result, nil
}

func (p *GitLabGraphQLProvider) GetMRNotes(mrIID int, repo string) ([]MRNote, error) {
	// Use REST API for notes as GraphQL already includes discussions in the MR query
	// This is mainly for backwards compatibility with the existing interface
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/merge_requests/%d/notes?per_page=100", urlEncodedRepo, mrIID),
		"--paginate")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to get notes: %w\nOutput: %s", err, string(output))
	}

	var notes []map[string]interface{}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for decoder.More() {
		var batch []map[string]interface{}
		if err := decoder.Decode(&batch); err != nil {
			break
		}
		notes = append(notes, batch...)
	}

	result := make([]MRNote, 0)
	for _, note := range notes {
		if system, ok := note["system"].(bool); ok && system {
			continue
		}

		mrNote := MRNote{
			ID:        fmt.Sprintf("%.0f", note["id"].(float64)),
			Body:      note["body"].(string),
			CreatedAt: note["created_at"].(string),
		}

		if author, ok := note["author"].(map[string]interface{}); ok {
			mrNote.Author = author["username"].(string)
		}

		if resolvable, ok := note["resolvable"].(bool); ok {
			mrNote.Resolvable = resolvable
		}

		if resolved, ok := note["resolved"].(bool); ok {
			mrNote.Resolved = resolved
		}

		result = append(result, mrNote)
	}

	return result, nil
}

// ApproveMR, UnapproveMR, and job control methods use REST API
// as GraphQL mutations are more complex and REST works well for these

func (p *GitLabGraphQLProvider) ApproveMR(id string) error {
	parts := strings.Split(id, "!")
	if len(parts) != 2 {
		return fmt.Errorf("invalid MR ID format: %s", id)
	}

	repo := parts[0]
	iid := parts[1]

	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/merge_requests/%s/approve", urlEncodedRepo, iid), "-X", "POST")

	if _, err := cmd.Output(); err != nil {
		return fmt.Errorf("failed to approve MR: %w", err)
	}

	return nil
}

func (p *GitLabGraphQLProvider) UnapproveMR(id string) error {
	parts := strings.Split(id, "!")
	if len(parts) != 2 {
		return fmt.Errorf("invalid MR ID format: %s", id)
	}

	repo := parts[0]
	iid := parts[1]

	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/merge_requests/%s/unapprove", urlEncodedRepo, iid), "-X", "POST")

	if _, err := cmd.Output(); err != nil {
		return fmt.Errorf("failed to unapprove MR: %w", err)
	}

	return nil
}

func (p *GitLabGraphQLProvider) CreateNote(mrIID int, body string, repo string) error {
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/merge_requests/%d/notes", urlEncodedRepo, mrIID),
		"-X", "POST", "-f", "body="+body)

	if _, err := cmd.Output(); err != nil {
		return fmt.Errorf("failed to create note: %w", err)
	}

	return nil
}

func (p *GitLabGraphQLProvider) ReplyToNote(mrIID int, noteID string, body string, repo string) error {
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/merge_requests/%d/discussions/%s/notes", urlEncodedRepo, mrIID, noteID),
		"-X", "POST", "-f", "body="+body)

	if _, err := cmd.Output(); err != nil {
		return fmt.Errorf("failed to reply to note: %w", err)
	}

	return nil
}

func (p *GitLabGraphQLProvider) GetJobTrace(jobID string, repo string) (string, error) {
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/jobs/%s/trace", urlEncodedRepo, jobID))

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get job trace: %w", err)
	}

	return string(output), nil
}

func (p *GitLabGraphQLProvider) RetryJob(jobID string, repo string) error {
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/jobs/%s/retry", urlEncodedRepo, jobID), "-X", "POST")

	if _, err := cmd.Output(); err != nil {
		return fmt.Errorf("failed to retry job: %w", err)
	}

	return nil
}

func (p *GitLabGraphQLProvider) PlayJob(jobID string, repo string) error {
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/jobs/%s/play", urlEncodedRepo, jobID), "-X", "POST")

	if _, err := cmd.Output(); err != nil {
		return fmt.Errorf("failed to play job: %w", err)
	}

	return nil
}

func (p *GitLabGraphQLProvider) CancelJob(jobID string, repo string) error {
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/jobs/%s/cancel", urlEncodedRepo, jobID), "-X", "POST")

	if _, err := cmd.Output(); err != nil {
		return fmt.Errorf("failed to cancel job: %w", err)
	}

	return nil
}

// Helper methods

func (p *GitLabGraphQLProvider) buildGlabCommand(args ...string) *exec.Cmd {
	cmd := exec.Command("glab", args...)

	// Set hostname for API commands
	if len(args) > 0 && args[0] == "api" {
		cmd.Args = append(cmd.Args[:1], append([]string{"--hostname", p.host}, cmd.Args[1:]...)...)
	} else {
		// For non-api commands, set via environment
		cmd.Env = append(cmd.Environ(), "GITLAB_HOST="+p.host)
	}

	return cmd
}

func (p *GitLabGraphQLProvider) executeGraphQL(query string, variables map[string]interface{}) (*graphql.GraphQLResponse, error) {
	args := []string{"api", "graphql", "-f", "query=" + query}

	// Add variables if provided
	for key, value := range variables {
		var valueStr string
		switch v := value.(type) {
		case string:
			valueStr = v
		case int:
			valueStr = strconv.Itoa(v)
		default:
			valueStr = fmt.Sprintf("%v", v)
		}
		args = append(args, "-F", fmt.Sprintf("%s=%s", key, valueStr))
	}

	cmd := p.buildGlabCommand(args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("graphql query failed: %w\nOutput: %s", err, string(output))
	}

	var response graphql.GraphQLResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, fmt.Errorf("failed to parse graphql response: %w", err)
	}

	if len(response.Errors) > 0 {
		return nil, fmt.Errorf("graphql errors: %v", response.Errors)
	}

	return &response, nil
}

func (p *GitLabGraphQLProvider) getQueryForScope(scope Scope) string {
	switch scope {
	case ScopeAuthored:
		return graphql.AuthoredMRsQuery
	case ScopeAssigned:
		return graphql.AssignedMRsQuery
	case ScopeReviewing:
		return graphql.ReviewRequestedMRsQuery
	default:
		return graphql.AuthoredMRsQuery
	}
}

func (p *GitLabGraphQLProvider) convertGraphQLMR(gqlMR graphql.MergeRequest) MergeRequest {
	mr := MergeRequest{
		ID:                gqlMR.Project.FullPath + "!" + gqlMR.IID,
		IID:               parseIID(gqlMR.IID),
		Title:             gqlMR.Title,
		Author:            gqlMR.Author.Username,
		SourceBranch:      gqlMR.SourceBranch,
		TargetBranch:      gqlMR.TargetBranch,
		WebURL:            gqlMR.WebURL,
		CreatedAt:         gqlMR.CreatedAt,
		UpdatedAt:         gqlMR.UpdatedAt,
		HasConflicts:      gqlMR.Conflicts,
		ApprovalCount:     len(gqlMR.ApprovedBy.Nodes),
		RequiredApprovals: gqlMR.ApprovalsRequired,
		ApprovalsLeft:     gqlMR.ApprovalsLeft,
		Approved:          gqlMR.Approved,
		RepoName:          gqlMR.Project.FullPath,
	}

	// Determine status
	if gqlMR.Draft {
		mr.Status = StatusDraft
	} else {
		switch strings.ToLower(gqlMR.State) {
		case "opened":
			mr.Status = StatusOpen
		case "merged":
			mr.Status = StatusMerged
		case "closed":
			mr.Status = StatusClosed
		default:
			mr.Status = StatusOpen
		}
	}

	// Check if current user approved
	for _, approver := range gqlMR.ApprovedBy.Nodes {
		if approver.Username == p.username {
			mr.UserApproved = true
			break
		}
	}

	// Convert pipeline and jobs
	if gqlMR.HeadPipeline != nil {
		pipelineID := extractNumericID(gqlMR.HeadPipeline.ID)
		mr.Pipeline = &Pipeline{
			ID:        pipelineID,
			Status:    convertPipelineStatus(gqlMR.HeadPipeline.Status),
			WebURL:    fmt.Sprintf("https://%s/%s/-/pipelines/%s", p.host, gqlMR.Project.FullPath, pipelineID),
			CreatedAt: gqlMR.HeadPipeline.CreatedAt,
			UpdatedAt: gqlMR.HeadPipeline.UpdatedAt,
			Duration:  time.Duration(gqlMR.HeadPipeline.Duration) * time.Second,
		}

		// Convert and cache pipeline jobs
		if len(gqlMR.HeadPipeline.Jobs.Nodes) > 0 {
			mr.PipelineJobs = make([]PipelineJob, 0, len(gqlMR.HeadPipeline.Jobs.Nodes))
			jobStatuses := make([]string, 0, len(gqlMR.HeadPipeline.Jobs.Nodes))

			for _, job := range gqlMR.HeadPipeline.Jobs.Nodes {
				mr.PipelineJobs = append(mr.PipelineJobs, PipelineJob{
					ID:     extractNumericID(job.ID),
					Name:   job.Name,
					Status: job.Status,
					Stage:  job.Stage.Name,
				})
				jobStatuses = append(jobStatuses, job.Status)
			}

			// Aggregate job statuses for more accurate pipeline status
			mr.Pipeline.Status = aggregateJobStatuses(jobStatuses)
		}
	}

	// Convert and cache discussions/notes
	mr.Notes = make([]MRNote, 0)
	for _, discussion := range gqlMR.Discussions.Nodes {
		// Check for unresolved discussions
		if discussion.Resolvable && !discussion.Resolved {
			mr.HasUnresolvedDiscussions = true
		}

		// Convert each note in the discussion
		for _, note := range discussion.Notes.Nodes {
			// Skip system notes
			if note.System {
				continue
			}

			mr.Notes = append(mr.Notes, MRNote{
				ID:         extractNumericID(note.Author.Username), // Not ideal, but GraphQL doesn't give us note ID
				Author:     note.Author.Username,
				Body:       note.Body,
				CreatedAt:  note.CreatedAt.Format(time.RFC3339),
				Resolvable: note.Resolvable,
				Resolved:   note.Resolved,
				System:     note.System,
			})
		}
	}

	return mr
}

func (p *GitLabGraphQLProvider) buildPipelineURL(projectPath, webPath string) string {
	if strings.HasPrefix(webPath, "http") {
		return webPath
	}
	return fmt.Sprintf("https://%s%s", p.host, webPath)
}

func parseIID(iid string) int {
	if num, err := strconv.Atoi(iid); err == nil {
		return num
	}
	return 0
}

func extractNumericID(globalID string) string {
	// Extract numeric ID from GraphQL global ID
	// e.g., "gid://gitlab/Ci::Pipeline/123" -> "123"
	parts := strings.Split(globalID, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return globalID
}

func convertPipelineStatus(status string) PipelineStatus {
	switch strings.ToUpper(status) {
	case "SUCCESS":
		return PipelineSuccess
	case "RUNNING":
		return PipelineRunning
	case "FAILED":
		return PipelineFailed
	case "PENDING":
		return PipelinePending
	case "SKIPPED":
		return PipelineSkipped
	default:
		return PipelinePending
	}
}

func aggregateJobStatuses(jobStatuses []string) PipelineStatus {
	hasFailed := false
	hasRunning := false
	hasPending := false
	hasManual := false

	for _, status := range jobStatuses {
		switch strings.ToLower(status) {
		case "failed":
			hasFailed = true
		case "running":
			hasRunning = true
		case "pending":
			hasPending = true
		case "manual":
			hasManual = true
		}
	}

	if hasFailed {
		return PipelineFailed
	}
	if hasRunning {
		return PipelineRunning
	}
	if hasPending {
		return PipelinePending
	}
	if hasManual {
		return PipelinePending
	}
	return PipelineSuccess
}

func urlEncode(s string) string {
	s = strings.ReplaceAll(s, "/", "%2F")
	s = strings.ReplaceAll(s, " ", "%20")
	return s
}
