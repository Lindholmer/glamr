# GraphQL Approach for glamr

## Current Approach vs GraphQL

### Current Approach (Multiple CLI Calls)
For each MR, the current implementation makes:
1. `glab mr list --output json` - Get basic MR info
2. `glab api projects/<repo>/merge_requests/<iid>` - Get detailed MR with pipeline
3. `glab api projects/<repo>/pipelines/<id>/jobs` - Get individual jobs
4. `glab api projects/<repo>/merge_requests/<iid>/approvals` - Get approval info
5. `glab api projects/<repo>/merge_requests/<iid>/notes?per_page=100` - Get notes (paginated)

**Total**: ~5 API calls per MR, multiplied by number of MRs

### GraphQL Approach (Single Query)
One query can fetch everything:
- All MRs with specified scope (authored/assigned/reviewing)
- Pipeline details including individual jobs
- Approval information
- Unresolved discussions
- Notes/comments

**Total**: 1-3 API calls (depending on pagination needs)

## Benefits of GraphQL Approach

1. **Performance**: Dramatically reduces API calls (5N → 1-3 calls)
2. **Network efficiency**: Single request/response cycle
3. **Exact data**: Request only what you need (no over-fetching)
4. **Atomic consistency**: All data from same point in time
5. **Less parsing**: Single JSON response structure
6. **Simpler code**: No need to orchestrate multiple calls

## Proposed GraphQL Query

```graphql
query getMergeRequests($scope: String!, $first: Int = 50) {
  currentUser {
    username
    
    # For authored MRs
    authoredMergeRequests(state: opened, first: $first) {
      pageInfo {
        hasNextPage
        endCursor
      }
      nodes {
        iid
        id
        title
        webUrl
        sourceBranch
        targetBranch
        state
        draft
        createdAt
        updatedAt
        conflicts
        
        author {
          username
          name
        }
        
        project {
          fullPath
          name
        }
        
        # Pipeline with jobs
        headPipeline {
          id
          status
          duration
          createdAt
          updatedAt
          webUrl
          jobs {
            nodes {
              name
              status
              stage
            }
          }
        }
        
        # Approvals
        approvedBy {
          nodes {
            username
          }
        }
        approved
        approvalsLeft
        approvalsRequired
        
        # Discussions (unresolved only can be filtered)
        discussions(first: 100) {
          nodes {
            resolved
            resolvable
            notes {
              nodes {
                author {
                  username
                  name
                }
                body
                createdAt
                system
                resolvable
                resolved
              }
            }
          }
        }
      }
    }
    
    # For assigned MRs
    assignedMergeRequests(state: opened, first: $first) {
      # ... same structure as above
    }
    
    # For reviewing MRs
    reviewRequestedMergeRequests(state: opened, first: $first) {
      # ... same structure as above
    }
  }
}
```

## Storage Options

### Option 1: In-Memory Only (Current Cache)
**Pros**:
- Fast access
- No disk I/O
- Simple implementation (already have cache.go)

**Cons**:
- Lost on restart
- Limited by memory
- Must refetch on startup

**Best for**: Current use case (real-time dashboard)

### Option 2: File-Based Cache
**Structure**:
```
~/.cache/glamr/
  ├── gitlab.example.com/
  │   ├── authored.json
  │   ├── assigned.json
  │   ├── reviewing.json
  │   └── metadata.json (timestamps, etc.)
  └── gitlab.com/
      └── ...
```

**Pros**:
- Persists across restarts
- Instant startup (show cached data while refreshing)
- Can be queried offline
- Historical data possible

**Cons**:
- Disk I/O overhead
- Stale data on startup
- Need cache invalidation strategy

**Best for**: Longer-term caching, offline mode

### Option 3: Hybrid Approach (Recommended)
- In-memory cache for active session (current cache.go)
- File-based persistence on exit / background writes
- Load from file on startup → show immediately → refresh in background

## Implementation Plan

### Phase 1: GraphQL Provider
1. Create `internal/provider/gitlab_graphql.go`
2. Implement single query to fetch all MR data
3. Parse GraphQL response into existing `MergeRequest` structs
4. Add feature flag to switch between REST and GraphQL

### Phase 2: Enhanced Caching
1. Extend `cache.go` to support file persistence
2. Add methods: `LoadFromDisk()`, `SaveToDisk()`
3. Use JSON for simple serialization
4. Store per host/scope combination

