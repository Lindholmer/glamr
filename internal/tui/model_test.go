package tui

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"glamr/internal/provider"
)

// --- helpers ---

func keyMsg(key string) tea.KeyMsg {
	special := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "esc": tea.KeyEsc, "backspace": tea.KeyBackspace,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"home": tea.KeyHome, "end": tea.KeyEnd, "shift+up": tea.KeyShiftUp, "shift+down": tea.KeyShiftDown,
		"space": tea.KeySpace, "ctrl+c": tea.KeyCtrlC,
	}
	if t, ok := special[key]; ok {
		return tea.KeyMsg{Type: t}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
}

// press sends keys in order and returns the model and the command from the last key.
func press(t *testing.T, m Model, keys ...string) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		var next tea.Model
		next, cmd = m.Update(keyMsg(k))
		m = next.(Model)
	}
	return m, cmd
}

func send(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

// run executes a command and feeds its message back into the model.
func run(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command, got nil")
	}
	m, _ = send(t, m, cmd())
	return m
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func testMR(iid int, title string) provider.MergeRequest {
	return provider.MergeRequest{
		ID:       "team/app!" + string(rune('0'+iid)),
		IID:      iid,
		Title:    title,
		RepoName: "team/app",
		WebURL:   "https://gitlab.example.com/team/app/-/merge_requests/" + string(rune('0'+iid)),
		Status:   provider.StatusOpen,
	}
}

// loadedModel returns a model that has finished its initial load with the given MRs.
func loadedModel(t *testing.T, p *fakeProvider, authored, assigned, reviewing []provider.MergeRequest) Model {
	t.Helper()
	m := NewModel(p)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m, _ = send(t, m, allScopesMRsMsg{authored: authored, assigned: assigned, reviewing: reviewing})
	return m
}

// --- loading & refresh ---

func TestInitialLoadPopulatesScopes(t *testing.T) {
	p := &fakeProvider{}
	m := loadedModel(t, p,
		[]provider.MergeRequest{testMR(1, "mine")},
		[]provider.MergeRequest{testMR(2, "assigned"), testMR(3, "assigned too")},
		nil)

	if m.loading || len(m.mrs) != 1 || m.mrs[0].Title != "mine" {
		t.Fatalf("after load: loading=%v mrs=%v", m.loading, m.mrs)
	}

	m, _ = press(t, m, "2")
	if m.scope != provider.ScopeAssigned || len(m.mrs) != 2 {
		t.Errorf("tab 2: scope=%v, %d MRs", m.scope, len(m.mrs))
	}
	m, _ = press(t, m, "3")
	if m.scope != provider.ScopeReviewing || len(m.mrs) != 0 {
		t.Errorf("tab 3: scope=%v, %d MRs", m.scope, len(m.mrs))
	}
	if !strings.Contains(m.View(), "No merge requests found") {
		t.Error("empty tab should say no merge requests found")
	}
}

func TestInitialLoadErrorShownAndDismissable(t *testing.T) {
	m := NewModel(&fakeProvider{})
	m, _ = send(t, m, allScopesMRsMsg{err: errors.New("401 Unauthorized")})

	if !strings.Contains(m.View(), "401 Unauthorized") {
		t.Fatal("initial load error should be shown")
	}

	m, cmd := press(t, m, "x")
	if m.err != nil || cmd != nil {
		t.Errorf("any key should dismiss the error (err=%v)", m.err)
	}
}

func TestQuitFromErrorScreen(t *testing.T) {
	m := NewModel(&fakeProvider{})
	m.err = errors.New("boom")
	if _, cmd := press(t, m, "q"); !isQuit(cmd) {
		t.Error("q should quit from the error screen")
	}
}

func TestBackgroundRefreshErrorKeepsData(t *testing.T) {
	m := loadedModel(t, &fakeProvider{}, []provider.MergeRequest{testMR(1, "mine")}, nil, nil)
	m, _ = send(t, m, allScopesMRsMsg{err: errors.New("timeout\nfull glab output")})

	if m.err != nil {
		t.Error("a failed background refresh must not replace the list with an error screen")
	}
	if len(m.mrs) != 1 {
		t.Error("existing data should be kept")
	}
	if status := m.renderStatus(); !strings.Contains(status, "Refresh failed: timeout") || strings.Contains(status, "full glab output") {
		t.Errorf("status should show the first error line only, got %q", status)
	}
}

func TestManualRefreshDoesNotStack(t *testing.T) {
	m := loadedModel(t, &fakeProvider{}, nil, nil, nil)

	m, cmd := press(t, m, "r")
	if cmd == nil || !m.refreshing {
		t.Fatal("r should start a refresh")
	}
	if _, cmd = press(t, m, "r"); cmd != nil {
		t.Error("r while refreshing should not start another refresh")
	}
}

func TestTickRefreshesOnlyAfterInterval(t *testing.T) {
	m := loadedModel(t, &fakeProvider{}, nil, nil, nil)

	m.lastRefreshStart = time.Now()
	m, _ = send(t, m, tickMsg(time.Now()))
	if m.refreshing {
		t.Error("should not refresh before the interval has passed")
	}

	m.lastRefreshStart = time.Now().Add(-m.refreshInterval - time.Second)
	m, _ = send(t, m, tickMsg(time.Now()))
	if !m.refreshing {
		t.Error("should refresh once the interval has passed")
	}

	// A failing refresh counts as an attempt: no retry on the next tick
	m, _ = send(t, m, allScopesMRsMsg{err: errors.New("fail")})
	m, _ = send(t, m, tickMsg(time.Now()))
	if m.refreshing {
		t.Error("a failed refresh should not be retried every tick")
	}
}

func TestFetchAllScopesMergesProviders(t *testing.T) {
	a := &fakeProvider{mrs: map[provider.Scope][]provider.MergeRequest{provider.ScopeAuthored: {testMR(1, "a")}}}
	b := &fakeProvider{mrs: map[provider.Scope][]provider.MergeRequest{
		provider.ScopeAuthored:  {testMR(2, "b")},
		provider.ScopeReviewing: {testMR(3, "review")},
	}}

	msg := fetchAllScopesMRs([]provider.Provider{a, b})().(allScopesMRsMsg)
	if msg.err != nil || len(msg.authored) != 2 || len(msg.reviewing) != 1 || len(msg.assigned) != 0 {
		t.Errorf("unexpected result: %+v", msg)
	}

	failing := &fakeProvider{listErr: errors.New("down")}
	if msg := fetchAllScopesMRs([]provider.Provider{a, failing})().(allScopesMRsMsg); msg.err == nil {
		t.Error("an error from any provider should be reported")
	}
}

func TestRefreshClampsCursor(t *testing.T) {
	m := loadedModel(t, &fakeProvider{}, []provider.MergeRequest{testMR(1, "a"), testMR(2, "b"), testMR(3, "c")}, nil, nil)
	m, _ = press(t, m, "down", "down")
	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", m.cursor)
	}
	m, _ = send(t, m, allScopesMRsMsg{authored: []provider.MergeRequest{testMR(1, "a")}})
	if m.cursor != 0 {
		t.Errorf("cursor = %d after list shrank, want 0", m.cursor)
	}
}

