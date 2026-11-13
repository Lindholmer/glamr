package provider

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"glamr/internal/graphql"
)

func TestGraphQLBuildGlabCommand(t *testing.T) {
	withHost := &GitLabGraphQLProvider{host: "gitlab.example.com"}
	cmd := withHost.buildGlabCommand("api", "projects/1")
	want := []string{"glab", "api", "--hostname", "gitlab.example.com", "projects/1"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("args = %q, want %q", cmd.Args, want)
	}

	// Without --host, glab's default host must be used rather than passing an empty --hostname
	noHost := &GitLabGraphQLProvider{}
	cmd = noHost.buildGlabCommand("api", "projects/1")
	want = []string{"glab", "api", "projects/1"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("args without host = %q, want %q", cmd.Args, want)
	}
}

func TestGraphQLArgs(t *testing.T) {
	args := graphQLArgs("query Q { x }", map[string]interface{}{
		"iid":   "42",
		"first": 10,
		"after": "eyJpZCI6IjEyMyJ9",
	})
	want := []string{
		"api", "graphql", "-f", "query=query Q { x }",
		// sorted by key; strings are raw (-f) so "42" stays a String, ints are typed (-F)
		"-f", "after=eyJpZCI6IjEyMyJ9",
		"-F", "first=10",
		"-f", "iid=42",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("graphQLArgs =\n%q\nwant\n%q", args, want)
	}
}

