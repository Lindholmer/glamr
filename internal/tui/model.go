package tui

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"glamr/internal/cache"
	"glamr/internal/provider"
)

type Model struct {
	providers         []provider.Provider
	scope             provider.Scope
	mrs               []provider.MergeRequest
	mrsAuthored       []provider.MergeRequest // Cached authored MRs
	mrsAssigned       []provider.MergeRequest // Cached assigned MRs
	mrsReviewing      []provider.MergeRequest // Cached reviewing MRs
	cursor            int
	scroll            int // Scroll offset for MR list
	loading           bool
	refreshing        bool // Background refresh in progress
	err               error
	cache             *cache.Cache
	refreshInterval   time.Duration
	width             int
	height            int
	detailView        *DetailView
	showingDetail     bool
	filesView         *FilesView
	showingFiles      bool
	mrChanges         *provider.MRChanges
	showingDiff       bool
	diffContent       string
	diffTitle         string
	diffScroll        int // Scroll offset for diff view
	showingJobLog     bool
	jobLog            string
	jobLogTitle       string
	showingNoteThread bool
	selectedNote      provider.MRNote
	noteThreadScroll  int    // Scroll offset for note thread view
	replyInput        string
	composingReply    bool
}

// Messages
type mrsFetchedMsg struct {
	mrs []provider.MergeRequest
	err error
}

type allScopesMRsMsg struct {
	authored  []provider.MergeRequest
	assigned  []provider.MergeRequest
	reviewing []provider.MergeRequest
	err       error
}

type detailDataMsg struct {
	jobs  []provider.PipelineJob
	notes []provider.MRNote
	err   error
}

type jobLogMsg struct {
	log   string
	title string
	err   error
}

type jobActionMsg struct {
	success bool
	err     error
}

type replyPostedMsg struct {
	success bool
	err     error
}

type tickMsg time.Time

type mrChangesMsg struct {
	changes  *provider.MRChanges
	err      error
	showDiff bool // If true, auto-show full diff after fetching
}

type diffMsg struct {
	content string
	title   string
	err     error
}

type approvalMsg struct {
	mrID     string
	approved bool
	err      error
}

type resolveMsg struct {
	discussionID string
	resolved     bool
	err          error
}

type draftMsg struct {
	mrID string
	err  error
}

