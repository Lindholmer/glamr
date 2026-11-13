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
