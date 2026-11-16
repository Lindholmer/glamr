package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"glamr/internal/provider"
)

// DefaultMentionWindow is how far back the Mentions tab looks.
const DefaultMentionWindow = 7 * 24 * time.Hour

const mentionTimeFormat = "Mon Jan 2 15:04"

// readMention is a row of the read view: a mention marked read and when its mark expires.
// loaded is false if the mention itself isn't in the fetched data (e.g. outside the window).
type readMention struct {
	mention   provider.Mention
	expiresAt time.Time
	loaded    bool
}

// The Mentions tab comes after the forge tabs: key 4 (GitLab only) or 7 (with GitHub).
func (m Model) mentionsTab() int { return m.tabCount() }

func (m Model) onMentionsTab() bool { return m.activeTab == m.mentionsTab() }

// listLen is the number of items in the active tab's list.
func (m Model) listLen() int {
	switch {
	case m.onMentionsTab() && m.showingRead:
		return len(m.readMentions)
	case m.onMentionsTab():
		return len(m.mentions)
	default:
		return len(m.mrs)
	}
}

// forgeProviders are the providers whose mentions are listed; index i fills forgeMentions[i].
func (m Model) forgeProviders() []provider.Provider {
	if m.github != nil {
		return []provider.Provider{m.gitlab, m.github}
	}
	return []provider.Provider{m.gitlab}
}

// mentionProvider is the forge a mention came from (by its ID prefix).
func (m Model) mentionProvider(mention provider.Mention) provider.Provider {
	if strings.HasPrefix(mention.ID, "github:") && m.github != nil {
		return m.github
	}
	return m.gitlab
}

// rebuildMentions derives both Mentions views from the fetched mentions and the read list:
// unread mentions newest first, and read ones soonest to expire first.
func (m *Model) rebuildMentions() {
	byID := map[string]provider.Mention{}
	var unread []provider.Mention
	for _, list := range m.forgeMentions {
		for _, mention := range list {
			byID[mention.ID] = mention
			if !m.readList.IsRead(mention.ID) {
				unread = append(unread, mention)
			}
		}
	}
	sort.SliceStable(unread, func(i, j int) bool { return unread[i].CreatedAt.After(unread[j].CreatedAt) })
	m.mentions = unread

	m.readMentions = nil
	for _, entry := range m.readList.Entries() {
		mention, loaded := byID[entry.ID]
		if !loaded {
			mention = provider.Mention{ID: entry.ID}
		}
		m.readMentions = append(m.readMentions, readMention{mention: mention, expiresAt: entry.ExpiresAt, loaded: loaded})
	}

	if m.onMentionsTab() && m.cursor >= m.listLen() {
		m.cursor = max(m.listLen()-1, 0)
		m.scroll = min(m.scroll, m.cursor)
	}
}

// selectedMention returns the mention under the cursor on the Mentions tab (either view).
// loaded is false for read marks whose mention isn't in the fetched data.
func (m Model) selectedMention() (mention provider.Mention, loaded, ok bool) {
	if !m.onMentionsTab() || m.cursor < 0 || m.cursor >= m.listLen() {
		return provider.Mention{}, false, false
	}
	if m.showingRead {
		r := m.readMentions[m.cursor]
		return r.mention, r.loaded, true
	}
	return m.mentions[m.cursor], true, true
}

// handleMentionKey handles the Mentions tab's own keys; navigation is shared with the MR lists.
func (m Model) handleMentionKey(key string) (tea.Model, tea.Cmd, bool) {
	mention, loaded, ok := m.selectedMention()
	switch key {
	case "s":
		// Toggle between unread mentions and the ones marked read
		m.showingRead = !m.showingRead
		m.cursor, m.scroll = 0, 0
		return m, nil, true
	case "a":
		if !ok {
			return m, nil, true
		}
		var err error
		if m.showingRead {
			err = m.readList.Unmark(mention.ID) // back to unread
		} else {
			// Kept in the read list until the mention leaves the window
			err = m.readList.MarkRead(mention.ID, mention.CreatedAt.Add(m.mentionWindow))
		}
		if err != nil {
			m.err = err
		}
		m.rebuildMentions()
		return m, nil, true
	case "o":
		if ok && mention.URL != "" {
			return m, openInBrowser(mention.URL), true
		}
		return m, nil, true
	case "enter":
		if ok && loaded {
			m.viewMention(mention)
		}
		return m, nil, true
	case "D", "shift+d":
		return m, nil, true // no MR actions on mentions
	}
	return m, nil, false
}

// viewMention shows the full comment in the scroll viewer (already loaded, no extra request).
func (m *Model) viewMention(mention provider.Mention) {
	m.viewingMention = &mention
	m.diffTitle = fmt.Sprintf("Mention: %s %s", mention.Ref(), mention.MRTitle)
	m.diffContent = fmt.Sprintf("@%s mentioned you on %s in %s %s\n%s\n\n%s",
		mention.Author, mention.CreatedAt.Local().Format(mentionTimeFormat), mention.RepoName, mention.Ref(), mention.URL, mention.Body)
	m.diffScroll = 0
	m.showingDiff = true
}