func NewModel(providers ...provider.Provider) Model {
	return Model{
		providers:       providers,
		scope:           provider.ScopeAuthored,
		cache:           cache.NewCache(5 * time.Minute), // 5 minute cache
		refreshInterval: 5 * time.Minute,                 // Check every 5 minutes
		loading:         true,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		fetchAllScopesMRs(m.providers), // Fetch all scopes at once
		tickCmd(),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// Handle composing reply
		if m.composingReply {
			switch msg.String() {
			case "esc":
				m.composingReply = false
				m.replyInput = ""
				return m, nil
			case "enter":
				// Post the reply
				if m.replyInput != "" && len(m.mrs) > 0 {
					selectedMR := m.mrs[m.cursor]
					var p provider.Provider
					if len(m.providers) > 0 {
						p = m.providers[0]
					}
					if p != nil {
						body := m.replyInput
						m.replyInput = ""
						m.composingReply = false
						return m, postReply(p, selectedMR, m.selectedNote.ID, body)
					}
				}
				return m, nil
			case "backspace":
				if len(m.replyInput) > 0 {
					m.replyInput = m.replyInput[:len(m.replyInput)-1]
				}
				return m, nil
			default:
				// Add character to input
				if len(msg.String()) == 1 {
					m.replyInput += msg.String()
				}
				return m, nil
			}
		}

		// Handle navigation in note thread view
		if m.showingNoteThread && !m.composingReply {
			switch msg.String() {
			case "up", "k":
				// Scroll up in note thread
				if m.noteThreadScroll > 0 {
					m.noteThreadScroll--
				}
				return m, nil
			case "down", "j":
				// Scroll down in note thread
				m.noteThreadScroll++
				return m, nil
			case "shift+up":
				// Scroll up 20 lines
				m.noteThreadScroll -= 20
				if m.noteThreadScroll < 0 {
					m.noteThreadScroll = 0
				}
				return m, nil
			case "shift+down":
				// Scroll down 20 lines
				m.noteThreadScroll += 20
				return m, nil
			case "home":
				// Go to top
				m.noteThreadScroll = 0
				return m, nil
			case "end":
				// Go to bottom - will be clamped in render function
				m.noteThreadScroll = 999999
				return m, nil
			case "r":
				// Start composing reply
				m.composingReply = true
				m.replyInput = ""
				return m, nil
			case "x":
				// Toggle resolve/unresolve discussion
				if m.selectedNote.Resolvable && m.selectedNote.ID != "" {
					selectedMR := m.mrs[m.cursor]
					var p provider.Provider
					if len(m.providers) > 0 {
						p = m.providers[0]
					}
					if p != nil {
						if m.selectedNote.Resolved {
							return m, unresolveDiscussion(p, selectedMR, m.selectedNote.ID)
						} else {
							return m, resolveDiscussion(p, selectedMR, m.selectedNote.ID)
						}
					}
				}
				return m, nil
			}
		}

		// Handle navigation in diff view
		if m.showingDiff {
			switch msg.String() {
			case "up", "k":
				if m.diffScroll > 0 {
					m.diffScroll--
				}
				return m, nil
			case "down", "j":
				m.diffScroll++
				return m, nil
			case "shift+up":
				m.diffScroll -= 20
				if m.diffScroll < 0 {
					m.diffScroll = 0
				}
				return m, nil
			case "shift+down":
				m.diffScroll += 20
				return m, nil
			case "home":
				m.diffScroll = 0
				return m, nil
			case "end":
				// Go to bottom - will be clamped in render function
				m.diffScroll = 999999
				return m, nil
			}
		}

		// Handle escape - exit note thread, job log, diff, files view, or detail view
		if msg.String() == "esc" {
			if m.showingDiff {
				m.showingDiff = false
				m.diffContent = ""
				m.diffTitle = ""
				m.diffScroll = 0
				return m, nil
			}
			if m.showingJobLog {
				m.showingJobLog = false
				m.jobLog = ""
				m.jobLogTitle = ""
				return m, nil
			}
			if m.showingNoteThread {
				if m.composingReply {
					// Cancel composing
					m.composingReply = false
					m.replyInput = ""
					return m, nil
				}
				// Exit note thread view
				m.showingNoteThread = false
				m.selectedNote = provider.MRNote{}
				return m, nil
			}
			if m.showingFiles {
				// Exit files view, return to detail view
				m.showingFiles = false
				m.filesView = nil
				return m, nil
			}
			if m.showingDetail {
				m.showingDetail = false
				m.detailView = nil
				m.err = nil // Clear any detail view errors
				return m, nil
			}
		}

		// Handle navigation in files view
		if m.showingFiles && m.filesView != nil {
			switch msg.String() {
			case "up", "k":
				if m.filesView.cursor > 0 {
					m.filesView.cursor--
				}
				return m, nil
			case "down", "j":
				if m.filesView.cursor < len(m.mrChanges.Changes)-1 {
					m.filesView.cursor++
				}
				return m, nil
			case "d":
				// Show diff for selected file
				if m.filesView.cursor < len(m.mrChanges.Changes) {
					selectedFile := m.mrChanges.Changes[m.filesView.cursor]
					return m, renderFileDiff(selectedFile)
				}
				return m, nil
			}
		}

		// Handle navigation in detail view
		if m.showingDetail && m.detailView != nil {
			switch msg.String() {
			case "left", "h":
				// Switch to jobs panel
				m.detailView.activePanel = JobsPanel
				return m, nil
			case "right", "l":
				// Switch to notes panel
				m.detailView.activePanel = NotesPanel
				return m, nil
			case "up", "k":
				// Navigate up in active panel
				if m.detailView.activePanel == JobsPanel {
					if m.detailView.jobsCursor > 0 {
						m.detailView.jobsCursor--
					}
				} else {
					if m.detailView.notesCursor > 0 {
						m.detailView.notesCursor--
					}
				}
				return m, nil
			case "down", "j":
				// Navigate down in active panel
				if m.detailView.activePanel == JobsPanel {
					if m.detailView.jobsCursor < len(m.detailView.jobs)-1 {
						m.detailView.jobsCursor++
					}
				} else {
					// Count unresolved notes
					unresolvedCount := 0
					for _, note := range m.detailView.notes {
						if !(note.Resolvable && note.Resolved) {
							unresolvedCount++
						}
					}
					if m.detailView.notesCursor < unresolvedCount-1 {
						m.detailView.notesCursor++
					}
				}
				return m, nil
			case "enter":
				if m.detailView.activePanel == JobsPanel && len(m.detailView.jobs) > 0 {
					// View job log when jobs panel is active
					selectedJob := m.detailView.jobs[m.detailView.jobsCursor]
					var p provider.Provider
					if len(m.providers) > 0 {
						p = m.providers[0]
					}
					if p != nil {
						return m, fetchJobLog(p, selectedJob, m.mrs[m.cursor].RepoName)
					}
				} else if m.detailView.activePanel == NotesPanel {
					// Open note thread when notes panel is active
					// Filter to get unresolved notes
					var unresolvedNotes []provider.MRNote
					for _, note := range m.detailView.notes {
						if note.Resolvable && note.Resolved {
							continue
						}
						unresolvedNotes = append(unresolvedNotes, note)
					}
					if len(unresolvedNotes) > 0 && m.detailView.notesCursor < len(unresolvedNotes) {
						m.selectedNote = unresolvedNotes[m.detailView.notesCursor]
						m.showingNoteThread = true
						m.composingReply = false
						m.replyInput = ""
						m.noteThreadScroll = 0 // Reset scroll when opening note
					}
				}
				return m, nil
			case "o":
				// Open MR in browser
				selectedMR := m.mrs[m.cursor]
				return m, openInBrowser(selectedMR.WebURL)
			case "a":
				// Toggle approve/unapprove MR
				selectedMR := m.mrs[m.cursor]
				var p provider.Provider
				if len(m.providers) > 0 {
					p = m.providers[0]
				}
				if p != nil {
					if selectedMR.UserApproved {
						return m, unapproveMR(p, selectedMR)
					} else {
						return m, approveMR(p, selectedMR)
					}
				}
				return m, nil
			case "D", "shift+d":
				// Toggle draft status
				selectedMR := m.mrs[m.cursor]
				var p provider.Provider
				if len(m.providers) > 0 {
					p = m.providers[0]
				}
				if p != nil {
					return m, toggleDraftStatus(p, selectedMR)
				}
				return m, nil
			case "f":
				// Show files view
				selectedMR := m.mrs[m.cursor]
				var p provider.Provider
				if len(m.providers) > 0 {
					p = m.providers[0]
				}
				if p != nil {
					return m, fetchMRChanges(p, selectedMR)
				}
				return m, nil
			case "d":
				// Show full MR diff
				selectedMR := m.mrs[m.cursor]
				if m.mrChanges == nil {
					// Fetch changes first, then auto-show diff
					var p provider.Provider
					if len(m.providers) > 0 {
						p = m.providers[0]
					}
					if p != nil {
						return m, fetchMRChangesForDiff(p, selectedMR)
					}
				} else {
					// We have changes, render the full diff
					return m, renderFullDiff(m.mrChanges, selectedMR)
				}
				return m, nil
			case "n":
				// Start composing a new note
				m.showingNoteThread = true
				m.composingReply = true
				m.replyInput = ""
				m.selectedNote = provider.MRNote{} // Empty note for new thread
				m.noteThreadScroll = 0
				return m, nil
			case "r":
				// Refresh detail view
				selectedMR := m.mrs[m.cursor]
				var p provider.Provider
				if len(m.providers) > 0 {
					p = m.providers[0]
				}
				if p != nil {
					return m, fetchDetailData(p, selectedMR)
				}
				return m, nil
			case "t":
				// Retry failed job
				if m.detailView.activePanel == JobsPanel && len(m.detailView.jobs) > 0 {
					selectedJob := m.detailView.jobs[m.detailView.jobsCursor]
					if selectedJob.Status == "failed" {
						var p provider.Provider
						if len(m.providers) > 0 {
							p = m.providers[0]
						}
						if p != nil {
							return m, retryJob(p, selectedJob, m.mrs[m.cursor].RepoName)
						}
					}
				}
				return m, nil
			case "p":
				// Play/approve manual job
				if m.detailView.activePanel == JobsPanel && len(m.detailView.jobs) > 0 {
					selectedJob := m.detailView.jobs[m.detailView.jobsCursor]
					if selectedJob.Status == "manual" {
						var p provider.Provider
						if len(m.providers) > 0 {
							p = m.providers[0]
						}
						if p != nil {
							return m, playJob(p, selectedJob, m.mrs[m.cursor].RepoName)
						}
					}
				}
				return m, nil
			case "c":
				// Cancel running or pending job
				if m.detailView.activePanel == JobsPanel && len(m.detailView.jobs) > 0 {
					selectedJob := m.detailView.jobs[m.detailView.jobsCursor]
					if selectedJob.Status == "running" || selectedJob.Status == "pending" {
						var p provider.Provider
						if len(m.providers) > 0 {
							p = m.providers[0]
						}
						if p != nil {
							return m, cancelJob(p, selectedJob, m.mrs[m.cursor].RepoName)
						}
					}
				}
				return m, nil
			}
		}

		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit

		case "r":
			// Manual refresh in background - keep showing current data
			m.refreshing = true
			return m, fetchAllScopesMRs(m.providers)

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				// Update scroll to keep cursor visible
				if m.cursor < m.scroll {
					m.scroll = m.cursor
				}
			}
			return m, nil

		case "down", "j":
			if m.cursor < len(m.mrs)-1 {
				m.cursor++
				// Update scroll to keep cursor visible
				linesPerMR := 6
				listHeight := m.height - 12 // Same as View() calculation
				indicatorLines := 4          // Account for scroll indicators
				availableLines := listHeight - indicatorLines
				visibleMRs := availableLines / linesPerMR
				if visibleMRs < 1 {
					visibleMRs = 1
				}
				if m.cursor >= m.scroll+visibleMRs {
					m.scroll = m.cursor - visibleMRs + 1
				}
			}
			return m, nil

		case "1":
			// Switch to authored scope - use cached data
			m.scope = provider.ScopeAuthored
			m.mrs = m.mrsAuthored
			m.cursor = 0
			m.scroll = 0
			return m, nil

		case "2":
			// Switch to assigned scope - use cached data
			m.scope = provider.ScopeAssigned
			m.mrs = m.mrsAssigned
			m.cursor = 0
			m.scroll = 0
			return m, nil

		case "3":
			// Switch to reviewing scope - use cached data
			m.scope = provider.ScopeReviewing
			m.mrs = m.mrsReviewing
			m.cursor = 0
			m.scroll = 0
			return m, nil

		case "o":
			// Open selected MR in browser from main list
			if len(m.mrs) > 0 && m.cursor < len(m.mrs) {
				selectedMR := m.mrs[m.cursor]
				return m, openInBrowser(selectedMR.WebURL)
			}
			return m, nil

		case "a":
			// Toggle approve/unapprove MR
			if len(m.mrs) > 0 && m.cursor < len(m.mrs) {
				selectedMR := m.mrs[m.cursor]
				var p provider.Provider
				if len(m.providers) > 0 {
					p = m.providers[0]
				}
				if p != nil {
					if selectedMR.UserApproved {
						return m, unapproveMR(p, selectedMR)
					} else {
						return m, approveMR(p, selectedMR)
					}
				}
			}
			return m, nil

		case "D", "shift+d":
			// Toggle draft status
			if len(m.mrs) > 0 && m.cursor < len(m.mrs) {
				selectedMR := m.mrs[m.cursor]
				var p provider.Provider
				if len(m.providers) > 0 {
					p = m.providers[0]
				}
				if p != nil {
					return m, toggleDraftStatus(p, selectedMR)
				}
			}
			return m, nil

		case "enter":
			// Show detail view for selected MR
			if len(m.mrs) > 0 && m.cursor < len(m.mrs) {
				selectedMR := m.mrs[m.cursor]
				m.detailView = &DetailView{
					mr:     selectedMR,
					jobs:   selectedMR.PipelineJobs, // Use cached jobs
					notes:  selectedMR.Notes,        // Use cached notes
					width:  m.width,
					height: m.height,
				}
				m.showingDetail = true
				// Clear any cached changes from previous MR
				m.mrChanges = nil
				// No need to fetch - data is already cached in the MR
			}
			return m, nil
		}

	case allScopesMRsMsg:
		m.loading = false
		m.refreshing = false // Clear refreshing flag
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.err = nil
			// Cache all three scopes
			m.mrsAuthored = msg.authored
			m.mrsAssigned = msg.assigned
			m.mrsReviewing = msg.reviewing

			// Set current view based on active scope
			switch m.scope {
			case provider.ScopeAuthored:
				m.mrs = m.mrsAuthored
			case provider.ScopeAssigned:
				m.mrs = m.mrsAssigned
			case provider.ScopeReviewing:
				m.mrs = m.mrsReviewing
			}

			// Reset cursor if out of bounds
			if m.cursor >= len(m.mrs) {
				m.cursor = 0
			}

			// Update cache with current scope's data
			m.cache.Set(m.mrs)
		}
		return m, nil

	case mrsFetchedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.err = nil
			// Atomically replace cache with new data
			m.cache.Set(msg.mrs)
			m.mrs = msg.mrs
			// Reset cursor if out of bounds
			if m.cursor >= len(m.mrs) {
				m.cursor = 0
			}
		}
		return m, nil

	case tickMsg:
		// Auto-refresh if cache is stale - do it in background
		if m.cache.IsStale() && !m.loading && !m.refreshing {
			m.refreshing = true
			return m, tea.Batch(
				fetchAllScopesMRs(m.providers), // Fetch all scopes on refresh
				tickCmd(),
			)
		}
		return m, tickCmd()

	case detailDataMsg:
		if m.showingDetail && m.detailView != nil {
			if msg.err != nil {
				// Store error to display
				m.err = msg.err
			} else {
				m.detailView.jobs = msg.jobs
				m.detailView.notes = msg.notes
			}
		}
		return m, nil

	case jobLogMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.showingJobLog = true
			m.jobLog = msg.log
			m.jobLogTitle = msg.title
		}
		return m, nil

	case jobActionMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			// Refresh detail data after action
			if m.showingDetail && len(m.mrs) > 0 {
				selectedMR := m.mrs[m.cursor]
				var p provider.Provider
				if len(m.providers) > 0 {
					p = m.providers[0]
				}
				if p != nil {
					return m, fetchDetailData(p, selectedMR)
				}
			}
		}
		return m, nil

	case approvalMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			// Update the MR in the cache
			for i := range m.mrs {
				if m.mrs[i].ID == msg.mrID {
					m.mrs[i].UserApproved = msg.approved
					if msg.approved {
						m.mrs[i].ApprovalCount++
					} else {
						m.mrs[i].ApprovalCount--
					}
					// Check if it's now fully approved
					m.mrs[i].Approved = m.mrs[i].ApprovalCount >= m.mrs[i].RequiredApprovals
					break
				}
			}
			// Also update in the scope caches
			for i := range m.mrsAuthored {
				if m.mrsAuthored[i].ID == msg.mrID {
					m.mrsAuthored[i].UserApproved = msg.approved
					if msg.approved {
						m.mrsAuthored[i].ApprovalCount++
					} else {
						m.mrsAuthored[i].ApprovalCount--
					}
					m.mrsAuthored[i].Approved = m.mrsAuthored[i].ApprovalCount >= m.mrsAuthored[i].RequiredApprovals
					break
				}
			}
			for i := range m.mrsAssigned {
				if m.mrsAssigned[i].ID == msg.mrID {
					m.mrsAssigned[i].UserApproved = msg.approved
					if msg.approved {
						m.mrsAssigned[i].ApprovalCount++
					} else {
						m.mrsAssigned[i].ApprovalCount--
					}
					m.mrsAssigned[i].Approved = m.mrsAssigned[i].ApprovalCount >= m.mrsAssigned[i].RequiredApprovals
					break
				}
			}
			for i := range m.mrsReviewing {
				if m.mrsReviewing[i].ID == msg.mrID {
					m.mrsReviewing[i].UserApproved = msg.approved
					if msg.approved {
						m.mrsReviewing[i].ApprovalCount++
					} else {
						m.mrsReviewing[i].ApprovalCount--
					}
					m.mrsReviewing[i].Approved = m.mrsReviewing[i].ApprovalCount >= m.mrsReviewing[i].RequiredApprovals
					break
				}
			}
			// Update detail view if showing
			if m.showingDetail && m.detailView != nil {
				if m.detailView.mr.ID == msg.mrID {
					m.detailView.mr.UserApproved = msg.approved
					if msg.approved {
						m.detailView.mr.ApprovalCount++
					} else {
						m.detailView.mr.ApprovalCount--
					}
					m.detailView.mr.Approved = m.detailView.mr.ApprovalCount >= m.detailView.mr.RequiredApprovals
				}
			}
		}
		return m, nil

	case resolveMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			// Update the note resolution status
			if m.selectedNote.ID == msg.discussionID {
				m.selectedNote.Resolved = msg.resolved
			}
			// Update in detail view notes cache
			if m.showingDetail && m.detailView != nil {
				for i := range m.detailView.notes {
					if m.detailView.notes[i].ID == msg.discussionID {
						m.detailView.notes[i].Resolved = msg.resolved
						break
					}
				}
			}
		}
		return m, nil

	case draftMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			// Toggle draft status in all caches - we need to refresh to get the actual new status
			// For now, just trigger a refresh
			return m, fetchAllScopesMRs(m.providers)
		}
		return m, nil

	case mrChangesMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.mrChanges = msg.changes

			if msg.showDiff {
				// Auto-show full diff after fetching
				if len(m.mrs) > 0 {
					selectedMR := m.mrs[m.cursor]
					return m, renderFullDiff(msg.changes, selectedMR)
				}
			} else {
				// Show files view
				if len(m.mrs) > 0 {
					selectedMR := m.mrs[m.cursor]
					m.filesView = &FilesView{
						mr:      selectedMR,
						changes: msg.changes,
						width:   m.width,
						height:  m.height,
					}
					m.showingFiles = true
				}
			}
		}
		return m, nil

	case diffMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.diffContent = msg.content
			m.diffTitle = msg.title
			m.showingDiff = true
		}
		return m, nil

	case replyPostedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		// Successfully posted - close thread view and refresh detail
		m.showingNoteThread = false
		m.composingReply = false
		m.replyInput = ""
		m.selectedNote = provider.MRNote{}

		if m.showingDetail && len(m.mrs) > 0 {
			selectedMR := m.mrs[m.cursor]
			var p provider.Provider
			if len(m.providers) > 0 {
				p = m.providers[0]
			}
			if p != nil {
				return m, fetchDetailData(p, selectedMR)
			}
		}
		return m, nil
	}

	return m, nil
}