// --- list navigation ---

func TestListNavigationAndScrolling(t *testing.T) {
	var mrs []provider.MergeRequest
	for i := 1; i <= 9; i++ {
		mrs = append(mrs, testMR(i, "mr"))
	}
	m := loadedModel(t, &fakeProvider{}, mrs, nil, nil)

	m, _ = press(t, m, "up") // already at top
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.cursor)
	}

	for i := 0; i < 20; i++ {
		m, _ = press(t, m, "j")
	}
	if m.cursor != len(mrs)-1 {
		t.Errorf("cursor = %d, want %d (clamped to last)", m.cursor, len(mrs)-1)
	}
	if visible := m.visibleMRCount(); m.cursor < m.scroll || m.cursor >= m.scroll+visible {
		t.Errorf("cursor %d not visible with scroll %d and %d visible", m.cursor, m.scroll, visible)
	}
}

// --- approvals ---

func TestApproveCountsOnce(t *testing.T) {
	mr := testMR(1, "needs approval")
	mr.RequiredApprovals = 2
	mr.ApprovalCount = 1
	p := &fakeProvider{}
	m := loadedModel(t, p, []provider.MergeRequest{mr}, nil, nil)

	m, cmd := press(t, m, "a")
	m = run(t, m, cmd)

	if got := p.Calls(); !reflect.DeepEqual(got, []string{"approve team/app!1"}) {
		t.Errorf("provider calls = %v", got)
	}
	// m.mrs aliases m.mrsAuthored; the approval must not be counted twice
	if got := m.mrs[0]; got.ApprovalCount != 2 || !got.UserApproved || !got.Approved {
		t.Errorf("after approve: count=%d userApproved=%v approved=%v, want 2/true/true",
			got.ApprovalCount, got.UserApproved, got.Approved)
	}

	// Pressing a again revokes the approval
	m, cmd = press(t, m, "a")
	m = run(t, m, cmd)
	if got := m.mrs[0]; got.ApprovalCount != 1 || got.UserApproved || got.Approved {
		t.Errorf("after unapprove: count=%d userApproved=%v approved=%v, want 1/false/false",
			got.ApprovalCount, got.UserApproved, got.Approved)
	}
	if got := p.Calls(); len(got) != 2 || got[1] != "unapprove team/app!1" {
		t.Errorf("provider calls = %v", got)
	}
}

