package tui

import (
	"fmt"
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
	cursor            int
	scroll            int // Scroll offset for MR list
	loading           bool
	err               error
	cache             *cache.Cache
	refreshInterval   time.Duration
	width             int
	height            int
	detailView        *DetailView
	showingDetail     bool
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

func NewModel(providers ...provider.Provider) Model {
	return Model{
		providers:       providers,
		scope:           provider.ScopeAuthored,
		cache:           cache.NewCache(60 * time.Second), // 1 minute cache
		refreshInterval: 60 * time.Second,                  // Check every 60 seconds
		loading:         true,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		fetchAllMRs(m.providers, m.scope),
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
			case "r":
				// Start composing reply
				m.composingReply = true
				m.replyInput = ""
				return m, nil
			}
		}

		// Handle escape - exit note thread, job log, or detail view
		if msg.String() == "esc" {
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
			if m.showingDetail {
				m.showingDetail = false
				m.detailView = nil
				m.err = nil // Clear any detail view errors
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
			// Manual refresh
			m.loading = true
			return m, fetchAllMRs(m.providers, m.scope)

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil

		case "down", "j":
			if m.cursor < len(m.mrs)-1 {
				m.cursor++
			}
			return m, nil

		case "1":
			m.scope = provider.ScopeAuthored
			m.loading = true
			return m, fetchAllMRs(m.providers, m.scope)

		case "2":
			m.scope = provider.ScopeAssigned
			m.loading = true
			return m, fetchAllMRs(m.providers, m.scope)

		case "3":
			m.scope = provider.ScopeReviewing
			m.loading = true
			return m, fetchAllMRs(m.providers, m.scope)

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
				// No need to fetch - data is already cached in the MR
			}
			return m, nil

		case "o":
			// Open selected MR in browser (future feature)
			return m, nil
		}

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
		// Auto-refresh if cache is stale
		if m.cache.IsStale() && !m.loading {
			m.loading = true
			return m, tea.Batch(
				fetchAllMRs(m.providers, m.scope),
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

	// Show job log if active
	if m.showingJobLog {
		return m.renderJobLog()
	}

	// Show note thread if active
	if m.showingNoteThread {
		return m.renderNoteThread()
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
	list := m.renderMRList()

	// Help
	help := helpStyle.Render("↑/↓: navigate | 1/2/3: switch scope | r: refresh | q: quit")

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
	tabs := []string{
		m.renderTab("1. Authored", m.scope == provider.ScopeAuthored),
		m.renderTab("2. Assigned", m.scope == provider.ScopeAssigned),
		m.renderTab("3. Reviewing", m.scope == provider.ScopeReviewing),
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
}

func (m Model) renderTab(label string, active bool) string {
	if active {
		return activeTabStyle.Render(label)
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

	return statusStyle.Render(fmt.Sprintf("Last refresh: %v ago | %d MRs from %d repos", timeSince, len(m.mrs), repoCount))
}

func (m Model) renderMRList() string {
	if m.loading {
		return emptyStyle.Render("Loading merge requests...")
	}

	if len(m.mrs) == 0 {
		return emptyStyle.Render("No merge requests found")
	}

	// Calculate visible area - each MR takes ~4 lines (status, title, meta, spacing)
	linesPerMR := 4
	visibleMRs := (m.height - 15) / linesPerMR // Account for header, tabs, status, help
	if visibleMRs < 1 {
		visibleMRs = 1
	}

	// Calculate scroll offset to keep cursor visible
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	} else if m.cursor >= m.scroll+visibleMRs {
		m.scroll = m.cursor - visibleMRs + 1
	}

	// Calculate which MRs to show
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

	// Approval status with color coding
	var approvalLine string
	if mr.RequiredApprovals > 0 {
		approvalStyle := lipgloss.NewStyle()

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
		promptText := "Press 'r' to reply, '↑↓ j/k' to scroll, 'esc' to go back"
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
		help = helpStyle.Render("↑/↓ j/k: scroll | r: reply | esc: back | q: quit")
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