func (m Model) View() string {
	if m.err != nil {
		return errorStyle.Render(fmt.Sprintf("Error: %v\n\nPress q to quit", m.err))
	}

	// Show diff if active
	if m.showingDiff {
		return m.renderDiff()
	}

	// Show job log if active
	if m.showingJobLog {
		return m.renderJobLog()
	}

	// Show note thread if active
	if m.showingNoteThread {
		return m.renderNoteThread()
	}

	// Show files view if active
	if m.showingFiles && m.filesView != nil {
		return m.filesView.Render()
	}

	// Show detail view if active
	if m.showingDetail && m.detailView != nil {
		return m.detailView.Render()
	}

	// Header
	header := headerStyle.Render(fmt.Sprintf("glamr - %s MRs", m.scopeName()))

	// Tabs
	tabs := m.renderTabs()

	// Status bar
	status := m.renderStatus()

	// MR list
	listContent := m.renderMRList()

	// Calculate available height for the list (account for header, tabs, status, help, spacing)
	listHeight := m.height - 12
	if listHeight < 5 {
		listHeight = 5
	}

	// Wrap list in a fixed-height container
	listStyle := lipgloss.NewStyle().
		MaxHeight(listHeight).
		Height(listHeight)
	list := listStyle.Render(listContent)

	// Help
	help := helpStyle.Render("↑/↓: navigate | enter: details | a: approve/unapprove | D: toggle draft | o: open in browser | 1/2/3: switch scope | r: refresh | q: quit")

	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		tabs,
		"",
		list,
		"",
		status,
		help,
	)
}