func TestApplyApprovalUpdatesReviewState(t *testing.T) {
	mr := provider.MergeRequest{UserReviewState: provider.ReviewStateUnreviewed}
	applyApproval(&mr, true)
	if mr.UserReviewState != provider.ReviewStateApproved {
		t.Errorf("review state = %q, want APPROVED", mr.UserReviewState)
	}
	applyApproval(&mr, true) // idempotent
	if mr.ApprovalCount != 1 {
		t.Errorf("ApprovalCount = %d, want 1", mr.ApprovalCount)
	}
	applyApproval(&mr, false)
	if mr.UserReviewState != provider.ReviewStateUnapproved || mr.ApprovalCount != 0 {
		t.Errorf("after unapprove: %+v", mr)
	}
	applyApproval(&mr, false)
	if mr.ApprovalCount != 0 {
		t.Error("ApprovalCount must not go negative")
	}

	// MRs outside the reviewing tab have no review state; don't invent one
	other := provider.MergeRequest{}
	applyApproval(&other, true)
	if other.UserReviewState != "" {
		t.Errorf("review state = %q, want empty", other.UserReviewState)
	}
}

func TestApprovalErrorIsShown(t *testing.T) {
	m := loadedModel(t, &fakeProvider{}, []provider.MergeRequest{testMR(1, "x")}, nil, nil)
	m, _ = send(t, m, approvalMsg{mrID: "team/app!1", err: errors.New("forbidden")})
	if !strings.Contains(m.View(), "forbidden") {
		t.Error("approval error should be displayed")
	}
	if m.mrs[0].UserApproved {
		t.Error("failed approval must not change the MR")
	}
}

func TestToggleDraftTriggersRefresh(t *testing.T) {
	p := &fakeProvider{}
	m := loadedModel(t, p, []provider.MergeRequest{testMR(1, "x")}, nil, nil)

	m, cmd := press(t, m, "D")
	m, cmd = send(t, m, cmd())
	if got := p.Calls(); !reflect.DeepEqual(got, []string{"draft team/app!1"}) {
		t.Errorf("provider calls = %v", got)
	}
	if cmd == nil || !m.refreshing {
		t.Error("toggling draft should refresh (and mark the refresh as in progress)")
	}
}

// --- detail view ---

func openDetail(t *testing.T, m Model) Model {
	t.Helper()
	m, _ = press(t, m, "enter")
	if !m.showingDetail || m.detailView == nil {
		t.Fatal("enter should open the detail view")
	}
	return m
}

func TestDetailActionsTargetOpenMREvenAfterRefreshReorders(t *testing.T) {
	p := &fakeProvider{}
	a, b := testMR(1, "A"), testMR(2, "B")
	m := loadedModel(t, p, []provider.MergeRequest{a, b}, nil, nil)
	m = openDetail(t, m) // detail for A (cursor 0)

	// Background refresh puts B first
	m, _ = send(t, m, allScopesMRsMsg{authored: []provider.MergeRequest{b, a}})

	_, cmd := press(t, m, "a")
	cmd()
	if got := p.Calls(); !reflect.DeepEqual(got, []string{"approve team/app!1"}) {
		t.Errorf("approve should target the MR in the detail view (A), calls = %v", got)
	}
}

