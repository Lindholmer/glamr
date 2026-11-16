package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"glamr/internal/provider"
	"glamr/internal/readlist"
)

func testMention(id, author string, age time.Duration, mrID string) provider.Mention {
	return provider.Mention{
		ID:        id,
		Author:    author,
		Body:      "@me please look at this\nsecond line",
		CreatedAt: time.Now().Add(-age),
		URL:       "https://example.com/" + id,
		MRID:      mrID,
		MRTitle:   "Some MR",
		RepoName:  "team/app",
	}
}

// mentionsModel loads a model whose providers return the given mentions, refreshing once.
func mentionsModel(t *testing.T, reads *readlist.List, window time.Duration, gitlabMentions, githubMentions []provider.Mention) Model {
	t.Helper()
	gitlab := &fakeProvider{mentions: gitlabMentions}
	var github provider.Provider
	if githubMentions != nil {
		github = &fakeProvider{name: "GitHub", mentions: githubMentions}
	}
	m := NewModel(gitlab, github, reads, window)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m, _ = send(t, m, fetchTabs(m)())
	return m
}

func TestMentionsTabMergesForgesNewestFirst(t *testing.T) {
	m := mentionsModel(t, nil, 0,
		[]provider.Mention{testMention("gitlab:1", "alice", 2*time.Hour, "team/app!1"), testMention("gitlab:2", "bob", 3*24*time.Hour, "team/app!2")},
		[]provider.Mention{testMention("github:9", "carol", time.Hour, "octo/app#9")})

	m, _ = press(t, m, "7")
	if !m.onMentionsTab() {
		t.Fatal("7 should open the Mentions tab when GitHub is enabled")
	}

	var ids []string
	for _, mention := range m.mentions {
		ids = append(ids, mention.ID)
	}
	if want := "github:9,gitlab:1,gitlab:2"; strings.Join(ids, ",") != want {
		t.Errorf("mentions = %v, want %s", ids, want)
	}

	view := m.View()
	for _, want := range []string{"glamr - Mentions (last 7 days)", "7. Mentions (3)", "@carol mentioned you", "#9 Some MR", "!1 Some MR", "@me please look at this"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
	if !strings.Contains(view, "@me please look at this second line") {
		t.Error("the list should show the start of the comment on one line")
	}
}

func TestMentionWindow(t *testing.T) {
	old := testMention("gitlab:old", "alice", 10*24*time.Hour, "team/app!1")
	recent := testMention("gitlab:new", "bob", time.Hour, "team/app!2")

	week := mentionsModel(t, nil, 0, []provider.Mention{old, recent}, nil)
	if len(week.mentions) != 1 || week.mentions[0].ID != "gitlab:new" {
		t.Errorf("7-day window: %+v", week.mentions)
	}

	month := mentionsModel(t, nil, 30*24*time.Hour, []provider.Mention{old, recent}, nil)
	if len(month.mentions) != 2 {
		t.Errorf("30-day window should include the 10-day-old mention: %+v", month.mentions)
	}
	if m, _ := press(t, month, "4"); !strings.Contains(m.View(), "Mentions (last 30 days)") {
		t.Error("header should show the window")
	}
}

func TestMarkMentionRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "read_mentions.json")
	reads, err := readlist.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	first := testMention("gitlab:1", "alice", time.Hour, "team/app!1")
	second := testMention("gitlab:2", "bob", 2*time.Hour, "team/app!2")
	m := mentionsModel(t, reads, 0, []provider.Mention{first, second}, nil)

	m, _ = press(t, m, "4", "a") // Mentions tab (GitLab only), mark the first read
	if len(m.mentions) != 1 || m.mentions[0].ID != "gitlab:2" {
		t.Fatalf("after marking read: %+v", m.mentions)
	}

	// Persisted, with the expiry at mention time + window
	reloaded, _ := readlist.Load(path)
	if !reloaded.IsRead("gitlab:1") {
		t.Error("read mark should be saved to disk")
	}

	// Stays hidden after a refresh
	m, _ = send(t, m, fetchTabs(m)())
	if len(m.mentions) != 1 {
		t.Errorf("read mention came back after refresh: %+v", m.mentions)
	}

	// Marking the last one leaves an empty list; 'a' again is harmless
	m, _ = press(t, m, "a", "a")
	if len(m.mentions) != 0 || !strings.Contains(m.View(), "No unread mentions in the last 7 days") {
		t.Errorf("expected empty mentions list, got %+v", m.mentions)
	}
	if strings.Contains(m.renderTabs(), "Mentions (") {
		t.Error("tab should show no count when everything is read")
	}
}

