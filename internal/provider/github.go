package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GitHubProvider implements Provider for GitHub pull requests through the gh CLI.
// PR lists come from GraphQL search; actions use REST or GraphQL mutations, like the GitLab provider.
type GitHubProvider struct {
	host     string   // optional GitHub Enterprise host; empty uses gh's default (github.com)
	repos    []string // optional owner/name filters, applied server-side as repo: qualifiers
	username string
}

// ghPageSize is the number of PRs per search request. Each PR includes its checks, reviews
// and threads, so this keeps a request well inside GitHub's node limit and 10s timeout.
const ghPageSize = 20

// NewGitHubProvider creates a GitHub provider. If repos are given, only PRs in those
// repositories are listed.
func NewGitHubProvider(host string, repos ...string) (*GitHubProvider, error) {
	p := &GitHubProvider{host: host}
	for _, r := range repos {
		if r = strings.TrimSpace(r); r != "" {
			p.repos = append(p.repos, r)
		}
	}

	username, err := p.GetCurrentUser()
	if err != nil {
		return nil, fmt.Errorf("failed to get current GitHub user: %w", err)
	}
	p.username = username
	return p, nil
}

func (p *GitHubProvider) Name() string { return "GitHub" }

// --- GraphQL ---

const ghChecksFields = `
  state
  contexts(first: 100) {
    nodes {
      __typename
      ... on CheckRun {
        databaseId
        name
        status
        conclusion
        checkSuite { workflowRun { workflow { name } } }
      }
      ... on StatusContext { context state }
    }
  }
`

const ghPullRequestFields = `
  id
  number
  title
  url
  isDraft
  state
  mergeable
  reviewDecision
  createdAt
  updatedAt
  headRefName
  baseRefName
  author { login }
  repository { nameWithOwner }
  commits(last: 1) { nodes { commit { oid statusCheckRollup {` + ghChecksFields + `} } } }
  latestReviews(first: 50) { nodes { author { login } state } }
  reviewRequests(first: 50) { nodes { requestedReviewer { __typename ... on User { login } } } }
  reviewThreads(first: 50) {
    nodes {
      id
      isResolved
      comments(first: 50) { nodes { author { login } body createdAt } }
    }
  }
  comments(first: 50) { nodes { id author { login } body createdAt } }
  reviews(first: 50) { nodes { id author { login } body state createdAt } }
`

const ghSearchQuery = `
query($q: String!, $first: Int!, $after: String) {
  search(type: ISSUE, query: $q, first: $first, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes { ... on PullRequest {` + ghPullRequestFields + `} }
  }
}
`

const ghPullRequestQuery = `
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {` + ghPullRequestFields + `}
  }
}
`

const ghCommitChecksQuery = `
query($owner: String!, $name: String!, $oid: GitObjectID!) {
  repository(owner: $owner, name: $name) {
    object(oid: $oid) { ... on Commit { statusCheckRollup {` + ghChecksFields + `} } }
  }
}
`

type ghActor struct {
	Login string `json:"login"`
}

