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

// The Mentions tab comes after the forge tabs: key 4 (GitLab only) or 7 (with GitHub).
func (m Model) mentionsTab() int { return m.tabCount() }

func (m Model) onMentionsTab() bool { return m.activeTab == m.mentionsTab() }

// listLen is the number of items in the active tab's list.
func (m Model) listLen() int {
	if m.onMentionsTab() {
		return len(m.mentions)
	}
	return len(m.mrs)
}

// forgeProviders are the providers whose mentions are listed; index i fills forgeMentions[i].
func (m Model) forgeProviders() []provider.Provider {
	if m.github != nil {
		return []provider.Provider{m.gitlab, m.github}
	}
	return []provider.Provider{m.gitlab}
}

// rebuildMentions merges the forges' mentions, newest first, without the ones marked read.
func (m *Model) rebuildMentions() {
	var all []provider.Mention
	for _, list := range m.forgeMentions {
		for _, mention := range list {
			if !m.readList.IsRead(mention.ID) {
				all = append(all, mention)
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	m.mentions = all

	if m.onMentionsTab() && m.cursor >= len(m.mentions) {
		m.cursor = max(len(m.mentions)-1, 0)
		m.scroll = min(m.scroll, m.cursor)
	}
}

// selectedMention returns the mention under the cursor on the Mentions tab.
func (m Model) selectedMention() (provider.Mention, bool) {
	if !m.onMentionsTab() || m.cursor < 0 || m.cursor >= len(m.mentions) {
		return provider.Mention{}, false
	}
	return m.mentions[m.cursor], true
}

// handleMentionKey handles the Mentions tab's own keys; navigation is shared with the MR lists.
func (m Model) handleMentionKey(key string) (tea.Model, tea.Cmd, bool) {
	mention, ok := m.selectedMention()
	switch key {
	case "a":
		// Mark read: kept in the read list until the mention leaves the window
		if ok {
			if err := m.readList.MarkRead(mention.ID, mention.CreatedAt.Add(m.mentionWindow)); err != nil {
				m.err = err
			}
			m.rebuildMentions()
		}
		return m, nil, true
	case "o":
		if ok {
			return m, openInBrowser(mention.URL), true
		}
		return m, nil, true
	case "enter":
		if ok {
			// Show the full comment in the scroll viewer (already loaded, no extra request)
			m.diffTitle = fmt.Sprintf("Mention: %s %s", mention.Ref(), mention.MRTitle)
			m.diffContent = fmt.Sprintf("@%s mentioned you on %s in %s %s\n%s\n\n%s",
				mention.Author, mention.CreatedAt.Local().Format("Mon Jan 2 15:04"), mention.RepoName, mention.Ref(), mention.URL, mention.Body)
			m.diffScroll = 0
			m.showingDiff = true
		}
		return m, nil, true
	case "D", "shift+d":
		return m, nil, true // no MR actions on mentions
	}
	return m, nil, false
}

func (m Model) mentionDays() int {
	return int(m.mentionWindow.Round(24*time.Hour) / (24 * time.Hour))
}

func (m Model) renderMentionList() string {
	if m.loading {
		return emptyStyle.Render("Loading mentions...")
	}
	if len(m.mentions) == 0 {
		return emptyStyle.Render(fmt.Sprintf("No unread mentions in the last %d days", m.mentionDays()))
	}

	visible := m.visibleMRCount()
	start := min(m.scroll, len(m.mentions))
	end := min(start+visible, len(m.mentions))

	grey := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	var items []string
	if start > 0 {
		items = append(items, grey.Render(fmt.Sprintf("▲ %d more above ▲", start)), "")
	}
	for i := start; i < end; i++ {
		items = append(items, m.renderMention(m.mentions[i], i == m.cursor))
	}
	if end < len(m.mentions) {
		items = append(items, "", grey.Render(fmt.Sprintf("▼ %d more below ▼", len(m.mentions)-end)))
	}
	return lipgloss.JoinVertical(lipgloss.Left, items...)
}

func (m Model) renderMention(mention provider.Mention, selected bool) string {
	author := lipgloss.NewStyle().Foreground(lipgloss.Color("#00ffff")).Bold(true).Render("@" + mention.Author)
	when := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Render(mention.CreatedAt.Local().Format("Mon Jan 2 15:04"))
	repo := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Italic(true).Render("   [" + mention.RepoName + "]")

	// Start of the comment on one line (comments often begin with "@you" on a line of its own)
	snippet := truncate(strings.Join(strings.Fields(mention.Body), " "), max(m.width-10, 20))

	content := lipgloss.JoinVertical(lipgloss.Left,
		fmt.Sprintf("💬 %s mentioned you · %s", author, when),
		fmt.Sprintf("   %s %s", mention.Ref(), mention.MRTitle),
		repo,
		"   "+snippet,
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
