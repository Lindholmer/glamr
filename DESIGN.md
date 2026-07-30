# glamr - GitLab Merge Request Manager

## Overview

**glamr** is a terminal-based GitLab merge request (MR) dashboard built in Go. It provides a rich, interactive TUI (Terminal User Interface) for monitoring and managing GitLab merge requests across multiple repositories. The application uses the `glab` CLI tool as its backend to interact with GitLab's API.

## Architecture

### High-Level Structure

```
glamr/
├── cmd/glamr/          # Application entry point
│   └── main.go         # CLI parsing, provider setup, TUI/simple mode selection
├── internal/
│   ├── provider/       # VCS provider abstraction layer
│   │   ├── provider.go # Interface definitions and core types
│   │   └── gitlab.go   # GitLab-specific implementation using glab CLI
│   ├── cache/          # Thread-safe data caching with TTL
│   │   └── cache.go
│   └── tui/            # Terminal UI using Bubble Tea
│       ├── model.go    # Main TUI model and business logic
│       └── detail.go   # Detail view for individual MRs
```

## Component Deep Dive

### 1. Main Entry Point (`cmd/glamr/main.go`)

**Purpose**: Bootstrap the application, parse command-line flags, and initialize the appropriate mode.

**Key Responsibilities**:
- **Flag Parsing**: Accepts `--host` (GitLab instance), `--repo` (specific repositories, can be specified multiple times), and `--no-tui` (simple stdout mode)
- **Provider Creation**: Creates one or more `GitLabProvider` instances depending on whether specific repos are requested or all accessible repos should be queried
- **Mode Selection**: Either runs in simple stdout mode (`runSimpleMode`) or launches the full TUI

**Simple Mode (`runSimpleMode`)**:
- Fetches authored MRs and prints them to stdout with emoji indicators
- Shows pipeline status (✅ passed, ❌ failed, 🔵 running, etc.)
- Displays conflicts, approvals, and web URLs
- Useful for scripting or quick CLI checks without the full TUI

**Custom Flag Type (`stringList`)**:
- Implements `flag.Value` interface to allow multiple `--repo` flags
- Accumulates repository names into a slice

### 2. Provider Layer (`internal/provider/`)

**Purpose**: Abstract away VCS-specific implementation details and provide a uniform interface for fetching merge request data.

#### `provider.go` - Core Types and Interface

**Data Structures**:

**`MergeRequest`**:
- Represents a merge/pull request with comprehensive metadata
- Fields include:
  - `ID`, `IID` (Internal ID shown in GitLab UI)
  - `Title`, `Author`, `SourceBranch`, `TargetBranch`
  - `Status` (open, merged, closed, draft)
  - `HasConflicts`, `HasUnresolvedDiscussions`
  - `Pipeline` (pointer to Pipeline struct)
  - `ApprovalCount`, `RequiredApprovals`, `ApprovalsLeft`, `UserApproved`, `Approved`
  - `RepoName` (for multi-repo support)
  - `WebURL`, `CreatedAt`, `UpdatedAt`

**`Pipeline`**:
- CI/CD pipeline information
- Fields: `ID`, `Status`, `WebURL`, `CreatedAt`, `UpdatedAt`, `Duration`
- Status types: success, running, failed, pending, skipped

**`MRStatus`** and **`PipelineStatus`**:
- Type-safe enums for representing states

**`Scope`**:
- Defines query filters: `authored` (created by user), `assigned` (assigned to user), `reviewing` (user is reviewer)

**`PipelineJob`**:
- Individual CI/CD job within a pipeline
- Fields: `Name`, `Status`, `Stage`

**`MRNote`**:
- Represents comments/discussions on an MR
- Fields: `Author`, `Body`, `CreatedAt`, `Resolvable`, `Resolved`

**`Provider` Interface**:
- Defines contract all VCS providers must implement:
  - `ListMRs(scope Scope)` - Fetch MRs for a given scope
  - `GetMR(id string)` - Get detailed info about a specific MR
  - `ApproveMR(id string)` - Approve an MR
  - `UnapproveMR(id string)` - Remove approval
  - `GetCurrentUser()` - Get authenticated user's username
  - `Name()` - Provider name (gitlab, github)
  - `ListRepos()` - List all accessible repositories
  - `GetPipelineJobs(pipelineID, repo)` - Fetch jobs for a pipeline
  - `GetMRNotes(mrIID, repo)` - Fetch notes/comments