### Phase 3: Migration
1. Test GraphQL provider against real GitLab instance
2. Compare performance (timing, API calls)
3. Ensure feature parity
4. Make GraphQL the default

## Code Structure

```
internal/
├── provider/
│   ├── provider.go           # Interface (unchanged)
│   ├── gitlab.go             # Current REST implementation (keep for fallback)
│   └── gitlab_graphql.go     # New GraphQL implementation
├── cache/
│   ├── cache.go              # Current in-memory cache (enhanced)
│   └── disk.go               # New file persistence layer
└── graphql/
    ├── queries.go            # GraphQL query templates
    └── types.go              # GraphQL-specific response types
```

## Example GraphQL Provider Implementation

```go
type GitLabGraphQLProvider struct {
    host     string
    repo     string  // optional, empty for all repos
    username string
}

func (p *GitLabGraphQLProvider) ListMRs(scope Scope) ([]MergeRequest, error) {
    query := buildQueryForScope(scope)
    
    // Execute via glab
    cmd := exec.Command("glab", "api", "graphql", 
        "--hostname", p.host,
        "-f", "query="+query)
    
    output, err := cmd.Output()
    if err != nil {
        return nil, err
    }
    
    // Parse GraphQL response
    var response GraphQLResponse
    json.Unmarshal(output, &response)
    
    // Convert to our MergeRequest type
    return convertGraphQLMRs(response), nil
}
```

## File Cache Implementation

```go
type DiskCache struct {
    baseDir string  // ~/.cache/glamr
}

func (c *DiskCache) Save(host string, scope Scope, mrs []MergeRequest) error {
    path := filepath.Join(c.baseDir, host, scope.String()+".json")
    os.MkdirAll(filepath.Dir(path), 0755)
    
    data := CacheFile{
        Timestamp: time.Now(),
        MRs:       mrs,
    }
    
    file, _ := os.Create(path)
    defer file.Close()
    json.NewEncoder(file).Encode(data)
    return nil
}

func (c *DiskCache) Load(host string, scope Scope) ([]MergeRequest, time.Time, error) {
    path := filepath.Join(c.baseDir, host, scope.String()+".json")
    
    file, err := os.Open(path)
    if err != nil {
        return nil, time.Time{}, err
    }
    defer file.Close()
    
    var data CacheFile
    json.NewDecoder(file).Decode(&data)
    
    return data.MRs, data.Timestamp, nil
}
```

## Performance Comparison

### Current REST Approach
- 20 MRs × 5 calls = 100 API calls
- Network time: ~100 × 50ms = 5 seconds
- Rate limiting risk: high

### GraphQL Approach
- 1 initial query + maybe 2 pagination = 3 API calls max
- Network time: ~3 × 100ms = 300ms
- Rate limiting risk: low

**Expected speedup**: ~15-20x faster

## Migration Strategy

### Step 1: Add GraphQL alongside REST
- Keep existing code working
- Add `--use-graphql` flag
- Test both in parallel

### Step 2: Validate
- Compare data completeness
- Check performance metrics
- Verify all fields map correctly

### Step 3: Switch Default
- Make GraphQL default
- Keep REST as fallback (`--use-rest`)
- Document any differences

### Step 4: Add File Caching
- Implement disk persistence
- Add `--cache-dir` flag
- Add `--no-cache` flag for testing

## Considerations

### GraphQL API Availability
- GitLab has robust GraphQL API (since v11.0)
- Most GitLab instances should support it
- Need fallback for very old instances

### Rate Limiting
- GraphQL has separate rate limits
- Typically more generous than REST
- Single query counts as 1 request (vs 5+ for REST)

### Complexity
- GraphQL queries more complex to write
- Response structure more nested
- But overall simpler than orchestrating multiple REST calls

### Error Handling
- GraphQL can return partial data with errors
- Need to handle `errors` field in response
- Can still get some data even if parts fail

## Recommendation

**Yes, definitely use GraphQL!**

1. **Phase 1** (Quick Win): Implement GraphQL provider, keep in-memory cache
   - Huge performance improvement
   - Minimal code change
   - Can do in a few hours

2. **Phase 2** (Enhancement): Add file persistence
   - Better UX (instant startup)
   - Can be done incrementally
   - Optional feature

Start with Phase 1 - the performance gain from GraphQL alone is worth it, even without file caching.
