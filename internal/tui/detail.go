package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"glamr/internal/provider"
)

// Panel represents which panel is active
type Panel int

const (
	JobsPanel Panel = iota
	NotesPanel
)

// DetailView renders the split view for a selected MR
type DetailView struct {
	mr           provider.MergeRequest
	jobs         []provider.PipelineJob
	notes        []provider.MRNote
	width        int
	height       int
	activePanel  Panel // Which panel is selected
	jobsCursor   int   // Selected job index
	notesCursor  int   // Selected note index
	jobsScroll   int   // Scroll offset for jobs panel
	notesScroll  int   // Scroll offset for notes panel
}

func NewDetailView(mr provider.MergeRequest, width, height int) DetailView {
	return DetailView{
		mr:     mr,
		width:  width,
		height: height,
	}
}

func (d DetailView) Render() string {
	// Header with MR info - title and metadata
	statusIcon := "🟢"
	switch d.mr.Status {
	case "draft":
		statusIcon = "📝"
	case "merged":
		statusIcon = "✅"
	case "closed":
		statusIcon = "❌"
	}

	titleLine := fmt.Sprintf("%s !%d %s", statusIcon, d.mr.IID, d.mr.Title)

	// Metadata line: branches and author
	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	metaLine := fmt.Sprintf("%s → %s | @%s", d.mr.SourceBranch, d.mr.TargetBranch, d.mr.Author)

	// URL hint
	urlStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Italic(true)
	urlHint := urlStyle.Render(fmt.Sprintf("Press 'o' to open in browser: %s", d.mr.WebURL))

	header := headerStyle.Render(titleLine + "\n" + metaStyle.Render(metaLine) + "\n" + urlHint)

	// Calculate available height for panels
	// Header takes ~7 lines (title + padding), help takes ~2 lines, spacing takes ~2 lines
	panelHeight := d.height - 11
	if panelHeight < 10 {
		panelHeight = 10
	}

	// Split view: left = CI jobs (1/3), right = comments (2/3)
	leftWidth := d.width / 3
	rightWidth := d.width - leftWidth

	leftPanel := d.renderJobs(leftWidth, panelHeight, d.activePanel == JobsPanel)
	rightPanel := d.renderNotes(rightWidth, panelHeight, d.activePanel == NotesPanel)

	content := lipgloss.JoinHorizontal(
		lipgloss.Top,
		leftPanel,
		rightPanel,
	)

	// Build dynamic help based on selected job status and active panel
	helpText := "a: approve/unapprove | D: toggle draft | f: files | d: full diff | o: open in browser | ↑/↓ j/k: navigate | ←/→ h/l: switch panel | r: refresh"
	if d.activePanel == JobsPanel && len(d.jobs) > 0 {
		helpText += " | enter: view log"
		selectedJob := d.jobs[d.jobsCursor]
		if selectedJob.Status == "failed" {
			helpText += " | t: retry"
		} else if selectedJob.Status == "manual" {
			helpText += " | p: play"
		} else if selectedJob.Status == "running" || selectedJob.Status == "pending" {
			helpText += " | c: cancel"
		}
	} else if d.activePanel == NotesPanel {
		helpText += " | enter: view thread | n: new note"
	}
	helpText += " | esc: back | q: quit"
	help := helpStyle.Render(helpText)

	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		"",
		content,
		"",
		help,
	)
}

