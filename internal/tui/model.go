package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
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
	refreshErr        error // Last background refresh error (shown in status bar, keeps existing data)
	cache             *cache.Cache
	refreshInterval   time.Duration
	lastRefreshStart  time.Time // When the last refresh was started (successful or not)
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
	showingNoteThread bool
	selectedNote      provider.MRNote
	noteThreadScroll  int // Scroll offset for note thread view
	replyInput        string
	composingReply    bool
}

// Messages
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
		providers:        providers,
		scope:            provider.ScopeAuthored,
		cache:            cache.NewCache(5 * time.Minute), // 5 minute cache
		refreshInterval:  5 * time.Minute,                 // Check every 5 minutes
		loading:          true,
		lastRefreshStart: time.Now(), // Init starts the first fetch
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
		if m.detailView != nil {
			m.detailView.width, m.detailView.height = msg.Width, msg.Height
		}
		if m.filesView != nil {
			m.filesView.width, m.filesView.height = msg.Width, msg.Height
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case allScopesMRsMsg:
		wasInitialLoad := m.loading
		m.loading = false
		m.refreshing = false // Clear refreshing flag
		if msg.err != nil {
			if wasInitialLoad {
				m.err = msg.err
			} else {
				// Keep showing existing data; retry on the next interval
				m.refreshErr = msg.err
			}
		} else {
			m.err = nil
			m.refreshErr = nil
			// Cache all three scopes
			m.mrsAuthored = msg.authored
			m.mrsAssigned = msg.assigned
			m.mrsReviewing = sortReviewingMRs(msg.reviewing)

			// Set current view based on active scope
			m.mrs = m.scopeMRs(m.scope)

			// Reset cursor if out of bounds
			if m.cursor >= len(m.mrs) {
				m.cursor = 0
				m.scroll = 0
			}

			// Update cache with current scope's data
			m.cache.Set(m.mrs)
		}
		return m, nil

	case tickMsg:
		// Auto-refresh in background once per interval. Based on when the last
		// refresh started (not succeeded), so a failing refresh isn't retried every tick.
		if time.Since(m.lastRefreshStart) > m.refreshInterval && !m.loading && !m.refreshing {
			m.refreshing = true
			m.lastRefreshStart = time.Now()
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
			// Job logs reuse the scrollable diff viewer, opened at the end where failures usually are
			m.diffContent = msg.log
			m.diffTitle = msg.title
			m.showingDiff = true
			m.diffScroll = m.diffMaxScroll()
		}
		return m, nil

	case jobActionMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			// Refresh detail data after action
			if p := m.activeProvider(); m.showingDetail && m.detailView != nil && p != nil {
				return m, fetchDetailData(p, m.detailView.mr)
			}
		}
		return m, nil

	case approvalMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			// Update every cached copy of the MR. m.mrs aliases one of the scope slices,
			// so applyApproval is idempotent to avoid counting the same approval twice.
			for _, list := range [][]provider.MergeRequest{m.mrs, m.mrsAuthored, m.mrsAssigned, m.mrsReviewing} {
				for i := range list {
					if list[i].ID == msg.mrID {
						applyApproval(&list[i], msg.approved)
					}
				}
			}
			if m.detailView != nil && m.detailView.mr.ID == msg.mrID {
				applyApproval(&m.detailView.mr, msg.approved)
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
					// All notes in a thread share the discussion ID
					if m.detailView.notes[i].ID == msg.discussionID {
						m.detailView.notes[i].Resolved = msg.resolved
					}
				}
			}
		}
		return m, nil

	case draftMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			// Refresh to pick up the new title/draft state
			m.refreshing = true
			m.lastRefreshStart = time.Now()
			return m, fetchAllScopesMRs(m.providers)
		}
		return m, nil

	case mrChangesMsg:
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.mrChanges = msg.changes

			if mr, ok := m.selectedMR(); ok {
				if msg.showDiff {
					// Auto-show full diff after fetching
					return m, renderFullDiff(msg.changes, mr, m.diffWidth())
				}
				// Show files view
				m.filesView = &FilesView{
					mr:      mr,
					changes: msg.changes,
					width:   m.width,
					height:  m.height,
				}
				m.showingFiles = true
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
			m.diffScroll = 0
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

		if p := m.activeProvider(); m.showingDetail && m.detailView != nil && p != nil {
			return m, fetchDetailData(p, m.detailView.mr)
		}
		return m, nil
	}

	return m, nil
}

