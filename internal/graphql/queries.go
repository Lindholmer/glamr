package graphql

// GraphQL query templates for fetching merge request data

const AuthoredMRsQuery = `
query getAuthoredMergeRequests($first: Int = 50, $after: String) {
  currentUser {
    username
    authoredMergeRequests(state: opened, first: $first, after: $after) {
      pageInfo {
        hasNextPage
        endCursor
      }
      nodes {
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
  }
}
`

const AssignedMRsQuery = `
query getAssignedMergeRequests($first: Int = 50, $after: String) {
  currentUser {
    username
    assignedMergeRequests(state: opened, first: $first, after: $after) {
      pageInfo {
        hasNextPage
        endCursor
      }
      nodes {
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
  }
}
`

const ReviewRequestedMRsQuery = `
query getReviewRequestedMergeRequests($first: Int = 50, $after: String) {
  currentUser {
    username
    reviewRequestedMergeRequests(state: opened, first: $first, after: $after) {
      pageInfo {
        hasNextPage
        endCursor
      }
      nodes {
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
  }
}
`

// AllScopesQuery fetches MRs from all three scopes in a single query
const AllScopesQuery = `
query getAllMergeRequests($first: Int = 50) {
  currentUser {
    username
    authoredMergeRequests(state: opened, first: $first) {
      nodes {
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
    assignedMergeRequests(state: opened, first: $first) {
      nodes {
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
    reviewRequestedMergeRequests(state: opened, first: $first) {
      nodes {
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
  }
}
`

// Query for getting a single MR with full details
const GetMRQuery = `
query getMergeRequest($projectPath: ID!, $iid: String!) {
  project(fullPath: $projectPath) {
    mergeRequest(iid: $iid) {
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
        webPath
        jobs {
          nodes {
            name
            status
            stage
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
}
`