func (d DetailView) renderJobs(width, height int, isActive bool) string {
	borderColor := lipgloss.Color("#666666")
	if isActive {
		borderColor = lipgloss.Color("#00ffff") // Cyan border when active
	}

	jobsStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		MaxHeight(height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1)

	title := lipgloss.NewStyle().Bold(true).Render("CI Pipeline Jobs")

	if d.mr.Pipeline == nil {
		return jobsStyle.Render(lipgloss.JoinVertical(lipgloss.Left, title, "", "No pipeline"))
	}

	if len(d.jobs) == 0 {
		pipelineInfo := fmt.Sprintf("Pipeline ID: %s", d.mr.Pipeline.ID)
		return jobsStyle.Render(lipgloss.JoinVertical(lipgloss.Left, title, "", "Loading jobs...", pipelineInfo))
	}

	// Calculate visible area - title takes 2 lines, padding takes some space
	visibleLines := height - 4 // Account for padding and border

	// Calculate scroll offset to keep cursor visible
	if d.jobsCursor < d.jobsScroll {
		// Cursor scrolled up past visible area
		d.jobsScroll = d.jobsCursor
	} else if d.jobsCursor >= d.jobsScroll+visibleLines {
		// Cursor scrolled down past visible area
		d.jobsScroll = d.jobsCursor - visibleLines + 1
	}

	// Calculate which jobs to show
	startIdx := d.jobsScroll
	endIdx := d.jobsScroll + visibleLines
	if endIdx > len(d.jobs) {
		endIdx = len(d.jobs)
	}

	var items []string
	items = append(items, title, "")

	// Show scroll indicator if there are items above
	if startIdx > 0 {
		items = append(items, lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render("▲ (more above)"))
	}

	// Calculate max width for job names (accounting for padding and border)
	maxJobWidth := width - 6 // 2 for border, 2 for padding, 2 for icon and space

	for i := startIdx; i < endIdx; i++ {
		job := d.jobs[i]
		var icon string
		var color lipgloss.Color

		// Convert status to lowercase for case-insensitive matching
		status := strings.ToLower(job.Status)

		switch status {
		case "success":
			icon = "✅"
			color = lipgloss.Color("#00ff00")
		case "failed":
			icon = "❌"
			color = lipgloss.Color("#ff0000")
		case "running":
			icon = "🔵"
			color = lipgloss.Color("#0066ff")
		case "pending", "created":
			icon = "⏸️"
			color = lipgloss.Color("#ffaa00")
		case "manual":
			icon = "⚙️"
			color = lipgloss.Color("#ffaa00")
		case "skipped":
			icon = "⏭️"
			color = lipgloss.Color("#888888")
		default:
			icon = "⚫"
			color = lipgloss.Color("#888888")
		}

		// Truncate long job names
		jobName := job.Name
		if len(jobName) > maxJobWidth {
			jobName = jobName[:maxJobWidth-3] + "..."
		}

		// Build line with consistent styling
		jobStyle := lipgloss.NewStyle().Foreground(color)
		line := fmt.Sprintf("%s %s", jobStyle.Render(icon), jobName)

		// Apply highlight for selected item
		if isActive && i == d.jobsCursor {
			line = lipgloss.NewStyle().
				Background(lipgloss.Color("#333333")).
				Width(width - 4). // Full width minus padding
				Render(line)
		}

		items = append(items, line)
	}

	// Show scroll indicator if there are items below
	if endIdx < len(d.jobs) {
		items = append(items, lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render("▼ (more below)"))
	}

	return jobsStyle.Render(lipgloss.JoinVertical(lipgloss.Left, items...))
}

