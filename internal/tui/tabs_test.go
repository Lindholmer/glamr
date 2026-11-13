package tui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"glamr/internal/provider"
)

func ghMR(number int, title string) provider.MergeRequest {
	return provider.MergeRequest{
		ID:       fmt.Sprintf("octo/app#%d", number),
		IID:      number,
		Title:    title,
		RepoName: "octo/app",
		WebURL:   fmt.Sprintf("https://github.com/octo/app/pull/%d", number),
		Status:   provider.StatusOpen,
	}
}

// bothForges returns a loaded model with GitLab and GitHub tabs.
func bothForges(t *testing.T, gitlabMRs, githubMRs [tabsPerForge][]provider.MergeRequest) (Model, *fakeProvider, *fakeProvider) {
	t.Helper()
	gitlab := &fakeProvider{}
	github := &fakeProvider{name: "GitHub"}
	m := NewModel(gitlab, github)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})

	msg := tabsFetchedMsg{count: maxTabs}
	for i := 0; i < tabsPerForge; i++ {
		msg.results[i].mrs = gitlabMRs[i]
		msg.results[tabsPerForge+i].mrs = githubMRs[i]
	}
	m, _ = send(t, m, msg)
	return m, gitlab, github
}

func TestGitHubTabsShownOnlyWithGitHub(t *testing.T) {
	gitlabOnly := loadedModel(t, &fakeProvider{}, nil, nil, nil)
	if tabs := gitlabOnly.renderTabs(); strings.Contains(tabs, "GitHub") || strings.Contains(tabs, "4.") {
		t.Errorf("GitLab-only tabs should not mention GitHub: %q", tabs)
	}

	m, _, _ := bothForges(t, [3][]provider.MergeRequest{}, [3][]provider.MergeRequest{})
	tabs := m.renderTabs()
	for _, want := range []string{"GitLab:", "GitHub:", "1. Authored", "3. Reviewing", "4. Authored", "6. Reviewing"} {
		if !strings.Contains(tabs, want) {
			t.Errorf("tabs missing %q: %q", want, tabs)
		}
	}
}

func TestSwitchingToGitHubTabs(t *testing.T) {
	m, _, _ := bothForges(t,
		[3][]provider.MergeRequest{{testMR(1, "gitlab mine")}},
		[3][]provider.MergeRequest{{ghMR(10, "github mine")}, {ghMR(11, "a"), ghMR(12, "b")}, nil})

	m, _ = press(t, m, "4")
	if m.activeTab != 3 || len(m.mrs) != 1 || m.mrs[0].Title != "github mine" {
		t.Fatalf("tab 4: activeTab=%d mrs=%v", m.activeTab, m.mrs)
	}
	view := m.View()
	if !strings.Contains(view, "glamr - GitHub Authored PRs") || !strings.Contains(view, "#10 github mine") {
		t.Errorf("GitHub tab should show PRs with #refs, got:\n%s", view)
	}

	m, _ = press(t, m, "5")
	if m.activeTab != 4 || len(m.mrs) != 2 {
		t.Errorf("tab 5: activeTab=%d, %d PRs", m.activeTab, len(m.mrs))
	}
	m, _ = press(t, m, "1")
	if m.activeTab != 0 || !strings.Contains(m.View(), "!1 gitlab mine") {
		t.Error("tab 1 should be back on GitLab")
	}
}

func TestActionsGoToTheTabsForge(t *testing.T) {
	m, gitlab, github := bothForges(t,
		[3][]provider.MergeRequest{{testMR(1, "gl")}},
		[3][]provider.MergeRequest{{ghMR(10, "gh")}})

	// List view: approve on tab 4 goes to GitHub
	m, _ = press(t, m, "4")
	_, cmd := press(t, m, "a")
	cmd()

	// Detail view opened from a GitHub tab acts on GitHub
	m, _ = press(t, m, "enter")
	_, cmd = press(t, m, "D")
	cmd()

	// Back on GitLab
	m, _ = press(t, m, "esc", "1")
	_, cmd = press(t, m, "a")
	cmd()

	if got, want := github.Calls(), []string{"approve octo/app#10", "draft octo/app#10"}; !reflect.DeepEqual(got, want) {
		t.Errorf("GitHub calls = %v, want %v", got, want)
	}
	if got, want := gitlab.Calls(), []string{"approve team/app!1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("GitLab calls = %v, want %v", got, want)
	}
}

