package provider

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParsePRID(t *testing.T) {
	owner, name, number, err := parsePRID("octo/app#42")
	if err != nil || owner != "octo" || name != "app" || number != 42 {
		t.Fatalf("parsePRID = (%q, %q, %d, %v)", owner, name, number, err)
	}
	for _, bad := range []string{"", "#42", "octo#42", "octo/app#", "octo/app#x", "octo/app!42", "/app#1"} {
		if _, _, _, err := parsePRID(bad); err == nil {
			t.Errorf("parsePRID(%q) expected error", bad)
		}
	}
}

func TestGitHubSearchQueries(t *testing.T) {
	p := &GitHubProvider{}
	common := "is:pr is:open archived:false sort:updated-desc"
	tests := map[Scope][]string{
		ScopeAuthored: {common + " author:@me"},
		ScopeAssigned: {common + " assignee:@me"},
		// No OR in GitHub search: two queries, so already-reviewed PRs stay in the tab
		ScopeReviewing: {common + " review-requested:@me -author:@me", common + " reviewed-by:@me -author:@me"},
	}
	for scope, want := range tests {
		if got := p.searchQueries(scope); !reflect.DeepEqual(got, want) {
			t.Errorf("searchQueries(%s) =\n%q\nwant\n%q", scope, got, want)
		}
	}

	filtered := &GitHubProvider{repos: []string{"octo/a", "octo/b"}}
	if got := filtered.searchQueries(ScopeAuthored)[0]; !strings.Contains(got, " repo:octo/a repo:octo/b ") {
		t.Errorf("repo filters missing: %q", got)
	}
}

func TestCheckRunStatus(t *testing.T) {
	tests := []struct{ status, conclusion, want string }{
		{"COMPLETED", "SUCCESS", "success"},
		{"COMPLETED", "FAILURE", "failed"},
		{"COMPLETED", "TIMED_OUT", "failed"},
		{"COMPLETED", "CANCELLED", "canceled"},
		{"COMPLETED", "SKIPPED", "skipped"},
		{"COMPLETED", "NEUTRAL", "skipped"},
		{"COMPLETED", "ACTION_REQUIRED", "pending"},
		{"IN_PROGRESS", "", "running"},
		{"QUEUED", "", "pending"},
		{"WAITING", "", "pending"},
	}
	for _, tt := range tests {
		if got := checkRunStatus(tt.status, tt.conclusion); got != tt.want {
			t.Errorf("checkRunStatus(%s, %s) = %q, want %q", tt.status, tt.conclusion, got, tt.want)
		}
	}
	if commitStatusState("ERROR") != "failed" || commitStatusState("EXPECTED") != "pending" || commitStatusState("SUCCESS") != "success" {
		t.Error("commitStatusState mapping wrong")
	}
}

func TestConvertChecks(t *testing.T) {
	var checks ghChecks
	checks.State = "PENDING"
	build := ghCheckContext{Typename: "CheckRun", DatabaseID: 101, Name: "build", Status: "COMPLETED", Conclusion: "FAILURE"}
	build.CheckSuite.WorkflowRun = &struct {
		Workflow struct {
			Name string `json:"name"`
		} `json:"workflow"`
	}{}
	build.CheckSuite.WorkflowRun.Workflow.Name = "CI"
	checks.Contexts.Nodes = []ghCheckContext{
		build,
		{Typename: "CheckRun", DatabaseID: 102, Name: "lint", Status: "IN_PROGRESS"},
		{Typename: "StatusContext", Context: "ci/jenkins", State: "SUCCESS"},
	}

	pipeline, jobs := convertChecks("abc123", checks)
	if pipeline.ID != "abc123" || pipeline.Status != PipelineFailed {
		t.Errorf("pipeline = %+v, want ID abc123 and failed (a failed job wins)", pipeline)
	}
	want := []PipelineJob{
		{ID: "101", Name: "build", Status: "failed", Stage: "CI"},
		{ID: "102", Name: "lint", Status: "running", Stage: "checks"},
		{ID: "", Name: "ci/jenkins", Status: "success", Stage: "status"},
	}
	if !reflect.DeepEqual(jobs, want) {
		t.Errorf("jobs =\n%+v\nwant\n%+v", jobs, want)
	}
}

