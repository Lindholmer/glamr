package graphql

// GraphQL query templates for fetching merge request data

// mergeRequestFields is the MergeRequest selection shared by every MR query, so the list
// queries and the single-MR query always return the same data (see convertGraphQLMR).
const mergeRequestFields = `
        iid
        id
        title
        description
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
        headPipeline {
          id
          status
          duration
          createdAt
          updatedAt
          jobs {
            nodes {
              id
              name
              status
              stage {
                name
              }
            }
          }
        }
        approvedBy {
          nodes {
            username
          }
        }
        approved
        approvalsLeft
        approvalsRequired
        userDiscussionsCount
        reviewers {
          nodes {
            username
            mergeRequestInteraction {
              reviewState
            }
          }
        }
        discussions(first: 100) {
          nodes {
            id
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
`

func scopeQuery(name, connection string) string {
	return `
query ` + name + `($first: Int = 50, $after: String) {
  currentUser {
    username
    ` + connection + `(state: opened, first: $first, after: $after) {
      pageInfo {
        hasNextPage
        endCursor
      }
      nodes {` + mergeRequestFields + `      }
    }
  }
}
`
}

var (
	AuthoredMRsQuery        = scopeQuery("getAuthoredMergeRequests", "authoredMergeRequests")
	AssignedMRsQuery        = scopeQuery("getAssignedMergeRequests", "assignedMergeRequests")
	ReviewRequestedMRsQuery = scopeQuery("getReviewRequestedMergeRequests", "reviewRequestedMergeRequests")
)

// GetMRQuery fetches a single MR with the same fields as the list queries
const GetMRQuery = `
query getMergeRequest($projectPath: ID!, $iid: String!) {
  project(fullPath: $projectPath) {
    mergeRequest(iid: $iid) {` + mergeRequestFields + `    }
  }
}
`
