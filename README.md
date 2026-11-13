# glamr

**Merge request dashboard** - A fast, terminal-based UI for your GitLab merge requests and, optionally, GitHub pull requests.

![glamr screenshot](https://img.shields.io/badge/TUI-Terminal%20UI-blue)
![Go version](https://img.shields.io/badge/Go-1.26%2B-00ADD8)

## Features

### 📋 MR Management
- **Tabs**: Authored, Assigned and Reviewing for GitLab (`1-3`), plus the same three for GitHub (`4-6`) with `--github`. All tabs are preloaded, so switching is instant
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
- **Job actions**: retry failed jobs, play manual jobs, cancel running/pending jobs (see [GitHub differences](#github-pull-requests))

### ⚡ Performance
- **GraphQL API**: one paginated query per tab (10 GitLab MRs or 20 GitHub PRs per request) that includes pipelines/checks, jobs, approvals and discussions
- **Parallel loading**: all tabs load concurrently; if one forge fails, the other's tabs still update
- **Background refresh**: every 5 minutes without blocking the UI; a failed refresh keeps the current data and shows the error in the status bar

## Installation

### Prerequisites
- **Go 1.26+** (for building, see `go.mod`)
- **[glab](https://gitlab.com/gitlab-org/cli)** - GitLab CLI, authenticated against your GitLab instance. glamr makes all API calls through `glab`, so it uses glab's credentials.
- **[gh](https://cli.github.com/)** - GitHub CLI, only needed with `--github`. Authenticate with `gh auth login`.
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

# Also show GitHub pull requests (tabs 4-6)
glamr --host your.gitlab.host --github

# Only PRs from specific GitHub repositories (implies --github)
glamr --host your.gitlab.host --github-repo octo/app --github-repo octo/lib

# Print your authored MRs (and PRs) to stdout instead of starting the TUI
glamr --host your.gitlab.host --no-tui
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--host` | glab's default host | GitLab hostname, e.g. `gitlab.example.com` |
| `--repo` | all accessible repos | Limit to a repository (`group/project`). Repeat for several repos. |
| `--github` | `false` | Add GitHub tabs (4-6) using the `gh` CLI |
| `--github-host` | gh's default host | GitHub Enterprise hostname (implies `--github`) |
| `--github-repo` | all repos | Limit GitHub tabs to a repository (`owner/name`). Repeat for several; filtered by GitHub's search, so it costs no extra calls. Implies `--github`. |
| `--no-tui` | `false` | Print authored MRs (and PRs) to stdout and exit |

### Keyboard Shortcuts

`ctrl+c` quits from anywhere. `q` quits from any view except while typing a note. When an error is shown, any key dismisses it.

#### Main List
| Key | Action |
|-----|--------|
| `↑/↓` or `k/j` | Navigate MRs |
| `Enter` | Open MR details |
| `1` / `2` / `3` | GitLab Authored / Assigned / Reviewing tab |
| `4` / `5` / `6` | GitHub Authored / Assigned / Reviewing tab (with `--github`) |
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

The tab label turns red if any non-draft MR is unreviewed or has new replies. With `--github`, each forge's Reviewing tab is flagged separately.

### GitHub pull requests

With `--github`, tabs 4-6 list your open PRs (`author:@me`, `assignee:@me`, and PRs you're requested to review or have reviewed). They work like the GitLab tabs, with these differences:

| | GitHub behaviour |
|---|---|
| References | PRs show as `#42`; IDs are `owner/repo#42` |
| Pipeline / jobs | The checks on the PR's latest commit. GitHub Actions jobs support logs, retry (`t`) and cancel (`c`); external commit statuses are shown but have no logs or actions |
| Cancel (`c`) | Cancels the whole workflow run the job belongs to (GitHub can't cancel single jobs) |
| Play (`p`) | Not available: GitHub Actions has no manual jobs |
| Approvals | GitHub only shows the required count to admins, so the list shows the review decision instead, e.g. `1 approved, review required` |
| Unapprove (`a` again) | Dismisses your approving review (needs write access to the repo) |
| Comments | Review threads can be replied to and resolved. Top-level comments and review summaries are a flat conversation: replying posts a new comment, and they can't be resolved |
| Reviewing tab | Two searches merged: open review requests, plus PRs you already reviewed (GitHub removes you from the requested reviewers once you review, and its search has no `OR`). Being re-requested marks a PR unreviewed again |

### List indicators

| Icon | Meaning |
|------|---------|
| 🟢 / 📝 / ✅ / ❌ | MR open / draft / merged / closed |
| ✅ / ❌ / 🔵 / ⏸️ / ⚠️ / ⚫ | Pipeline passed / failed / running / pending (incl. manual jobs) / other (e.g. canceled) / no pipeline |
| ⚠️ conflicts | MR has merge conflicts |
| 💬 unresolved | MR has unresolved discussions |
| `n/m` | `n` approvals given, `m` required (GitHub: `n approved` + review decision) |

The pipeline status is derived from its jobs: any failed job shows the pipeline as failed, even if GitLab still reports it as running.

## Architecture

### Components
- **TUI layer** (`internal/tui/`) - [Bubble Tea](https://github.com/charmbracelet/bubbletea) model and views, styled with [Lipgloss](https://github.com/charmbracelet/lipgloss)
  - `model.go` - state, message handling, commands and the list/diff/thread views
  - `keys.go` - key handling, one handler per view
  - `tabs.go` - the tab layout (3 GitLab tabs, 3 more for GitHub), parallel tab refresh
  - `detail.go`, `files.go` - detail and files views
- **Provider layer** (`internal/provider/`) - the `Provider` interface and its implementations
  - `gitlab_graphql.go` - GitLab: MR lists come from GraphQL; actions (approve, notes, jobs, diffs) use the REST API.
  - `github.go` - GitHub: PR lists come from GraphQL search; actions use REST or GraphQL mutations.
  - `cli.go` - builds `glab api` / `gh api` invocations
  - `common.go` - helpers (ID parsing, pagination, discussions, draft titles, status aggregation, CLI errors)
- **GraphQL layer** (`internal/graphql/`) - query templates and response types
- **Cache layer** (`internal/cache/`) - thread-safe in-memory cache with TTL

All access goes through `glab api` and `gh api`, so glamr has no tokens or HTTP client of its own.

### Data Flow
1. **Startup**: all tabs are fetched in parallel. Each tab is one GraphQL query (two for the GitHub Reviewing tab), paginated (10 GitLab MRs / 20 GitHub PRs per request) to stay well inside the query complexity and timeout limits.
2. **Preloading**: pipeline jobs and discussions come with the MR data, so opening an MR is instant.
3. **Navigation**: switching tabs uses the preloaded data.
4. **Refresh**: every 5 minutes in the background (or on `r`). Only one refresh runs at a time; a failed refresh is not retried until the next interval. Tabs that fail keep their previous data while the others update.
5. **Actions**: approve, notes, resolve and job actions update the view right away; reloading details (`r` in the detail view) refetches jobs and discussions.

### MR IDs
MRs are identified as `group/project!iid` (e.g. `team/app!42`), PRs as `owner/repo#42`. Actions use this ID and the tab's provider, so they work regardless of the current directory and with any number of `--repo` flags.

`--repo` filters your MRs to the given projects after fetching. The same queries run with or without it, so any number of `--repo` flags costs no extra API calls. `--github-repo` is passed to GitHub's search as `repo:` filters.

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
- For GitHub tabs: `gh auth status`, and `gh auth login` (add `--hostname` for GitHub Enterprise)

### GraphQL complexity or timeout errors
Each GitLab request fetches `mrPageSize` MRs (10) with their jobs and discussions. If a tab still times out (e.g. MRs with very many discussions), lower `mrPageSize` in `internal/provider/gitlab_graphql.go`. `--repo` doesn't help here, since filtering happens after fetching. For GitHub, the equivalent is `ghPageSize` (20) in `internal/provider/github.go`.

### One forge's tabs show old data
If GitLab or GitHub fails during a refresh, its tabs keep their last data and the status bar names the failing tab (e.g. `Refresh failed: GitHub Reviewing: ...`). The other forge's tabs still update.

### Diffs aren't highlighted
- glamr falls back to plain diffs when delta isn't found. Check `delta --version`, and that it's on your `PATH` or in `~/.local/bin/`.

### An action (approve, retry job, comment, ...) fails
- The TUI shows the error returned by GitLab or GitHub. `403 Forbidden` usually means you lack the permission in that project, and `insufficient_scope` means glab's token needs the `api` scope.
- GitHub doesn't let you approve your own PR, and dismissing an approval needs write access to the repo.

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

The tests need no GitLab/GitHub access or network:
- **GraphQL query tests** check that every MR query selects the same fields.
- **Provider tests** check the conversion of GraphQL/REST responses, and drive both providers end-to-end through a fake `glab` or `gh` script placed first on `PATH`. The script records the arguments of each call and returns canned JSON, so tests can check both the parsing and the exact command lines (pagination cursors, search queries, `--repo`, `--hostname`, ...).
- **TUI tests** send key presses and messages to the Bubble Tea model with in-memory fake providers, and check the resulting state and that actions reach the right forge.
- **Cache tests** cover TTL expiry and concurrent access.

The provider tests use a POSIX shell for the fake CLIs, and are skipped on Windows.

`test_graphql.sh` is a manual smoke test against a real GitLab instance: it checks that glab is installed and authenticated, runs a GraphQL query, builds glamr and runs it with `--no-tui`.

### Project Structure
```
glamr/
├── cmd/glamr/           # Entry point and flags
├── internal/
│   ├── cache/           # In-memory MR cache
│   ├── graphql/         # GraphQL queries and response types
│   ├── provider/        # Provider interface, GitLab and GitHub implementations
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
- [gh](https://cli.github.com/) - GitHub CLI
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

**Note**: This is an unofficial tool and is not affiliated with GitLab Inc. or GitHub, Inc.
