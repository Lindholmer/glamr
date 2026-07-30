package provider

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// GitLabProvider implements Provider interface using glab CLI
type GitLabProvider struct {
	currentUser string
	host        string // GitLab host (e.g., gitlab.com or custom instance)
	repo        string // Repository (e.g., group/project) - empty if querying all repos
}

// NewGitLabProvider creates a new GitLab provider
// If host is empty, uses the default configured glab host
// If repo is empty, will query all accessible repos
func NewGitLabProvider(host, repo string) (*GitLabProvider, error) {
	provider := &GitLabProvider{host: host, repo: repo}

	// Get current user upfront
	user, err := provider.GetCurrentUser()
	if err != nil {
		return nil, fmt.Errorf("failed to get current user: %w", err)
	}
	provider.currentUser = user

	return provider, nil
}

// buildGlabCommand builds a glab command with optional hostname
// Uses --hostname flag for API commands, GITLAB_HOST env var for others
func (g *GitLabProvider) buildGlabCommand(args ...string) *exec.Cmd {
	var finalArgs []string

	// For 'api' subcommand, use --hostname flag after 'api'
	if len(args) > 0 && args[0] == "api" {
		finalArgs = append(finalArgs, args[0])
		if g.host != "" {
			finalArgs = append(finalArgs, "--hostname", g.host)
		}
		finalArgs = append(finalArgs, args[1:]...)
		return exec.Command("glab", finalArgs...)
	}

	// For other commands, use GITLAB_HOST environment variable
	cmd := exec.Command("glab", args...)

	// Important: Inherit parent environment (os.Environ()), then add GITLAB_HOST
	if g.host != "" {
		cmd.Env = append(cmd.Environ(), fmt.Sprintf("GITLAB_HOST=%s", g.host))
	}

	return cmd
}

func (g *GitLabProvider) Name() string {
	return "gitlab"
}

func (g *GitLabProvider) GetCurrentUser() (string, error) {
	if g.currentUser != "" {
		return g.currentUser, nil
	}

	cmd := g.buildGlabCommand("api", "user")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get current user: %w", err)
	}

	// Parse JSON response
	var userData map[string]interface{}
	if err := json.Unmarshal(output, &userData); err != nil {
		return "", fmt.Errorf("failed to parse user data: %w", err)
	}

	username, ok := userData["username"].(string)
	if !ok {
		return "", fmt.Errorf("username not found in response")
	}

	return username, nil
}

func (g *GitLabProvider) ListMRs(scope Scope) ([]MergeRequest, error) {
	// If no specific repo, list all repos first
	if g.repo == "" {
		return g.listMRsAllRepos(scope)
	}

	return g.listMRsForRepo(g.repo, scope)
}

func (g *GitLabProvider) listMRsForRepo(repo string, scope Scope) ([]MergeRequest, error) {
	var args []string

	switch scope {
	case ScopeAuthored:
		args = []string{"mr", "list", "--author", "@me", "--per-page", "100", "--repo", repo}
	case ScopeAssigned:
		args = []string{"mr", "list", "--assignee", "@me", "--per-page", "100", "--repo", repo}
	case ScopeReviewing:
		args = []string{"mr", "list", "--reviewer", "@me", "--per-page", "100", "--repo", repo}
	default:
		return nil, fmt.Errorf("unknown scope: %s", scope)
	}

	// Get JSON output from glab
	cmd := g.buildGlabCommand( append(args, "--output", "json")...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list MRs: %w\nOutput: %s", err, string(output))
	}

	// Parse glab JSON output
	var glabMRs []map[string]interface{}
	if err := json.Unmarshal(output, &glabMRs); err != nil {
		return nil, fmt.Errorf("failed to parse MR list: %w", err)
	}

	// Convert to our MergeRequest format
	mrs := make([]MergeRequest, 0, len(glabMRs))
	for _, mr := range glabMRs {
		converted, err := g.convertGlabMR(mr)
		if err != nil {
			// Log but don't fail on individual MR conversion errors
			continue
		}

		// Fetch pipeline and approval info for this MR
		if converted.IID > 0 {
			pipeline, _ := g.fetchMRPipelineForRepo(repo, converted.IID)
			if pipeline != nil {
				converted.Pipeline = pipeline
			}

			// Fetch approval data
			g.fetchMRApprovalsForRepo(repo, converted.IID, &converted)
		}

		// Add repo name for display
		converted.RepoName = repo

		mrs = append(mrs, converted)
	}

	return mrs, nil
}