func basePR() ghPullRequest {
	pr := ghPullRequest{
		ID:          "PR_node",
		Number:      7,
		Title:       "Add feature",
		URL:         "https://github.com/octo/app/pull/7",
		State:       "OPEN",
		Mergeable:   "MERGEABLE",
		HeadRefName: "feature",
		BaseRefName: "main",
		Author:      ghActor{Login: "alice"},
	}
	pr.Repository.NameWithOwner = "octo/app"
	return pr
}

func TestConvertPRBasics(t *testing.T) {
	p := &GitHubProvider{username: "me"}
	pr := basePR()
	pr.Mergeable = "CONFLICTING"

	mr := p.convertPR(pr)
	if mr.ID != "octo/app#7" || mr.IID != 7 || mr.Ref() != "#7" || mr.RepoName != "octo/app" {
		t.Errorf("ID/IID/Ref/Repo = %q/%d/%q/%q", mr.ID, mr.IID, mr.Ref(), mr.RepoName)
	}
	if mr.Status != StatusOpen || !mr.HasConflicts || mr.SourceBranch != "feature" || mr.TargetBranch != "main" || mr.Author != "alice" {
		t.Errorf("unexpected fields: %+v", mr)
	}
	if mr.Pipeline != nil {
		t.Error("no checks should mean no pipeline")
	}
	if mr.Notes == nil {
		t.Error("Notes should be an empty slice, not nil")
	}

	for state, want := range map[string]MRStatus{"MERGED": StatusMerged, "CLOSED": StatusClosed} {
		pr := basePR()
		pr.State = state
		if got := p.convertPR(pr).Status; got != want {
			t.Errorf("state %s -> %q, want %q", state, got, want)
		}
	}
	draft := basePR()
	draft.IsDraft = true
	if p.convertPR(draft).Status != StatusDraft {
		t.Error("draft PR should be StatusDraft")
	}
}

func withReviews(pr ghPullRequest, reviews ...ghComment) ghPullRequest {
	pr.LatestReviews.Nodes = reviews
	return pr
}

func TestConvertPRApprovals(t *testing.T) {
	p := &GitHubProvider{username: "me"}
	pr := withReviews(basePR(),
		ghComment{Author: ghActor{Login: "bob"}, State: "APPROVED"},
		ghComment{Author: ghActor{Login: "me"}, State: "APPROVED"},
		ghComment{Author: ghActor{Login: "carol"}, State: "COMMENTED"},
	)
	pr.ReviewDecision = "REVIEW_REQUIRED"

	mr := p.convertPR(pr)
	if mr.ApprovalCount != 2 || !mr.UserApproved || mr.Approved || mr.ReviewDecision != "REVIEW_REQUIRED" {
		t.Errorf("approvals: count=%d userApproved=%v approved=%v decision=%q", mr.ApprovalCount, mr.UserApproved, mr.Approved, mr.ReviewDecision)
	}

	pr.ReviewDecision = "APPROVED"
	if !p.convertPR(pr).Approved {
		t.Error("APPROVED decision should mean approved")
	}
	pr.ReviewDecision = "" // no review rule: nothing required
	if !p.convertPR(pr).Approved {
		t.Error("without a review rule the PR counts as approved, like GitLab with 0 required")
	}
}