#### `gitlab.go` - GitLab Implementation

**Purpose**: Implements the `Provider` interface using the `glab` CLI tool to interact with GitLab's API.

**Key Design Decisions**:

1. **CLI-Based Approach**: Uses `glab` CLI instead of direct HTTP API calls
   - Leverages existing authentication (glab handles tokens)
   - Automatically handles API pagination, rate limiting, retries
   - Simpler than managing HTTP client, auth, pagination manually

2. **Host Configuration**: Supports custom GitLab instances
   - For `api` subcommands: uses `--hostname` flag
   - For other commands: sets `GITLAB_HOST` environment variable
   - Inherits parent environment to preserve other configurations

3. **Multi-Repo Support**: Two operational modes
   - **Single Repo Mode**: `repo` field is set, queries one repository
   - **All Repos Mode**: `repo` field is empty, discovers and queries all accessible repos

**Core Methods**:

**`NewGitLabProvider(host, repo)`**:
- Creates provider instance
- Pre-fetches current user's username (cached for filtering)
- Validates connectivity by testing authentication

**`buildGlabCommand(args...)`**:
- Builds `exec.Cmd` with proper host configuration
- Handles two different patterns for different glab subcommands
- Ensures environment variables are properly inherited

**`ListMRs(scope)`**:
- Routes to either `listMRsForRepo` or `listMRsAllRepos` based on configuration
- For each MR, enriches data with pipeline and approval information

**`listMRsForRepo(repo, scope)`**:
- Constructs appropriate glab command based on scope (`--author @me`, `--assignee @me`, `--reviewer @me`)
- Parses JSON output from glab
- For each MR:
  - Converts glab format to internal `MergeRequest` struct
  - Fetches pipeline information (if exists)
  - Fetches approval data
  - Checks for unresolved discussions

**`listMRsAllRepos(scope)`**:
- First calls `ListRepos()` to discover all accessible repos
- Iterates through repos, calling `listMRsForRepo` for each
- Collects errors but continues processing (fault-tolerant)
- Aggregates MRs from all repos into single list

**`ListRepos()`**:
- Uses GitLab API endpoint: `projects?membership=true&per_page=100&simple=true`
- Uses `--paginate` flag to automatically fetch all pages
- Extracts `path_with_namespace` (e.g., "group/project")

**`fetchMRPipelineForRepo(repo, mrIID)`**:
- Fetches detailed MR data including pipeline information
- Tries `head_pipeline` first (most recent), falls back to `pipeline`
- **Critical Feature**: Fetches individual jobs to determine true pipeline status
- Overrides pipeline status with aggregated job status (see `aggregateJobStatuses`)

**`aggregateJobStatuses(jobs)`**:
- Determines worst status from all jobs in a pipeline
- Priority order: failed > running > pending > manual > success
- This is important because GitLab's pipeline status might be "success" even if some jobs are "manual" or "pending"

**`fetchMRApprovalsForRepo(repo, mrIID, mr)`**:
- Fetches approval state from `/approvals` endpoint
- Extracts:
  - Required approvals
  - Approvals left
  - Overall approved status
  - Whether current user has approved
  - Count of approvers
- Also fetches notes with pagination to check for unresolved discussions
- Iterates through notes looking for resolvable but unresolved items

**`GetPipelineJobs(pipelineID, repo)`**:
- Fetches all jobs for a specific pipeline
- Used by detail view to show CI job breakdown

**`GetMRNotes(mrIID, repo)`**:
- Fetches all notes/comments with pagination
- Uses JSON decoder to handle multiple JSON arrays (pagination output)
- Filters out system notes (automatic comments from GitLab)
- Extracts author, body, timestamps, resolvability status

**`convertGlabMR(data)`**:
- Converts glab's JSON format to internal `MergeRequest` struct
- Handles type conversions (JSON floats to ints)
- Determines status based on `state` and `draft` fields
- Parses RFC3339 timestamps
- Extracts approval information and checks if current user approved