func (g *GitLabProvider) listMRsAllRepos(scope Scope) ([]MergeRequest, error) {
	// Get all repos accessible to the user
	repos, err := g.ListRepos()
	if err != nil {
		return nil, fmt.Errorf("failed to list repos: %w", err)
	}

	var allMRs []MergeRequest
	var errors []string

	for _, repo := range repos {
		mrs, err := g.listMRsForRepo(repo, scope)
		if err != nil {
			// Collect errors but continue with other repos
			errors = append(errors, fmt.Sprintf("%s: %v", repo, err))
			continue
		}
		allMRs = append(allMRs, mrs...)
	}

	// Log errors if any (could be exposed to UI later)
	if len(errors) > 0 {
		// For now, just continue - could add a way to show these in UI
	}

	return allMRs, nil
}

func (g *GitLabProvider) ListRepos() ([]string, error) {
	// Use GitLab API to get all projects the user is a member of
	// Query parameters are passed in the URL for glab api
	cmd := g.buildGlabCommand("api", "projects?membership=true&per_page=100&simple=true", "--paginate")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}

	var projects []map[string]interface{}
	if err := json.Unmarshal(output, &projects); err != nil {
		return nil, fmt.Errorf("failed to parse projects: %w", err)
	}

	var repos []string
	for _, proj := range projects {
		if pathWithNamespace, ok := proj["path_with_namespace"].(string); ok {
			repos = append(repos, pathWithNamespace)
		}
	}

	return repos, nil
}

// fetchMRPipelineForRepo fetches pipeline information for a specific MR in a specific repo
// It fetches job statuses to determine the true status (failed jobs take priority)
func (g *GitLabProvider) fetchMRPipelineForRepo(repo string, mrIID int) (*Pipeline, error) {
	// Use GitLab API to get MR details including pipeline
	apiPath := fmt.Sprintf("projects/%s/merge_requests/%d", g.urlEncode(repo), mrIID)
	cmd := g.buildGlabCommand("api", apiPath)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch MR pipeline: %w", err)
	}

	var mrData map[string]interface{}
	if err := json.Unmarshal(output, &mrData); err != nil {
		return nil, fmt.Errorf("failed to parse MR data: %w", err)
	}

	// Try head_pipeline first, then pipeline
	var pipelineData map[string]interface{}
	if hp, ok := mrData["head_pipeline"].(map[string]interface{}); ok && hp != nil {
		pipelineData = hp
	} else if p, ok := mrData["pipeline"].(map[string]interface{}); ok && p != nil {
		pipelineData = p
	}

	if pipelineData == nil {
		return nil, nil // No pipeline
	}

	pipeline := g.convertPipeline(pipelineData)

	// Fetch jobs to determine true status (priority: failed > running > pending > manual > success)
	if pipeline.ID != "" {
		jobsPath := fmt.Sprintf("projects/%s/pipelines/%s/jobs", g.urlEncode(repo), pipeline.ID)
		jobsCmd := g.buildGlabCommand("api", jobsPath)
		jobsOutput, err := jobsCmd.Output()
		if err == nil {
			var jobs []map[string]interface{}
			if json.Unmarshal(jobsOutput, &jobs) == nil {
				// Override pipeline status with aggregated job status
				aggregated := g.aggregateJobStatuses(jobs)
				if aggregated != "" {
					pipeline.Status = aggregated
				}
			}
		}
	}

	return pipeline, nil
}

