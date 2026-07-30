package provider

import "time"

// MRStatus represents the state of a merge request
type MRStatus string

const (
	StatusOpen   MRStatus = "open"
	StatusMerged MRStatus = "merged"
	StatusClosed MRStatus = "closed"
	StatusDraft  MRStatus = "draft"
)

// PipelineStatus represents CI/CD pipeline state
type PipelineStatus string

const (
	PipelineSuccess PipelineStatus = "success"
	PipelineRunning PipelineStatus = "running"
	PipelineFailed  PipelineStatus = "failed"
	PipelinePending PipelineStatus = "pending"
	PipelineSkipped PipelineStatus = "skipped"
)

// MergeRequest represents a merge/pull request
type MergeRequest struct {
	ID                string
	IID               int // Internal ID (the number shown in UI)
	Title             string
	Author            string
	SourceBranch      string
	TargetBranch      string
	Status            MRStatus
	WebURL            string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	HasConflicts      bool
	HasUnresolvedDiscussions bool
	Pipeline          *Pipeline
	PipelineJobs      []PipelineJob // Cached pipeline jobs
	Notes             []MRNote      // Cached notes/discussions
	ApprovalCount     int
	RequiredApprovals int
	ApprovalsLeft     int
	UserApproved      bool // Did the current user approve?
	Approved          bool // Has enough approvals
	RepoName          string // Repository name (for multi-repo support)
}

// Pipeline represents a CI/CD pipeline
type Pipeline struct {
	ID        string
	Status    PipelineStatus
	WebURL    string
	CreatedAt time.Time
	UpdatedAt time.Time
	Duration  time.Duration
}

// Scope defines which MRs to fetch
type Scope string

const (
	ScopeAuthored  Scope = "authored"  // MRs created by user
	ScopeAssigned  Scope = "assigned"  // MRs assigned to user
	ScopeReviewing Scope = "reviewing" // MRs user needs to review
)

// Provider is the interface that all VCS providers must implement
type Provider interface {
	// ListMRs fetches merge requests for a given scope
	ListMRs(scope Scope) ([]MergeRequest, error)

	// GetMR fetches detailed information about a specific MR
	GetMR(id string) (*MergeRequest, error)

	// ApproveMR approves a merge request
	ApproveMR(id string) error

	// UnapproveMR removes approval from a merge request
	UnapproveMR(id string) error

	// GetCurrentUser returns the username of the authenticated user
	GetCurrentUser() (string, error)

	// Name returns the provider name (gitlab, github)
	Name() string

	// ListRepos returns all repositories accessible to the user
	ListRepos() ([]string, error)

	// GetPipelineJobs fetches jobs for a pipeline
	GetPipelineJobs(pipelineID string, repo string) ([]PipelineJob, error)

	// GetMRNotes fetches notes/comments for an MR
	GetMRNotes(mrIID int, repo string) ([]MRNote, error)

	// CreateNote creates a new note/comment on an MR
	CreateNote(mrIID int, body string, repo string) error

	// ReplyToNote adds a reply to a discussion/note
	ReplyToNote(mrIID int, noteID string, body string, repo string) error

	// GetJobTrace fetches the log output for a specific job
	GetJobTrace(jobID string, repo string) (string, error)

	// RetryJob retries a failed job
	RetryJob(jobID string, repo string) error

	// PlayJob plays/triggers a manual job
	PlayJob(jobID string, repo string) error

	// CancelJob cancels/stops a running or pending job
	CancelJob(jobID string, repo string) error

	// GetMRChanges fetches the diff/changes for an MR
	GetMRChanges(mrIID int, repo string) (*MRChanges, error)

	// ResolveDiscussion marks a discussion as resolved
	ResolveDiscussion(mrIID int, discussionID string, repo string) error

	// UnresolveDiscussion marks a discussion as unresolved
	UnresolveDiscussion(mrIID int, discussionID string, repo string) error

	// ToggleDraftStatus toggles the draft status of an MR
	ToggleDraftStatus(mrID string) error
}

// PipelineJob represents a CI/CD job
type PipelineJob struct {
	ID     string
	Name   string
	Status string
	Stage  string
}

// MRNote represents a comment/discussion on an MR
type MRNote struct {
	ID         string
	Author     string
	Body       string
	CreatedAt  string
	Resolvable bool
	Resolved   bool
	System     bool // System-generated note
}

// MRChanges represents the changes/diffs in an MR
type MRChanges struct {
	Changes []FileChange
}

// FileChange represents a single file change in an MR
type FileChange struct {
	OldPath     string
	NewPath     string
	NewFile     bool
	RenamedFile bool
	DeletedFile bool
	Diff        string
}
