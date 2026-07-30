package graphql

import "time"

// GraphQL response structures
// These mirror the GraphQL API response format and are converted to provider.MergeRequest types

type GraphQLResponse struct {
	Data   Data                   `json:"data"`
	Errors []GraphQLError         `json:"errors,omitempty"`
}

type GraphQLError struct {
	Message string `json:"message"`
	Path    []any  `json:"path,omitempty"`
}

type Data struct {
	CurrentUser *CurrentUser `json:"currentUser"`
	Project     *Project     `json:"project"`
}

type CurrentUser struct {
	Username                      string                `json:"username"`
	AuthoredMergeRequests         *MergeRequestConnection `json:"authoredMergeRequests,omitempty"`
	AssignedMergeRequests         *MergeRequestConnection `json:"assignedMergeRequests,omitempty"`
	ReviewRequestedMergeRequests  *MergeRequestConnection `json:"reviewRequestedMergeRequests,omitempty"`
}

type Project struct {
	FullPath     string        `json:"fullPath"`
	Name         string        `json:"name"`
	MergeRequest *MergeRequest `json:"mergeRequest,omitempty"`
}

type MergeRequestConnection struct {
	PageInfo PageInfo       `json:"pageInfo"`
	Nodes    []MergeRequest `json:"nodes"`
}

type PageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type MergeRequest struct {
	IID                   string              `json:"iid"`
	ID                    string              `json:"id"`
	Title                 string              `json:"title"`
	Description           string              `json:"description"`
	WebURL                string              `json:"webUrl"`
	SourceBranch          string              `json:"sourceBranch"`
	TargetBranch          string              `json:"targetBranch"`
	State                 string              `json:"state"`
	Draft                 bool                `json:"draft"`
	CreatedAt             time.Time           `json:"createdAt"`
	UpdatedAt             time.Time           `json:"updatedAt"`
	Conflicts             bool                `json:"conflicts"`
	Author                User                `json:"author"`
	Project               ProjectInfo         `json:"project"`
	HeadPipeline          *Pipeline           `json:"headPipeline"`
	ApprovedBy            UserConnection      `json:"approvedBy"`
	Approved              bool                `json:"approved"`
	ApprovalsLeft         int                 `json:"approvalsLeft"`
	ApprovalsRequired     int                 `json:"approvalsRequired"`
	UserDiscussionsCount  int                 `json:"userDiscussionsCount"`
	Discussions           DiscussionConnection `json:"discussions"`
}

type User struct {
	Username string `json:"username"`
	Name     string `json:"name"`
}

type UserConnection struct {
	Nodes []User `json:"nodes"`
}

type ProjectInfo struct {
	FullPath string `json:"fullPath"`
	Name     string `json:"name"`
}

type Pipeline struct {
	ID        string        `json:"id"`
	Status    string        `json:"status"`
	Duration  int           `json:"duration"`
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
	Jobs      JobConnection `json:"jobs"`
}

type JobConnection struct {
	Nodes []Job `json:"nodes"`
}

type Job struct {
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	Status string    `json:"status"`
	Stage  CiStage   `json:"stage"`
}

type CiStage struct {
	Name string `json:"name"`
}

type DiscussionConnection struct {
	Nodes []Discussion `json:"nodes"`
}

type Discussion struct {
	Resolved   bool           `json:"resolved"`
	Resolvable bool           `json:"resolvable"`
	Notes      NoteConnection `json:"notes"`
}

type NoteConnection struct {
	Nodes []Note `json:"nodes"`
}

type Note struct {
	Author     User      `json:"author"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
	System     bool      `json:"system"`
	Resolvable bool      `json:"resolvable"`
	Resolved   bool      `json:"resolved"`
}