func TestConvertPRReviewState(t *testing.T) {
	p := &GitHubProvider{username: "me"}
	tests := []struct {
		name      string
		myReview  string
		requested bool
		want      ReviewState
	}{
		{"approved", "APPROVED", false, ReviewStateApproved},
		{"changes requested", "CHANGES_REQUESTED", false, ReviewStateRequestedChanges},
		{"commented", "COMMENTED", false, ReviewStateReviewed},
		{"dismissed", "DISMISSED", false, ReviewStateUnapproved},
		{"re-requested after approving", "APPROVED", true, ReviewStateUnreviewed},
		{"requested, never reviewed", "", true, ReviewStateUnreviewed},
		{"not involved", "", false, ""},
	}
	for _, tt := range tests {
		pr := basePR()
		if tt.myReview != "" {
			pr = withReviews(pr, ghComment{Author: ghActor{Login: "me"}, State: tt.myReview})
		}
		if tt.requested {
			pr.ReviewRequests.Nodes = make([]struct {
				RequestedReviewer struct {
					Typename string `json:"__typename"`
					Login    string `json:"login"`
				} `json:"requestedReviewer"`
			}, 1)
			pr.ReviewRequests.Nodes[0].RequestedReviewer.Typename = "User"
			pr.ReviewRequests.Nodes[0].RequestedReviewer.Login = "me"
		}
		if got := p.convertPR(pr).UserReviewState; got != tt.want {
			t.Errorf("%s: UserReviewState = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestConvertPRNotes(t *testing.T) {
	p := &GitHubProvider{username: "me"}
	pr := basePR()
	t0 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	pr.Comments.Nodes = []ghComment{{ID: "IC_2", Author: ghActor{Login: "bob"}, Body: "second", CreatedAt: t0.Add(2 * time.Hour)}}
	pr.Reviews.Nodes = []ghComment{
		{ID: "PRR_1", Author: ghActor{Login: "carol"}, Body: "first: looks good", State: "APPROVED", CreatedAt: t0.Add(time.Hour)},
		{ID: "PRR_2", Author: ghActor{Login: "dave"}, Body: "  ", State: "APPROVED", CreatedAt: t0}, // empty body: skipped
	}
	pr.ReviewThreads.Nodes = make([]struct {
		ID         string `json:"id"`
		IsResolved bool   `json:"isResolved"`
		Comments   struct {
			Nodes []ghComment `json:"nodes"`
		} `json:"comments"`
	}, 2)
	pr.ReviewThreads.Nodes[0].ID = "PRRT_open"
	pr.ReviewThreads.Nodes[0].Comments.Nodes = []ghComment{
		{Author: ghActor{Login: "me"}, Body: "why?"},
		{Author: ghActor{Login: "alice"}, Body: "because"},
	}
	pr.ReviewThreads.Nodes[1].ID = "PRRT_done"
	pr.ReviewThreads.Nodes[1].IsResolved = true
	pr.ReviewThreads.Nodes[1].Comments.Nodes = []ghComment{{Author: ghActor{Login: "me"}, Body: "fixed?"}, {Author: ghActor{Login: "alice"}, Body: "yes"}}

	mr := p.convertPR(pr)

	var got []string
	for _, n := range mr.Notes {
		got = append(got, n.ID+" "+n.Author+" "+n.Body)
	}
	want := []string{
		"comment:PRR_1 carol first: looks good", // conversation, chronological
		"comment:IC_2 bob second",
		"PRRT_open me why?", // then threads
		"PRRT_open alice because",
		"PRRT_done me fixed?",
		"PRRT_done alice yes",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("notes =\n%q\nwant\n%q", got, want)
	}
	if mr.Notes[0].Resolvable || !mr.Notes[2].Resolvable || mr.Notes[2].Resolved || !mr.Notes[4].Resolved {
		t.Error("resolvable/resolved flags wrong")
	}
	if !mr.HasUnresolvedDiscussions || !mr.HasUnresolvedReplies {
		t.Errorf("unresolved=%v replies=%v, want both true (alice answered last in an open thread)", mr.HasUnresolvedDiscussions, mr.HasUnresolvedReplies)
	}
}

// --- through a fake gh ---

func prJSON(number int, updated string) string {
	return `{"id":"PR_` + updated + `","number":` + strconv.Itoa(number) + `,"title":"PR ` + strconv.Itoa(number) + `","state":"OPEN",` +
		`"updatedAt":"` + updated + `","repository":{"nameWithOwner":"octo/app"}}`
}

func TestGitHubListMRsReviewingMergesSearches(t *testing.T) {
	requested := `{"data":{"search":{"pageInfo":{"hasNextPage":false},"nodes":[` + prJSON(1, "2026-01-01T00:00:00Z") + `,{}]}}}`
	reviewed := `{"data":{"search":{"pageInfo":{"hasNextPage":false},"nodes":[` + prJSON(2, "2026-03-01T00:00:00Z") + `,` + prJSON(1, "2026-01-01T00:00:00Z") + `]}}}`
	r1 := writeFixture(t, "requested.json", requested)
	r2 := writeFixture(t, "reviewed.json", reviewed)
	logPath := installFakeGh(t, `case "$*" in
  *review-requested:@me*) cat "`+r1+`" ;;
  *reviewed-by:@me*) cat "`+r2+`" ;;
  *) echo "unexpected: $*" >&2; exit 1 ;;
esac`)

	p := &GitHubProvider{host: "ghe.example.com", username: "me"}
	mrs, err := p.ListMRs(ScopeReviewing)
	if err != nil {
		t.Fatalf("ListMRs: %v", err)
	}

	var ids []string
	for _, mr := range mrs {
		ids = append(ids, mr.ID)
		if mr.UserReviewState != ReviewStateUnreviewed {
			t.Errorf("%s: review state %q, want UNREVIEWED default in the reviewing tab", mr.ID, mr.UserReviewState)
		}
	}
	// Deduplicated, non-PR search results dropped, most recently updated first
	if want := []string{"octo/app#2", "octo/app#1"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("IDs = %v, want %v", ids, want)
	}

	calls := glabCalls(t, logPath)
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "api --hostname ghe.example.com graphql") {
		t.Errorf("calls = %q", calls)
	}
}

func TestGitHubSearchPaginates(t *testing.T) {
	page1 := writeFixture(t, "p1.json", `{"data":{"search":{"pageInfo":{"hasNextPage":true,"endCursor":"CUR"},"nodes":[`+prJSON(1, "2026-01-01T00:00:00Z")+`]}}}`)
	page2 := writeFixture(t, "p2.json", `{"data":{"search":{"pageInfo":{"hasNextPage":false},"nodes":[`+prJSON(2, "2026-01-01T00:00:00Z")+`]}}}`)
	logPath := installFakeGh(t, `case "$*" in
  *after=CUR*) cat "`+page2+`" ;;
  *) cat "`+page1+`" ;;
esac`)

	mrs, err := (&GitHubProvider{username: "me"}).ListMRs(ScopeAuthored)
	if err != nil || len(mrs) != 2 {
		t.Fatalf("ListMRs = %d PRs, %v; want 2", len(mrs), err)
	}
	if calls := glabCalls(t, logPath); len(calls) != 2 || !strings.Contains(calls[0], "-F first=20") {
		t.Errorf("calls = %q", calls)
	}
}

func TestGitHubGraphQLErrors(t *testing.T) {
	installFakeGh(t, `echo '{"data":null,"errors":[{"message":"Something went wrong"}]}'`)
	if _, err := (&GitHubProvider{}).GetCurrentUser(); err == nil || !strings.Contains(err.Error(), "Something went wrong") {
		t.Errorf("expected GraphQL error message, got %v", err)
	}

	installFakeGh(t, `echo 'gh: To get started with GitHub CLI, please run: gh auth login' >&2; exit 4`)
	if _, err := (&GitHubProvider{}).GetCurrentUser(); err == nil || !strings.Contains(err.Error(), "gh auth login") {
		t.Errorf("expected gh's stderr, got %v", err)
	}
}

func TestGitHubApproveAndDismiss(t *testing.T) {
	reviews := writeFixture(t, "reviews.json", `[
		{"id": 1, "user": {"login": "me"}, "state": "APPROVED"},
		{"id": 2, "user": {"login": "bob"}, "state": "APPROVED"}
	][
		{"id": 3, "user": {"login": "me"}, "state": "COMMENTED"},
		{"id": 4, "user": {"login": "me"}, "state": "APPROVED"}
	]`)
	logPath := installFakeGh(t, `case "$*" in
  *"reviews?per_page=100"*) cat "`+reviews+`" ;;
  *) echo '{}' ;;
esac`)

	p := &GitHubProvider{username: "me"}
	if err := p.ApproveMR("octo/app#7"); err != nil {
		t.Fatal(err)
	}
	if err := p.UnapproveMR("octo/app#7"); err != nil {
		t.Fatal(err)
	}

	calls := glabCalls(t, logPath)
	want := []string{
		"api repos/octo/app/pulls/7/reviews -X POST -f event=APPROVE",
		"api repos/octo/app/pulls/7/reviews?per_page=100 --paginate",
		"api repos/octo/app/pulls/7/reviews/4/dismissals -X PUT -f message=Approval withdrawn -f event=DISMISS", // latest own approval
	}
	for i := range want {
		if i >= len(calls) || strings.TrimSpace(calls[i]) != want[i] {
			t.Errorf("call %d = %q, want %q", i, calls[i], want[i])
		}
	}
}

func TestGitHubDismissWithoutApproval(t *testing.T) {
	installFakeGh(t, `echo '[{"id": 1, "user": {"login": "bob"}, "state": "APPROVED"}]'`)
	err := (&GitHubProvider{username: "me"}).UnapproveMR("octo/app#7")
	if err == nil || !strings.Contains(err.Error(), "no approval of yours") {
		t.Errorf("expected 'no approval' error, got %v", err)
	}
}

func TestGitHubReplies(t *testing.T) {
	logPath := installFakeGh(t, `echo '{"data":{}}'`)
	p := &GitHubProvider{}

	if err := p.ReplyToNote(7, "comment:IC_1", "thanks", "octo/app"); err != nil {
		t.Fatal(err)
	}
	if err := p.ReplyToNote(7, "PRRT_abc", "fixed", "octo/app"); err != nil {
		t.Fatal(err)
	}
	if err := p.ResolveDiscussion(7, "PRRT_abc", "octo/app"); err != nil {
		t.Fatal(err)
	}

	calls := glabCalls(t, logPath)
	if !strings.HasPrefix(calls[0], "api repos/octo/app/issues/7/comments -X POST -f body=thanks") {
		t.Errorf("reply to a top-level comment should post a comment, got %q", calls[0])
	}
	if !strings.Contains(calls[1], "addPullRequestReviewThreadReply") || !strings.Contains(calls[1], "-f id=PRRT_abc") {
		t.Errorf("reply to a thread should use the thread mutation, got %q", calls[1])
	}
	if !strings.Contains(calls[2], "resolveReviewThread") {
		t.Errorf("resolve should use resolveReviewThread, got %q", calls[2])
	}
}

func TestGitHubToggleDraft(t *testing.T) {
	pr := writeFixture(t, "pr.json", `{"data":{"repository":{"pullRequest":{"id":"PR_x","number":7,"isDraft":true,"repository":{"nameWithOwner":"octo/app"}}}}}`)
	logPath := installFakeGh(t, `case "$*" in
  *mutation*) echo '{"data":{}}' ;;
  *) cat "`+pr+`" ;;
esac`)

	if err := (&GitHubProvider{}).ToggleDraftStatus("octo/app#7"); err != nil {
		t.Fatal(err)
	}
	calls := glabCalls(t, logPath)
	if len(calls) != 2 || !strings.Contains(calls[1], "markPullRequestReadyForReview") || !strings.Contains(calls[1], "-f id=PR_x") {
		t.Errorf("a draft PR should be marked ready for review, calls = %q", calls)
	}
}

func TestGitHubJobActions(t *testing.T) {
	logPath := installFakeGh(t, `case "$*" in
  *"actions/jobs/55 "*|*"actions/jobs/55") echo '{"run_id": 900}' ;;
  *) echo '{}' ;;
esac`)
	p := &GitHubProvider{}

	if err := p.RetryJob("55", "octo/app"); err != nil {
		t.Fatal(err)
	}
	if err := p.CancelJob("55", "octo/app"); err != nil {
		t.Fatal(err)
	}
	calls := glabCalls(t, logPath)
	want := []string{
		"api repos/octo/app/actions/jobs/55/rerun -X POST",
		"api repos/octo/app/actions/jobs/55",
		"api repos/octo/app/actions/runs/900/cancel -X POST", // GitHub cancels whole runs
	}
	for i := range want {
		if i >= len(calls) || strings.TrimSpace(calls[i]) != want[i] {
			t.Errorf("call %d = %q, want %q", i, calls[i], want[i])
		}
	}

	// External status checks have no job ID
	for name, err := range map[string]error{
		"logs":   func() error { _, err := p.GetJobTrace("", "octo/app"); return err }(),
		"retry":  p.RetryJob("", "octo/app"),
		"cancel": p.CancelJob("0", "octo/app"),
		"play":   p.PlayJob("55", "octo/app"),
	} {
		if err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestGitHubGetMRChanges(t *testing.T) {
	installFakeGh(t, `printf '%s' '[{"filename":"a.go","status":"modified","patch":"@@ -1 +1 @@\n-a\n+b"},{"filename":"new.go","previous_filename":"old.go","status":"renamed"}][{"filename":"gone.go","status":"removed","patch":"@@ -1 +0,0 @@\n-x\n"},{"filename":"add.go","status":"added","patch":"@@ -0,0 +1 @@\n+y\n"}]'`)

	changes, err := (&GitHubProvider{}).GetMRChanges(7, "octo/app")
	if err != nil {
		t.Fatal(err)
	}
	want := []FileChange{
		{OldPath: "a.go", NewPath: "a.go", Diff: "@@ -1 +1 @@\n-a\n+b\n"},
		{OldPath: "old.go", NewPath: "new.go", RenamedFile: true},
		{OldPath: "gone.go", NewPath: "gone.go", DeletedFile: true, Diff: "@@ -1 +0,0 @@\n-x\n"},
		{OldPath: "add.go", NewPath: "add.go", NewFile: true, Diff: "@@ -0,0 +1 @@\n+y\n"},
	}
	if !reflect.DeepEqual(changes.Changes, want) {
		t.Errorf("changes =\n%+v\nwant\n%+v", changes.Changes, want)
	}
}

// gh prints the API's error body on stdout and only a status line on stderr
func TestGitHubErrorShowsAPIReason(t *testing.T) {
	installFakeGh(t, `echo '{"message":"Unprocessable Entity","errors":["Can not approve your own pull request"],"status":"422"}'
echo 'gh: Unprocessable Entity (HTTP 422)' >&2
exit 1`)

	err := (&GitHubProvider{}).ApproveMR("octo/app#1")
	want := "failed to approve PR: Can not approve your own pull request\ngh: Unprocessable Entity (HTTP 422)"
	if err == nil || err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

func TestGitHubListMentions(t *testing.T) {
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	page := `{"data":{"search":{"pageInfo":{"hasNextPage":false},"nodes":[{
	  "id":"PR_1","number":4,"title":"Feature","url":"https://github.com/octo/app/pull/4","createdAt":"` + at(time.Hour) + `",
	  "body":"Fixes stuff, cc @Me","author":{"login":"alice"},"repository":{"nameWithOwner":"octo/app"},
	  "comments":{"nodes":[
	    {"id":"IC_old","url":"u1","author":{"login":"bob"},"body":"@me old","createdAt":"` + at(10*24*time.Hour) + `"},
	    {"id":"IC_self","url":"u2","author":{"login":"me"},"body":"note to @me","createdAt":"` + at(time.Minute) + `"},
	    {"id":"IC_none","url":"u3","author":{"login":"bob"},"body":"no mention","createdAt":"` + at(time.Minute) + `"},
	    {"id":"IC_yes","url":"https://github.com/octo/app/pull/4#issuecomment-1","author":{"login":"bob"},"body":"@me thoughts?","createdAt":"` + at(30*time.Minute) + `"}
	  ]},
	  "reviews":{"nodes":[{"id":"PRR_1","url":"u5","author":{"login":"carol"},"body":"LGTM but @me should check","createdAt":"` + at(10*time.Minute) + `"}]},
	  "reviewThreads":{"nodes":[{"comments":{"nodes":[{"id":"RC_1","url":"u6","author":{"login":"dave"},"body":"@me here","createdAt":"` + at(5*time.Minute) + `"}]}}]}
	}]}}}`
	fixture := writeFixture(t, "mentions.json", page)
	logPath := installFakeGh(t, `cat "`+fixture+`"`)

	p := &GitHubProvider{username: "me", repos: []string{"octo/app"}}
	since := now.Add(-7 * 24 * time.Hour)
	mentions, err := p.ListMentions(since)
	if err != nil {
		t.Fatal(err)
	}

	var ids []string
	for _, m := range mentions {
		ids = append(ids, m.ID)
	}
	// Newest first; the old comment, own comment and non-mention are left out
	want := []string{"github:RC_1", "github:PRR_1", "github:IC_yes", "github:PR_1:description"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("mention IDs = %v, want %v", ids, want)
	}
	if mentions[2].MRID != "octo/app#4" || mentions[2].Ref() != "#4" || mentions[2].URL != "https://github.com/octo/app/pull/4#issuecomment-1" {
		t.Errorf("mention = %+v", mentions[2])
	}

	calls := glabCalls(t, logPath)
	wantQuery := "-f q=is:pr mentions:@me archived:false updated:>=" + since.Format("2006-01-02") + " repo:octo/app"
	if len(calls) != 1 || !strings.Contains(calls[0], wantQuery) {
		t.Errorf("calls = %q, want query %q", calls, wantQuery)
	}
}