func (m Model) View() string {
	if m.err != nil {
		return errorStyle.Render(fmt.Sprintf("Error: %v\n\nPress any key to continue, q to quit", m.err))
	}

	// Show diff if active
	if m.showingDiff {
		return m.renderDiff()
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
	} else if m.refreshErr != nil {
		// First line only - glab errors include the full command output
		errLine, _, _ := strings.Cut(m.refreshErr.Error(), "\n")
		status += " | ⚠ Refresh failed: " + errLine
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

	visibleMRs := m.visibleMRCount()

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

// toggleApproval approves the MR, or revokes the current user's approval if already given.
func toggleApproval(p provider.Provider, mr provider.MergeRequest) tea.Cmd {
	if mr.UserApproved {
		return unapproveMR(p, mr)
	}
	return approveMR(p, mr)
}

func approveMR(p provider.Provider, mr provider.MergeRequest) tea.Cmd {
	return func() tea.Msg {
		if err := p.ApproveMR(mr.ID); err != nil {
			return approvalMsg{mrID: mr.ID, err: err}
		}
		return approvalMsg{mrID: mr.ID, approved: true}
	}
}

func unapproveMR(p provider.Provider, mr provider.MergeRequest) tea.Cmd {
	return func() tea.Msg {
		if err := p.UnapproveMR(mr.ID); err != nil {
			return approvalMsg{mrID: mr.ID, err: err}
		}
		return approvalMsg{mrID: mr.ID, approved: false}
	}
}

// applyApproval optimistically updates an MR after the user (un)approved it.
// It's a no-op if the MR already reflects the change.
func applyApproval(mr *provider.MergeRequest, approved bool) {
	if mr.UserApproved == approved {
		return
	}
	mr.UserApproved = approved
	if approved {
		mr.ApprovalCount++
	} else if mr.ApprovalCount > 0 {
		mr.ApprovalCount--
	}
	mr.Approved = mr.ApprovalCount >= mr.RequiredApprovals
	if mr.UserReviewState != "" {
		if approved {
			mr.UserReviewState = provider.ReviewStateApproved
		} else {
			mr.UserReviewState = provider.ReviewStateUnapproved
		}
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

func fetchMRChanges(p provider.Provider, mr provider.MergeRequest, showDiff bool) tea.Cmd {
	return func() tea.Msg {
		changes, err := p.GetMRChanges(mr.IID, mr.RepoName)
		if err != nil {
			return mrChangesMsg{err: err}
		}
		return mrChangesMsg{changes: changes, showDiff: showDiff}
	}
}

func renderFullDiff(changes *provider.MRChanges, mr provider.MergeRequest, width int) tea.Cmd {
	return func() tea.Msg {
		if changes == nil || len(changes.Changes) == 0 {
			return diffMsg{err: fmt.Errorf("no changes to display")}
		}

		var diff strings.Builder
		for _, change := range changes.Changes {
			diff.WriteString(fileDiffHeader(change))
			diff.WriteString(change.Diff)
			if !strings.HasSuffix(change.Diff, "\n") {
				diff.WriteString("\n")
			}
		}

		content, err := formatDiff(diff.String(), width)
		if err != nil {
			return diffMsg{err: err}
		}
		return diffMsg{
			content: content,
			title:   fmt.Sprintf("Full Diff: !%d %s", mr.IID, mr.Title),
		}
	}
}

func renderFileDiff(file provider.FileChange, width int) tea.Cmd {
	return func() tea.Msg {
		content, err := formatDiff(fileDiffHeader(file)+file.Diff, width)
		if err != nil {
			return diffMsg{err: err}
		}
		return diffMsg{
			content: content,
			title:   fmt.Sprintf("File Diff: %s", file.NewPath),
		}
	}
}

// fileDiffHeader returns git-style file headers. GitLab's API diffs start at the first
// hunk, so without these delta (and readers) can't tell which file a hunk belongs to.
func fileDiffHeader(file provider.FileChange) string {
	oldPath, newPath := "a/"+file.OldPath, "b/"+file.NewPath
	if file.NewFile {
		oldPath = "/dev/null"
	}
	if file.DeletedFile {
		newPath = "/dev/null"
	}
	return fmt.Sprintf("diff --git a/%s b/%s\n--- %s\n+++ %s\n", file.OldPath, file.NewPath, oldPath, newPath)
}

// findDelta locates the delta binary on PATH, falling back to ~/.local/bin/delta.
func findDelta() (string, bool) {
	if path, err := exec.LookPath("delta"); err == nil {
		return path, true
	}
	if home, err := os.UserHomeDir(); err == nil {
		path := filepath.Join(home, ".local", "bin", "delta")
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, true
		}
	}
	return "", false
}

// formatDiff syntax-highlights a unified diff with delta. Without delta, the plain diff is shown.
func formatDiff(diff string, width int) (string, error) {
	deltaPath, ok := findDelta()
	if !ok {
		return diff, nil
	}

	args := []string{"--paging", "never"}
	if width > 0 {
		args = append(args, "--width", strconv.Itoa(width))
	}
	cmd := exec.Command(deltaPath, args...)
	cmd.Stdin = strings.NewReader(diff)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to format diff with delta: %w", err)
	}
	return string(output), nil
}

// sortReviewingMRs sorts MRs in the reviewing tab by priority:
// 1. Unreviewed (not commented/approved/requested changes)
// 2. Commented (reviewed but not approved/requested changes)
// 3. Requested changes
// 4. Approved
// 5. Drafts (lowest priority)
func sortReviewingMRs(mrs []provider.MergeRequest) []provider.MergeRequest {
	// Create a copy to avoid modifying the original slice
	sorted := make([]provider.MergeRequest, len(mrs))
	copy(sorted, mrs)

	// Define priority based on review state and draft status
	getPriority := func(mr provider.MergeRequest) int {
		// Drafts always go last
		if mr.Status == provider.StatusDraft {
			return 5
		}

		// Check for unresolved replies first - these are highest priority
		if mr.HasUnresolvedReplies {
			return 0
		}

		switch mr.UserReviewState {
		case provider.ReviewStateUnreviewed, provider.ReviewStateUnapproved:
			return 1 // Unreviewed
		case provider.ReviewStateReviewed:
			return 2 // Commented
		case provider.ReviewStateRequestedChanges:
			return 3 // Requested changes
		case provider.ReviewStateApproved:
			return 4 // Approved
		default:
			return 1 // Treat unknown states as unreviewed
		}
	}

	// Stable sort keeps the API's order (most recently updated first) within a priority
	sort.SliceStable(sorted, func(i, j int) bool {
		return getPriority(sorted[i]) < getPriority(sorted[j])
	})

	return sorted
}

// selectedMR returns the MR being acted on: the one open in the detail view (which stays
// correct even if a background refresh reorders the list), else the highlighted list item.
func (m Model) selectedMR() (provider.MergeRequest, bool) {
	if m.showingDetail && m.detailView != nil {
		return m.detailView.mr, true
	}
	if m.cursor >= 0 && m.cursor < len(m.mrs) {
		return m.mrs[m.cursor], true
	}
	return provider.MergeRequest{}, false
}

// activeProvider returns the provider used for actions. Actions carry the repo in their
// arguments (MR ID "repo!iid" or an explicit repo), so any configured provider can perform them.
func (m Model) activeProvider() provider.Provider {
	if len(m.providers) > 0 {
		return m.providers[0]
	}
	return nil
}

// scopeMRs returns the cached MRs for a tab.
func (m Model) scopeMRs(scope provider.Scope) []provider.MergeRequest {
	switch scope {
	case provider.ScopeAssigned:
		return m.mrsAssigned
	case provider.ScopeReviewing:
		return m.mrsReviewing
	default:
		return m.mrsAuthored
	}
}

// visibleMRCount is how many MRs fit in the main list.
func (m Model) visibleMRCount() int {
	const linesPerMR = 6     // title, repo, branch, pipeline, approvals, spacing
	const indicatorLines = 4 // scroll indicators above/below
	visible := (m.height - 12 - indicatorLines) / linesPerMR
	if visible < 1 {
		visible = 1
	}
	return visible
}

// diffWidth is the width passed to delta so its output fits inside the viewer border.
func (m Model) diffWidth() int {
	if m.width <= 8 {
		return 0
	}
	return m.width - 8
}

func (m Model) diffViewportHeight() int {
	h := m.height - 8 // Account for header, borders, help
	if h < 5 {
		h = 5
	}
	return h
}

func (m Model) diffMaxScroll() int {
	lines := strings.Count(m.diffContent, "\n") + 1
	return max(lines-m.diffViewportHeight(), 0)
}

func (m Model) renderDiff() string {
	header := headerStyle.Render(m.diffTitle)

	lines := strings.Split(m.diffContent, "\n")
	viewportHeight := m.diffViewportHeight()

	startIdx := min(max(m.diffScroll, 0), m.diffMaxScroll())
	endIdx := min(startIdx+viewportHeight, len(lines))

	var visibleLines []string

	// Show scroll indicator if there are lines above
	if startIdx > 0 {
		indicator := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(fmt.Sprintf("▲ (%d lines above)", startIdx))
		visibleLines = append(visibleLines, indicator)
	}

	visibleLines = append(visibleLines, lines[startIdx:endIdx]...)

	// Show scroll indicator if there are lines below
	if endIdx < len(lines) {
		remaining := len(lines) - endIdx
		indicator := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(fmt.Sprintf("▼ (%d lines below)", remaining))
		visibleLines = append(visibleLines, indicator)
	}

	diffStyle := lipgloss.NewStyle().
		Width(max(m.width-4, 0)).
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

func (m Model) noteThreadViewHeight() int {
	return max(m.height-12, 5)
}

// noteThreadVisibleLines is how many content lines fit in the thread box (minus padding and border).
func (m Model) noteThreadVisibleLines() int {
	return max(m.noteThreadViewHeight()-4, 1)
}

// noteThreadLines returns the full (unscrolled) content of the note thread view.
func (m Model) noteThreadLines() []string {
	var allLines []string

	if m.selectedNote.ID != "" {
		authorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00ffff")).Bold(true)
		timeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))

		icon := "💭"
		if m.selectedNote.Resolvable {
			icon = "💬"
		}

		noteHeader := fmt.Sprintf("%s %s %s",
			icon,
			authorStyle.Render(fmt.Sprintf("@%s", m.selectedNote.Author)),
			timeStyle.Render(m.selectedNote.CreatedAt),
		)
		if m.selectedNote.Resolved {
			noteHeader += timeStyle.Render(" (resolved)")
		}

		allLines = append(allLines, noteHeader, "")
		allLines = append(allLines, strings.Split(m.selectedNote.Body, "\n")...)
		allLines = append(allLines, "", lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render("─────────────────────"), "")
	}

	if m.composingReply {
		replyLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00")).Bold(true).Render("Your reply:")
		allLines = append(allLines, replyLabel, "", m.replyInput+"│")
	} else {
		allLines = append(allLines, lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Render("Press 'r' to reply"))
	}
	return allLines
}

func (m Model) noteThreadMaxScroll() int {
	return max(len(m.noteThreadLines())-m.noteThreadVisibleLines(), 0)
}

func (m Model) renderNoteThread() string {
	mr, _ := m.selectedMR()

	var header string
	if m.selectedNote.ID == "" {
		header = headerStyle.Render(fmt.Sprintf("New Note - !%d %s", mr.IID, mr.Title))
	} else {
		header = headerStyle.Render(fmt.Sprintf("Discussion - !%d %s", mr.IID, mr.Title))
	}

	viewHeight := m.noteThreadViewHeight()
	threadStyle := lipgloss.NewStyle().
		Width(max(m.width-4, 0)).
		Height(viewHeight).
		MaxHeight(viewHeight).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#666666")).
		Padding(1)

	allLines := m.noteThreadLines()
	totalLines := len(allLines)
	visibleLines := m.noteThreadVisibleLines()

	// While composing, keep the input line in view
	scroll := m.noteThreadScroll
	if m.composingReply {
		scroll = endOfContent
	}
	startIdx := min(max(scroll, 0), m.noteThreadMaxScroll())
	endIdx := min(startIdx+visibleLines, totalLines)

	var visibleContent []string
	if startIdx > 0 {
		visibleContent = append(visibleContent, lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(fmt.Sprintf("▲ (line %d/%d)", startIdx+1, totalLines)))
	}
	visibleContent = append(visibleContent, allLines[startIdx:endIdx]...)
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