func TestDetailViewIgnoresListKeys(t *testing.T) {
	m := loadedModel(t, &fakeProvider{}, []provider.MergeRequest{testMR(1, "A")}, []provider.MergeRequest{testMR(2, "B")}, nil)
	m = openDetail(t, m)

	m, _ = press(t, m, "2")
	if m.scope != provider.ScopeAuthored {
		t.Error("tab keys must not switch scope while the detail view is open")
	}

	m, _ = press(t, m, "esc")
	if m.showingDetail {
		t.Error("esc should close the detail view")
	}
}

func TestJobActionsRequireMatchingStatus(t *testing.T) {
	mr := testMR(1, "with jobs")
	mr.Pipeline = &provider.Pipeline{ID: "9"}
	mr.PipelineJobs = []provider.PipelineJob{
		{ID: "100", Name: "build", Status: "failed"},
		{ID: "101", Name: "deploy", Status: "manual"},
		{ID: "102", Name: "test", Status: "running"},
	}
	p := &fakeProvider{}
	m := openDetail(t, loadedModel(t, p, []provider.MergeRequest{mr}, nil, nil))

	press1 := func(key string) {
		t.Helper()
		var cmd tea.Cmd
		m, cmd = press(t, m, key)
		if cmd != nil {
			cmd()
		}
	}

	press1("p") // wrong status (failed): nothing
	press1("c") // wrong status: nothing
	press1("t") // retry failed job
	press1("j")
	press1("t") // wrong status (manual): nothing
	press1("p") // play manual job
	press1("j")
	press1("c") // cancel running job

	want := []string{"retry team/app 100", "play team/app 101", "cancel team/app 102"}
	if got := p.Calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("provider calls = %v, want %v", got, want)
	}
}

func TestJobLogOpensScrolledToEnd(t *testing.T) {
	mr := testMR(1, "x")
	mr.Pipeline = &provider.Pipeline{ID: "9"}
	mr.PipelineJobs = []provider.PipelineJob{{ID: "100", Name: "build", Status: "failed"}}
	m := openDetail(t, loadedModel(t, &fakeProvider{}, []provider.MergeRequest{mr}, nil, nil))

	log := strings.Repeat("log line\n", 200)
	m, _ = send(t, m, jobLogMsg{log: log, title: "Job Log: build"})

	if !m.showingDiff || m.diffScroll != m.diffMaxScroll() || m.diffScroll == 0 {
		t.Errorf("job log should open in the scroll viewer at the end (scroll=%d, max=%d)", m.diffScroll, m.diffMaxScroll())
	}
	m, _ = press(t, m, "up")
	if m.diffScroll != m.diffMaxScroll()-1 {
		t.Error("scrolling up from the end should move immediately")
	}
	m, _ = press(t, m, "esc")
	if m.showingDiff || !m.showingDetail {
		t.Error("esc should return from the log to the detail view")
	}
}

func TestOverlayViewsSwallowActionKeys(t *testing.T) {
	p := &fakeProvider{}
	m := openDetail(t, loadedModel(t, p, []provider.MergeRequest{testMR(1, "x")}, nil, nil))
	m, _ = send(t, m, diffMsg{content: "diff", title: "Diff"})

	for _, key := range []string{"a", "D", "f", "n", "t", "r"} {
		var cmd tea.Cmd
		m, cmd = press(t, m, key)
		if cmd != nil {
			cmd()
		}
	}
	if calls := p.Calls(); len(calls) != 0 {
		t.Errorf("keys in the diff view leaked to the detail view: %v", calls)
	}
	if m.showingNoteThread {
		t.Error("n in the diff view should not open a new note")
	}
}

// --- notes ---

func TestNewNoteSupportsUnicodeAndPaste(t *testing.T) {
	p := &fakeProvider{}
	m := openDetail(t, loadedModel(t, p, []provider.MergeRequest{testMR(1, "x")}, nil, nil))

	m, _ = press(t, m, "n")
	if !m.composingReply || !m.showingNoteThread {
		t.Fatal("n should start composing a new note")
	}
	m, _ = press(t, m, "S", "å", "space", "q", "x", "backspace")
	m, _ = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" pasted ø"), Paste: true})
	if m.replyInput != "Så q pasted ø" {
		t.Fatalf("replyInput = %q", m.replyInput)
	}

	m, cmd := press(t, m, "enter")
	m = run(t, m, cmd)
	if got := p.Calls(); !reflect.DeepEqual(got, []string{"note team/app!1 Så q pasted ø"}) {
		t.Errorf("provider calls = %v", got)
	}
	if m.showingNoteThread || m.composingReply {
		t.Error("posting should close the note view")
	}
}