**`convertPipeline(data)`**:
- Converts pipeline JSON to `Pipeline` struct
- Maps GitLab status strings to internal enum types

**`urlEncode(s)`**:
- Simple URL encoder for GitLab API paths
- Replaces `/` with `%2F` (required for project paths in API)
- Handles spaces

### 3. Cache Layer (`internal/cache/cache.go`)

**Purpose**: Provide thread-safe caching of MR data with time-to-live (TTL) expiration to reduce API calls.

**Design**:
- **Thread-Safety**: Uses `sync.RWMutex` for concurrent access
  - Read operations use `RLock()` (multiple readers allowed)
  - Write operations use `Lock()` (exclusive access)
- **TTL-Based Invalidation**: Tracks `lastRefresh` timestamp
- **Atomic Replacement**: `Set()` replaces entire cache atomically (not incremental updates)

**Methods**:

**`NewCache(ttl)`**:
- Creates cache with specified TTL duration
- Typically initialized with 60 seconds in the TUI

**`Get()`**:
- Returns cached data if still valid (within TTL)
- Returns `(nil, false)` if stale

**`Set(data)`**:
- Atomically replaces entire cache
- Updates `lastRefresh` to current time
- Used after successful API fetch

**`LastRefresh()`**:
- Thread-safe read of last refresh timestamp
- Used by UI to show "refreshed X ago"

**`IsStale()`**:
- Checks if cache needs refresh (TTL expired)
- Used by auto-refresh ticker

### 4. TUI Layer (`internal/tui/`)

**Purpose**: Provide rich terminal UI using the Bubble Tea framework (Elm architecture for terminal apps).

#### `model.go` - Main TUI Model

**Elm Architecture Pattern**:
- **Model**: Holds application state
- **Update**: Handles messages (events) and returns updated model + commands
- **View**: Renders current state to string

**Model Structure**:
```go
type Model struct {
    providers       []provider.Provider  // GitLab providers to query
    scope           provider.Scope       // Current scope filter
    mrs             []provider.MergeRequest
    cursor          int                  // Selected item index
    loading         bool                 // Fetching data
    err             error                // Last error
    cache           *cache.Cache         // Data cache
    refreshInterval time.Duration        // Auto-refresh interval
    width, height   int                  // Terminal dimensions
    detailView      *DetailView          // Detail panel (when showing MR details)
    showingDetail   bool                 // Detail view active
}
```

**Message Types**:

**`mrsFetchedMsg`**:
- Sent when MR fetch completes
- Contains MR list or error
- Triggers cache update and UI refresh

**`detailDataMsg`**:
- Sent when detail data (jobs, notes) is fetched
- Updates detail view with CI jobs and comments

**`tickMsg`**:
- Sent every second by ticker
- Triggers auto-refresh if cache is stale

**Initialization (`Init`)**:
- Starts initial MR fetch
- Starts ticker for auto-refresh

**Update Logic (`Update`)**:

1. **Window Resize**: Updates dimensions for responsive layout

2. **Key Handling**:
   - **Escape**: Exit detail view
   - **Navigation in detail view**: j/k (up/down) for notes pagination, h/l (left/right) for jobs pagination
   - **q / Ctrl+C**: Quit application
   - **r**: Manual refresh (bypass cache)
   - **Up/Down (k/j)**: Navigate MR list
   - **1/2/3**: Switch scope (authored/assigned/reviewing)
   - **Enter**: Open detail view for selected MR
   - **o**: Placeholder for browser open (future feature)

3. **MR Fetch Completion**:
   - Updates `mrs` slice with new data
   - Updates cache atomically
   - Resets cursor if out of bounds
   - Clears loading state

4. **Tick Messages**:
   - Checks if cache is stale
   - Triggers background refresh if needed
   - Reschedules next tick

5. **Detail Data Fetch**:
   - Updates detail view with jobs and notes
   - Handles errors gracefully

**View Rendering (`View`)**:

Returns a string representing the entire TUI, composed of:

1. **Header**: Application title and current scope
2. **Tabs**: Switchable tabs for authored/assigned/reviewing
3. **MR List**: Scrollable list of merge requests
4. **Status Bar**: Last refresh time, MR count, repo count
5. **Help Bar**: Keyboard shortcuts