// fetchMRApprovalsForRepo fetches approval information for a specific MR
func (g *GitLabProvider) fetchMRApprovalsForRepo(repo string, mrIID int, mr *MergeRequest) {
	apiPath := fmt.Sprintf("projects/%s/merge_requests/%d/approvals", g.urlEncode(repo), mrIID)
	cmd := g.buildGlabCommand("api", apiPath)
	output, err := cmd.Output()
	if err != nil {
		// Approval data not available - skip
		return
	}

	var approvalData map[string]interface{}
	if err := json.Unmarshal(output, &approvalData); err != nil {
		return
	}

	// Extract approval info
	if approvalsRequired, ok := approvalData["approvals_required"].(float64); ok {
		mr.RequiredApprovals = int(approvalsRequired)
	}

	if approvalsLeft, ok := approvalData["approvals_left"].(float64); ok {
		mr.ApprovalsLeft = int(approvalsLeft)
	}

	if approved, ok := approvalData["approved"].(bool); ok {
		mr.Approved = approved
	}

	if userHasApproved, ok := approvalData["user_has_approved"].(bool); ok {
		mr.UserApproved = userHasApproved
	}

	// Count approvals
	if approvedBy, ok := approvalData["approved_by"].([]interface{}); ok {
		mr.ApprovalCount = len(approvedBy)
	}

	// Check for unresolved discussions by fetching notes with pagination
	notesPath := fmt.Sprintf("projects/%s/merge_requests/%d/notes?per_page=100", g.urlEncode(repo), mrIID)
	notesCmd := g.buildGlabCommand("api", notesPath, "--paginate")
	notesOutput, err := notesCmd.Output()
	if err == nil {
		var notes []map[string]interface{}
		if json.Unmarshal(notesOutput, &notes) == nil {
			hasUnresolved := false
			for _, note := range notes {
				if resolvable, ok := note["resolvable"].(bool); ok && resolvable {
					if resolved, ok := note["resolved"].(bool); ok && !resolved {
						hasUnresolved = true
						break
					}
				}
			}
			mr.HasUnresolvedDiscussions = hasUnresolved
		}
	}
}

// GetPipelineJobs fetches jobs for a specific pipeline
func (g *GitLabProvider) GetPipelineJobs(pipelineID string, repo string) ([]PipelineJob, error) {
	jobsPath := fmt.Sprintf("projects/%s/pipelines/%s/jobs", g.urlEncode(repo), pipelineID)
	cmd := g.buildGlabCommand("api", jobsPath)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pipeline jobs: %w", err)
	}

	var jobsData []map[string]interface{}
	if err := json.Unmarshal(output, &jobsData); err != nil {
		return nil, fmt.Errorf("failed to parse jobs: %w", err)
	}

	var jobs []PipelineJob
	for _, jobData := range jobsData {
		job := PipelineJob{}

		if id, ok := jobData["id"].(float64); ok {
			job.ID = strconv.Itoa(int(id))
		}

		if name, ok := jobData["name"].(string); ok {
			job.Name = name
		}

		if status, ok := jobData["status"].(string); ok {
			job.Status = status
		}

		if stage, ok := jobData["stage"].(string); ok {
			job.Stage = stage
		}

		jobs = append(jobs, job)
	}

	return jobs, nil
}

// GetMRNotes fetches notes/comments for an MR
func (g *GitLabProvider) GetMRNotes(mrIID int, repo string) ([]MRNote, error) {
	notesPath := fmt.Sprintf("projects/%s/merge_requests/%d/notes?per_page=100", g.urlEncode(repo), mrIID)
	cmd := g.buildGlabCommand("api", notesPath, "--paginate")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch notes: %w", err)
	}

	// --paginate returns multiple JSON arrays, parse them separately
	var notesData []map[string]interface{}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for decoder.More() {
		var page []map[string]interface{}
		if err := decoder.Decode(&page); err != nil {
			return nil, fmt.Errorf("failed to parse notes page: %w", err)
		}
		notesData = append(notesData, page...)
	}

	var notes []MRNote
	for _, noteData := range notesData {
		note := MRNote{}

		if id, ok := noteData["id"].(float64); ok {
			note.ID = strconv.Itoa(int(id))
		}

		// Check if system note
		if system, ok := noteData["system"].(bool); ok {
			note.System = system
			// Skip system notes in the list
			if system {
				continue
			}
		}

		if author, ok := noteData["author"].(map[string]interface{}); ok {
			if username, ok := author["username"].(string); ok {
				note.Author = username
			}
		}

		if body, ok := noteData["body"].(string); ok {
			note.Body = body
		}

		if createdAt, ok := noteData["created_at"].(string); ok {
			note.CreatedAt = createdAt
		}

		if resolvable, ok := noteData["resolvable"].(bool); ok {
			note.Resolvable = resolvable
		}

		if resolved, ok := noteData["resolved"].(bool); ok {
			note.Resolved = resolved
		}

		notes = append(notes, note)
	}

	return notes, nil
}