func TestExtractNumericID(t *testing.T) {
	tests := map[string]string{
		"gid://gitlab/Ci::Pipeline/123":         "123",
		"gid://gitlab/Discussion/abcdef0123456": "abcdef0123456",
		"456":                                   "456",
		"":                                      "",
	}
	for in, want := range tests {
		if got := extractNumericID(in); got != want {
			t.Errorf("extractNumericID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseIID(t *testing.T) {
	if got := parseIID("17"); got != 17 {
		t.Errorf("parseIID(17) = %d", got)
	}
	if got := parseIID("abc"); got != 0 {
		t.Errorf("parseIID(abc) = %d, want 0", got)
	}
}

func TestConvertPipelineStatus(t *testing.T) {
	tests := map[string]PipelineStatus{
		"SUCCESS":              PipelineSuccess,
		"running":              PipelineRunning,
		"FAILED":               PipelineFailed,
		"PENDING":              PipelinePending,
		"CREATED":              PipelinePending,
		"WAITING_FOR_RESOURCE": PipelinePending,
		"SKIPPED":              PipelineSkipped,
		"CANCELED":             PipelineStatus("canceled"),
		"MANUAL":               PipelineStatus("manual"),
		"":                     PipelinePending,
	}
	for in, want := range tests {
		if got := convertPipelineStatus(in); got != want {
			t.Errorf("convertPipelineStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGetQueryForScope(t *testing.T) {
	p := &GitLabGraphQLProvider{}
	if p.getQueryForScope(ScopeAuthored) != graphql.AuthoredMRsQuery ||
		p.getQueryForScope(ScopeAssigned) != graphql.AssignedMRsQuery ||
		p.getQueryForScope(ScopeReviewing) != graphql.ReviewRequestedMRsQuery {
		t.Error("getQueryForScope returned the wrong query")
	}
}

func baseGraphQLMR() graphql.MergeRequest {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return graphql.MergeRequest{
		IID:          "42",
		Title:        "Add feature",
		WebURL:       "https://gitlab.example.com/team/app/-/merge_requests/42",
		SourceBranch: "feature",
		TargetBranch: "main",
		State:        "opened",
		CreatedAt:    created,
		UpdatedAt:    created,
		Author:       graphql.User{Username: "alice"},
		Project:      graphql.ProjectInfo{FullPath: "team/app"},
	}
}

func TestConvertGraphQLMRBasics(t *testing.T) {
	p := &GitLabGraphQLProvider{host: "gitlab.example.com", username: "me"}
	gql := baseGraphQLMR()
	gql.Conflicts = true
	gql.ApprovalsRequired = 2
	gql.ApprovalsLeft = 1
	gql.ApprovedBy = graphql.UserConnection{Nodes: []graphql.User{{Username: "bob"}, {Username: "me"}}}

	mr := p.convertGraphQLMR(gql)

	if mr.ID != "team/app!42" || mr.IID != 42 || mr.RepoName != "team/app" {
		t.Errorf("ID/IID/RepoName = %q/%d/%q", mr.ID, mr.IID, mr.RepoName)
	}
	if mr.Status != StatusOpen {
		t.Errorf("Status = %q, want open", mr.Status)
	}
	if !mr.HasConflicts || mr.Author != "alice" || mr.SourceBranch != "feature" || mr.TargetBranch != "main" {
		t.Errorf("unexpected basic fields: %+v", mr)
	}
	if mr.ApprovalCount != 2 || mr.RequiredApprovals != 2 || mr.ApprovalsLeft != 1 || !mr.UserApproved {
		t.Errorf("approvals = count %d, required %d, left %d, userApproved %v",
			mr.ApprovalCount, mr.RequiredApprovals, mr.ApprovalsLeft, mr.UserApproved)
	}
	if mr.Pipeline != nil {
		t.Errorf("expected no pipeline, got %+v", mr.Pipeline)
	}
}

func TestConvertGraphQLMRStatus(t *testing.T) {
	p := &GitLabGraphQLProvider{}
	tests := []struct {
		state string
		draft bool
		want  MRStatus
	}{
		{"opened", false, StatusOpen},
		{"opened", true, StatusDraft},
		{"merged", false, StatusMerged},
		{"closed", false, StatusClosed},
		{"locked", false, StatusOpen},
	}
	for _, tt := range tests {
		gql := baseGraphQLMR()
		gql.State, gql.Draft = tt.state, tt.draft
		if got := p.convertGraphQLMR(gql).Status; got != tt.want {
			t.Errorf("state=%q draft=%v -> %q, want %q", tt.state, tt.draft, got, tt.want)
		}
	}
}

func TestConvertGraphQLMRPipeline(t *testing.T) {
	p := &GitLabGraphQLProvider{host: "gitlab.example.com"}
	gql := baseGraphQLMR()
	gql.HeadPipeline = &graphql.Pipeline{
		ID:       "gid://gitlab/Ci::Pipeline/555",
		Status:   "SUCCESS",
		Duration: 90,
		Jobs: graphql.JobConnection{Nodes: []graphql.Job{
			{ID: "gid://gitlab/Ci::Build/1", Name: "build", Status: "SUCCESS", Stage: graphql.CiStage{Name: "build"}},
			{ID: "gid://gitlab/Ci::Build/2", Name: "test", Status: "FAILED", Stage: graphql.CiStage{Name: "test"}},
		}},
	}

	mr := p.convertGraphQLMR(gql)

	if mr.Pipeline == nil {
		t.Fatal("expected pipeline")
	}
	if mr.Pipeline.ID != "555" || mr.Pipeline.Duration != 90*time.Second {
		t.Errorf("pipeline ID/duration = %q/%v", mr.Pipeline.ID, mr.Pipeline.Duration)
	}
	if mr.Pipeline.WebURL != "https://gitlab.example.com/team/app/-/pipelines/555" {
		t.Errorf("pipeline URL = %q", mr.Pipeline.WebURL)
	}
	// A failed job overrides the pipeline's reported status
	if mr.Pipeline.Status != PipelineFailed {
		t.Errorf("pipeline status = %q, want failed", mr.Pipeline.Status)
	}
	// Job statuses are normalised to lower case so the TUI's retry/play/cancel checks match
	want := []PipelineJob{
		{ID: "1", Name: "build", Status: "success", Stage: "build"},
		{ID: "2", Name: "test", Status: "failed", Stage: "test"},
	}
	if !reflect.DeepEqual(mr.PipelineJobs, want) {
		t.Errorf("jobs = %+v, want %+v", mr.PipelineJobs, want)
	}
}

func TestConvertGraphQLMRPipelineKeepsStatusWhenJobsUnrecognised(t *testing.T) {
	p := &GitLabGraphQLProvider{}
	gql := baseGraphQLMR()
	gql.HeadPipeline = &graphql.Pipeline{
		ID:     "gid://gitlab/Ci::Pipeline/1",
		Status: "CANCELED",
		Jobs:   graphql.JobConnection{Nodes: []graphql.Job{{ID: "1", Status: "CANCELED"}}},
	}
	if got := p.convertGraphQLMR(gql).Pipeline.Status; got != PipelineStatus("canceled") {
		t.Errorf("status = %q, want canceled (not success)", got)
	}
}

func TestConvertGraphQLMRReviewState(t *testing.T) {
	p := &GitLabGraphQLProvider{username: "me"}
	gql := baseGraphQLMR()
	gql.Reviewers = graphql.ReviewerConnection{Nodes: []graphql.Reviewer{
		{Username: "other", MergeRequestInteraction: graphql.MergeRequestInteraction{ReviewState: "APPROVED"}},
		{Username: "me", MergeRequestInteraction: graphql.MergeRequestInteraction{ReviewState: "REQUESTED_CHANGES"}},
	}}
	if got := p.convertGraphQLMR(gql).UserReviewState; got != ReviewStateRequestedChanges {
		t.Errorf("UserReviewState = %q, want REQUESTED_CHANGES", got)
	}
}

func note(author, body string) graphql.Note {
	return graphql.Note{Author: graphql.User{Username: author}, Body: body, Resolvable: true}
}

func TestConvertGraphQLMRDiscussions(t *testing.T) {
	tests := []struct {
		name            string
		discussion      graphql.Discussion
		wantUnresolved  bool
		wantNeedsAnswer bool
	}{
		{
			name: "someone replied after me",
			discussion: graphql.Discussion{Resolvable: true, Notes: graphql.NoteConnection{Nodes: []graphql.Note{
				note("me", "question"), note("alice", "answer"),
			}}},
			wantUnresolved: true, wantNeedsAnswer: true,
		},
		{
			name: "I had the last word",
			discussion: graphql.Discussion{Resolvable: true, Notes: graphql.NoteConnection{Nodes: []graphql.Note{
				note("alice", "question"), note("me", "answer"),
			}}},
			wantUnresolved: true,
		},
		{
			name: "system notes don't count as replies",
			discussion: graphql.Discussion{Resolvable: true, Notes: graphql.NoteConnection{Nodes: []graphql.Note{
				note("me", "question"), {Author: graphql.User{Username: "bot"}, System: true},
			}}},
			wantUnresolved: true,
		},
		{
			name: "thread I never joined",
			discussion: graphql.Discussion{Resolvable: true, Notes: graphql.NoteConnection{Nodes: []graphql.Note{
				note("alice", "a"), note("bob", "b"),
			}}},
			wantUnresolved: true,
		},
		{
			name: "resolved thread",
			discussion: graphql.Discussion{Resolvable: true, Resolved: true, Notes: graphql.NoteConnection{Nodes: []graphql.Note{
				note("me", "question"), note("alice", "answer"),
			}}},
		},
	}

	p := &GitLabGraphQLProvider{username: "me"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gql := baseGraphQLMR()
			gql.Discussions = graphql.DiscussionConnection{Nodes: []graphql.Discussion{tt.discussion}}
			mr := p.convertGraphQLMR(gql)
			if mr.HasUnresolvedDiscussions != tt.wantUnresolved {
				t.Errorf("HasUnresolvedDiscussions = %v, want %v", mr.HasUnresolvedDiscussions, tt.wantUnresolved)
			}
			if mr.HasUnresolvedReplies != tt.wantNeedsAnswer {
				t.Errorf("HasUnresolvedReplies = %v, want %v", mr.HasUnresolvedReplies, tt.wantNeedsAnswer)
			}
		})
	}
}

func TestConvertGraphQLMRNotes(t *testing.T) {
	p := &GitLabGraphQLProvider{}
	gql := baseGraphQLMR()
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	gql.Discussions = graphql.DiscussionConnection{Nodes: []graphql.Discussion{{
		ID: "gid://gitlab/Discussion/abc",
		Notes: graphql.NoteConnection{Nodes: []graphql.Note{
			{Author: graphql.User{Username: "alice"}, Body: "hello", CreatedAt: at, Resolvable: true},
			{Author: graphql.User{Username: "bot"}, Body: "changed title", System: true},
		}},
	}}}

	mr := p.convertGraphQLMR(gql)
	want := []MRNote{{ID: "abc", Author: "alice", Body: "hello", CreatedAt: "2026-03-04T05:06:07Z", Resolvable: true}}
	if !reflect.DeepEqual(mr.Notes, want) {
		t.Errorf("notes = %+v, want %+v", mr.Notes, want)
	}
}

// --- Tests driving the provider through a fake glab binary ---

const graphQLPage1 = `{"data":{"currentUser":{"username":"me","authoredMergeRequests":{
  "pageInfo":{"hasNextPage":true,"endCursor":"CUR1"},
  "nodes":[
    {"iid":"1","title":"In app","state":"opened","project":{"fullPath":"team/app"}},
    {"iid":"2","title":"In other repo","state":"opened","project":{"fullPath":"team/other"}}
  ]}}}}`

const graphQLPage2 = `{"data":{"currentUser":{"username":"me","authoredMergeRequests":{
  "pageInfo":{"hasNextPage":false,"endCursor":""},
  "nodes":[
    {"iid":"3","title":"Also in app","state":"opened","project":{"fullPath":"team/app"}}
  ]}}}}`

func writeFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGraphQLListMRsPaginatesAndFiltersByRepo(t *testing.T) {
	page1 := writeFixture(t, "page1.json", graphQLPage1)
	page2 := writeFixture(t, "page2.json", graphQLPage2)
	logPath := installFakeGlab(t, `case "$*" in
  *after=CUR1*) cat "`+page2+`" ;;
  *) cat "`+page1+`" ;;
esac`)

	p := &GitLabGraphQLProvider{host: "gitlab.example.com", repos: repoSet([]string{"Team/App", "team/lib"}), username: "me"}
	mrs, err := p.ListMRs(ScopeAuthored)
	if err != nil {
		t.Fatalf("ListMRs: %v", err)
	}

	var ids []string
	for _, mr := range mrs {
		ids = append(ids, mr.ID)
	}
	if want := []string{"team/app!1", "team/app!3"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("MR IDs = %v, want %v (only --repo's MRs, from both pages)", ids, want)
	}

	calls := glabCalls(t, logPath)
	if len(calls) != 2 {
		t.Fatalf("expected 2 glab calls (one per page), got %d: %v", len(calls), calls)
	}
	if !strings.Contains(calls[1], "-f after=CUR1") {
		t.Errorf("second page should pass the cursor, got %q", calls[1])
	}
}

func TestGraphQLListMRsWithoutRepoReturnsAll(t *testing.T) {
	page2 := writeFixture(t, "page.json", strings.Replace(graphQLPage1, `"hasNextPage":true`, `"hasNextPage":false`, 1))
	installFakeGlab(t, `cat "`+page2+`"`)

	p := &GitLabGraphQLProvider{username: "me"}
	mrs, err := p.ListMRs(ScopeAuthored)
	if err != nil {
		t.Fatalf("ListMRs: %v", err)
	}
	if len(mrs) != 2 {
		t.Errorf("got %d MRs, want 2", len(mrs))
	}
}

func TestGraphQLErrorsAreReported(t *testing.T) {
	installFakeGlab(t, `echo '{"data":{},"errors":[{"message":"Query has complexity of 300"}]}'`)
	p := &GitLabGraphQLProvider{}
	_, err := p.ListMRs(ScopeAuthored)
	if err == nil || !strings.Contains(err.Error(), "complexity") {
		t.Errorf("expected complexity error, got %v", err)
	}
}

func TestGraphQLIgnoresStderrNoise(t *testing.T) {
	installFakeGlab(t, `echo 'A new version of glab is available' >&2
echo '{"data":{"currentUser":{"username":"me"}}}'`)
	user, err := (&GitLabGraphQLProvider{}).GetCurrentUser()
	if err != nil || user != "me" {
		t.Errorf("GetCurrentUser = (%q, %v), want (me, nil)", user, err)
	}
}

func TestGraphQLCommandFailureIncludesStderr(t *testing.T) {
	installFakeGlab(t, `echo '401 Unauthorized' >&2; exit 1`)
	_, err := (&GitLabGraphQLProvider{}).GetCurrentUser()
	if err == nil || !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Errorf("expected error containing stderr, got %v", err)
	}
}

func TestGraphQLGetMRNotesUsesDiscussions(t *testing.T) {
	logPath := installFakeGlab(t, `echo '[{"id":"d1","notes":[{"body":"hi","author":{"username":"alice"}}]}]'`)
	p := &GitLabGraphQLProvider{}

	notes, err := p.GetMRNotes(42, "team/app")
	if err != nil {
		t.Fatalf("GetMRNotes: %v", err)
	}
	if len(notes) != 1 || notes[0].ID != "d1" {
		t.Errorf("notes = %+v, want one note with discussion ID d1", notes)
	}
	if calls := glabCalls(t, logPath); !strings.Contains(calls[0], "projects/team%2Fapp/merge_requests/42/discussions") {
		t.Errorf("unexpected API call %q", calls[0])
	}
}

func TestGraphQLApproveMR(t *testing.T) {
	logPath := installFakeGlab(t, `echo '{}'`)
	p := &GitLabGraphQLProvider{host: "gitlab.example.com"}

	if err := p.ApproveMR("team/app!42"); err != nil {
		t.Fatalf("ApproveMR: %v", err)
	}
	want := "api --hostname gitlab.example.com projects/team%2Fapp/merge_requests/42/approve -X POST"
	if calls := glabCalls(t, logPath); len(calls) != 1 || strings.TrimSpace(calls[0]) != want {
		t.Errorf("glab calls = %q, want %q", calls, want)
	}

	if err := p.ApproveMR("42"); err == nil {
		t.Error("expected error for MR ID without project path")
	}
}

func TestGraphQLToggleDraftStatus(t *testing.T) {
	logPath := installFakeGlab(t, `echo '{"draft":true,"title":"Draft: Add feature"}'`)
	p := &GitLabGraphQLProvider{}

	if err := p.ToggleDraftStatus("team/app!42"); err != nil {
		t.Fatalf("ToggleDraftStatus: %v", err)
	}
	calls := glabCalls(t, logPath)
	if len(calls) != 2 || !strings.Contains(calls[1], "-X PUT -f title=Add feature") {
		t.Errorf("expected a PUT with the draft prefix removed, got %q", calls)
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestGraphQLListReposPaginated(t *testing.T) {
	installFakeGlab(t, `echo '[{"path_with_namespace": "team/a"}][{"path_with_namespace": "team/b"}]'`)
	repos, err := (&GitLabGraphQLProvider{}).ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if want := []string{"team/a", "team/b"}; !reflect.DeepEqual(repos, want) {
		t.Errorf("repos = %v, want %v (both paginated pages)", repos, want)
	}
}

func TestGraphQLActionErrorsIncludeGlabOutput(t *testing.T) {
	installFakeGlab(t, `echo '{"message":"403 Forbidden"}' >&2; exit 1`)
	p := &GitLabGraphQLProvider{}

	errs := map[string]error{
		"RetryJob":   p.RetryJob("1", "team/app"),
		"ApproveMR":  p.ApproveMR("team/app!1"),
		"CreateNote": p.CreateNote(1, "hi", "team/app"),
	}
	for name, err := range errs {
		if err == nil || !strings.Contains(err.Error(), "403 Forbidden") {
			t.Errorf("%s error should include glab's output, got %v", name, err)
		}
	}
}

func TestRepoSet(t *testing.T) {
	got := repoSet([]string{"Team/App", " team/lib ", ""})
	want := map[string]bool{"team/app": true, "team/lib": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("repoSet = %v, want %v", got, want)
	}
}

func TestGitLabErrorShowsAPIReason(t *testing.T) {
	installFakeGlab(t, `echo '{"message":"401 Unauthorized"}'
echo 'glab: 401 Unauthorized (HTTP 401)' >&2
exit 1`)

	err := (&GitLabGraphQLProvider{}).RetryJob("1", "team/app")
	want := "failed to retry job: 401 Unauthorized\nglab: 401 Unauthorized (HTTP 401)"
	if err == nil || err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

func TestGitLabListMentions(t *testing.T) {
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	page := `{"data":{"currentUser":{"username":"me","todos":{"pageInfo":{"hasNextPage":true,"endCursor":"NEXT"},"nodes":[
	  {"id":"gid://gitlab/Todo/3","createdAt":"` + at(time.Hour) + `","body":"@me look","targetUrl":"https://gl/team/app/-/merge_requests/5#note_1","author":{"username":"alice"},"project":{"fullPath":"team/app"},"target":{"iid":"5","title":"Fix"}},
	  {"id":"gid://gitlab/Todo/2","createdAt":"` + at(2*time.Hour) + `","body":"other repo","targetUrl":"u","author":{"username":"bob"},"project":{"fullPath":"team/other"},"target":{"iid":"9","title":"X"}},
	  {"id":"gid://gitlab/Todo/1","createdAt":"` + at(10*24*time.Hour) + `","body":"too old","targetUrl":"u","author":{"username":"carol"},"project":{"fullPath":"team/app"},"target":{"iid":"1","title":"Old"}}
	]}}}}`
	fixture := writeFixture(t, "todos.json", page)
	logPath := installFakeGlab(t, `cat "`+fixture+`"`)

	p := &GitLabGraphQLProvider{repos: repoSet([]string{"team/app"})}
	mentions, err := p.ListMentions(now.Add(-7 * 24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(mentions) != 1 {
		t.Fatalf("mentions = %+v, want only the recent one in team/app", mentions)
	}
	m := mentions[0]
	if m.ID != "gitlab:gid://gitlab/Todo/3" || m.Author != "alice" || m.MRID != "team/app!5" || m.Ref() != "!5" || m.URL != "https://gl/team/app/-/merge_requests/5#note_1" {
		t.Errorf("mention = %+v", m)
	}
	// Newest first: stops at the first old to-do instead of fetching the next page
	if calls := glabCalls(t, logPath); len(calls) != 1 || !strings.Contains(calls[0], "todos(action: [mentioned, directly_addressed]") {
		t.Errorf("calls = %d %q", len(calls), calls)
	}
}