func TestReadMarkExpiry(t *testing.T) {
	reads := readlist.New()
	mention := testMention("gitlab:1", "alice", time.Hour, "team/app!1")
	m := mentionsModel(t, reads, 0, []provider.Mention{mention}, nil)
	m, _ = press(t, m, "4", "a")

	// The mark expires when the mention leaves the window
	before := time.Now().Add(DefaultMentionWindow - time.Hour - time.Minute)
	after := time.Now().Add(DefaultMentionWindow - time.Hour + time.Minute)
	_ = reads.Prune(before)
	if !reads.IsRead("gitlab:1") {
		t.Error("read mark should be kept while the mention is in the window")
	}
	_ = reads.Prune(after)
	if reads.IsRead("gitlab:1") {
		t.Error("read mark should expire once the mention is out of the window")
	}
}

func TestRefreshPrunesExpiredReadMarks(t *testing.T) {
	reads := readlist.New()
	_ = reads.MarkRead("gitlab:gone", time.Now().Add(-time.Minute))
	_ = reads.MarkRead("gitlab:kept", time.Now().Add(time.Hour))

	m := mentionsModel(t, reads, 0, nil, nil)
	_ = m
	if reads.IsRead("gitlab:gone") || !reads.IsRead("gitlab:kept") {
		t.Error("refresh should prune expired read marks only")
	}
}

func TestMentionKeys(t *testing.T) {
	m := mentionsModel(t, nil, 0, []provider.Mention{
		testMention("gitlab:1", "alice", time.Hour, "team/app!1"),
		testMention("gitlab:2", "bob", 2*time.Hour, "team/app!2"),
	}, nil)
	m, _ = press(t, m, "4")

	m, _ = press(t, m, "j", "j") // clamped at the last mention
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1", m.cursor)
	}

	if _, cmd := press(t, m, "o"); cmd == nil {
		t.Error("o should open the comment in the browser")
	}
	if _, cmd := press(t, m, "D"); cmd != nil {
		t.Error("D does nothing on mentions")
	}

	m, _ = press(t, m, "enter")
	if !m.showingDiff || !strings.Contains(m.diffContent, "second line") || !strings.Contains(m.diffContent, "@bob mentioned you") {
		t.Errorf("enter should show the full mention, got %q", m.diffContent)
	}
	m, _ = press(t, m, "esc")
	if m.showingDiff || !m.onMentionsTab() {
		t.Error("esc should return to the Mentions tab")
	}
}

func TestMentionFetchFailureKeepsPrevious(t *testing.T) {
	m := mentionsModel(t, nil, 0, []provider.Mention{testMention("gitlab:1", "alice", time.Hour, "team/app!1")}, nil)

	msg := tabsFetchedMsg{count: tabsPerForge, mentionCount: 1}
	msg.mentions[0].err = errors.New("todos unavailable")
	m, _ = send(t, m, msg)

	if len(m.mentions) != 1 {
		t.Error("mentions should be kept when fetching them fails")
	}
	if m.refreshErr == nil || !strings.Contains(m.refreshErr.Error(), "GitLab Mentions: todos unavailable") {
		t.Errorf("refreshErr = %v", m.refreshErr)
	}
}

func TestMentionsRenderOnSmallTerminals(t *testing.T) {
	long := testMention("gitlab:1", strings.Repeat("a", 50), time.Hour, "team/app!1")
	long.Body = strings.Repeat("very long comment ", 50)
	for _, github := range [][]provider.Mention{nil, {}} {
		m := mentionsModel(t, nil, 0, []provider.Mention{long}, github)
		m, _ = press(t, m, "7", "4") // whichever is the Mentions tab
		for _, size := range []tea.WindowSizeMsg{{Width: 0, Height: 0}, {Width: 5, Height: 3}, {Width: 30, Height: 10}} {
			m, _ = send(t, m, size)
			_ = m.View()
			m2, _ := press(t, m, "enter")
			_ = m2.View()
		}
	}
}

func TestReplyToMention(t *testing.T) {
	inThread := testMention("gitlab:1", "alice", time.Hour, "team/app!5")
	inThread.IID, inThread.ReplyTo = 5, "disc123"
	inDescription := testMention("gitlab:2", "bob", 2*time.Hour, "team/app!6")
	inDescription.IID = 6
	gitlab := &fakeProvider{mentions: []provider.Mention{inThread, inDescription}}
	github := &fakeProvider{name: "GitHub", mentions: []provider.Mention{}}
	m := NewModel(gitlab, github, nil, 0)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m, _ = send(t, m, fetchTabs(m)())

	// Read the first mention in full, press m, type, post
	m, _ = press(t, m, "7", "enter")
	if !strings.Contains(m.View(), "m: reply") {
		t.Error("the full-read view should offer m: reply")
	}
	m, _ = press(t, m, "m")
	if !m.composingReply || !strings.Contains(m.View(), "Reply to @alice - !5 Some MR") || !strings.Contains(m.View(), "@me please look at this") {
		t.Fatalf("m should open the reply box with the mention shown:\n%s", m.View())
	}
	m, cmd := press(t, m, "o", "k", "enter")
	m = run(t, m, cmd)

	if m.showingNoteThread || m.mentionReply != nil || !m.showingDiff || m.viewingMention == nil {
		t.Error("after posting, glamr should be back on the mention's full-read view")
	}

	// Esc cancels a reply and also returns to the mention
	m, _ = press(t, m, "m", "x", "esc")
	if m.showingNoteThread || m.composingReply || !m.showingDiff {
		t.Error("esc should cancel the reply and return to the mention")
	}

	// A mention in the description has no thread: the reply is a new comment
	m, _ = press(t, m, "esc", "j", "enter", "m")
	if !strings.Contains(m.View(), "Reply to @bob (new comment)") {
		t.Errorf("description mention reply should be a new comment:\n%s", m.View())
	}
	m, cmd = press(t, m, "h", "i", "enter")
	m = run(t, m, cmd)

	want := []string{"reply team/app!5 disc123 ok", "note team/app!6 hi"}
	if got := gitlab.Calls(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("GitLab calls = %v, want %v", got, want)
	}
	if len(github.Calls()) != 0 {
		t.Error("GitLab mentions must not be answered through GitHub")
	}
}