// aggregateJobStatuses determines the worst status from a list of jobs
// Priority: failed > running > pending > manual > success
func (g *GitLabProvider) aggregateJobStatuses(jobs []map[string]interface{}) PipelineStatus {
	hasFailed := false
	hasRunning := false
	hasPending := false
	hasManual := false
	hasSuccess := false

	for _, job := range jobs {
		status, ok := job["status"].(string)
		if !ok {
			continue
		}

		switch status {
		case "failed":
			hasFailed = true
		case "running":
			hasRunning = true
		case "pending", "created":
			hasPending = true
		case "manual":
			hasManual = true
		case "success":
			hasSuccess = true
		}
	}

	// Return in priority order: failed > running > pending > manual > success
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
		return PipelineStatus("manual")
	}
	if hasSuccess {
		return PipelineSuccess
	}

	return PipelineStatus("")
}

// urlEncode encodes a string for use in URLs (simple implementation)
func (g *GitLabProvider) urlEncode(s string) string {
	// Replace / with %2F for GitLab API paths
	encoded := ""
	for _, char := range s {
		switch char {
		case '/':
			encoded += "%2F"
		case ' ':
			encoded += "%20"
		default:
			encoded += string(char)
		}
	}
	return encoded
}

func (g *GitLabProvider) GetMR(id string) (*MergeRequest, error) {
	cmd := g.buildGlabCommand( "mr", "view", id, "--output", "json")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get MR: %w", err)
	}

	var glabMR map[string]interface{}
	if err := json.Unmarshal(output, &glabMR); err != nil {
		return nil, fmt.Errorf("failed to parse MR: %w", err)
	}

	mr, err := g.convertGlabMR(glabMR)
	if err != nil {
		return nil, err
	}

	return &mr, nil
}

func (g *GitLabProvider) ApproveMR(id string) error {
	cmd := g.buildGlabCommand( "mr", "approve", id)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to approve MR: %w", err)
	}
	return nil
}

func (g *GitLabProvider) UnapproveMR(id string) error {
	cmd := g.buildGlabCommand( "mr", "unapprove", id)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to unapprove MR: %w", err)
	}
	return nil
}