func TestOneForgeFailingKeepsTheOther(t *testing.T) {
	m, _, _ := bothForges(t,
		[3][]provider.MergeRequest{{testMR(1, "old gitlab")}},
		[3][]provider.MergeRequest{{ghMR(10, "old github")}})

	// Refresh: GitLab succeeds with new data, GitHub fails
	msg := tabsFetchedMsg{count: maxTabs}
	msg.results[0].mrs = []provider.MergeRequest{testMR(2, "new gitlab")}
	for i := tabsPerForge; i < maxTabs; i++ {
		msg.results[i].err = errors.New("gh: rate limited")
	}
	m, _ = send(t, m, msg)

	if m.err != nil {
		t.Fatalf("a partial failure must not show the error screen: %v", m.err)
	}
	if m.tabMRs[0][0].Title != "new gitlab" {
		t.Error("GitLab tabs should be updated")
	}
	if m.tabMRs[3][0].Title != "old github" {
		t.Error("failed GitHub tabs should keep their previous data")
	}
	if status := m.renderStatus(); !strings.Contains(status, "GitHub Authored: gh: rate limited") {
		t.Errorf("status should name the failing tab, got %q", status)
	}
}

func TestInitialLoadPartialFailureShowsList(t *testing.T) {
	m := NewModel(&fakeProvider{}, &fakeProvider{name: "GitHub"})
	msg := tabsFetchedMsg{count: maxTabs}
	msg.results[0].mrs = []provider.MergeRequest{testMR(1, "gl")}
	for i := tabsPerForge; i < maxTabs; i++ {
		msg.results[i].err = errors.New("gh not authenticated")
	}
	m, _ = send(t, m, msg)

	if m.err != nil || m.loading || len(m.mrs) != 1 {
		t.Errorf("GitLab data should show despite GitHub failing: err=%v loading=%v mrs=%d", m.err, m.loading, len(m.mrs))
	}
	if m.refreshErr == nil {
		t.Error("the GitHub failure should be reported in the status bar")
	}
}

func TestGitHubReviewingTabNeedsAttention(t *testing.T) {
	waiting := ghMR(5, "please review")
	waiting.UserReviewState = provider.ReviewStateUnreviewed
	m, _, _ := bothForges(t, [3][]provider.MergeRequest{}, [3][]provider.MergeRequest{nil, nil, {waiting}})

	if needsAttention(m.tabMRs[2]) {
		t.Error("GitLab reviewing tab has nothing to review")
	}
	if !needsAttention(m.tabMRs[5]) {
		t.Error("GitHub reviewing tab should need attention")
	}
}

func TestRenderApprovalLine(t *testing.T) {
	tests := []struct {
		name      string
		mr        provider.MergeRequest
		reviewing bool
		want      string
	}{
		{"gitlab required, waiting", provider.MergeRequest{ApprovalCount: 1, RequiredApprovals: 2}, false, "○ 1/2 approvals"},
		{"gitlab approved by you", provider.MergeRequest{ApprovalCount: 2, RequiredApprovals: 2, UserApproved: true, Approved: true}, false, "✓ You approved | 2/2"},
		{"gitlab optional", provider.MergeRequest{}, false, "○ 0/0 approvals (optional)"},
		{"github review required", provider.MergeRequest{ApprovalCount: 0, ReviewDecision: "REVIEW_REQUIRED"}, false, "○ 0 approved, review required"},
		{"github approved", provider.MergeRequest{ApprovalCount: 2, Approved: true, ReviewDecision: "APPROVED"}, false, "✓ 2 approved"},
		{"github changes requested", provider.MergeRequest{ApprovalCount: 1, ReviewDecision: "CHANGES_REQUESTED"}, false, "○ 1 approved, changes requested"},
		{"github no review rule", provider.MergeRequest{ApprovalCount: 1, Approved: true}, false, "✓ 1/0 approved"},
		{"reviewing: commented", provider.MergeRequest{UserReviewState: provider.ReviewStateReviewed, ApprovalCount: 1, ReviewDecision: "REVIEW_REQUIRED"}, true, "💬 You commented | 1 approved, review required"},
		{"reviewing: new replies", provider.MergeRequest{UserReviewState: provider.ReviewStateApproved, HasUnresolvedReplies: true, RequiredApprovals: 1}, true, "🔴 New replies to review | 0/1"},
	}
	for _, tt := range tests {
		if got := renderApprovalLine(tt.mr, tt.reviewing); !strings.Contains(got, tt.want) {
			t.Errorf("%s: got %q, want it to contain %q", tt.name, got, tt.want)
		}
	}
}