type ghCheckContext struct {
	Typename   string `json:"__typename"`
	DatabaseID int64  `json:"databaseId"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	CheckSuite struct {
		WorkflowRun *struct {
			Workflow struct {
				Name string `json:"name"`
			} `json:"workflow"`
		} `json:"workflowRun"`
	} `json:"checkSuite"`
	Context string `json:"context"`
	State   string `json:"state"`
}

type ghChecks struct {
	State    string `json:"state"`
	Contexts struct {
		Nodes []ghCheckContext `json:"nodes"`
	} `json:"contexts"`
}

type ghComment struct {
	ID        string    `json:"id"`
	Author    ghActor   `json:"author"`
	Body      string    `json:"body"`
	State     string    `json:"state"` // reviews only
	CreatedAt time.Time `json:"createdAt"`
}

type ghPullRequest struct {
	ID             string    `json:"id"`
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	IsDraft        bool      `json:"isDraft"`
	State          string    `json:"state"`
	Mergeable      string    `json:"mergeable"`
	ReviewDecision string    `json:"reviewDecision"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	HeadRefName    string    `json:"headRefName"`
	BaseRefName    string    `json:"baseRefName"`
	Author         ghActor   `json:"author"`
	Repository     struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				Oid               string    `json:"oid"`
				StatusCheckRollup *ghChecks `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	LatestReviews struct {
		Nodes []ghComment `json:"nodes"`
	} `json:"latestReviews"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer struct {
				Typename string `json:"__typename"`
				Login    string `json:"login"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	ReviewThreads struct {
		Nodes []struct {
			ID         string `json:"id"`
			IsResolved bool   `json:"isResolved"`
			Comments   struct {
				Nodes []ghComment `json:"nodes"`
			} `json:"comments"`
		} `json:"nodes"`
	} `json:"reviewThreads"`
	Comments struct {
		Nodes []ghComment `json:"nodes"`
	} `json:"comments"`
	Reviews struct {
		Nodes []ghComment `json:"nodes"`
	} `json:"reviews"`
}

// graphql runs a GitHub GraphQL request and decodes its "data" into out.
func (p *GitHubProvider) graphql(query string, variables map[string]interface{}, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), graphQLTimeout)
	defer cancel()

	output, err := runCLI(cliCommand(ctx, "gh", p.host, graphQLArgs(query, variables)...))
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("github graphql query timed out after %v", graphQLTimeout)
	}
	if err != nil {
		return commandError("github graphql query failed", err)
	}

	var response struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return fmt.Errorf("failed to parse github graphql response: %w", err)
	}
	if len(response.Errors) > 0 {
		msgs := make([]string, 0, len(response.Errors))
		for _, e := range response.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("github graphql errors: %s", strings.Join(msgs, "; "))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(response.Data, out); err != nil {
		return fmt.Errorf("failed to parse github graphql data: %w", err)
	}
	return nil
}

// api runs a REST call through `gh api` and returns stdout.
func (p *GitHubProvider) api(action string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), graphQLTimeout)
	defer cancel()
	output, err := runCLI(cliCommand(ctx, "gh", p.host, append([]string{"api"}, args...)...))
	if err != nil {
		return nil, commandError(action, err)
	}
	return output, nil
}

// --- listing ---

func (p *GitHubProvider) GetCurrentUser() (string, error) {
	var data struct {
		Viewer ghActor `json:"viewer"`
	}
	if err := p.graphql(`query { viewer { login } }`, nil, &data); err != nil {
		return "", err
	}
	if data.Viewer.Login == "" {
		return "", fmt.Errorf("no current user found")
	}
	return data.Viewer.Login, nil
}

// searchQueries returns the GitHub search queries for a tab. Search doesn't support OR
// between qualifiers, so the reviewing tab runs two searches: open review requests, and
// PRs the user already reviewed (GitHub drops you from the requested reviewers once you review).
func (p *GitHubProvider) searchQueries(scope Scope) []string {
	var qualifiers []string
	switch scope {
	case ScopeAssigned:
		qualifiers = []string{"assignee:@me"}
	case ScopeReviewing:
		qualifiers = []string{"review-requested:@me -author:@me", "reviewed-by:@me -author:@me"}
	default:
		qualifiers = []string{"author:@me"}
	}

	common := "is:pr is:open archived:false sort:updated-desc"
	for _, r := range p.repos {
		common += " repo:" + r // several repo: qualifiers match any of them
	}

	queries := make([]string, 0, len(qualifiers))
	for _, q := range qualifiers {
		queries = append(queries, common+" "+q)
	}
	return queries
}

func (p *GitHubProvider) ListMRs(scope Scope) ([]MergeRequest, error) {
	var mrs []MergeRequest
	seen := map[string]bool{}

	for _, query := range p.searchQueries(scope) {
		prs, err := p.search(query)
		if err != nil {
			return nil, err
		}
		for _, pr := range prs {
			mr := p.convertPR(pr)
			if seen[mr.ID] {
				continue
			}
			seen[mr.ID] = true
			if scope == ScopeReviewing && mr.UserReviewState == "" {
				mr.UserReviewState = ReviewStateUnreviewed // e.g. requested via a team
			}
			mrs = append(mrs, mr)
		}
	}

	if len(p.searchQueries(scope)) > 1 {
		// Merged results: restore most-recently-updated-first order
		sort.SliceStable(mrs, func(i, j int) bool { return mrs[i].UpdatedAt.After(mrs[j].UpdatedAt) })
	}
	return mrs, nil
}

// search fetches all pages of a PR search.
func (p *GitHubProvider) search(query string) ([]ghPullRequest, error) {
	var all []ghPullRequest
	cursor := ""
	for {
		variables := map[string]interface{}{"q": query, "first": ghPageSize}
		if cursor != "" {
			variables["after"] = cursor
		}

		var data struct {
			Search struct {
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
				Nodes []ghPullRequest `json:"nodes"`
			} `json:"search"`
		}
		if err := p.graphql(ghSearchQuery, variables, &data); err != nil {
			return nil, err
		}

		for _, pr := range data.Search.Nodes {
			if pr.Number != 0 { // non-PR results decode as empty nodes
				all = append(all, pr)
			}
		}
		if !data.Search.PageInfo.HasNextPage || data.Search.PageInfo.EndCursor == "" {
			return all, nil
		}
		cursor = data.Search.PageInfo.EndCursor
	}
}

// parsePRID splits "owner/repo#123" into owner, repo name and number.
func parsePRID(id string) (owner, name string, number int, err error) {
	i := strings.LastIndex(id, "#")
	if i <= 0 {
		return "", "", 0, fmt.Errorf("invalid PR ID format: %s (expected owner/repo#123)", id)
	}
	owner, name, ok := strings.Cut(id[:i], "/")
	number, convErr := strconv.Atoi(id[i+1:])
	if !ok || owner == "" || name == "" || convErr != nil || number <= 0 {
		return "", "", 0, fmt.Errorf("invalid PR ID format: %s (expected owner/repo#123)", id)
	}
	return owner, name, number, nil
}

func splitRepo(repo string) (owner, name string, err error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return "", "", fmt.Errorf("invalid repository %q (expected owner/name)", repo)
	}
	return owner, name, nil
}

func (p *GitHubProvider) getPR(id string) (*ghPullRequest, error) {
	owner, name, number, err := parsePRID(id)
	if err != nil {
		return nil, err
	}
	var data struct {
		Repository *struct {
			PullRequest *ghPullRequest `json:"pullRequest"`
		} `json:"repository"`
	}
	vars := map[string]interface{}{"owner": owner, "name": name, "number": number}
	if err := p.graphql(ghPullRequestQuery, vars, &data); err != nil {
		return nil, err
	}
	if data.Repository == nil || data.Repository.PullRequest == nil {
		return nil, fmt.Errorf("PR not found: %s", id)
	}
	return data.Repository.PullRequest, nil
}

func (p *GitHubProvider) GetMR(id string) (*MergeRequest, error) {
	pr, err := p.getPR(id)
	if err != nil {
		return nil, err
	}
	mr := p.convertPR(*pr)
	return &mr, nil
}

func (p *GitHubProvider) ListRepos() ([]string, error) {
	output, err := p.api("failed to list repos", "user/repos?per_page=100", "--paginate")
	if err != nil {
		return nil, err
	}
	items, err := decodeJSONArrays(output)
	if err != nil {
		return nil, fmt.Errorf("failed to parse repos: %w", err)
	}
	repos := make([]string, 0, len(items))
	for _, item := range items {
		if name, ok := item["full_name"].(string); ok {
			repos = append(repos, name)
		}
	}
	return repos, nil
}

// --- conversion ---

func (p *GitHubProvider) convertPR(pr ghPullRequest) MergeRequest {
	repo := pr.Repository.NameWithOwner
	mr := MergeRequest{
		ID:             fmt.Sprintf("%s#%d", repo, pr.Number),
		IID:            pr.Number,
		Title:          pr.Title,
		Author:         pr.Author.Login,
		SourceBranch:   pr.HeadRefName,
		TargetBranch:   pr.BaseRefName,
		WebURL:         pr.URL,
		CreatedAt:      pr.CreatedAt,
		UpdatedAt:      pr.UpdatedAt,
		HasConflicts:   pr.Mergeable == "CONFLICTING",
		RepoName:       repo,
		ReviewDecision: pr.ReviewDecision,
	}

	switch {
	case pr.IsDraft:
		mr.Status = StatusDraft
	case pr.State == "MERGED":
		mr.Status = StatusMerged
	case pr.State == "CLOSED":
		mr.Status = StatusClosed
	default:
		mr.Status = StatusOpen
	}

	// Approvals. GitHub only exposes the required count to admins, so Approved follows the
	// review decision; with no review rule (decision empty) nothing is required.
	var myReview string
	for _, review := range pr.LatestReviews.Nodes {
		if review.State == "APPROVED" {
			mr.ApprovalCount++
		}
		if review.Author.Login == p.username {
			myReview = review.State
		}
	}
	mr.UserApproved = myReview == "APPROVED"
	mr.Approved = pr.ReviewDecision == "APPROVED" || pr.ReviewDecision == ""

	// Review state. Being (re-)requested means a review is wanted, whatever came before.
	requested := false
	for _, req := range pr.ReviewRequests.Nodes {
		if req.RequestedReviewer.Typename == "User" && req.RequestedReviewer.Login == p.username {
			requested = true
		}
	}
	switch {
	case requested:
		mr.UserReviewState = ReviewStateUnreviewed
	case myReview == "APPROVED":
		mr.UserReviewState = ReviewStateApproved
	case myReview == "CHANGES_REQUESTED":
		mr.UserReviewState = ReviewStateRequestedChanges
	case myReview == "COMMENTED":
		mr.UserReviewState = ReviewStateReviewed
	case myReview == "DISMISSED":
		mr.UserReviewState = ReviewStateUnapproved
	}

	// Checks
	if len(pr.Commits.Nodes) > 0 {
		commit := pr.Commits.Nodes[0].Commit
		if commit.StatusCheckRollup != nil {
			mr.Pipeline, mr.PipelineJobs = convertChecks(commit.Oid, *commit.StatusCheckRollup)
			mr.Pipeline.WebURL = pr.URL + "/checks"
		}
	}

	// Notes: top-level comments and review summaries (flat conversation, not resolvable),
	// then review threads (resolvable, identified by thread ID).
	mr.Notes = make([]MRNote, 0)
	var conversation []ghComment
	conversation = append(conversation, pr.Comments.Nodes...)
	for _, review := range pr.Reviews.Nodes {
		if strings.TrimSpace(review.Body) != "" {
			conversation = append(conversation, review)
		}
	}
	sort.SliceStable(conversation, func(i, j int) bool { return conversation[i].CreatedAt.Before(conversation[j].CreatedAt) })
	for _, c := range conversation {
		mr.Notes = append(mr.Notes, MRNote{
			ID:        ghCommentNotePrefix + c.ID,
			Author:    c.Author.Login,
			Body:      c.Body,
			CreatedAt: c.CreatedAt.Format(time.RFC3339),
		})
	}

	for _, thread := range pr.ReviewThreads.Nodes {
		if !thread.IsResolved {
			mr.HasUnresolvedDiscussions = true
		}
		authors := make([]string, 0, len(thread.Comments.Nodes))
		for _, c := range thread.Comments.Nodes {
			authors = append(authors, c.Author.Login)
			mr.Notes = append(mr.Notes, MRNote{
				ID:         thread.ID,
				Author:     c.Author.Login,
				Body:       c.Body,
				CreatedAt:  c.CreatedAt.Format(time.RFC3339),
				Resolvable: true,
				Resolved:   thread.IsResolved,
			})
		}
		if !thread.IsResolved && awaitsReply(authors, p.username) {
			mr.HasUnresolvedReplies = true
		}
	}

	return mr
}

// ghCommentNotePrefix marks notes that are top-level PR comments rather than review threads.
// Replying to one posts a new top-level comment, since GitHub's conversation is flat.
const ghCommentNotePrefix = "comment:"

// convertChecks maps a commit's check rollup to a pipeline (ID = commit SHA) and its jobs.
func convertChecks(sha string, checks ghChecks) (*Pipeline, []PipelineJob) {
	pipeline := &Pipeline{ID: sha}
	switch checks.State {
	case "SUCCESS":
		pipeline.Status = PipelineSuccess
	case "FAILURE", "ERROR":
		pipeline.Status = PipelineFailed
	default: // PENDING, EXPECTED
		pipeline.Status = PipelinePending
	}

	jobs := make([]PipelineJob, 0, len(checks.Contexts.Nodes))
	statuses := make([]string, 0, len(checks.Contexts.Nodes))
	for _, c := range checks.Contexts.Nodes {
		var job PipelineJob
		if c.Typename == "StatusContext" {
			// Commit statuses (external CI) have no job ID: no logs or retry
			job = PipelineJob{Name: c.Context, Status: commitStatusState(c.State), Stage: "status"}
		} else {
			job = PipelineJob{
				ID:     strconv.FormatInt(c.DatabaseID, 10),
				Name:   c.Name,
				Status: checkRunStatus(c.Status, c.Conclusion),
				Stage:  "checks",
			}
			if c.CheckSuite.WorkflowRun != nil && c.CheckSuite.WorkflowRun.Workflow.Name != "" {
				job.Stage = c.CheckSuite.WorkflowRun.Workflow.Name
			}
		}
		jobs = append(jobs, job)
		statuses = append(statuses, job.Status)
	}

	// Same rule as GitLab: a failed job marks the pipeline failed, a running one running
	if aggregated := aggregateJobStatuses(statuses); aggregated != "" {
		pipeline.Status = aggregated
	}
	return pipeline, jobs
}

// checkRunStatus maps a check run's status/conclusion to glamr's lower-case job statuses.
func checkRunStatus(status, conclusion string) string {
	switch strings.ToUpper(status) {
	case "COMPLETED":
		switch strings.ToUpper(conclusion) {
		case "SUCCESS":
			return "success"
		case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE":
			return "failed"
		case "CANCELLED":
			return "canceled"
		case "SKIPPED", "NEUTRAL", "STALE":
			return "skipped"
		default: // ACTION_REQUIRED
			return "pending"
		}
	case "IN_PROGRESS":
		return "running"
	default: // QUEUED, PENDING, WAITING, REQUESTED
		return "pending"
	}
}

func commitStatusState(state string) string {
	switch strings.ToUpper(state) {
	case "SUCCESS":
		return "success"
	case "FAILURE", "ERROR":
		return "failed"
	default:
		return "pending"
	}
}

// --- detail data ---

// GetPipelineJobs returns the checks of a commit; for GitHub the pipeline ID is the commit SHA.
func (p *GitHubProvider) GetPipelineJobs(pipelineID string, repo string) ([]PipelineJob, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	var data struct {
		Repository *struct {
			Object *struct {
				StatusCheckRollup *ghChecks `json:"statusCheckRollup"`
			} `json:"object"`
		} `json:"repository"`
	}
	vars := map[string]interface{}{"owner": owner, "name": name, "oid": pipelineID}
	if err := p.graphql(ghCommitChecksQuery, vars, &data); err != nil {
		return nil, err
	}
	if data.Repository == nil || data.Repository.Object == nil || data.Repository.Object.StatusCheckRollup == nil {
		return []PipelineJob{}, nil
	}
	_, jobs := convertChecks(pipelineID, *data.Repository.Object.StatusCheckRollup)
	return jobs, nil
}

func (p *GitHubProvider) GetMRNotes(mrIID int, repo string) ([]MRNote, error) {
	mr, err := p.GetMR(fmt.Sprintf("%s#%d", repo, mrIID))
	if err != nil {
		return nil, err
	}
	return mr.Notes, nil
}

func (p *GitHubProvider) GetMRChanges(mrIID int, repo string) (*MRChanges, error) {
	output, err := p.api("failed to get PR files", fmt.Sprintf("repos/%s/pulls/%d/files?per_page=100", repo, mrIID), "--paginate")
	if err != nil {
		return nil, err
	}
	files, err := decodeJSONArrays(output)
	if err != nil {
		return nil, fmt.Errorf("failed to parse PR files: %w", err)
	}

	changes := &MRChanges{Changes: make([]FileChange, 0, len(files))}
	for _, f := range files {
		filename, _ := f["filename"].(string)
		status, _ := f["status"].(string)
		patch, _ := f["patch"].(string) // absent for binary or very large files
		change := FileChange{
			OldPath:     filename,
			NewPath:     filename,
			NewFile:     status == "added",
			DeletedFile: status == "removed",
			RenamedFile: status == "renamed",
			Diff:        patch,
		}
		if previous, ok := f["previous_filename"].(string); ok && previous != "" {
			change.OldPath = previous
		}
		if patch != "" && !strings.HasSuffix(patch, "\n") {
			change.Diff += "\n"
		}
		changes.Changes = append(changes.Changes, change)
	}
	return changes, nil
}

// --- actions ---

func (p *GitHubProvider) ApproveMR(id string) error {
	owner, name, number, err := parsePRID(id)
	if err != nil {
		return err
	}
	_, err = p.api("failed to approve PR", fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, name, number),
		"-X", "POST", "-f", "event=APPROVE")
	return err
}

// UnapproveMR dismisses the user's latest approving review. GitHub has no way to withdraw an
// approval otherwise; dismissing needs write access to the repository.
func (p *GitHubProvider) UnapproveMR(id string) error {
	owner, name, number, err := parsePRID(id)
	if err != nil {
		return err
	}
	reviewsPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, name, number)
	output, err := p.api("failed to list reviews", reviewsPath+"?per_page=100", "--paginate")
	if err != nil {
		return err
	}
	reviews, err := decodeJSONArrays(output)
	if err != nil {
		return fmt.Errorf("failed to parse reviews: %w", err)
	}

	reviewID := ""
	for _, r := range reviews { // chronological; keep the latest approval by the user
		user, _ := r["user"].(map[string]any)
		login, _ := user["login"].(string)
		state, _ := r["state"].(string)
		if id, ok := r["id"].(float64); ok && login == p.username && state == "APPROVED" {
			reviewID = strconv.FormatInt(int64(id), 10)
		}
	}
	if reviewID == "" {
		return errors.New("no approval of yours to dismiss on this PR")
	}

	_, err = p.api("failed to dismiss approval", reviewsPath+"/"+reviewID+"/dismissals",
		"-X", "PUT", "-f", "message=Approval withdrawn", "-f", "event=DISMISS")
	return err
}

func (p *GitHubProvider) CreateNote(mrIID int, body string, repo string) error {
	_, err := p.api("failed to create comment", fmt.Sprintf("repos/%s/issues/%d/comments", repo, mrIID),
		"-X", "POST", "-f", "body="+body)
	return err
}

func (p *GitHubProvider) ReplyToNote(mrIID int, noteID string, body string, repo string) error {
	if strings.HasPrefix(noteID, ghCommentNotePrefix) {
		// Top-level conversation is flat: a reply is a new comment
		return p.CreateNote(mrIID, body, repo)
	}
	return p.graphql(`mutation($id: ID!, $body: String!) {
  addPullRequestReviewThreadReply(input: {pullRequestReviewThreadId: $id, body: $body}) { comment { id } }
}`, map[string]interface{}{"id": noteID, "body": body}, nil)
}

func (p *GitHubProvider) ResolveDiscussion(mrIID int, discussionID string, repo string) error {
	return p.graphql(`mutation($id: ID!) { resolveReviewThread(input: {threadId: $id}) { thread { id } } }`,
		map[string]interface{}{"id": discussionID}, nil)
}

func (p *GitHubProvider) UnresolveDiscussion(mrIID int, discussionID string, repo string) error {
	return p.graphql(`mutation($id: ID!) { unresolveReviewThread(input: {threadId: $id}) { thread { id } } }`,
		map[string]interface{}{"id": discussionID}, nil)
}

func (p *GitHubProvider) ToggleDraftStatus(mrID string) error {
	pr, err := p.getPR(mrID)
	if err != nil {
		return err
	}
	mutation := `mutation($id: ID!) { convertPullRequestToDraft(input: {pullRequestId: $id}) { pullRequest { id } } }`
	if pr.IsDraft {
		mutation = `mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { id } } }`
	}
	return p.graphql(mutation, map[string]interface{}{"id": pr.ID}, nil)
}

var errNotActionsJob = errors.New("only GitHub Actions jobs support logs and job actions (this is an external status check)")

func (p *GitHubProvider) GetJobTrace(jobID string, repo string) (string, error) {
	if jobID == "" || jobID == "0" {
		return "", errNotActionsJob
	}
	output, err := p.api("failed to get job log", fmt.Sprintf("repos/%s/actions/jobs/%s/logs", repo, jobID))
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func (p *GitHubProvider) RetryJob(jobID string, repo string) error {
	if jobID == "" || jobID == "0" {
		return errNotActionsJob
	}
	_, err := p.api("failed to re-run job", fmt.Sprintf("repos/%s/actions/jobs/%s/rerun", repo, jobID), "-X", "POST")
	return err
}

func (p *GitHubProvider) PlayJob(jobID string, repo string) error {
	return errors.New("GitHub Actions has no manual jobs to play")
}

// CancelJob cancels the workflow run the job belongs to; GitHub can't cancel single jobs.
func (p *GitHubProvider) CancelJob(jobID string, repo string) error {
	if jobID == "" || jobID == "0" {
		return errNotActionsJob
	}
	output, err := p.api("failed to look up job", fmt.Sprintf("repos/%s/actions/jobs/%s", repo, jobID))
	if err != nil {
		return err
	}
	var job struct {
		RunID int64 `json:"run_id"`
	}
	if err := json.Unmarshal(output, &job); err != nil || job.RunID == 0 {
		return fmt.Errorf("failed to find the workflow run of job %s", jobID)
	}
	_, err = p.api("failed to cancel workflow run", fmt.Sprintf("repos/%s/actions/runs/%d/cancel", repo, job.RunID), "-X", "POST")
	return err
}