// convertGlabMR converts glab's JSON format to our MergeRequest struct
func (g *GitLabProvider) convertGlabMR(data map[string]interface{}) (MergeRequest, error) {
	mr := MergeRequest{}

	// Extract basic fields
	if iid, ok := data["iid"].(float64); ok {
		mr.IID = int(iid)
		mr.ID = strconv.Itoa(int(iid))
	}

	if title, ok := data["title"].(string); ok {
		mr.Title = title
	}

	if author, ok := data["author"].(map[string]interface{}); ok {
		if username, ok := author["username"].(string); ok {
			mr.Author = username
		}
	}

	if sourceBranch, ok := data["source_branch"].(string); ok {
		mr.SourceBranch = sourceBranch
	}

	if targetBranch, ok := data["target_branch"].(string); ok {
		mr.TargetBranch = targetBranch
	}

	if webURL, ok := data["web_url"].(string); ok {
		mr.WebURL = webURL
	}

	// Parse status
	state := ""
	if s, ok := data["state"].(string); ok {
		state = s
	}
	draft := false
	if d, ok := data["draft"].(bool); ok {
		draft = d
	}

	if draft {
		mr.Status = StatusDraft
	} else if state == "merged" {
		mr.Status = StatusMerged
	} else if state == "closed" {
		mr.Status = StatusClosed
	} else {
		mr.Status = StatusOpen
	}

	// Parse timestamps
	if createdAt, ok := data["created_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			mr.CreatedAt = t
		}
	}

	if updatedAt, ok := data["updated_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
			mr.UpdatedAt = t
		}
	}

	// Check for conflicts
	if hasConflicts, ok := data["has_conflicts"].(bool); ok {
		mr.HasConflicts = hasConflicts
	}

	// Check for unresolved discussions
	if blockingDiscussionsResolved, ok := data["blocking_discussions_resolved"].(bool); ok {
		mr.HasUnresolvedDiscussions = !blockingDiscussionsResolved
	}

	// Parse pipeline
	if pipeline, ok := data["pipeline"].(map[string]interface{}); ok && pipeline != nil {
		mr.Pipeline = g.convertPipeline(pipeline)
	} else if headPipeline, ok := data["head_pipeline"].(map[string]interface{}); ok && headPipeline != nil {
		mr.Pipeline = g.convertPipeline(headPipeline)
	}

	// Parse approvals
	if approvedBy, ok := data["approved_by"].([]interface{}); ok {
		mr.ApprovalCount = len(approvedBy)
		// Check if current user approved
		for _, approver := range approvedBy {
			if approverMap, ok := approver.(map[string]interface{}); ok {
				if username, ok := approverMap["username"].(string); ok && username == g.currentUser {
					mr.UserApproved = true
					break
				}
			}
		}
	}

	return mr, nil
}

func (g *GitLabProvider) convertPipeline(data map[string]interface{}) *Pipeline {
	pipeline := &Pipeline{}

	if id, ok := data["id"].(float64); ok {
		pipeline.ID = strconv.Itoa(int(id))
	}

	if status, ok := data["status"].(string); ok {
		switch status {
		case "success":
			pipeline.Status = PipelineSuccess
		case "running":
			pipeline.Status = PipelineRunning
		case "failed":
			pipeline.Status = PipelineFailed
		case "pending":
			pipeline.Status = PipelinePending
		case "skipped":
			pipeline.Status = PipelineSkipped
		default:
			pipeline.Status = PipelineStatus(status)
		}
	}

	if webURL, ok := data["web_url"].(string); ok {
		pipeline.WebURL = webURL
	}

	if createdAt, ok := data["created_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			pipeline.CreatedAt = t
		}
	}

	if updatedAt, ok := data["updated_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
			pipeline.UpdatedAt = t
		}
	}

	if duration, ok := data["duration"].(float64); ok {
		pipeline.Duration = time.Duration(duration) * time.Second
	}

	return pipeline
}

// GetJobTrace fetches the log output for a specific job
func (g *GitLabProvider) GetJobTrace(jobID string, repo string) (string, error) {
	tracePath := fmt.Sprintf("projects/%s/jobs/%s/trace", g.urlEncode(repo), jobID)
	cmd := g.buildGlabCommand("api", tracePath)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to fetch job trace: %w", err)
	}

	return string(output), nil
}

// RetryJob retries a failed job
func (g *GitLabProvider) RetryJob(jobID string, repo string) error {
	retryPath := fmt.Sprintf("projects/%s/jobs/%s/retry", g.urlEncode(repo), jobID)
	cmd := g.buildGlabCommand("api", retryPath, "--method", "POST")
	output, err := cmd.CombinedOutput()
	if err != nil {
		outputStr := string(output)
		// Check for common permission errors
		if strings.Contains(outputStr, "insufficient_scope") {
			return fmt.Errorf("insufficient permissions: your GitLab token needs 'api' scope to retry jobs")
		}
		if strings.Contains(outputStr, "403") {
			return fmt.Errorf("permission denied: you may not have access to retry jobs in this project")
		}
		return fmt.Errorf("failed to retry job: %w (output: %s)", err, outputStr)
	}
	return nil
}