func (d DetailView) renderNotes(width, height int, isActive bool) string {
	borderColor := lipgloss.Color("#666666")
	if isActive {
		borderColor = lipgloss.Color("#00ffff") // Cyan border when active
	}

	notesStyle := lipgloss.NewStyle().
		Width(width).
		Height(height).
		MaxHeight(height).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1)

	title := lipgloss.NewStyle().Bold(true).Render("Comments & Discussions")

	// Filter unresolved notes first to see if we have any
	var filteredNotes []provider.MRNote
	for _, note := range d.notes {
		if note.Resolvable && note.Resolved {
			continue
		}
		filteredNotes = append(filteredNotes, note)
	}

	if len(filteredNotes) == 0 {
		// Show appropriate message: either no notes at all, or all are resolved
		message := "No unresolved discussions"
		if len(d.notes) == 0 {
			message = "No notes or discussions"
		}
		return notesStyle.Render(lipgloss.JoinVertical(lipgloss.Left, title, "", message))
	}

	// filteredNotes already populated above

	// Calculate visible area - estimate 6 lines per note (header + body + spacing)
	linesPerNote := 6
	visibleNotes := (height - 4) / linesPerNote
	if visibleNotes < 1 {
		visibleNotes = 1
	}

	// Calculate scroll offset to keep cursor visible
	if d.notesCursor < d.notesScroll {
		d.notesScroll = d.notesCursor
	} else if d.notesCursor >= d.notesScroll+visibleNotes {
		d.notesScroll = d.notesCursor - visibleNotes + 1
	}

	// Calculate which notes to show
	startIdx := d.notesScroll
	endIdx := d.notesScroll + visibleNotes
	if endIdx > len(filteredNotes) {
		endIdx = len(filteredNotes)
	}

	var items []string
	items = append(items, title, "")

	// Show scroll indicator if there are items above
	if startIdx > 0 {
		items = append(items, lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(fmt.Sprintf("▲ (%d more above)", startIdx)))
	}

	// Render each note in visible range
	for i := startIdx; i < endIdx; i++ {
		note := filteredNotes[i]
		var icon string
		if note.Resolvable {
			icon = "💬" // Unresolved discussion
		} else {
			icon = "💭" // Regular comment
		}

		authorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00ffff")).Bold(true)
		timeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))

		// Parse timestamp to relative time
		relTime := note.CreatedAt
		// TODO: Could format as "2h ago" etc

		header := fmt.Sprintf("%s %s %s",
			icon,
			authorStyle.Render(fmt.Sprintf("@%s", note.Author)),
			timeStyle.Render(relTime),
		)

		// Show only first 3 lines of body - press Enter to see full thread
		bodyWidth := width - 4 // Account for padding and border
		body := note.Body
		hasMore := false

		// Split by newlines first, then wrap long lines
		wrappedLines := []string{}
		lines := strings.Split(body, "\n")

		for _, line := range lines {
			if len(wrappedLines) >= 3 {
				hasMore = true
				break
			}

			// If line fits, add it
			if len(line) <= bodyWidth {
				wrappedLines = append(wrappedLines, line)
				continue
			}

			// Line is too long, wrap it
			remaining := line
			for len(remaining) > 0 && len(wrappedLines) < 3 {
				if len(remaining) <= bodyWidth {
					wrappedLines = append(wrappedLines, remaining)
					break
				}

				// Find last space before width
				cutPoint := bodyWidth
				for cutPoint > 0 && remaining[cutPoint] != ' ' {
					cutPoint--
				}
				if cutPoint == 0 {
					cutPoint = bodyWidth
				}

				wrappedLines = append(wrappedLines, remaining[:cutPoint])
				remaining = strings.TrimLeft(remaining[cutPoint:], " ")
			}

			if len(remaining) > 0 {
				hasMore = true
			}
		}

		// Add indicator if there's more content
		if hasMore || len(lines) > len(wrappedLines) {
			wrappedLines = append(wrappedLines, "... (press Enter to view full)")
		}

		// Build note content - compact format
		noteLines := []string{header}
		noteLines = append(noteLines, wrappedLines...)
		noteContent := lipgloss.JoinVertical(lipgloss.Left, noteLines...)

		// Highlight selected note if this panel is active
		if isActive && i == d.notesCursor {
			noteContent = selectedItemStyle.Render(noteContent)
		}

		items = append(items, noteContent, "")
	}

	// Show scroll indicator if there are items below
	if endIdx < len(filteredNotes) {
		remaining := len(filteredNotes) - endIdx
		items = append(items, lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(fmt.Sprintf("▼ (%d more below)", remaining)))
	}

	return notesStyle.Render(lipgloss.JoinVertical(lipgloss.Left, items...))
}
