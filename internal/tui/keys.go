package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"glamr/internal/provider"
)

// endOfContent is a scroll target meaning "the last line"; it's clamped to the real maximum.
const endOfContent = 1 << 30

// handleKey routes a key press to the handler of the view currently on screen.
// Each view only reacts to its own keys, so e.g. 'a' in a diff never approves the MR underneath.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "ctrl+c" {
		return m, tea.Quit
	}

	// An error screen is dismissed by any key (q still quits)
	if m.err != nil {
		if key == "q" {
			return m, tea.Quit
		}
		m.err = nil
		return m, nil
	}

	if m.composingReply {
		return m.handleReplyKey(msg)
	}

	if key == "esc" {
		return m.handleEsc()
	}

	switch {
	case m.showingDiff:
		return m.handleDiffKey(key)
	case m.showingNoteThread:
		return m.handleNoteThreadKey(key)
	case m.showingFiles && m.filesView != nil:
		return m.handleFilesKey(key)
	case m.showingDetail && m.detailView != nil:
		return m.handleDetailKey(key)
	default:
		return m.handleListKey(key)
	}
}

// handleReplyKey edits the reply/new-note text being composed.
func (m Model) handleReplyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.composingReply = false
		m.replyInput = ""
		if m.selectedNote.ID == "" {
			// Cancelling a new note: there's no thread to go back to
			m.showingNoteThread = false
		}
		return m, nil
	case tea.KeyEnter:
		mr, ok := m.selectedMR()
		p := m.activeProvider()
		if m.replyInput == "" || !ok || p == nil {
			return m, nil
		}
		body := m.replyInput
		m.replyInput = ""
		m.composingReply = false
		return m, postReply(p, mr, m.selectedNote.ID, body)
	case tea.KeyBackspace:
		if r := []rune(m.replyInput); len(r) > 0 {
			m.replyInput = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeySpace:
		m.replyInput += " "
		return m, nil
	case tea.KeyRunes:
		// Covers typed characters (including non-ASCII) and pasted text
		m.replyInput += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

// handleEsc closes the top-most view.
func (m Model) handleEsc() (tea.Model, tea.Cmd) {
	switch {
	case m.showingDiff:
		m.showingDiff = false
		m.diffContent = ""
		m.diffTitle = ""
		m.diffScroll = 0
	case m.showingNoteThread:
		m.showingNoteThread = false
		m.selectedNote = provider.MRNote{}
	case m.showingFiles:
		m.showingFiles = false
		m.filesView = nil
	case m.showingDetail:
		m.showingDetail = false
		m.detailView = nil
	}
	return m, nil
}

// scrollBy applies a scroll key to an offset, clamped to [0, limit].
func scrollBy(key string, offset, limit int) (int, bool) {
	switch key {
	case "up", "k":
		offset--
	case "down", "j":
		offset++
	case "shift+up":
		offset -= 20
	case "shift+down":
		offset += 20
	case "home":
		offset = 0
	case "end":
		offset = limit
	default:
		return offset, false
	}
	if offset > limit {
		offset = limit
	}
	if offset < 0 {
		offset = 0
	}
	return offset, true
}

// handleDiffKey scrolls the diff / job log viewer.
func (m Model) handleDiffKey(key string) (tea.Model, tea.Cmd) {
	if offset, ok := scrollBy(key, m.diffScroll, m.diffMaxScroll()); ok {
		m.diffScroll = offset
		return m, nil
	}
	if key == "q" {
		return m, tea.Quit
	}
	return m, nil
}

// handleNoteThreadKey handles scrolling, replying and resolving in a discussion thread.
func (m Model) handleNoteThreadKey(key string) (tea.Model, tea.Cmd) {
	if offset, ok := scrollBy(key, m.noteThreadScroll, m.noteThreadMaxScroll()); ok {
		m.noteThreadScroll = offset
		return m, nil
	}

	switch key {
	case "r":
		m.composingReply = true
		m.replyInput = ""
		return m, nil
	case "x":
		mr, ok := m.selectedMR()
		p := m.activeProvider()
		if !m.selectedNote.Resolvable || m.selectedNote.ID == "" || !ok || p == nil {
			return m, nil
		}
		if m.selectedNote.Resolved {
			return m, unresolveDiscussion(p, mr, m.selectedNote.ID)
		}
		return m, resolveDiscussion(p, mr, m.selectedNote.ID)
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

// handleFilesKey navigates the changed-files list.
func (m Model) handleFilesKey(key string) (tea.Model, tea.Cmd) {
	count := 0
	if m.mrChanges != nil {
		count = len(m.mrChanges.Changes)
	}

	switch key {
	case "up", "k":
		if m.filesView.cursor > 0 {
			m.filesView.cursor--
		}
	case "down", "j":
		if m.filesView.cursor < count-1 {
			m.filesView.cursor++
		}
	case "d":
		if m.filesView.cursor < count {
			return m, renderFileDiff(m.mrChanges.Changes[m.filesView.cursor], m.diffWidth())
		}
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

// handleDetailKey handles the MR detail view (jobs and notes panels).
func (m Model) handleDetailKey(key string) (tea.Model, tea.Cmd) {
	d := m.detailView
	mr := d.mr
	p := m.activeProvider()

	switch key {
	case "left", "h":
		d.activePanel = JobsPanel
	case "right", "l":
		d.activePanel = NotesPanel
	case "up", "k":
		if d.activePanel == JobsPanel {
			if d.jobsCursor > 0 {
				d.jobsCursor--
			}
		} else if d.notesCursor > 0 {
			d.notesCursor--
		}
	case "down", "j":
		if d.activePanel == JobsPanel {
			if d.jobsCursor < len(d.jobs)-1 {
				d.jobsCursor++
			}
		} else if d.notesCursor < len(unresolvedNotes(d.notes))-1 {
			d.notesCursor++
		}
	case "enter":
		if d.activePanel == JobsPanel {
			if job, ok := d.selectedJob(); ok && p != nil {
				return m, fetchJobLog(p, job, mr.RepoName)
			}
		} else {
			notes := unresolvedNotes(d.notes)
			if d.notesCursor < len(notes) {
				m.selectedNote = notes[d.notesCursor]
				m.showingNoteThread = true
				m.composingReply = false
				m.replyInput = ""
				m.noteThreadScroll = 0
			}
		}
	case "o":
		return m, openInBrowser(mr.WebURL)
	case "a":
		if p != nil {
			return m, toggleApproval(p, mr)
		}
	case "D", "shift+d":
		if p != nil {
			return m, toggleDraftStatus(p, mr)
		}
	case "f":
		if p != nil {
			return m, fetchMRChanges(p, mr, false)
		}
	case "d":
		if m.mrChanges != nil {
			return m, renderFullDiff(m.mrChanges, mr, m.diffWidth())
		}
		if p != nil {
			return m, fetchMRChanges(p, mr, true)
		}
	case "n":
		m.showingNoteThread = true
		m.composingReply = true
		m.replyInput = ""
		m.selectedNote = provider.MRNote{} // Empty note = new thread
		m.noteThreadScroll = 0
	case "r":
		if p != nil {
			return m, fetchDetailData(p, mr)
		}
	case "t", "p", "c":
		job, ok := d.selectedJob()
		if !ok || d.activePanel != JobsPanel || p == nil {
			return m, nil
		}
		switch {
		case key == "t" && job.Status == "failed":
			return m, retryJob(p, job, mr.RepoName)
		case key == "p" && job.Status == "manual":
			return m, playJob(p, job, mr.RepoName)
		case key == "c" && (job.Status == "running" || job.Status == "pending"):
			return m, cancelJob(p, job, mr.RepoName)
		}
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

// handleListKey handles the main MR list.
func (m Model) handleListKey(key string) (tea.Model, tea.Cmd) {
	mr, hasMR := m.selectedMR()
	p := m.activeProvider()

	switch key {
	case "q":
		return m, tea.Quit

	case "r":
		// Manual refresh in background - keep showing current data
		if m.loading || m.refreshing {
			return m, nil // Don't stack concurrent refreshes
		}
		m.refreshing = true
		m.lastRefreshStart = time.Now()
		return m, fetchAllScopesMRs(m.providers)

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			if m.cursor < m.scroll {
				m.scroll = m.cursor
			}
		}

	case "down", "j":
		if m.cursor < len(m.mrs)-1 {
			m.cursor++
			if visible := m.visibleMRCount(); m.cursor >= m.scroll+visible {
				m.scroll = m.cursor - visible + 1
			}
		}

	case "1":
		m.switchScope(provider.ScopeAuthored)
	case "2":
		m.switchScope(provider.ScopeAssigned)
	case "3":
		m.switchScope(provider.ScopeReviewing)

	case "o":
		if hasMR {
			return m, openInBrowser(mr.WebURL)
		}

	case "a":
		if hasMR && p != nil {
			return m, toggleApproval(p, mr)
		}

	case "D", "shift+d":
		if hasMR && p != nil {
			return m, toggleDraftStatus(p, mr)
		}

	case "enter":
		if hasMR {
			m.detailView = &DetailView{
				mr:     mr,
				jobs:   mr.PipelineJobs, // Use cached jobs
				notes:  mr.Notes,        // Use cached notes
				width:  m.width,
				height: m.height,
			}
			m.showingDetail = true
			m.mrChanges = nil // Clear cached changes from previous MR
		}
	}
	return m, nil
}

// switchScope shows the cached MRs of another tab.
func (m *Model) switchScope(scope provider.Scope) {
	m.scope = scope
	m.mrs = m.scopeMRs(scope)
	m.cursor = 0
	m.scroll = 0
}