func TestEscCancelsNewNote(t *testing.T) {
	m := openDetail(t, loadedModel(t, &fakeProvider{}, []provider.MergeRequest{testMR(1, "x")}, nil, nil))
	m, _ = press(t, m, "n", "h", "i", "esc")
	if m.composingReply || m.showingNoteThread || m.replyInput != "" {
		t.Error("esc on a new note should discard it and return to the detail view")
	}
	if !m.showingDetail {
		t.Error("should be back in the detail view")
	}
}

func TestReplyAndResolveThread(t *testing.T) {
	mr := testMR(1, "x")
	mr.Notes = []provider.MRNote{
		{ID: "d1", Author: "alice", Body: "please fix", Resolvable: true},
		{ID: "d1", Author: "bob", Body: "done", Resolvable: true},
		{ID: "d2", Author: "carol", Body: "other thread", Resolvable: true},
	}
	p := &fakeProvider{}
	m := openDetail(t, loadedModel(t, p, []provider.MergeRequest{mr}, nil, nil))

	m, _ = press(t, m, "l", "enter") // notes panel, open first note
	if !m.showingNoteThread || m.selectedNote.ID != "d1" {
		t.Fatalf("expected thread d1 to be open, got %+v", m.selectedNote)
	}

	// Resolve: every note of the thread is marked resolved and hidden from the panel
	m, cmd := press(t, m, "x")
	m = run(t, m, cmd)
	if !m.selectedNote.Resolved {
		t.Error("selected note should be resolved")
	}
	if remaining := unresolvedNotes(m.detailView.notes); len(remaining) != 1 || remaining[0].ID != "d2" {
		t.Errorf("unresolved notes after resolving d1 = %+v", remaining)
	}

	// Reply
	m, cmd = press(t, m, "r", "o", "k", "enter")
	m = run(t, m, cmd)
	if m.showingNoteThread {
		t.Error("posting a reply should close the thread")
	}
	_, cmd = press(t, m, "r", "enter") // empty reply
	if cmd != nil {
		t.Error("empty reply should not be posted")
	}

	want := []string{"resolve team/app!1 d1", "reply team/app!1 d1 ok"}
	if got := p.Calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("provider calls = %v, want %v", got, want)
	}
}

func TestNotesCursorStaysWithinUnresolvedNotes(t *testing.T) {
	mr := testMR(1, "x")
	mr.Notes = []provider.MRNote{
		{ID: "d1", Body: "resolved", Resolvable: true, Resolved: true},
		{ID: "d2", Body: "open", Resolvable: true},
	}
	m := openDetail(t, loadedModel(t, &fakeProvider{}, []provider.MergeRequest{mr}, nil, nil))
	m, _ = press(t, m, "l", "j", "j", "enter")
	if m.detailView.notesCursor != 0 || m.selectedNote.ID != "d2" {
		t.Errorf("cursor=%d selected=%q, want 0/d2", m.detailView.notesCursor, m.selectedNote.ID)
	}
}

// --- files & diffs ---

func TestFilesViewAndDiff(t *testing.T) {
	// No delta available: diffs are shown as plain text
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	p := &fakeProvider{changes: &provider.MRChanges{Changes: []provider.FileChange{
		{OldPath: "a.go", NewPath: "a.go", Diff: "@@ -1 +1 @@\n-old\n+new\n"},
		{OldPath: "b.go", NewPath: "b.go", NewFile: true, Diff: "@@ -0,0 +1 @@\n+added\n"},
	}}}
	m := openDetail(t, loadedModel(t, p, []provider.MergeRequest{testMR(1, "x")}, nil, nil))

	m, cmd := press(t, m, "f")
	m = run(t, m, cmd)
	if !m.showingFiles {
		t.Fatal("f should open the files view")
	}
	if !strings.Contains(m.View(), "Changed files: 2") {
		t.Error("files view should list the changed files")
	}

	m, _ = press(t, m, "j", "j") // clamped at the last file
	if m.filesView.cursor != 1 {
		t.Errorf("files cursor = %d, want 1", m.filesView.cursor)
	}

	m, cmd = press(t, m, "d")
	m = run(t, m, cmd)
	if !m.showingDiff || m.diffTitle != "File Diff: b.go" {
		t.Fatalf("expected diff for b.go, got showingDiff=%v title=%q", m.showingDiff, m.diffTitle)
	}
	if !strings.Contains(m.diffContent, "+++ b/b.go") || !strings.Contains(m.diffContent, "--- /dev/null") {
		t.Errorf("diff should include file headers, got %q", m.diffContent)
	}

	m, _ = press(t, m, "esc")
	if m.showingDiff || !m.showingFiles {
		t.Error("esc from diff should return to the files view")
	}
	m, _ = press(t, m, "esc")
	if m.showingFiles || !m.showingDetail {
		t.Error("esc from files should return to the detail view")
	}

	// Full diff reuses the fetched changes
	m, cmd = press(t, m, "d")
	m = run(t, m, cmd)
	if !strings.Contains(m.diffContent, "+++ b/a.go") || !strings.Contains(m.diffContent, "+++ b/b.go") {
		t.Errorf("full diff should contain both files, got %q", m.diffContent)
	}
}

