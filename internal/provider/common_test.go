package provider

import (
	"reflect"
	"testing"
)

func TestURLEncode(t *testing.T) {
	tests := map[string]string{
		"group/project":          "group%2Fproject",
		"group/sub group/my-app": "group%2Fsub%20group%2Fmy-app",
		"plain":                  "plain",
	}
	for in, want := range tests {
		if got := urlEncode(in); got != want {
			t.Errorf("urlEncode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseMRID(t *testing.T) {
	repo, iid, err := parseMRID("group/sub/project!42")
	if err != nil || repo != "group/sub/project" || iid != "42" {
		t.Fatalf("parseMRID = (%q, %q, %v), want (group/sub/project, 42, nil)", repo, iid, err)
	}

	for _, bad := range []string{"", "42", "!42", "group/project!", "group/project"} {
		if _, _, err := parseMRID(bad); err == nil {
			t.Errorf("parseMRID(%q) expected error", bad)
		}
	}
}

func TestDecodeJSONArrays(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"single array", `[{"id":1},{"id":2}]`, 2},
		{"concatenated pages", `[{"id":1}][{"id":2},{"id":3}]`, 3},
		{"pages separated by newline", "[{\"id\":1}]\n[{\"id\":2}]\n", 2},
		{"empty array", `[]`, 0},
		{"empty output", ``, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeJSONArrays([]byte(tt.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tt.want {
				t.Errorf("got %d items, want %d", len(got), tt.want)
			}
		})
	}

	if _, err := decodeJSONArrays([]byte(`{"message":"401 Unauthorized"}`)); err == nil {
		t.Error("expected error for non-array JSON")
	}
}

func TestToggleDraftTitle(t *testing.T) {
	tests := []struct {
		title   string
		isDraft bool
		want    string
	}{
		{"Add feature", false, "Draft: Add feature"},
		{"Draft: Add feature", true, "Add feature"},
		{"draft: Add feature", true, "Add feature"},
		{"Draft:Add feature", true, "Add feature"},
		{"[Draft] Add feature", true, "Add feature"},
		{"(Draft) Add feature", true, "Add feature"},
		{"WIP: Add feature", true, "Add feature"},
		{"Draft: WIP: Add feature", true, "Add feature"},
		// Not a prefix GitLab recognises - leave the title alone
		{"Drafting the docs", true, "Drafting the docs"},
	}
	for _, tt := range tests {
		if got := toggleDraftTitle(tt.title, tt.isDraft); got != tt.want {
			t.Errorf("toggleDraftTitle(%q, %v) = %q, want %q", tt.title, tt.isDraft, got, tt.want)
		}
	}
}

func TestAggregateJobStatuses(t *testing.T) {
	tests := []struct {
		name     string
		statuses []string
		want     PipelineStatus
	}{
		{"failed wins", []string{"success", "running", "failed"}, PipelineFailed},
		{"running beats pending", []string{"success", "pending", "running"}, PipelineRunning},
		{"pending", []string{"success", "pending"}, PipelinePending},
		{"created counts as pending", []string{"success", "created"}, PipelinePending},
		{"manual counts as pending", []string{"success", "manual"}, PipelinePending},
		{"all success", []string{"success", "success"}, PipelineSuccess},
		{"case insensitive (GraphQL)", []string{"SUCCESS", "FAILED"}, PipelineFailed},
		{"skipped jobs ignored", []string{"success", "skipped"}, PipelineSuccess},
		{"nothing recognised", []string{"canceled", "skipped"}, ""},
		{"no jobs", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := aggregateJobStatuses(tt.statuses); got != tt.want {
				t.Errorf("aggregateJobStatuses(%v) = %q, want %q", tt.statuses, got, tt.want)
			}
		})
	}
}

func TestParseRESTJobs(t *testing.T) {
	jobs := parseRESTJobs([]map[string]any{
		{"id": float64(1001), "name": "build", "status": "success", "stage": "build"},
		{"id": float64(1002), "name": "deploy", "status": "MANUAL"},
		{"name": "missing id"}, // must not panic on missing fields
	})

	want := []PipelineJob{
		{ID: "1001", Name: "build", Status: "success", Stage: "build"},
		{ID: "1002", Name: "deploy", Status: "manual"},
		{Name: "missing id"},
	}
	if !reflect.DeepEqual(jobs, want) {
		t.Errorf("parseRESTJobs = %+v, want %+v", jobs, want)
	}
}

func TestParseDiscussions(t *testing.T) {
	input := `[
		{"id": "abc123", "notes": [
			{"body": "Please fix", "author": {"username": "alice"}, "created_at": "2026-01-01T10:00:00Z", "resolvable": true, "resolved": false},
			{"body": "Done", "author": {"username": "bob"}, "created_at": "2026-01-01T11:00:00Z", "resolvable": true, "resolved": false}
		]},
		{"id": "sys1", "notes": [
			{"body": "added 1 commit", "author": {"username": "bob"}, "system": true}
		]}
	][
		{"id": "def456", "notes": [
			{"body": "LGTM", "author": {"username": "carol"}, "created_at": "2026-01-02T10:00:00Z"}
		]}
	]`

	notes, err := parseDiscussions([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []MRNote{
		{ID: "abc123", Author: "alice", Body: "Please fix", CreatedAt: "2026-01-01T10:00:00Z", Resolvable: true},
		{ID: "abc123", Author: "bob", Body: "Done", CreatedAt: "2026-01-01T11:00:00Z", Resolvable: true},
		{ID: "def456", Author: "carol", Body: "LGTM", CreatedAt: "2026-01-02T10:00:00Z"},
	}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("parseDiscussions =\n%+v\nwant\n%+v", notes, want)
	}

	if _, err := parseDiscussions([]byte(`not json`)); err == nil {
		t.Error("expected error for invalid JSON")
	}
}