**MR List Item (`renderMR`)**:

Each MR displays:
- **Status Icon**: 🟢 open, 📝 draft, ✅ merged, ❌ closed
- **IID and Title**: e.g., "!123 Add user authentication"
- **Repository**: Shown in italics (for multi-repo support)
- **Branches**: source → target
- **Pipeline Status**: With colored icon (✅ passed, ❌ failed, 🔵 running, ⏸️ pending, ⚫ no pipeline)
- **Indicators**: ⚠️ conflicts, 💬 unresolved discussions
- **Approval Status**: Color-coded based on state:
  - Green checkmark: You approved AND fully approved
  - Green circle: You approved but waiting for others
  - Bright checkmark: Fully approved (not by you)
  - Gray circle: Waiting for approvals (you haven't approved)
- **Selected State**: Highlighted background for cursor position

**Tab Rendering**:
- Active tab: Bold, cyan foreground, gray background
- Inactive tab: Gray foreground

**Status Bar**:
- Shows "⟳ Loading..." when fetching
- Otherwise shows time since last refresh, MR count, repo count
- Intelligently counts unique repos when in all-repos mode

**Commands**:

**`fetchMRs(p, scope)`**:
- Async command to fetch MRs from a single provider
- Returns `mrsFetchedMsg`

**`fetchAllMRs(providers, scope)`**:
- Async command to fetch MRs from all providers
- Aggregates results
- Returns single `mrsFetchedMsg` with all MRs (atomic update)

**`tickCmd()`**:
- Returns command that ticks every second
- Used for auto-refresh timer

**`fetchDetailData(p, mr)`**:
- Async command to fetch pipeline jobs and notes for an MR
- Returns `detailDataMsg`

**Styling**:
- Uses `lipgloss` library for declarative styling
- Defines styles for header, tabs, items, status, help, error states
- Active/selected items have different backgrounds
- Color-coded indicators for status (green success, red failed, blue running, etc.)

#### `detail.go` - Detail View

**Purpose**: Split-pane detail view showing CI jobs and comments for a selected MR.

**Structure**:
```go
type DetailView struct {
    mr         provider.MergeRequest
    jobs       []provider.PipelineJob
    notes      []provider.MRNote
    width      int
    height     int
    notesPage  int  // Pagination for notes
    jobsPage   int  // Pagination for jobs
}
```

**Layout**: Horizontal split with border:
```
┌─────────────────────────────────────────┐
│ !123 Add user authentication            │
├──────────────────┬──────────────────────┤
│ CI Pipeline Jobs │ Comments & Discussions│
│                  │                       │
│ ✅ test         │ 💬 @reviewer1         │
│ ✅ lint         │    Looks good!        │
│ ❌ build        │                       │
│ 🔵 deploy       │ 💬 @author           │
│                  │    Thanks!            │
└──────────────────┴──────────────────────┘
│ shortcuts...                            │
└─────────────────────────────────────────┘
```

**Left Panel (`renderJobs`)**:
- Shows all CI/CD jobs in the pipeline
- Each job has status icon and name
- Icons: ✅ success, ❌ failed, 🔵 running, ⏸️ pending, ⚙️ manual, ⏭️ skipped
- Color-coded based on status
- Shows "No pipeline" or "Loading jobs..." states

**Right Panel (`renderNotes`)**:
- Shows comments and discussions
- Filters out resolved discussions (only shows unresolved)
- Icons: 💬 unresolved discussion, 💭 regular comment
- Each note shows:
  - Author (in cyan, bold)
  - Timestamp (in gray)
  - Body (word-wrapped to fit panel width)
- Simple word wrapping: breaks at spaces, max 5 lines per comment with "..." continuation

**Pagination** (not fully implemented):
- `notesPage` and `jobsPage` fields track page state
- j/k and h/l keys in main model adjust these
- Future: implement scrolling for long lists

**Help Bar**:
- Shows navigation shortcuts: j/k for comments, h/l for jobs
- Placeholder shortcuts: o (open), d (diff), m (merge)
- Escape to return to list view

## Data Flow

### Startup Flow
1. User runs `glamr --host gitlab.example.com --repo group/project1`
2. `main()` parses flags, creates `GitLabProvider` instances
3. Provider initialization authenticates and caches current username
4. TUI `Model` is created with providers
5. `Init()` triggers initial MR fetch and starts ticker

### MR Fetch Flow
1. `fetchAllMRs` command executes in goroutine
2. For each provider:
   - If single-repo mode: calls `listMRsForRepo`
   - If all-repos mode: calls `ListRepos`, then `listMRsForRepo` for each
3. For each MR:
   - Base data fetched via `glab mr list --output json`
   - Pipeline data fetched via `glab api projects/<repo>/merge_requests/<iid>`
   - Pipeline jobs fetched to determine true status
   - Approval data fetched via `glab api projects/<repo>/merge_requests/<iid>/approvals`
   - Notes fetched (paginated) to check for unresolved discussions
4. All MRs aggregated and sent as single `mrsFetchedMsg`
5. Model updates cache and `mrs` slice atomically
6. `View()` re-renders with new data

### Auto-Refresh Flow
1. `tickCmd()` sends `tickMsg` every second
2. `Update()` receives tick, checks if cache is stale
3. If stale and not already loading: triggers fetch
4. Ticker reschedules itself for next second

### Detail View Flow
1. User presses Enter on an MR in the list
2. `Update()` creates `DetailView` and sets `showingDetail = true`
3. `fetchDetailData` command fetches jobs and notes
4. `detailDataMsg` populates detail view
5. `View()` delegates rendering to `DetailView.Render()`
6. User navigates with j/k/h/l
7. Escape key clears detail view, returns to list

### Scope Switching Flow
1. User presses 1, 2, or 3
2. `Update()` changes `scope` field
3. Triggers fresh MR fetch with new scope
4. Cache updated with new data
5. View re-renders with filtered MRs

## Key Design Patterns

### 1. Provider Interface Pattern
- **Abstraction**: `Provider` interface allows swapping VCS backends (GitHub, Bitbucket, etc.)
- **Current Implementation**: Only GitLab via glab CLI
- **Future**: Could add GitHub provider using `gh` CLI, or direct HTTP clients

### 2. Elm Architecture (Bubble Tea)
- **Unidirectional Data Flow**: Model → View → User Input → Update → New Model
- **Immutability**: Update returns new model (though Go doesn't enforce this strictly)
- **Commands**: Side effects (API calls, timers) returned as commands, executed by framework

### 3. Atomic Cache Updates
- Never updates individual MRs in place
- Always replaces entire cache atomically with `Set()`
- Prevents partial/inconsistent states
- Simplifies concurrency model

### 4. Fault Tolerance
- Multi-repo fetch continues even if individual repos fail
- Errors collected but don't stop processing
- Graceful degradation: missing pipeline/approval data doesn't fail MR display

### 5. Progressive Enhancement
- Basic data shown immediately (from `glab mr list`)
- Rich data (pipelines, approvals) fetched separately
- Detail view loads asynchronously (jobs and notes)
- User sees something quickly, details fill in

### 6. CLI Composition
- Delegates complex logic to `glab` CLI
- Avoids reimplementing API client, auth, pagination
- Trade-off: dependency on external tool, but gains simplicity

## Performance Considerations

### Caching Strategy
- **TTL**: 60 seconds default
- **Rationale**: Balance between freshness and API load
- **Atomic Updates**: No incremental updates, always full refresh
- **Manual Override**: "r" key forces immediate refresh

### Parallel Fetching
- Providers queried sequentially (not parallel)
- Within each provider, repos queried sequentially
- **Future Optimization**: Could parallelize repo queries with worker pool

### API Call Minimization
- Batch calls where possible (e.g., `--per-page 100`)
- Pagination handled by glab (`--paginate` flag)
- Cache prevents redundant fetches during TTL window

### Job Status Aggregation
- Prevents incorrect pipeline status display
- Small overhead (extra API call per MR with pipeline)
- Worth it for accuracy (manual jobs would otherwise show as "success")

## Configuration and Extensibility

### Command-Line Configuration
- `--host`: Custom GitLab instance
- `--repo`: Specific repositories (repeatable)
- `--no-tui`: Simple stdout mode

### Future Configuration Options
- Cache TTL
- Refresh interval
- Default scope
- Theme customization
- Additional providers (GitHub, etc.)

### Extensibility Points
- **Provider Interface**: Add new VCS providers
- **Scopes**: Add custom filters (e.g., "needs review", "failing CI")
- **Detail View**: Add more panels (diffs, merge options)
- **Actions**: Approve, merge, comment directly from TUI

## Error Handling

### Authentication Errors
- Fail fast on startup if `GetCurrentUser()` fails
- Prevents running with invalid credentials

### API Errors
- Individual MR fetch errors logged but don't crash
- Multi-repo queries continue even if some fail
- Error state displayed in UI with retry option (press "r")

### Parsing Errors
- JSON parsing failures skip individual items
- Protects against malformed glab output
- Graceful degradation

## Future Enhancements

### Planned Features
1. **Browser Integration**: "o" key to open MR in browser
2. **Diff View**: "d" key to show code changes
3. **Merge Action**: "m" key to merge MR directly
4. **Comment/Approve**: Interactive actions from TUI
5. **Filtering**: Search/filter MRs by title, author, labels
6. **Sorting**: Sort by updated time, pipeline status, approvals
7. **Multi-Provider**: Support GitHub, Bitbucket side-by-side
8. **Configuration File**: Persist settings, saved queries
9. **Notifications**: Desktop notifications for pipeline failures, new comments
10. **Dashboard Mode**: Widget-style layout for monitoring multiple repos

### Technical Improvements
1. **Parallel Fetching**: Worker pool for multi-repo queries
2. **Incremental Updates**: Update individual MRs without full refresh
3. **Better Pagination**: Handle very long job/note lists in detail view
4. **Better Word Wrapping**: Use proper text wrapping library
5. **Relative Time Formatting**: "2h ago" instead of timestamps
6. **Theming**: Configurable color schemes
7. **Keyboard Shortcuts**: More customizable bindings
8. **Mouse Support**: Click to select, scroll

## Dependencies

### Core Libraries
- **Bubble Tea** (`charmbracelet/bubbletea`): TUI framework (Elm architecture)
- **Lipgloss** (`charmbracelet/lipgloss`): Declarative styling for terminal
- **Bubbles** (`charmbracelet/bubbles`): TUI components (currently not heavily used, but available)

### External Tools
- **glab**: GitLab CLI tool (required, must be installed and authenticated)

### Go Standard Library
- `encoding/json`: Parse glab JSON output
- `os/exec`: Execute glab commands
- `time`: Caching, timestamps, durations
- `sync`: Mutex for thread-safe cache
- `flag`: Command-line parsing

## Testing Strategy

### Current State
- No automated tests yet (typical for rapid prototyping)

### Recommended Test Coverage
1. **Provider Tests**:
   - Mock glab output, verify parsing
   - Test URL encoding
   - Test status aggregation logic
   - Test error handling

2. **Cache Tests**:
   - Concurrent access (race detector)
   - TTL expiration
   - Atomic updates

3. **TUI Tests**:
   - Message handling (key presses, fetch results)
   - View rendering snapshots
   - Navigation logic

4. **Integration Tests**:
   - End-to-end with test GitLab instance
   - Mock glab CLI responses

## Deployment

### Build
```bash
go build -o glamr ./cmd/glamr
```

### Installation
```bash
# Ensure glab is installed and authenticated
glab auth login

# Run glamr
./glamr --host gitlab.example.com
```

### Distribution
- Single static binary (Go advantage)
- No runtime dependencies except glab
- Cross-platform (Linux, macOS, Windows with appropriate glab)

## Conclusion

**glamr** is a well-structured, maintainable TUI application that leverages the power of the `glab` CLI to provide a rich merge request monitoring experience. Its design emphasizes:

- **Separation of Concerns**: Clear boundaries between provider, cache, and UI layers
- **Extensibility**: Provider interface allows multiple VCS backends
- **User Experience**: Rich TUI with real-time updates, color-coded status, and detail views
- **Robustness**: Fault-tolerant multi-repo queries, thread-safe caching, graceful error handling
- **Performance**: Smart caching reduces API load, atomic updates prevent inconsistencies

The codebase is ready for both immediate use and future enhancement, with clear extension points for additional features and providers.