func (m Model) scopeName() string {
	switch m.scope {
	case provider.ScopeAuthored:
		return "Authored"
	case provider.ScopeAssigned:
		return "Assigned"
	case provider.ScopeReviewing:
		return "Reviewing"
	default:
		return "Unknown"
	}
}

func (m Model) renderTabs() string {
	// Check if there are truly unreviewed MRs in reviewing tab
	// Mark as needing attention if:
	// 1. UNREVIEWED or UNAPPROVED (not commented/approved/requested changes)
	// 2. OR has unresolved replies where user is not the last commenter
	hasUnreviewed := false
	for _, mr := range m.mrsReviewing {
		// Skip draft MRs
		if mr.Status == provider.StatusDraft {
			continue
		}
		// Flag if truly unreviewed (not commented/approved/requested changes)
		if mr.UserReviewState == provider.ReviewStateUnreviewed || mr.UserReviewState == provider.ReviewStateUnapproved {
			hasUnreviewed = true
			break
		}
		// Also flag if there are unresolved replies where user commented but is not the last commenter
		if mr.HasUnresolvedReplies {
			hasUnreviewed = true
			break
		}
	}

	tabs := []string{
		m.renderTab("1. Authored", m.scope == provider.ScopeAuthored, false),
		m.renderTab("2. Assigned", m.scope == provider.ScopeAssigned, false),
		m.renderTab("3. Reviewing", m.scope == provider.ScopeReviewing, hasUnreviewed),
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
}

func (m Model) renderTab(label string, active bool, needsAttention bool) string {
	if active {
		return activeTabStyle.Render(label)
	}
	if needsAttention {
		// Red color for tabs that need attention
		redTabStyle := lipgloss.NewStyle().
			Padding(0, 2).
			Foreground(lipgloss.Color("#ff0000")).
			Bold(true)
		return redTabStyle.Render(label)
	}
	return tabStyle.Render(label)
}

func (m Model) renderStatus() string {
	if m.loading {
		return statusStyle.Render("⟳ Loading...")
	}

	timeSince := time.Since(m.cache.LastRefresh()).Round(time.Second)

	// Count repos
	repoCount := len(m.providers)
	if len(m.providers) == 1 {
		// Check if it's the "all repos" provider
		if len(m.mrs) > 0 {
			// Count unique repos from MRs
			repoMap := make(map[string]bool)
			for _, mr := range m.mrs {
				repoMap[mr.RepoName] = true
			}
			repoCount = len(repoMap)
		}
	}

	status := fmt.Sprintf("Last refresh: %v ago | %d MRs from %d repos", timeSince, len(m.mrs), repoCount)
	if m.refreshing {
		status += " | ⟳ Refreshing..."
	}
	return statusStyle.Render(status)
}

func (m Model) renderMRList() string {
	if m.loading {
		return emptyStyle.Render("Loading merge requests...")
	}

	if len(m.mrs) == 0 {
		return emptyStyle.Render("No merge requests found")
	}

	// Calculate visible area - each MR takes ~6 lines (status, title, repo, branch, pipeline, approvals, spacing)
	linesPerMR := 6
	listHeight := m.height - 12 // Same as View() calculation

	// Account for scroll indicators (2 lines each + 2 blank lines)
	indicatorLines := 4
	availableLines := listHeight - indicatorLines
	visibleMRs := availableLines / linesPerMR
	if visibleMRs < 1 {
		visibleMRs = 1
	}

	// Calculate which MRs to show based on scroll (scroll is updated in Update())
	startIdx := m.scroll
	endIdx := m.scroll + visibleMRs
	if endIdx > len(m.mrs) {
		endIdx = len(m.mrs)
	}

	var items []string

	// Show scroll indicator if there are items above
	if startIdx > 0 {
		indicator := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(fmt.Sprintf("▲ %d more above ▲", startIdx))
		items = append(items, indicator, "")
	}

	for i := startIdx; i < endIdx; i++ {
		items = append(items, m.renderMR(m.mrs[i], i == m.cursor))
	}

	// Show scroll indicator if there are items below
	if endIdx < len(m.mrs) {
		remaining := len(m.mrs) - endIdx
		indicator := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(fmt.Sprintf("▼ %d more below ▼", remaining))
		items = append(items, "", indicator)
	}

	return lipgloss.JoinVertical(lipgloss.Left, items...)
}

func (m Model) renderMR(mr provider.MergeRequest, selected bool) string {
	// Pipeline status icon
	pipelineIcon := "⚫"
	pipelineColor := lipgloss.Color("#888888")

	if mr.Pipeline != nil {
		switch mr.Pipeline.Status {
		case provider.PipelineFailed:
			pipelineIcon = "❌"
			pipelineColor = lipgloss.Color("#ff0000")
		case provider.PipelineRunning:
			pipelineIcon = "🔵"
			pipelineColor = lipgloss.Color("#0066ff")
		case provider.PipelinePending:
			pipelineIcon = "⏸️"
			pipelineColor = lipgloss.Color("#ffaa00")
		case provider.PipelineSuccess:
			pipelineIcon = "✅"
			pipelineColor = lipgloss.Color("#00ff00")
		default:
			pipelineIcon = "⚠️"
			pipelineColor = lipgloss.Color("#ffaa00")
		}
	}

	// Status icon
	statusIcon := "🟢"
	if mr.Status == provider.StatusDraft {
		statusIcon = "📝"
	} else if mr.Status == provider.StatusMerged {
		statusIcon = "✅"
	} else if mr.Status == provider.StatusClosed {
		statusIcon = "❌"
	}

	// Conflict and discussion indicators
	var indicators []string
	if mr.HasConflicts {
		indicators = append(indicators, "⚠️ conflicts")
	}
	if mr.HasUnresolvedDiscussions {
		indicators = append(indicators, "💬 unresolved")
	}

	indicatorStr := ""
	if len(indicators) > 0 {
		indicatorStr = " | " + lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00")).Render(strings.Join(indicators, ", "))
	}

	// Format the MR
	title := fmt.Sprintf("%s !%d %s", statusIcon, mr.IID, mr.Title)
	repo := fmt.Sprintf("   [%s]", mr.RepoName)
	branch := fmt.Sprintf("   %s → %s", mr.SourceBranch, mr.TargetBranch)

	pipelineStyle := lipgloss.NewStyle().Foreground(pipelineColor)
	var pipeline string
	if mr.Pipeline == nil {
		pipeline = fmt.Sprintf("   %s no pipeline%s", pipelineStyle.Render(pipelineIcon), indicatorStr)
	} else {
		pipeline = fmt.Sprintf("   %s %s%s", pipelineStyle.Render(pipelineIcon), mr.Pipeline.Status, indicatorStr)
	}

	// Approval/Review status with color coding
	var approvalLine string
	approvalStyle := lipgloss.NewStyle()

	// For reviewing scope, use review state for coloring
	if m.scope == provider.ScopeReviewing && mr.UserReviewState != "" {
		// If there are unresolved replies where user is not the last commenter, show red
		if mr.HasUnresolvedReplies {
			approvalStyle = approvalStyle.Foreground(lipgloss.Color("#ff0000"))
			if mr.RequiredApprovals > 0 {
				approvalLine = approvalStyle.Render(fmt.Sprintf("   🔴 New replies to review | %d/%d", mr.ApprovalCount, mr.RequiredApprovals))
			} else {
				approvalLine = approvalStyle.Render(fmt.Sprintf("   🔴 New replies to review | %d/0", mr.ApprovalCount))
			}
		} else {
			switch mr.UserReviewState {
			case provider.ReviewStateApproved:
				// Approved - green
				approvalStyle = approvalStyle.Foreground(lipgloss.Color("#00ff00"))
				if mr.RequiredApprovals > 0 {
					approvalLine = approvalStyle.Render(fmt.Sprintf("   ✓ You approved | %d/%d", mr.ApprovalCount, mr.RequiredApprovals))
				} else {
					approvalLine = approvalStyle.Render(fmt.Sprintf("   ✓ You approved | %d/0", mr.ApprovalCount))
				}
			case provider.ReviewStateReviewed:
				// Commented (no approval/changes) - yellow
				approvalStyle = approvalStyle.Foreground(lipgloss.Color("#ffaa00"))
				if mr.RequiredApprovals > 0 {
					approvalLine = approvalStyle.Render(fmt.Sprintf("   💬 You commented | %d/%d", mr.ApprovalCount, mr.RequiredApprovals))
				} else {
					approvalLine = approvalStyle.Render(fmt.Sprintf("   💬 You commented | %d/0", mr.ApprovalCount))
				}
			case provider.ReviewStateRequestedChanges:
				// Requested changes - red
				approvalStyle = approvalStyle.Foreground(lipgloss.Color("#ff0000"))
				if mr.RequiredApprovals > 0 {
					approvalLine = approvalStyle.Render(fmt.Sprintf("   ✗ You requested changes | %d/%d", mr.ApprovalCount, mr.RequiredApprovals))
				} else {
					approvalLine = approvalStyle.Render(fmt.Sprintf("   ✗ You requested changes | %d/0", mr.ApprovalCount))
				}
			default:
				// Unreviewed/Unapproved - default/white (needs review)
				if mr.RequiredApprovals > 0 {
					approvalStyle = approvalStyle.Foreground(lipgloss.Color("#888888"))
					approvalLine = approvalStyle.Render(fmt.Sprintf("   ○ %d/%d approvals", mr.ApprovalCount, mr.RequiredApprovals))
				} else {
					approvalStyle = approvalStyle.Foreground(lipgloss.Color("#888888"))
					approvalLine = approvalStyle.Render(fmt.Sprintf("   ○ %d/0 approvals (optional)", mr.ApprovalCount))
				}
			}
		}
	} else {
		// For authored/assigned scopes, use existing approval logic
		if mr.RequiredApprovals > 0 {
			if mr.UserApproved && mr.Approved {
				// You approved AND fully approved - green checkmark
				approvalStyle = approvalStyle.Foreground(lipgloss.Color("#00ff00"))
				approvalLine = approvalStyle.Render(fmt.Sprintf("   ✓ You approved | %d/%d", mr.ApprovalCount, mr.RequiredApprovals))
			} else if mr.UserApproved {
				// You approved but waiting for others - green circle
				approvalStyle = approvalStyle.Foreground(lipgloss.Color("#00ff00"))
				approvalLine = approvalStyle.Render(fmt.Sprintf("   ○ You approved | %d/%d", mr.ApprovalCount, mr.RequiredApprovals))
			} else if mr.Approved {
				// Fully approved but not by you - bright checkmark
				approvalLine = fmt.Sprintf("   ✓ %d/%d approved", mr.ApprovalCount, mr.RequiredApprovals)
			} else {
				// Waiting for approvals (you haven't approved) - grayscale/dim
				approvalStyle = approvalStyle.Foreground(lipgloss.Color("#888888"))
				approvalLine = approvalStyle.Render(fmt.Sprintf("   ○ %d/%d approvals", mr.ApprovalCount, mr.RequiredApprovals))
			}
		} else {
			// No approvals required (optional reviews)
			if mr.UserApproved {
				// You approved even though optional - green checkmark
				approvalStyle = approvalStyle.Foreground(lipgloss.Color("#00ff00"))
				approvalLine = approvalStyle.Render(fmt.Sprintf("   ✓ You approved | %d/0", mr.ApprovalCount))
			} else if mr.ApprovalCount > 0 {
				// Others approved but not you
				approvalLine = fmt.Sprintf("   ✓ %d/0 approved", mr.ApprovalCount)
			} else {
				// No approvals yet (optional)
				approvalStyle = approvalStyle.Foreground(lipgloss.Color("#888888"))
				approvalLine = approvalStyle.Render("   ○ 0/0 approvals (optional)")
			}
		}
	}

	repoStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Italic(true)
	repoLine := repoStyle.Render(repo)

	var lines []string
	lines = append(lines, title, repoLine, branch, pipeline)
	if approvalLine != "" {
		lines = append(lines, approvalLine)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	if selected {
		return selectedItemStyle.Render(content)
	}
	return itemStyle.Render(content)
}

// Commands
func fetchMRs(p provider.Provider, scope provider.Scope) tea.Cmd {
	return func() tea.Msg {
		mrs, err := p.ListMRs(scope)
		return mrsFetchedMsg{mrs: mrs, err: err}
	}
}

func fetchAllMRs(providers []provider.Provider, scope provider.Scope) tea.Cmd {
	return func() tea.Msg {
		var allMRs []provider.MergeRequest
		var lastErr error

		// Fetch from all providers sequentially
		// Note: The provider itself handles fetching from multiple repos if configured that way
		for _, p := range providers {
			mrs, err := p.ListMRs(scope)
			if err != nil {
				lastErr = err
				continue
			}
			allMRs = append(allMRs, mrs...)
		}

		// Return all MRs at once - no partial updates
		return mrsFetchedMsg{mrs: allMRs, err: lastErr}
	}
}

// fetchAllScopesMRs fetches MRs from all three scopes in parallel queries
func fetchAllScopesMRs(providers []provider.Provider) tea.Cmd {
	return func() tea.Msg {
		// Use channels to fetch all three scopes in parallel
		type scopeResult struct {
			scope provider.Scope
			mrs   []provider.MergeRequest
			err   error
		}

		results := make(chan scopeResult, 3)

		// Fetch each scope in parallel
		for _, scope := range []provider.Scope{provider.ScopeAuthored, provider.ScopeAssigned, provider.ScopeReviewing} {
			go func(s provider.Scope) {
				var allMRs []provider.MergeRequest
				var lastErr error

				for _, p := range providers {
					mrs, err := p.ListMRs(s)
					if err != nil {
						lastErr = err
						continue
					}
					allMRs = append(allMRs, mrs...)
				}

				results <- scopeResult{scope: s, mrs: allMRs, err: lastErr}
			}(scope)
		}

		// Collect all results
		var authored, assigned, reviewing []provider.MergeRequest
		var lastErr error

		for i := 0; i < 3; i++ {
			result := <-results
			if result.err != nil {
				lastErr = result.err
			}
			switch result.scope {
			case provider.ScopeAuthored:
				authored = result.mrs
			case provider.ScopeAssigned:
				assigned = result.mrs
			case provider.ScopeReviewing:
				reviewing = result.mrs
			}
		}

		return allScopesMRsMsg{
			authored:  authored,
			assigned:  assigned,
			reviewing: reviewing,
			err:       lastErr,
		}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func fetchDetailData(p provider.Provider, mr provider.MergeRequest) tea.Cmd {
	return func() tea.Msg {
		var jobs []provider.PipelineJob
		var notes []provider.MRNote
		var err error

		// Fetch pipeline jobs if pipeline exists
		if mr.Pipeline != nil && mr.Pipeline.ID != "" {
			jobs, err = p.GetPipelineJobs(mr.Pipeline.ID, mr.RepoName)
			if err != nil {
				return detailDataMsg{err: err}
			}
		}

		// Fetch notes/comments
		notes, err = p.GetMRNotes(mr.IID, mr.RepoName)
		if err != nil {
			return detailDataMsg{jobs: jobs, err: err}
		}

		return detailDataMsg{jobs: jobs, notes: notes}
	}
}

func fetchJobLog(p provider.Provider, job provider.PipelineJob, repo string) tea.Cmd {
	return func() tea.Msg {
		log, err := p.GetJobTrace(job.ID, repo)
		if err != nil {
			return jobLogMsg{err: err}
		}
		return jobLogMsg{
			log:   log,
			title: fmt.Sprintf("Job Log: %s", job.Name),
		}
	}
}

func retryJob(p provider.Provider, job provider.PipelineJob, repo string) tea.Cmd {
	return func() tea.Msg {
		err := p.RetryJob(job.ID, repo)
		if err != nil {
			return jobActionMsg{success: false, err: err}
		}
		return jobActionMsg{success: true}
	}
}

func playJob(p provider.Provider, job provider.PipelineJob, repo string) tea.Cmd {
	return func() tea.Msg {
		err := p.PlayJob(job.ID, repo)
		if err != nil {
			return jobActionMsg{success: false, err: err}
		}
		return jobActionMsg{success: true}
	}
}

func cancelJob(p provider.Provider, job provider.PipelineJob, repo string) tea.Cmd {
	return func() tea.Msg {
		err := p.CancelJob(job.ID, repo)
		if err != nil {
			return jobActionMsg{success: false, err: err}
		}
		return jobActionMsg{success: true}
	}
}

func postReply(p provider.Provider, mr provider.MergeRequest, noteID string, body string) tea.Cmd {
	return func() tea.Msg {
		var err error
		if noteID == "" {
			// Create new note
			err = p.CreateNote(mr.IID, body, mr.RepoName)
		} else {
			// Reply to existing note
			err = p.ReplyToNote(mr.IID, noteID, body, mr.RepoName)
		}
		if err != nil {
			return replyPostedMsg{success: false, err: err}
		}
		return replyPostedMsg{success: true}
	}
}

func approveMR(p provider.Provider, mr provider.MergeRequest) tea.Cmd {
	return func() tea.Msg {
		err := p.ApproveMR(mr.ID)
		if err != nil {
			return approvalMsg{mrID: mr.ID, err: err}
		}
		return approvalMsg{mrID: mr.ID, approved: true}
	}
}

func unapproveMR(p provider.Provider, mr provider.MergeRequest) tea.Cmd {
	return func() tea.Msg {
		err := p.UnapproveMR(mr.ID)
		if err != nil {
			return approvalMsg{mrID: mr.ID, err: err}
		}
		return approvalMsg{mrID: mr.ID, approved: false}
	}
}

func resolveDiscussion(p provider.Provider, mr provider.MergeRequest, discussionID string) tea.Cmd {
	return func() tea.Msg {
		err := p.ResolveDiscussion(mr.IID, discussionID, mr.RepoName)
		if err != nil {
			return resolveMsg{discussionID: discussionID, err: err}
		}
		return resolveMsg{discussionID: discussionID, resolved: true}
	}
}

func unresolveDiscussion(p provider.Provider, mr provider.MergeRequest, discussionID string) tea.Cmd {
	return func() tea.Msg {
		err := p.UnresolveDiscussion(mr.IID, discussionID, mr.RepoName)
		if err != nil {
			return resolveMsg{discussionID: discussionID, err: err}
		}
		return resolveMsg{discussionID: discussionID, resolved: false}
	}
}

func toggleDraftStatus(p provider.Provider, mr provider.MergeRequest) tea.Cmd {
	return func() tea.Msg {
		err := p.ToggleDraftStatus(mr.ID)
		if err != nil {
			return draftMsg{mrID: mr.ID, err: err}
		}
		return draftMsg{mrID: mr.ID}
	}
}

func openInBrowser(url string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd

		// Detect OS and use appropriate command
		switch runtime.GOOS {
		case "linux":
			cmd = exec.Command("xdg-open", url)
		case "darwin":
			cmd = exec.Command("open", url)
		case "windows":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default:
			return nil
		}

		if err := cmd.Start(); err != nil {
			// Silently fail - don't interrupt the TUI
			return nil
		}
		return nil
	}
}

func fetchMRChanges(p provider.Provider, mr provider.MergeRequest) tea.Cmd {
	return func() tea.Msg {
		changes, err := p.GetMRChanges(mr.IID, mr.RepoName)
		if err != nil {
			return mrChangesMsg{err: err}
		}
		return mrChangesMsg{changes: changes, showDiff: false}
	}
}

func fetchMRChangesForDiff(p provider.Provider, mr provider.MergeRequest) tea.Cmd {
	return func() tea.Msg {
		changes, err := p.GetMRChanges(mr.IID, mr.RepoName)
		if err != nil {
			return mrChangesMsg{err: err}
		}
		return mrChangesMsg{changes: changes, showDiff: true}
	}
}

func renderFullDiff(changes *provider.MRChanges, mr provider.MergeRequest) tea.Cmd {
	return func() tea.Msg {
		if changes == nil || len(changes.Changes) == 0 {
			return diffMsg{err: fmt.Errorf("no changes to display")}
		}

		// Create temp file with all diffs concatenated
		tmpfile, err := os.CreateTemp("", "glamr-full-diff-*.patch")
		if err != nil {
			return diffMsg{err: err}
		}
		defer os.Remove(tmpfile.Name())

		// Write all diffs to the temp file
		for _, change := range changes.Changes {
			tmpfile.Write([]byte(change.Diff))
			tmpfile.Write([]byte("\n"))
		}
		tmpfile.Close()

		// Run delta with --paging never to capture the formatted output
		cmd := exec.Command("sh", "-c", fmt.Sprintf("~/.local/bin/delta --paging never < %s", tmpfile.Name()))
		output, err := cmd.Output()
		if err != nil {
			return diffMsg{err: fmt.Errorf("failed to format diff: %w", err)}
		}

		return diffMsg{
			content: string(output),
			title:   fmt.Sprintf("Full Diff: !%d %s", mr.IID, mr.Title),
		}
	}
}

func renderFileDiff(file provider.FileChange) tea.Cmd {
	return func() tea.Msg {
		// Create temp file with the diff
		tmpfile, err := os.CreateTemp("", "glamr-diff-*.patch")
		if err != nil {
			return diffMsg{err: err}
		}
		defer os.Remove(tmpfile.Name())

		if _, err := tmpfile.Write([]byte(file.Diff)); err != nil {
			return diffMsg{err: err}
		}
		tmpfile.Close()

		// Run delta with --paging never to capture the formatted output
		cmd := exec.Command("sh", "-c", fmt.Sprintf("~/.local/bin/delta --paging never < %s", tmpfile.Name()))
		output, err := cmd.Output()
		if err != nil {
			return diffMsg{err: fmt.Errorf("failed to format diff: %w", err)}
		}

		return diffMsg{
			content: string(output),
			title:   fmt.Sprintf("File Diff: %s", file.NewPath),
		}
	}
}

func urlEncode(s string) string {
	s = strings.ReplaceAll(s, "/", "%2F")
	s = strings.ReplaceAll(s, " ", "%20")
	return s
}

func (m Model) renderDiff() string {
	header := headerStyle.Render(m.diffTitle)

	// Split content into lines for scrolling
	lines := strings.Split(m.diffContent, "\n")

	// Calculate visible area
	viewportHeight := m.height - 8 // Account for header, borders, help
	if viewportHeight < 5 {
		viewportHeight = 5
	}

	// Ensure scroll doesn't go past the end
	maxScroll := len(lines) - viewportHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.diffScroll > maxScroll {
		m.diffScroll = maxScroll
	}

	// Calculate which lines to show
	startIdx := m.diffScroll
	endIdx := m.diffScroll + viewportHeight
	if endIdx > len(lines) {
		endIdx = len(lines)
	}

	var visibleLines []string

	// Show scroll indicator if there are lines above
	if startIdx > 0 {
		indicator := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(fmt.Sprintf("▲ (%d lines above)", startIdx))
		visibleLines = append(visibleLines, indicator)
	}

	// Add visible content lines
	visibleLines = append(visibleLines, lines[startIdx:endIdx]...)

	// Show scroll indicator if there are lines below
	if endIdx < len(lines) {
		remaining := len(lines) - endIdx
		indicator := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(fmt.Sprintf("▼ (%d lines below)", remaining))
		visibleLines = append(visibleLines, indicator)
	}

	diffStyle := lipgloss.NewStyle().
		Width(m.width - 4).
		Height(viewportHeight + 2). // +2 for scroll indicators
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#666666")).
		Padding(1)

	diffContent := diffStyle.Render(strings.Join(visibleLines, "\n"))

	help := helpStyle.Render("↑/↓ j/k: scroll | shift+↑/↓: scroll 20 lines | home/end: top/bottom | esc: back | q: quit")

	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		"",
		diffContent,
		"",
		help,
	)
}

func (m Model) renderJobLog() string {
	header := headerStyle.Render(m.jobLogTitle)

	logStyle := lipgloss.NewStyle().
		Width(m.width - 4).
		Height(m.height - 8).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#666666")).
		Padding(1)

	logContent := logStyle.Render(m.jobLog)

	help := helpStyle.Render("esc: back | q: quit")

	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		"",
		logContent,
		"",
		help,
	)
}

func (m Model) renderNoteThread() string {
	selectedMR := m.mrs[m.cursor]

	var header string
	if m.selectedNote.ID == "" {
		// New note
		header = headerStyle.Render(fmt.Sprintf("New Note - !%d %s", selectedMR.IID, selectedMR.Title))
	} else {
		// Existing note thread
		header = headerStyle.Render(fmt.Sprintf("Discussion - !%d %s", selectedMR.IID, selectedMR.Title))
	}

	viewHeight := m.height - 12
	threadStyle := lipgloss.NewStyle().
		Width(m.width - 4).
		Height(viewHeight).
		MaxHeight(viewHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#666666")).
		Padding(1)

	var allLines []string

	if m.selectedNote.ID != "" {
		// Show the original note
		authorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00ffff")).Bold(true)
		timeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))

		var icon string
		if m.selectedNote.Resolvable {
			icon = "💬"
		} else {
			icon = "💭"
		}

		noteHeader := fmt.Sprintf("%s %s %s",
			icon,
			authorStyle.Render(fmt.Sprintf("@%s", m.selectedNote.Author)),
			timeStyle.Render(m.selectedNote.CreatedAt),
		)

		allLines = append(allLines, noteHeader, "")

		// Split note body by lines to enable scrolling
		bodyLines := strings.Split(m.selectedNote.Body, "\n")
		allLines = append(allLines, bodyLines...)
		allLines = append(allLines, "", lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render("─────────────────────"), "")
	}

	// Reply input section
	if m.composingReply {
		replyLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00")).Bold(true).Render("Your reply:")
		allLines = append(allLines, replyLabel, "", m.replyInput+"│")
	} else {
		promptText := "Press 'r' to reply"
		allLines = append(allLines, lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Render(promptText))
	}

	// Calculate visible window
	visibleLines := viewHeight - 4 // Account for padding and border
	totalLines := len(allLines)

	// Adjust scroll bounds
	maxScroll := totalLines - visibleLines
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.noteThreadScroll > maxScroll {
		m.noteThreadScroll = maxScroll
	}
	if m.noteThreadScroll < 0 {
		m.noteThreadScroll = 0
	}

	// Get visible slice
	startIdx := m.noteThreadScroll
	endIdx := startIdx + visibleLines
	if endIdx > totalLines {
		endIdx = totalLines
	}

	var visibleContent []string

	// Add scroll indicator if scrolled
	if startIdx > 0 {
		visibleContent = append(visibleContent, lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(fmt.Sprintf("▲ (line %d/%d)", startIdx+1, totalLines)))
	}

	visibleContent = append(visibleContent, allLines[startIdx:endIdx]...)

	// Add more below indicator
	if endIdx < totalLines {
		visibleContent = append(visibleContent, lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(fmt.Sprintf("▼ (%d more lines)", totalLines-endIdx)))
	}

	threadContent := threadStyle.Render(lipgloss.JoinVertical(lipgloss.Left, visibleContent...))

	var help string
	if m.composingReply {
		help = helpStyle.Render("Type your reply | enter: post | esc: cancel")
	} else {
		help = helpStyle.Render("↑/↓ j/k: scroll | shift+↑/↓: scroll 20 lines | home/end: top/bottom | r: reply | x: resolve/unresolve | esc: back | q: quit")
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		"",
		threadContent,
		"",
		help,
	)
}

// Styles
var (
	headerStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#00ffff")).
		Padding(1, 2)

	tabStyle = lipgloss.NewStyle().
		Padding(0, 2).
		Foreground(lipgloss.Color("#888888"))

	activeTabStyle = lipgloss.NewStyle().
		Padding(0, 2).
		Bold(true).
		Foreground(lipgloss.Color("#00ffff")).
		Background(lipgloss.Color("#333333"))

	itemStyle = lipgloss.NewStyle().
		Padding(0, 2).
		MarginBottom(1)

	selectedItemStyle = lipgloss.NewStyle().
		Padding(0, 2).
		MarginBottom(1).
		Background(lipgloss.Color("#333333"))

	statusStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#888888")).
		Padding(1, 2)

	helpStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#666666")).
		Padding(0, 2)

	emptyStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#888888")).
		Padding(2, 2)

	errorStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#ff0000")).
		Padding(2, 2)
)