// startMentionReply opens the reply box for the mention being read. The reply goes into the
// mention's thread when it has one, otherwise it's posted as a new comment on the MR/PR.
func (m *Model) startMentionReply() {
	mention := *m.viewingMention
	m.mentionReply = &mention
	m.selectedNote = provider.MRNote{
		ID:        mention.ReplyTo,
		Author:    mention.Author,
		Body:      mention.Body,
		CreatedAt: mention.CreatedAt.Local().Format(mentionTimeFormat),
	}
	m.showingNoteThread = true
	m.composingReply = true
	m.replyInput = ""
	m.noteThreadScroll = 0
}

// closeMentionReply leaves the reply box, back to the mention being read.
func (m *Model) closeMentionReply() {
	m.mentionReply = nil
	m.showingNoteThread = false
	m.composingReply = false
	m.replyInput = ""
	m.selectedNote = provider.MRNote{}
}

func (m Model) mentionDays() int {
	return int(m.mentionWindow.Round(24*time.Hour) / (24 * time.Hour))
}

func (m Model) mentionsHeader() string {
	if m.showingRead {
		return "glamr - Mentions marked read (soonest to expire first)"
	}
	return fmt.Sprintf("glamr - Mentions (last %d days)", m.mentionDays())
}

func (m Model) mentionsHelp() string {
	tabs := fmt.Sprintf("1-%d: switch tab", m.mentionsTab()+1)
	if m.showingRead {
		return "↑/↓: navigate | enter: read in full | a: mark unread | o: open comment in browser | s: back to unread | " + tabs + " | r: refresh | q: quit"
	}
	return "↑/↓: navigate | enter: read in full | a: mark read | o: open comment in browser | s: show read | " + tabs + " | r: refresh | q: quit"
}

func (m Model) renderMentionList() string {
	if m.loading {
		return emptyStyle.Render("Loading mentions...")
	}
	count := m.listLen()
	if count == 0 {
		if m.showingRead {
			return emptyStyle.Render("No mentions marked read")
		}
		return emptyStyle.Render(fmt.Sprintf("No unread mentions in the last %d days", m.mentionDays()))
	}

	visible := m.visibleMRCount()
	start := min(m.scroll, count)
	end := min(start+visible, count)

	grey := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	var items []string
	if start > 0 {
		items = append(items, grey.Render(fmt.Sprintf("▲ %d more above ▲", start)), "")
	}
	for i := start; i < end; i++ {
		if m.showingRead {
			items = append(items, m.renderReadMention(m.readMentions[i], i == m.cursor))
		} else {
			items = append(items, m.renderMention(m.mentions[i], "", i == m.cursor))
		}
	}
	if end < count {
		items = append(items, "", grey.Render(fmt.Sprintf("▼ %d more below ▼", count-end)))
	}
	return lipgloss.JoinVertical(lipgloss.Left, items...)
}

// renderMention renders a mention row; extra is appended to its first line.
func (m Model) renderMention(mention provider.Mention, extra string, selected bool) string {
	grey := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	author := lipgloss.NewStyle().Foreground(lipgloss.Color("#00ffff")).Bold(true).Render("@" + mention.Author)
	when := grey.Render(mention.CreatedAt.Local().Format(mentionTimeFormat))
	repo := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Italic(true).Render("   [" + mention.RepoName + "]")

	// Start of the comment on one line (comments often begin with "@you" on a line of its own)
	snippet := truncate(strings.Join(strings.Fields(mention.Body), " "), max(m.width-10, 20))

	first := fmt.Sprintf("💬 %s mentioned you · %s", author, when)
	if extra != "" {
		first += grey.Render(" · " + extra)
	}
	content := lipgloss.JoinVertical(lipgloss.Left,
		first,
		fmt.Sprintf("   %s %s", mention.Ref(), mention.MRTitle),
		repo,
		"   "+snippet,
	)
	if selected {
		return selectedItemStyle.Render(content)
	}
	return itemStyle.Render(content)
}

func (m Model) renderReadMention(r readMention, selected bool) string {
	expires := "read, mark expires " + r.expiresAt.Local().Format(mentionTimeFormat)
	if r.loaded {
		return m.renderMention(r.mention, expires, selected)
	}

	// Mark without its mention (outside the current window, or its forge didn't respond)
	grey := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	content := lipgloss.JoinVertical(lipgloss.Left,
		"💬 "+r.mention.ID+grey.Render(" · "+expires),
		grey.Render("   (not loaded: outside the current window)"),
		"",
		"",
	)
	if selected {
		return selectedItemStyle.Render(content)
	}
	return itemStyle.Render(content)
}

type mentionResult struct {
	mentions []provider.Mention
	err      error
}
