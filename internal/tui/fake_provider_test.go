package tui

import (
	"fmt"
	"sync"

	"glamr/internal/provider"
)

// fakeProvider is an in-memory provider.Provider that records the actions performed on it.
type fakeProvider struct {
	mu sync.Mutex

	mrs     map[provider.Scope][]provider.MergeRequest
	listErr error
	notes   []provider.MRNote
	changes *provider.MRChanges

	calls []string // e.g. "approve team/app!1"
}

func (f *fakeProvider) record(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf(format, args...))
}

func (f *fakeProvider) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeProvider) ListMRs(scope provider.Scope) ([]provider.MergeRequest, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.mrs[scope], nil
}

func (f *fakeProvider) GetMR(id string) (*provider.MergeRequest, error) { return nil, nil }

func (f *fakeProvider) ApproveMR(id string) error {
	f.record("approve %s", id)
	return nil
}

func (f *fakeProvider) UnapproveMR(id string) error {
	f.record("unapprove %s", id)
	return nil
}

func (f *fakeProvider) GetCurrentUser() (string, error) { return "me", nil }
func (f *fakeProvider) Name() string                    { return "fake" }
func (f *fakeProvider) ListRepos() ([]string, error)    { return nil, nil }

func (f *fakeProvider) GetPipelineJobs(pipelineID string, repo string) ([]provider.PipelineJob, error) {
	return nil, nil
}

func (f *fakeProvider) GetMRNotes(mrIID int, repo string) ([]provider.MRNote, error) {
	return f.notes, nil
}

func (f *fakeProvider) CreateNote(mrIID int, body string, repo string) error {
	f.record("note %s!%d %s", repo, mrIID, body)
	return nil
}

func (f *fakeProvider) ReplyToNote(mrIID int, noteID string, body string, repo string) error {
	f.record("reply %s!%d %s %s", repo, mrIID, noteID, body)
	return nil
}

func (f *fakeProvider) GetJobTrace(jobID string, repo string) (string, error) {
	return "line1\nline2\nline3", nil
}

func (f *fakeProvider) RetryJob(jobID string, repo string) error {
	f.record("retry %s %s", repo, jobID)
	return nil
}

func (f *fakeProvider) PlayJob(jobID string, repo string) error {
	f.record("play %s %s", repo, jobID)
	return nil
}

func (f *fakeProvider) CancelJob(jobID string, repo string) error {
	f.record("cancel %s %s", repo, jobID)
	return nil
}

func (f *fakeProvider) GetMRChanges(mrIID int, repo string) (*provider.MRChanges, error) {
	return f.changes, nil
}

func (f *fakeProvider) ResolveDiscussion(mrIID int, discussionID string, repo string) error {
	f.record("resolve %s!%d %s", repo, mrIID, discussionID)
	return nil
}

func (f *fakeProvider) UnresolveDiscussion(mrIID int, discussionID string, repo string) error {
	f.record("unresolve %s!%d %s", repo, mrIID, discussionID)
	return nil
}

func (f *fakeProvider) ToggleDraftStatus(mrID string) error {
	f.record("draft %s", mrID)
	return nil
}
