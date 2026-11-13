# glamr

**GitLab Merge Request dashboard** - A fast, terminal-based UI for managing your GitLab merge requests.

![glamr screenshot](https://img.shields.io/badge/TUI-Terminal%20UI-blue)
![Go version](https://img.shields.io/badge/Go-1.26%2B-00ADD8)

## Features

### 📋 MR Management
- **Three tabs**: Authored, Assigned and Reviewing, with instant switching (all tabs are preloaded)
- **Status at a glance**: pipeline status, approvals, conflicts and unresolved discussions
- **Quick actions**: approve/revoke approval, toggle draft, open in browser
- **Review triage**: the Reviewing tab is sorted by what needs your attention and turns red when something does (see [Reviewing tab](#reviewing-tab))

### 🔍 Code Review
- **Files view**: browse changed files with status icons (added `+`, deleted `-`, renamed `→`, modified `M`)
- **Diff viewer**: full MR diff or a single file, syntax-highlighted with [delta](https://github.com/dandavison/delta) when available (plain diff otherwise), scrollable in the TUI

### 💬 Discussions
- **Threads**: unresolved discussions and comments are listed in the detail view (first 3 lines, wrapped)
- **Reply / new note**: write replies and new comments without leaving the terminal (Unicode and paste supported)
- **Resolve**: mark threads resolved/unresolved

### 🚀 CI/CD
- **Pipeline jobs**: name and status of every job in the head pipeline
- **Job logs**: scrollable in-TUI log viewer that opens at the end of the log
- **Job actions**: retry failed jobs, play manual jobs, cancel running/pending jobs

### ⚡ Performance
- **GraphQL API**: one paginated query per tab (10 MRs per request) that includes pipelines, jobs, approvals and discussions
- **Parallel loading**: all three tabs load concurrently at startup
- **Background refresh**: every 5 minutes without blocking the UI; a failed refresh keeps the current data and shows the error in the status bar

## Installation

### Prerequisites
- **Go 1.26+** (for building, see `go.mod`)
- **[glab](https://gitlab.com/gitlab-org/cli)** - GitLab CLI, authenticated against your GitLab instance. glamr makes all API calls through `glab`, so it uses glab's credentials.
- **[delta](https://github.com/dandavison/delta)** - optional, for syntax-highlighted diffs. glamr looks for `delta` on your `PATH`, then in `~/.local/bin/delta`. Without it, diffs are shown as plain text.

### Install delta (optional)
Use your package manager (e.g. `brew install git-delta`, `apt install git-delta`, `pacman -S git-delta`) or download a binary from the [releases page](https://github.com/dandavison/delta/releases) and put it on your `PATH` or in `~/.local/bin`.

### Authenticate with GitLab
```bash
glab auth login --hostname your.gitlab.host
```

### Build glamr
```bash
git clone https://github.com/Lindholmer/glamr.git
cd glamr
go build -o glamr ./cmd/glamr

# Optional: install to PATH
sudo mv glamr /usr/local/bin/
# or: go install ./cmd/glamr
```

## Usage

```bash
# All MRs across every project you can access (uses glab's default host)
glamr

# A specific GitLab host
glamr --host your.gitlab.host

# Only MRs from specific repositories
glamr --host your.gitlab.host --repo org/project1 --repo org/project2

# Print your authored MRs to stdout instead of starting the TUI
glamr --host your.gitlab.host --no-tui
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--host` | glab's default host | GitLab hostname, e.g. `gitlab.example.com` |
| `--repo` | all accessible repos | Limit to a repository (`group/project`). Repeat for several repos. |
| `--no-tui` | `false` | Print authored MRs to stdout and exit |

### Keyboard Shortcuts

`ctrl+c` quits from anywhere. `q` quits from any view except while typing a note. When an error is shown, any key dismisses it.

#### Main List
| Key | Action |
|-----|--------|
| `↑/↓` or `k/j` | Navigate MRs |
| `Enter` | Open MR details |
| `1` / `2` / `3` | Authored / Assigned / Reviewing tab |
| `a` | Approve, or revoke your approval |
| `D` | Toggle draft status |
| `o` | Open MR in browser |
| `r` | Refresh now |
| `q` | Quit |

#### Detail View
| Key | Action |
|-----|--------|
| `←/→` or `h/l` | Switch between Jobs and Notes panels |
| `↑/↓` or `k/j` | Navigate within the active panel |
| `Enter` | View job log (Jobs panel) or open thread (Notes panel) |
| `t` | Retry the selected job (failed jobs) |
| `p` | Play the selected job (manual jobs) |
| `c` | Cancel the selected job (running/pending jobs) |
| `n` | Write a new note |
| `a` | Approve, or revoke your approval |
| `D` | Toggle draft status |
| `f` | Show changed files |
| `d` | Show full diff |
| `o` | Open in browser |
| `r` | Reload jobs and notes |
| `Esc` | Back to the list |

#### Files View
| Key | Action |
|-----|--------|
| `↑/↓` or `k/j` | Navigate files |
| `d` | View diff of the selected file |
| `Esc` | Back to the detail view |

#### Diff and Job Log Viewer
| Key | Action |
|-----|--------|
| `↑/↓` or `k/j` | Scroll one line |
| `Shift+↑/↓` | Scroll 20 lines |
| `Home` / `End` | Jump to top / bottom |
| `Esc` | Back |

#### Discussion Thread
| Key | Action |
|-----|--------|
| `↑/↓` or `k/j`, `Shift+↑/↓`, `Home/End` | Scroll |
| `r` | Reply to the thread |
| `x` | Resolve / unresolve the thread |
| `Esc` | Back to the detail view |

While writing a reply or note: type (or paste) the text, `Enter` posts it, `Esc` cancels.

### Reviewing tab

MRs where you are a reviewer are sorted by priority:

1. 🔴 **New replies** - an unresolved thread you took part in where someone else had the last word
2. **Unreviewed** - you haven't reviewed it yet
3. 💬 **Commented** - you reviewed without approving
4. ✗ **Requested changes**
5. ✓ **Approved**
6. 📝 **Drafts** - always last

The tab label turns red if any non-draft MR is unreviewed or has new replies.

### List indicators

| Icon | Meaning |
|------|---------|
| 🟢 / 📝 / ✅ / ❌ | MR open / draft / merged / closed |
| ✅ / ❌ / 🔵 / ⏸️ / ⚠️ / ⚫ | Pipeline passed / failed / running / pending (incl. manual jobs) / other (e.g. canceled) / no pipeline |
| ⚠️ conflicts | MR has merge conflicts |
| 💬 unresolved | MR has unresolved discussions |
| `n/m` | `n` approvals given, `m` required |

The pipeline status is derived from its jobs: any failed job shows the pipeline as failed, even if GitLab still reports it as running.

## Architecture

### Components
- **TUI layer** (`internal/tui/`) - [Bubble Tea](https://github.com/charmbracelet/bubbletea) model and views, styled with [Lipgloss](https://github.com/charmbracelet/lipgloss)
  - `model.go` - state, message handling, commands and the list/diff/thread views
  - `keys.go` - key handling, one handler per view
  - `detail.go`, `files.go` - detail and files views
- **Provider layer** (`internal/provider/`) - the `Provider` interface and its GitLab implementation
  - `gitlab_graphql.go` - MR lists come from GraphQL; actions (approve, notes, jobs, diffs) use the REST API.
  - `common.go` - helpers (ID parsing, pagination, discussions, draft titles, status aggregation, glab errors)
- **GraphQL layer** (`internal/graphql/`) - query templates and response types
- **Cache layer** (`internal/cache/`) - thread-safe in-memory cache with TTL

All GitLab access goes through `glab api`, so glamr has no tokens or HTTP client of its own.

### Data Flow
1. **Startup**: the three tabs are fetched in parallel. Each tab is one GraphQL query, paginated 10 MRs per request to stay well inside GitLab's query complexity and timeout limits.
2. **Preloading**: pipeline jobs and discussions come with the MR data, so opening an MR is instant.
3. **Navigation**: switching tabs uses the preloaded data.
4. **Refresh**: every 5 minutes in the background (or on `r`). Only one refresh runs at a time; a failed refresh is not retried until the next interval.
5. **Actions**: approve, notes, resolve and job actions update the view right away; reloading details (`r` in the detail view) refetches jobs and discussions.

### MR IDs
MRs are identified as `group/project!iid` (e.g. `team/app!42`). Actions use this ID, so they work regardless of the current directory and with any number of `--repo` flags.

`--repo` filters your MRs to the given projects after fetching. The same queries run with or without it, so any number of `--repo` flags costs no extra API calls.

## Configuration

All configuration is via command-line flags.

### Refresh Interval
The refresh interval is 5 minutes. To change it, edit `NewModel` in `internal/tui/model.go`:
```go
cache:           cache.NewCache(5 * time.Minute),
refreshInterval: 5 * time.Minute,
```

### Delta Configuration
glamr runs `delta --paging never --width <terminal width>`, so delta's settings from `~/.gitconfig` apply:
```ini
[delta]
    syntax-theme = Monokai Extended
    line-numbers = true
    side-by-side = false
```

See the [delta documentation](https://dandavison.github.io/delta/) for more options.

## Troubleshooting

### "401 Unauthorized" error
- Check you're authenticated: `glab auth status`
- Check the hostname you pass with `--host` matches the one glab is logged in to
- Re-authenticate if needed: `glab auth login --hostname your.gitlab.host`

### GraphQL complexity or timeout errors
Each request fetches `mrPageSize` MRs (10) with their jobs and discussions. If a tab still times out (e.g. MRs with very many discussions), lower `mrPageSize` in `internal/provider/gitlab_graphql.go`. `--repo` doesn't help here, since filtering happens after fetching.

### Diffs aren't highlighted
- glamr falls back to plain diffs when delta isn't found. Check `delta --version`, and that it's on your `PATH` or in `~/.local/bin/`.

### An action (approve, retry job, comment, ...) fails
- The TUI shows the error returned by GitLab. `403 Forbidden` usually means you lack the permission in that project, and `insufficient_scope` means glab's token needs the `api` scope.

### Terminal scrolling issues with PageUp/PageDown
- Use `Shift+↑/↓` to scroll faster, or `Home/End` to jump to top/bottom.

## Development

```bash
git clone https://github.com/Lindholmer/glamr.git
cd glamr
go mod download
go build -o glamr ./cmd/glamr
```

### Running the tests
```bash
go test ./...          # unit tests
go test -race ./...    # with the race detector
go test -cover ./...   # with coverage
```

The tests need no GitLab access or network:
- **GraphQL query tests** check that every MR query selects the same fields.
- **Provider tests** check the conversion of GraphQL/REST responses, and drive the provider end-to-end through a fake `glab` script placed first on `PATH`. The script records the arguments of each call and returns canned JSON, so tests can check both the parsing and the exact `glab` command lines (pagination cursors, `--repo`, `--hostname`, ...).
- **TUI tests** send key presses and messages to the Bubble Tea model with an in-memory fake provider, and check the resulting state and the actions sent to the provider.
- **Cache tests** cover TTL expiry and concurrent access.

The provider tests use a POSIX shell for the fake `glab`, and are skipped on Windows.

`test_graphql.sh` is a manual smoke test against a real GitLab instance: it checks that glab is installed and authenticated, runs a GraphQL query, builds glamr and runs it with `--no-tui`.

### Project Structure
```
glamr/
├── cmd/glamr/           # Entry point and flags
├── internal/
│   ├── cache/           # In-memory MR cache
│   ├── graphql/         # GraphQL queries and response types
│   ├── provider/        # Provider interface and the GitLab implementation
│   └── tui/             # Terminal UI
├── test_graphql.sh      # Manual smoke test against a real GitLab
├── go.mod
└── README.md
```

## Contributing

Contributions are welcome! Please run `gofmt`, `go vet ./...` and `go test ./...` before opening a pull request.

## Acknowledgments

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - Terminal UI framework
- [Lipgloss](https://github.com/charmbracelet/lipgloss) - Terminal styling
- [glab](https://gitlab.com/gitlab-org/cli) - GitLab CLI
- [delta](https://github.com/dandavison/delta) - Syntax-aware diff viewer

## Roadmap

Potential future features:
- Configuration file (refresh interval, default host and repos)
- Multiple GitLab instances at once
- Merge MRs from the TUI
- Custom color schemes
- Relative timestamps ("2h ago")
- Notifications for new MRs

---

**Note**: This is an unofficial tool and is not affiliated with GitLab Inc.
