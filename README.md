# glamr

**GitLab Merge Request dashboard** - A fast, terminal-based UI for managing your GitLab merge requests.

![glamr screenshot](https://img.shields.io/badge/TUI-Terminal%20UI-blue)
![Go version](https://img.shields.io/badge/Go-1.21%2B-00ADD8)

## Features

### 📋 MR Management
- **Three views**: Authored, Assigned, Reviewing tabs with instant switching
- **Real-time status**: Pipeline status, approvals, conflicts, discussions
- **Quick actions**: Approve/unapprove, toggle draft, open in browser
- **Smart indicators**: Red tab for unreviewed MRs, color-coded statuses

### 🔍 Code Review
- **Files view**: Browse changed files with status icons (added/modified/deleted/renamed)
- **Diff viewer**: Syntax-highlighted diffs using [delta](https://github.com/dandavison/delta)
  - View full MR diff or individual file diffs
  - Rendered in-TUI with full color support
  - Scrollable with keyboard navigation

### 💬 Discussions
- **View threads**: See unresolved discussions inline
- **Reply**: Add comments and replies
- **Resolve**: Mark discussions as resolved/unresolved
- **Compact display**: First 3 lines with line wrapping

### 🚀 CI/CD
- **Pipeline jobs**: Status, stage, and job details
- **Job logs**: View full output in-TUI
- **Job actions**: Retry failed jobs, play manual jobs, cancel running jobs

### ⚡ Performance
- **GraphQL API**: 15-20x faster than REST (3 queries vs ~100)
- **Parallel loading**: All scopes loaded at startup
- **Cached data**: Instant tab switching with 5-minute refresh
- **Background refresh**: Smooth updates without blocking UI

## Installation

### Prerequisites
- **Go 1.21+** (for building)
- **[glab](https://gitlab.com/gitlab-org/cli)** - GitLab CLI (must be authenticated)
- **[delta](https://github.com/dandavison/delta)** - For diff viewing (optional but recommended)

### Install delta
```bash
# Download and install delta
curl -L https://github.com/dandavison/delta/releases/latest/download/delta-0.19.2-x86_64-unknown-linux-gnu.tar.gz -o delta.tar.gz
tar -xzf delta.tar.gz
mkdir -p ~/.local/bin
cp delta-*/delta ~/.local/bin/
rm -rf delta.tar.gz delta-*
```

### Authenticate with GitLab
```bash
# Authenticate to your GitLab instance
glab auth login --hostname your.gitlab.host
```

### Build glamr
```bash
git clone https://github.com/Lindholmer/glamr.git
cd glamr
go build -o glamr cmd/glamr/main.go

# Optional: Install to PATH
sudo mv glamr /usr/local/bin/
```

## Usage

### Basic Usage
```bash
# Launch with all accessible repos
glamr --host your.gitlab.host

# Query specific repositories
glamr --host your.gitlab.host --repo org/project1 --repo org/project2

# Use REST API instead of GraphQL
glamr --host your.gitlab.host --use-graphql=false
```

### Keyboard Shortcuts

#### Main List View
- `↑/↓` or `j/k` - Navigate MRs
- `Enter` - View MR details
- `a` - Approve/unapprove MR
- `D` or `Shift+d` - Toggle draft status
- `o` - Open MR in browser
- `1/2/3` - Switch between Authored/Assigned/Reviewing tabs
- `r` - Refresh all data
- `q` - Quit

#### Detail View
- `←/→` or `h/l` - Switch between Jobs/Notes panels
- `↑/↓` or `j/k` - Navigate within panel
- `Enter` - View job log (Jobs panel) or note thread (Notes panel)
- `a` - Approve/unapprove MR
- `D` - Toggle draft status
- `f` - Show files view
- `d` - Show full diff
- `o` - Open in browser
- `n` - Create new note
- `r` - Refresh
- `Esc` - Back to main list

##### Job Actions (when job selected)
- `t` - Retry failed job
- `p` - Play manual job
- `c` - Cancel running/pending job

#### Files View
- `↑/↓` or `j/k` - Navigate files
- `d` - View diff for selected file
- `Esc` - Back to detail view

#### Diff View
- `↑/↓` or `j/k` - Scroll one line
- `Shift+↑/↓` - Scroll 20 lines
- `Home` - Jump to top
- `End` - Jump to bottom
- `Esc` - Back to previous view

#### Note Thread View
- `↑/↓` or `j/k` - Scroll one line
- `Shift+↑/↓` - Scroll 20 lines
- `Home/End` - Jump to top/bottom
- `r` - Reply to thread
- `x` - Resolve/unresolve discussion
- `Esc` - Back to detail view

## Architecture

### Components
- **TUI Layer** (`internal/tui/`)
  - Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea)
  - Styled with [Lipgloss](https://github.com/charmbracelet/lipgloss)
  - Main list, detail view, files view, diff viewer
  
- **Provider Layer** (`internal/provider/`)
  - Abstraction for VCS providers
  - GitLab GraphQL implementation (default)
  - GitLab REST implementation (fallback)
  
- **GraphQL Layer** (`internal/graphql/`)
  - Query templates for all scopes
  - Type definitions matching GitLab schema
  - Optimized to stay under complexity limits
  
- **Cache Layer** (`internal/cache/`)
  - In-memory cache with TTL
  - 5-minute default refresh interval

### Data Flow
1. **Startup**: Parallel GraphQL queries for all 3 scopes (Authored, Assigned, Reviewing)
2. **Cache**: All MRs, pipeline jobs, and notes stored in memory
3. **Navigation**: Instant tab switching using cached data
4. **Refresh**: Background updates every 5 minutes (non-blocking)
5. **Details**: MR data (jobs, notes) pre-cached at startup

## Configuration

Currently all configuration is via command-line flags. Environment variables and config files may be added in future releases.

### Refresh Interval
The refresh interval is set to 5 minutes by default. To change it, modify `internal/tui/model.go`:
```go
cache:           cache.NewCache(5 * time.Minute),
refreshInterval: 5 * time.Minute,
```

### Delta Configuration
glamr uses delta for syntax highlighting. To customize delta's appearance, create `~/.gitconfig`:
```ini
[delta]
    syntax-theme = Monokai Extended
    line-numbers = true
    side-by-side = false
```

See [delta documentation](https://dandavison.github.io/delta/) for more options.

## Troubleshooting

### "401 Unauthorized" error
- Ensure you're authenticated: `glab auth status`
- Check hostname: `glab config get host`
- Re-authenticate if needed: `glab auth login --hostname your.gitlab.host`

### GraphQL complexity errors
- Switch to REST API: `--use-graphql=false`
- Or reduce number of repos being queried

### Diff viewer not working
- Ensure delta is installed: `delta --version`
- Check it's in PATH: `which delta` or verify at `~/.local/bin/delta`

### Terminal scrolling issues with pageup/pagedown
- Use `Shift+↑/↓` for fast scrolling instead
- Or use `Home/End` keys to jump to top/bottom

## Contributing

Contributions are welcome! Please feel free to submit issues or pull requests.

### Development Setup
```bash
git clone https://github.com/Lindholmer/glamr.git
cd glamr
go mod download
go build -o glamr cmd/glamr/main.go
```

### Project Structure
```
glamr/
├── cmd/glamr/           # Main entry point
├── internal/
│   ├── cache/          # Caching layer
│   ├── graphql/        # GraphQL queries and types
│   ├── provider/       # VCS provider abstraction
│   └── tui/            # Terminal UI components
├── go.mod
└── README.md
```

## Acknowledgments

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - Terminal UI framework
- [Lipgloss](https://github.com/charmbracelet/lipgloss) - Terminal styling
- [glab](https://gitlab.com/gitlab-org/cli) - GitLab CLI
- [delta](https://github.com/dandavison/delta) - Syntax-aware diff viewer

## Roadmap

Potential future features:
- Configuration file support
- Multiple GitLab instance support simultaneously
- Merge MR from TUI
- Custom color schemes
- Relative timestamps ("2h ago")
- Notification system for new MRs

---

**Note**: This is an unofficial tool and is not affiliated with GitLab Inc.