// PlayJob plays/triggers a manual job
func (g *GitLabProvider) PlayJob(jobID string, repo string) error {
	playPath := fmt.Sprintf("projects/%s/jobs/%s/play", g.urlEncode(repo), jobID)
	cmd := g.buildGlabCommand("api", playPath, "--method", "POST")
	output, err := cmd.CombinedOutput()
	if err != nil {
		outputStr := string(output)
		// Check for common permission errors
		if strings.Contains(outputStr, "insufficient_scope") {
			return fmt.Errorf("insufficient permissions: your GitLab token needs 'api' scope to trigger jobs")
		}
		if strings.Contains(outputStr, "403") {
			return fmt.Errorf("permission denied: you may not have access to trigger jobs in this project")
		}
		return fmt.Errorf("failed to play job: %w (output: %s)", err, outputStr)
	}
	return nil
}

// CancelJob cancels/stops a running or pending job
func (g *GitLabProvider) CancelJob(jobID string, repo string) error {
	cancelPath := fmt.Sprintf("projects/%s/jobs/%s/cancel", g.urlEncode(repo), jobID)
	cmd := g.buildGlabCommand("api", cancelPath, "--method", "POST")
	output, err := cmd.CombinedOutput()
	if err != nil {
		outputStr := string(output)
		// Check for common permission errors
		if strings.Contains(outputStr, "insufficient_scope") {
			return fmt.Errorf("insufficient permissions: your GitLab token needs 'api' scope to cancel jobs")
		}
		if strings.Contains(outputStr, "403") {
			return fmt.Errorf("permission denied: you may not have access to cancel jobs in this project")
		}
		return fmt.Errorf("failed to cancel job: %w (output: %s)", err, outputStr)
	}
	return nil
}

// ReplyToNote adds a reply to a discussion/note
func (g *GitLabProvider) CreateNote(mrIID int, body string, repo string) error {
	// Use GitLab API to create a new note
	notePath := fmt.Sprintf("projects/%s/merge_requests/%d/notes", g.urlEncode(repo), mrIID)

	cmd := g.buildGlabCommand("api", notePath, "--method", "POST", "--raw-field", fmt.Sprintf("body=%s", body))
	output, err := cmd.CombinedOutput()
	if err != nil {
		outputStr := string(output)
		if strings.Contains(outputStr, "insufficient_scope") {
			return fmt.Errorf("insufficient permissions: your GitLab token needs 'api' scope to post comments")
		}
		if strings.Contains(outputStr, "403") {
			return fmt.Errorf("permission denied: you may not have access to comment on this MR")
		}
		return fmt.Errorf("failed to create note: %w (output: %s)", err, outputStr)
	}
	return nil
}

func (g *GitLabProvider) ReplyToNote(mrIID int, noteID string, body string, repo string) error {
	// Use GitLab API to add a reply to a discussion
	replyPath := fmt.Sprintf("projects/%s/merge_requests/%d/discussions/%s/notes", g.urlEncode(repo), mrIID, noteID)

	cmd := g.buildGlabCommand("api", replyPath, "--method", "POST", "--raw-field", fmt.Sprintf("body=%s", body))
	output, err := cmd.CombinedOutput()
	if err != nil {
		outputStr := string(output)
		if strings.Contains(outputStr, "insufficient_scope") {
			return fmt.Errorf("insufficient permissions: your GitLab token needs 'api' scope to post comments")
		}
		if strings.Contains(outputStr, "403") {
			return fmt.Errorf("permission denied: you may not have access to comment on this MR")
		}
		return fmt.Errorf("failed to post reply: %w (output: %s)", err, outputStr)
	}
	return nil
}

func (g *GitLabProvider) GetMRChanges(mrIID int, repo string) (*MRChanges, error) {
	changesPath := fmt.Sprintf("projects/%s/merge_requests/%d/changes", g.urlEncode(repo), mrIID)

	cmd := g.buildGlabCommand("api", changesPath)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get MR changes: %w", err)
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
		return nil, fmt.Errorf("failed to parse MR changes: %w", err)
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
