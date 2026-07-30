package tui

import (
	"fmt"

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

	// Split view: left = CI jobs (1/3), right = comments (2/3)
	leftWidth := d.width / 3
	rightWidth := d.width - leftWidth

	leftPanel := d.renderJobs(leftWidth, d.height-4, d.activePanel == JobsPanel)
	rightPanel := d.renderNotes(rightWidth, d.height-4, d.activePanel == NotesPanel)

	content := lipgloss.JoinHorizontal(
		lipgloss.Top,
		leftPanel,
		rightPanel,
	)

	// Build dynamic help based on selected job status and active panel
	helpText := "o: open in browser | ↑/↓ j/k: navigate | ←/→ h/l: switch panel | r: refresh"
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

	var items []string
	items = append(items, title, "")

	// Calculate max width for job names (accounting for padding and border)
	maxJobWidth := width - 6 // 2 for border, 2 for padding, 2 for icon and space

	for i, job := range d.jobs {
		var icon string
		var color lipgloss.Color

		switch job.Status {
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
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1)

	title := lipgloss.NewStyle().Bold(true).Render("Comments & Discussions")

	if len(d.notes) == 0 {
		return notesStyle.Render(lipgloss.JoinVertical(lipgloss.Left, title, "", "Loading notes..."))
	}

	// Filter unresolved notes
	var filteredNotes []provider.MRNote
	for _, note := range d.notes {
		if note.Resolvable && note.Resolved {
			continue
		}
		filteredNotes = append(filteredNotes, note)
	}

	var items []string
	items = append(items, title, "")

	// Render each note
	for i, note := range filteredNotes {
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

		// Wrap body text to fit width
		bodyWidth := width - 4 // Account for padding and border
		body := note.Body

		// Simple word wrapping - split at width
		wrappedLines := []string{}
		for len(body) > 0 {
			if len(body) <= bodyWidth {
				wrappedLines = append(wrappedLines, body)
				break
			}
			// Find last space before width
			cutPoint := bodyWidth
			for cutPoint > 0 && body[cutPoint] != ' ' && body[cutPoint] != '\n' {
				cutPoint--
			}
			if cutPoint == 0 {
				cutPoint = bodyWidth
			}
			wrappedLines = append(wrappedLines, body[:cutPoint])
			body = body[cutPoint:]
			if len(body) > 0 && body[0] == ' ' {
				body = body[1:]
			}
			// Limit to 5 lines per comment
			if len(wrappedLines) >= 5 {
				wrappedLines = append(wrappedLines, "...")
				break
			}
		}

		// Build note content
		noteLines := []string{header}
		noteLines = append(noteLines, wrappedLines...)
		noteContent := lipgloss.JoinVertical(lipgloss.Left, noteLines...)

		// Highlight selected note if this panel is active
		if isActive && i == d.notesCursor {
			noteContent = selectedItemStyle.Render(noteContent)
		}

		items = append(items, noteContent, "")
	}

	return notesStyle.Render(lipgloss.JoinVertical(lipgloss.Left, items...))
}