func TestDiffScrollIsClamped(t *testing.T) {
	m := loadedModel(t, &fakeProvider{}, nil, nil, nil)
	m, _ = send(t, m, diffMsg{content: strings.Repeat("line\n", 100), title: "Diff"})

	m, _ = press(t, m, "end")
	maxScroll := m.diffMaxScroll()
	if m.diffScroll != maxScroll || maxScroll == 0 {
		t.Fatalf("end: scroll = %d, want %d", m.diffScroll, maxScroll)
	}
	m, _ = press(t, m, "shift+down", "down")
	if m.diffScroll != maxScroll {
		t.Errorf("scroll went past the end: %d > %d", m.diffScroll, maxScroll)
	}
	m, _ = press(t, m, "home", "up")
	if m.diffScroll != 0 {
		t.Errorf("scroll = %d, want 0", m.diffScroll)
	}
}

func TestFormatDiffWithoutDeltaReturnsPlainDiff(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	diff := "@@ -1 +1 @@\n-a\n+b\n"
	got, err := formatDiff(diff, 80)
	if err != nil || got != diff {
		t.Errorf("formatDiff = (%q, %v), want plain diff", got, err)
	}
}

func TestFormatDiffUsesDeltaFromLocalBin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", home)
	if err := os.MkdirAll(home+"/.local/bin", 0o755); err != nil {
		t.Fatal(err)
	}
	// Fake delta that proves it received the diff on stdin and the width flag
	// (shell builtins only - PATH is empty)
	script := "#!/bin/sh\necho \"delta $*\"\nwhile IFS= read -r line; do echo \"$line\"; done\n"
	if err := os.WriteFile(home+"/.local/bin/delta", []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := formatDiff("+added\n", 100)
	if err != nil {
		t.Fatal(err)
	}
	if want := "delta --paging never --width 100\n+added\n"; got != want {
		t.Errorf("formatDiff = %q, want %q", got, want)
	}
}

func TestFileDiffHeader(t *testing.T) {
	tests := []struct {
		file provider.FileChange
		want string
	}{
		{provider.FileChange{OldPath: "a", NewPath: "a"}, "diff --git a/a b/a\n--- a/a\n+++ b/a\n"},
		{provider.FileChange{OldPath: "a", NewPath: "a", NewFile: true}, "diff --git a/a b/a\n--- /dev/null\n+++ b/a\n"},
		{provider.FileChange{OldPath: "a", NewPath: "a", DeletedFile: true}, "diff --git a/a b/a\n--- a/a\n+++ /dev/null\n"},
		{provider.FileChange{OldPath: "old", NewPath: "new", RenamedFile: true}, "diff --git a/old b/new\n--- a/old\n+++ b/new\n"},
	}
	for _, tt := range tests {
		if got := fileDiffHeader(tt.file); got != tt.want {
			t.Errorf("fileDiffHeader(%+v) = %q, want %q", tt.file, got, tt.want)
		}
	}
}

// --- rendering ---

