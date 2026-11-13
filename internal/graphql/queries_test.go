package graphql

import (
	"strings"
	"testing"
)

// Every MR query must select the same fields, since all results go through the same conversion.
func TestQueriesShareMergeRequestFields(t *testing.T) {
	queries := map[string]string{
		"AuthoredMRsQuery":        AuthoredMRsQuery,
		"AssignedMRsQuery":        AssignedMRsQuery,
		"ReviewRequestedMRsQuery": ReviewRequestedMRsQuery,
		"GetMRQuery":              GetMRQuery,
	}
	for name, q := range queries {
		if !strings.Contains(q, mergeRequestFields) {
			t.Errorf("%s does not use mergeRequestFields", name)
		}
		if strings.Count(q, "{") != strings.Count(q, "}") {
			t.Errorf("%s has unbalanced braces", name)
		}
	}
}

func TestScopeQueriesSupportPagination(t *testing.T) {
	for _, q := range []string{AuthoredMRsQuery, AssignedMRsQuery, ReviewRequestedMRsQuery} {
		for _, want := range []string{"$first: Int", "$after: String", "after: $after", "hasNextPage", "endCursor"} {
			if !strings.Contains(q, want) {
				t.Errorf("scope query missing %q", want)
			}
		}
	}
}
