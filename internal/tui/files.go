package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"glamr/internal/provider"
)

// FilesView renders the list of changed files in an MR
type FilesView struct {
	mr      provider.MergeRequest
	changes *provider.MRChanges
	width   int
	height  int
	cursor  int
	scroll  int
}

func NewFilesView(mr provider.MergeRequest, changes *provider.MRChanges, width, height int) FilesView {
	return FilesView{
		mr:      mr,
		changes: changes,
		width:   width,
		height:  height,
	}
}

func (f FilesView) Render() string {
	// Header with MR info
	statusIcon := "🟢"
	switch f.mr.Status {
	case "draft":
		statusIcon = "📝"
	case "merged":
		statusIcon = "✅"
	case "closed":
		statusIcon = "❌"
	}

	titleLine := fmt.Sprintf("%s !%d %s", statusIcon, f.mr.IID, f.mr.Title)
	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	metaLine := fmt.Sprintf("Changed files: %d", len(f.changes.Changes))

	header := headerStyle.Render(titleLine + "\n" + metaStyle.Render(metaLine))

	// Calculate available height for file list
	listHeight := f.height - 10
	if listHeight < 5 {
		listHeight = 5
	}

	// Render file list with scrolling
	fileList := f.renderFileList(listHeight)

	help := helpStyle.Render("↑/↓ j/k: navigate | d: view diff | esc: back | q: quit")

	return lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		"",
		fileList,
		"",
		help,
	)
}

func (f FilesView) renderFileList(height int) string {
	if f.changes == nil || len(f.changes.Changes) == 0 {
		return lipgloss.NewStyle().
			Width(f.width).
			Height(height).
			Border(lipgloss.RoundedBorder()).
			Padding(1).
			Render("No changes found")
	}

	// Calculate scroll offset to keep cursor visible
	if f.cursor < f.scroll {
		f.scroll = f.cursor
	} else if f.cursor >= f.scroll+height {
		f.scroll = f.cursor - height + 1
	}

	// Calculate which files to show
	startIdx := f.scroll
	endIdx := f.scroll + height
	if endIdx > len(f.changes.Changes) {
		endIdx = len(f.changes.Changes)
	}

	var items []string

	// Show scroll indicator if there are items above
	if startIdx > 0 {
		items = append(items, lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(fmt.Sprintf("▲ (%d more above)", startIdx)))
	}

	for i := startIdx; i < endIdx; i++ {
		file := f.changes.Changes[i]

		var icon string
		var color lipgloss.Color

		if file.NewFile {
			icon = "+"
			color = lipgloss.Color("#00ff00")
		} else if file.DeletedFile {
			icon = "-"
			color = lipgloss.Color("#ff0000")
		} else if file.RenamedFile {
			icon = "→"
			color = lipgloss.Color("#ffaa00")
		} else {
			icon = "M"
			color = lipgloss.Color("#0066ff")
		}

		path := file.NewPath
		if file.RenamedFile && file.OldPath != file.NewPath {
			path = fmt.Sprintf("%s → %s", file.OldPath, file.NewPath)
		}

		// Truncate long paths
		maxWidth := f.width - 10
		if len(path) > maxWidth {
			path = "..." + path[len(path)-maxWidth+3:]
		}

		fileStyle := lipgloss.NewStyle().Foreground(color).Bold(true)
		line := fmt.Sprintf("%s %s", fileStyle.Render(icon), path)

		// Highlight selected item
		if i == f.cursor {
			line = lipgloss.NewStyle().
				Background(lipgloss.Color("#333333")).
				Width(f.width - 4).
				Render(line)
		}

		items = append(items, line)
	}

	// Show scroll indicator if there are items below
	if endIdx < len(f.changes.Changes) {
		remaining := len(f.changes.Changes) - endIdx
		items = append(items, lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render(fmt.Sprintf("▼ (%d more below)", remaining)))
	}

	return lipgloss.NewStyle().
		Width(f.width).
		Height(height).
		Border(lipgloss.RoundedBorder()).
		Padding(1).
		Render(lipgloss.JoinVertical(lipgloss.Left, items...))
}