func TestRenderingNeverPanicsOnSmallTerminals(t *testing.T) {
	mr := testMR(1, strings.Repeat("very long title ", 10))
	mr.Pipeline = &provider.Pipeline{ID: "9", Status: provider.PipelineFailed}
	mr.PipelineJobs = []provider.PipelineJob{{ID: "1", Name: strings.Repeat("long-job-name-", 10), Status: "failed"}}
	mr.Notes = []provider.MRNote{{ID: "d1", Author: "alice", Body: strings.Repeat("word ", 100), Resolvable: true}}
	changes := &provider.MRChanges{Changes: []provider.FileChange{{OldPath: "x", NewPath: strings.Repeat("dir/", 30) + "file.go"}}}

	for _, size := range []tea.WindowSizeMsg{{Width: 0, Height: 0}, {Width: 5, Height: 3}, {Width: 20, Height: 10}, {Width: 200, Height: 60}} {
		m := loadedModel(t, &fakeProvider{changes: changes}, []provider.MergeRequest{mr}, nil, nil)
		m, _ = send(t, m, size)
		_ = m.View()

		m, _ = press(t, m, "enter")
		_ = m.View()
		m, _ = press(t, m, "l")
		_ = m.View()

		m, cmd := press(t, m, "f")
		m = run(t, m, cmd)
		_ = m.View()
		m, _ = press(t, m, "esc", "enter")
		_ = m.View() // note thread
		m, _ = press(t, m, "r")
		_ = m.View() // composing
	}
}

func TestWindowResizeUpdatesOpenViews(t *testing.T) {
	m := openDetail(t, loadedModel(t, &fakeProvider{}, []provider.MergeRequest{testMR(1, "x")}, nil, nil))
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 200, Height: 50})
	if m.detailView.width != 200 || m.detailView.height != 50 {
		t.Errorf("detail view size = %dx%d, want 200x50", m.detailView.width, m.detailView.height)
	}
}

// --- pure helpers ---

func TestSortReviewingMRs(t *testing.T) {
	mk := func(title string, state provider.ReviewState, draft, replies bool) provider.MergeRequest {
		mr := provider.MergeRequest{Title: title, UserReviewState: state, HasUnresolvedReplies: replies, Status: provider.StatusOpen}
		if draft {
			mr.Status = provider.StatusDraft
		}
		return mr
	}
	input := []provider.MergeRequest{
		mk("approved", provider.ReviewStateApproved, false, false),
		mk("draft", provider.ReviewStateUnreviewed, true, true),
		mk("commented", provider.ReviewStateReviewed, false, false),
		mk("unreviewed-1", provider.ReviewStateUnreviewed, false, false),
		mk("changes", provider.ReviewStateRequestedChanges, false, false),
		mk("replies", provider.ReviewStateApproved, false, true),
		mk("unreviewed-2", provider.ReviewStateUnapproved, false, false),
		mk("unknown", "", false, false),
	}

	sorted := sortReviewingMRs(input)

	var titles []string
	for _, mr := range sorted {
		titles = append(titles, mr.Title)
	}
	want := []string{"replies", "unreviewed-1", "unreviewed-2", "unknown", "commented", "changes", "approved", "draft"}
	if !reflect.DeepEqual(titles, want) {
		t.Errorf("order = %v\nwant    %v", titles, want)
	}
	if input[0].Title != "approved" {
		t.Error("sortReviewingMRs must not modify its input")
	}
}

func TestScrollBy(t *testing.T) {
	tests := []struct {
		key        string
		offset     int
		want       int
		wantHandle bool
	}{
		{"down", 0, 1, true},
		{"j", 10, 10, true}, // clamped at limit
		{"up", 0, 0, true},  // clamped at 0
		{"k", 5, 4, true},
		{"shift+down", 0, 10, true},
		{"shift+up", 5, 0, true},
		{"home", 7, 0, true},
		{"end", 0, 10, true},
		{"x", 3, 3, false},
	}
	for _, tt := range tests {
		got, handled := scrollBy(tt.key, tt.offset, 10)
		if got != tt.want || handled != tt.wantHandle {
			t.Errorf("scrollBy(%q, %d, 10) = (%d, %v), want (%d, %v)", tt.key, tt.offset, got, handled, tt.want, tt.wantHandle)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		in    string
		width int
		want  string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is too long", 10, "this is..."},
		{"æøåæøåæøå", 5, "æø..."}, // rune-aware, no broken UTF-8
		{"abc", 2, "ab"},
		{"abc", -5, ""},
	}
	for _, tt := range tests {
		if got := truncate(tt.in, tt.width); got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
		}
	}
}
