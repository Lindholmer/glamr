package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"glamr/internal/graphql"
)

// mrPageSize is the number of MRs fetched per GraphQL request. Query cost grows with
// MRs x discussions; 50 per page took ~22s for a busy reviewer, close to GitLab's 30s
// request timeout. 10 per page keeps each request at a few seconds.
const mrPageSize = 10

// graphQLTimeout bounds a single glab invocation so a hung request can't block refreshes forever
const graphQLTimeout = 60 * time.Second

// GitLabGraphQLProvider implements the Provider interface using GitLab's GraphQL API
type GitLabGraphQLProvider struct {
	host     string
	repos    map[string]bool // lower-cased project paths to include; empty means all
	username string
}

// NewGitLabGraphQLProvider creates a new GitLab GraphQL provider
// NewGitLabGraphQLProvider creates a GitLab provider. If repos are given, only MRs in
// those projects are listed; otherwise MRs from every project are included.
func NewGitLabGraphQLProvider(host string, repos ...string) (*GitLabGraphQLProvider, error) {
	p := &GitLabGraphQLProvider{
		host:  host,
		repos: repoSet(repos),
	}

	// Get current user for filtering
	username, err := p.GetCurrentUser()
	if err != nil {
		return nil, commandError("failed to get current user", err)
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
			"first": mrPageSize,
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

		// Convert GraphQL MRs to provider MRs. The currentUser queries span all projects,
		// so restrict to the configured repo when one was given (--repo).
		for _, gqlMR := range conn.Nodes {
			if len(p.repos) > 0 && !p.repos[strings.ToLower(gqlMR.Project.FullPath)] {
				continue
			}
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
	projectPath, iid, err := parseMRID(id)
	if err != nil {
		return nil, err
	}

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
		return nil, commandError("failed to list repos", err)
	}

	projects, err := decodeJSONArrays(output)
	if err != nil {
		return nil, commandError("failed to parse repos", err)
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
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/pipelines/%s/jobs?per_page=100", urlEncodedRepo, numericID), "--paginate")

	output, err := cmd.Output()
	if err != nil {
		return nil, commandError("failed to get pipeline jobs", err)
	}

	jobs, err := decodeJSONArrays(output)
	if err != nil {
		return nil, commandError("failed to parse jobs", err)
	}

	return parseRESTJobs(jobs), nil
}

func (p *GitLabGraphQLProvider) GetMRNotes(mrIID int, repo string) ([]MRNote, error) {
	// Use the REST discussions endpoint so each note carries its discussion ID
	// (needed for replying/resolving), matching the notes cached from the GraphQL MR query.
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/merge_requests/%d/discussions?per_page=100", urlEncodedRepo, mrIID),
		"--paginate")

	output, err := cmd.Output()
	if err != nil {
		return nil, commandError("failed to get notes", err)
	}

	return parseDiscussions(output)
}

// ApproveMR, UnapproveMR, and job control methods use REST API
// as GraphQL mutations are more complex and REST works well for these

func (p *GitLabGraphQLProvider) ApproveMR(id string) error {
	repo, iid, err := parseMRID(id)
	if err != nil {
		return err
	}

	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/merge_requests/%s/approve", urlEncodedRepo, iid), "-X", "POST")

	if _, err := cmd.Output(); err != nil {
		return commandError("failed to approve MR", err)
	}

	return nil
}

func (p *GitLabGraphQLProvider) UnapproveMR(id string) error {
	repo, iid, err := parseMRID(id)
	if err != nil {
		return err
	}

	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/merge_requests/%s/unapprove", urlEncodedRepo, iid), "-X", "POST")

	if _, err := cmd.Output(); err != nil {
		return commandError("failed to unapprove MR", err)
	}

	return nil
}

func (p *GitLabGraphQLProvider) CreateNote(mrIID int, body string, repo string) error {
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/merge_requests/%d/notes", urlEncodedRepo, mrIID),
		"-X", "POST", "-f", "body="+body)

	if _, err := cmd.Output(); err != nil {
		return commandError("failed to create note", err)
	}

	return nil
}

func (p *GitLabGraphQLProvider) ReplyToNote(mrIID int, noteID string, body string, repo string) error {
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/merge_requests/%d/discussions/%s/notes", urlEncodedRepo, mrIID, noteID),
		"-X", "POST", "-f", "body="+body)

	if _, err := cmd.Output(); err != nil {
		return commandError("failed to reply to note", err)
	}

	return nil
}

func (p *GitLabGraphQLProvider) GetJobTrace(jobID string, repo string) (string, error) {
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/jobs/%s/trace", urlEncodedRepo, jobID))

	output, err := cmd.Output()
	if err != nil {
		return "", commandError("failed to get job trace", err)
	}

	return string(output), nil
}