func TestReplyToGitHubMentionUsesGitHub(t *testing.T) {
	mention := testMention("github:IC_1", "carol", time.Hour, "octo/app#9")
	mention.IID, mention.RepoName = 9, "octo/app"
	gitlab := &fakeProvider{}
	github := &fakeProvider{name: "GitHub", mentions: []provider.Mention{mention}}
	m := NewModel(gitlab, github, nil, 0)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m, _ = send(t, m, fetchTabs(m)())

	m, _ = press(t, m, "7", "enter", "m", "y")
	m, cmd := press(t, m, "enter")
	run(t, m, cmd)

	// fakeProvider formats notes as repo!iid whatever the forge
	if got := github.Calls(); len(got) != 1 || got[0] != "note octo/app!9 y" {
		t.Errorf("GitHub calls = %v", got)
	}
	if len(gitlab.Calls()) != 0 {
		t.Errorf("GitHub mention answered through GitLab: %v", gitlab.Calls())
	}
}

func TestReadView(t *testing.T) {
	reads := readlist.New()
	soon := testMention("gitlab:soon", "alice", 6*24*time.Hour, "team/app!1") // expires in ~1 day
	later := testMention("gitlab:later", "bob", time.Hour, "team/app!2")      // expires in ~7 days
	unread := testMention("gitlab:unread", "carol", 2*time.Hour, "team/app!3")
	m := mentionsModel(t, reads, 0, []provider.Mention{soon, later, unread}, nil)
	m, _ = press(t, m, "4")

	// Mark later, then soon read (in list order: later, unread, soon)
	m, _ = press(t, m, "a") // later
	m, _ = press(t, m, "j", "a")
	if len(m.mentions) != 1 || m.mentions[0].ID != "gitlab:unread" {
		t.Fatalf("unread = %+v", m.mentions)
	}

	// An old mark whose mention isn't loaded (e.g. outside a shorter window)
	_ = reads.MarkRead("gitlab:gone", time.Now().Add(3*24*time.Hour))
	m.rebuildMentions()

	m, _ = press(t, m, "s")
	if !m.showingRead || !strings.Contains(m.View(), "Mentions marked read (soonest to expire first)") {
		t.Fatal("s should show the read mentions")
	}
	var order []string
	for _, r := range m.readMentions {
		order = append(order, r.mention.ID)
	}
	if got := strings.Join(order, ","); got != "gitlab:soon,gitlab:gone,gitlab:later" {
		t.Errorf("read order = %s, want soonest expiry first", got)
	}
	view := m.View()
	if !strings.Contains(view, "mark expires") || !strings.Contains(view, "not loaded: outside the current window") {
		t.Errorf("read view should show expiry and unloaded marks:\n%s", view)
	}

	// Enter reads it; enter on an unloaded mark does nothing
	m, _ = press(t, m, "enter")
	if !m.showingDiff || !strings.Contains(m.diffContent, "@alice") {
		t.Error("enter should open a read mention in full")
	}
	m, _ = press(t, m, "esc", "j", "enter")
	if m.showingDiff {
		t.Error("an unloaded mark has nothing to show")
	}

	// a marks unread again
	m, _ = press(t, m, "k", "a")
	if reads.IsRead("gitlab:soon") || len(m.readMentions) != 2 {
		t.Errorf("a in the read view should mark unread: %+v", m.readMentions)
	}
	m, _ = press(t, m, "s")
	if m.showingRead || len(m.mentions) != 2 {
		t.Errorf("back to unread: showingRead=%v unread=%d", m.showingRead, len(m.mentions))
	}

	// Switching tabs leaves the read view
	m, _ = press(t, m, "s", "1", "4")
	if m.showingRead {
		t.Error("switching tabs should reset to the unread view")
	}
}