func (p *GitLabGraphQLProvider) RetryJob(jobID string, repo string) error {
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/jobs/%s/retry", urlEncodedRepo, jobID), "-X", "POST")

	if _, err := cmd.Output(); err != nil {
		return commandError("failed to retry job", err)
	}

	return nil
}

func (p *GitLabGraphQLProvider) PlayJob(jobID string, repo string) error {
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/jobs/%s/play", urlEncodedRepo, jobID), "-X", "POST")

	if _, err := cmd.Output(); err != nil {
		return commandError("failed to play job", err)
	}

	return nil
}

func (p *GitLabGraphQLProvider) CancelJob(jobID string, repo string) error {
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/jobs/%s/cancel", urlEncodedRepo, jobID), "-X", "POST")

	if _, err := cmd.Output(); err != nil {
		return commandError("failed to cancel job", err)
	}

	return nil
}

func (p *GitLabGraphQLProvider) GetMRChanges(mrIID int, repo string) (*MRChanges, error) {
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/merge_requests/%d/changes", urlEncodedRepo, mrIID))

	output, err := cmd.Output()
	if err != nil {
		return nil, commandError("failed to get MR changes", err)
	}

	var response struct {
		Changes []struct {
			OldPath     string `json:"old_path"`
			NewPath     string `json:"new_path"`
			NewFile     bool   `json:"new_file"`
			RenamedFile bool   `json:"renamed_file"`
			DeletedFile bool   `json:"deleted_file"`
			Diff        string `json:"diff"`
		} `json:"changes"`
	}

	if err := json.Unmarshal(output, &response); err != nil {
		return nil, commandError("failed to parse MR changes", err)
	}

	changes := &MRChanges{
		Changes: make([]FileChange, 0, len(response.Changes)),
	}

	for _, change := range response.Changes {
		changes.Changes = append(changes.Changes, FileChange{
			OldPath:     change.OldPath,
			NewPath:     change.NewPath,
			NewFile:     change.NewFile,
			RenamedFile: change.RenamedFile,
			DeletedFile: change.DeletedFile,
			Diff:        change.Diff,
		})
	}

	return changes, nil
}

func (p *GitLabGraphQLProvider) ResolveDiscussion(mrIID int, discussionID string, repo string) error {
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/merge_requests/%d/discussions/%s", urlEncodedRepo, mrIID, discussionID),
		"-X", "PUT", "-f", "resolved=true")

	if _, err := cmd.Output(); err != nil {
		return commandError("failed to resolve discussion", err)
	}

	return nil
}

func (p *GitLabGraphQLProvider) UnresolveDiscussion(mrIID int, discussionID string, repo string) error {
	urlEncodedRepo := urlEncode(repo)
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/merge_requests/%d/discussions/%s", urlEncodedRepo, mrIID, discussionID),
		"-X", "PUT", "-f", "resolved=false")

	if _, err := cmd.Output(); err != nil {
		return commandError("failed to unresolve discussion", err)
	}

	return nil
}

func (p *GitLabGraphQLProvider) ToggleDraftStatus(mrID string) error {
	repo, iid, err := parseMRID(mrID)
	if err != nil {
		return err
	}

	urlEncodedRepo := urlEncode(repo)

	// First, get the MR to check current draft status
	cmd := p.buildGlabCommand("api", fmt.Sprintf("projects/%s/merge_requests/%s", urlEncodedRepo, iid))
	output, err := cmd.Output()
	if err != nil {
		return commandError("failed to get MR status", err)
	}

	var mrData struct {
		Draft bool   `json:"draft"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(output, &mrData); err != nil {
		return commandError("failed to parse MR data", err)
	}

	// Toggle draft status by updating the title prefix
	newTitle := toggleDraftTitle(mrData.Title, mrData.Draft)
	cmd = p.buildGlabCommand("api", fmt.Sprintf("projects/%s/merge_requests/%s", urlEncodedRepo, iid),
		"-X", "PUT", "-f", "title="+newTitle)

	if _, err := cmd.Output(); err != nil {
		return commandError("failed to toggle draft status", err)
	}

	return nil
}

// Helper methods

func (p *GitLabGraphQLProvider) buildGlabCommand(args ...string) *exec.Cmd {
	return p.buildGlabCommandContext(context.Background(), args...)
}

func (p *GitLabGraphQLProvider) buildGlabCommandContext(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "glab", args...)

	// No host means "use glab's default/authenticated host"
	if p.host == "" {
		return cmd
	}

	// Set hostname for API commands
	if len(args) > 0 && args[0] == "api" {
		cmd.Args = append(cmd.Args[:2], append([]string{"--hostname", p.host}, cmd.Args[2:]...)...)
	} else {
		// For non-api commands, set via environment
		cmd.Env = append(cmd.Environ(), "GITLAB_HOST="+p.host)
	}

	return cmd
}

// graphQLArgs builds the glab arguments for a GraphQL request. Strings are passed with
// -f (raw) so values such as IIDs ("123") or cursors aren't coerced to other JSON types;
// everything else uses -F so ints/bools keep their type.
func graphQLArgs(query string, variables map[string]interface{}) []string {
	args := []string{"api", "graphql", "-f", "query=" + query}

	keys := make([]string, 0, len(variables))
	for key := range variables {
		keys = append(keys, key)
	}
	sort.Strings(keys) // deterministic argument order

	for _, key := range keys {
		switch v := variables[key].(type) {
		case string:
			args = append(args, "-f", key+"="+v)
		case int:
			args = append(args, "-F", key+"="+strconv.Itoa(v))
		default:
			args = append(args, "-F", fmt.Sprintf("%s=%v", key, v))
		}
	}
	return args
}

func (p *GitLabGraphQLProvider) executeGraphQL(query string, variables map[string]interface{}) (*graphql.GraphQLResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), graphQLTimeout)
	defer cancel()
	cmd := p.buildGlabCommandContext(ctx, graphQLArgs(query, variables)...)

	// Only stdout is JSON; glab may print notices (e.g. update hints) on stderr
	output, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("graphql query timed out after %v", graphQLTimeout)
	}
	if err != nil {
		return nil, commandError("graphql query failed", err)
	}

	var response graphql.GraphQLResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, commandError("failed to parse graphql response", err)
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
				// GraphQL reports statuses in upper case ("FAILED"); REST and the TUI use lower case
				status := strings.ToLower(job.Status)
				mr.PipelineJobs = append(mr.PipelineJobs, PipelineJob{
					ID:     extractNumericID(job.ID),
					Name:   job.Name,
					Status: status,
					Stage:  job.Stage.Name,
				})
				jobStatuses = append(jobStatuses, status)
			}

			// Aggregate job statuses for more accurate pipeline status
			if aggregated := aggregateJobStatuses(jobStatuses); aggregated != "" {
				mr.Pipeline.Status = aggregated
			}
		}
	}

	// Extract current user's review state (for reviewing scope)
	for _, reviewer := range gqlMR.Reviewers.Nodes {
		if reviewer.Username == p.username {
			mr.UserReviewState = ReviewState(reviewer.MergeRequestInteraction.ReviewState)
			break
		}
	}

	// Convert and cache discussions/notes
	mr.Notes = make([]MRNote, 0)
	for _, discussion := range gqlMR.Discussions.Nodes {
		// Check for unresolved discussions
		if discussion.Resolvable && !discussion.Resolved {
			mr.HasUnresolvedDiscussions = true
		}

		// Check if user commented but is not the last commenter on unresolved threads
		if discussion.Resolvable && !discussion.Resolved && len(discussion.Notes.Nodes) > 0 {
			userCommented := false
			var lastCommenter string

			for _, note := range discussion.Notes.Nodes {
				// Skip system notes
				if note.System {
					continue
				}

				if note.Author.Username == p.username {
					userCommented = true
				}
				lastCommenter = note.Author.Username
			}

			// If user commented but is not the last commenter, mark as needing attention
			if userCommented && lastCommenter != "" && lastCommenter != p.username {
				mr.HasUnresolvedReplies = true
			}
		}

		// Convert each note in the discussion
		// For resolvable discussions, we store the discussion ID so we can resolve it later
		discussionID := extractNumericID(discussion.ID)
		for _, note := range discussion.Notes.Nodes {
			// Skip system notes
			if note.System {
				continue
			}

			mr.Notes = append(mr.Notes, MRNote{
				ID:         discussionID, // Use discussion ID for resolving
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

// repoSet builds the case-insensitive project filter used by ListMRs.
func repoSet(repos []string) map[string]bool {
	set := make(map[string]bool, len(repos))
	for _, r := range repos {
		if r = strings.TrimSpace(r); r != "" {
			set[strings.ToLower(r)] = true
		}
	}
	return set
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
	case "PENDING", "CREATED", "PREPARING", "WAITING_FOR_RESOURCE", "SCHEDULED":
		return PipelinePending
	case "SKIPPED":
		return PipelineSkipped
	case "":
		return PipelinePending
	default:
		// e.g. CANCELED, MANUAL - show the real status rather than pretending it's pending
		return PipelineStatus(strings.ToLower(status))
	}
}
