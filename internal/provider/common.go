package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Helpers shared by the REST and GraphQL GitLab providers.

// urlEncode encodes a project path for use as a single GitLab API path segment
// (e.g. "group/project" -> "group%2Fproject").
func urlEncode(s string) string {
	return url.PathEscape(s)
}

// commandError wraps a failed glab invocation, including glab's stderr (which holds the
// actual API error, e.g. "401 Unauthorized") when available.
func commandError(msg string, err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return fmt.Errorf("%s: %w\nOutput: %s", msg, err, strings.TrimSpace(string(exitErr.Stderr)))
	}
	return fmt.Errorf("%s: %w", msg, err)
}

// awaitsReply reports whether a thread (given its comment authors in order) needs the user's
// attention: the user took part, but someone else wrote the latest comment.
func awaitsReply(authors []string, me string) bool {
	if len(authors) == 0 || authors[len(authors)-1] == me {
		return false
	}
	for _, a := range authors {
		if a == me {
			return true
		}
	}
	return false
}

// parseMRID splits an MR ID of the form "group/project!123" into its project path and IID.
func parseMRID(id string) (repo, iid string, err error) {
	i := strings.LastIndex(id, "!")
	if i <= 0 || i == len(id)-1 {
		return "", "", fmt.Errorf("invalid MR ID format: %s (expected format: project/path!123)", id)
	}
	return id[:i], id[i+1:], nil
}

// decodeJSONArrays decodes glab output that is either a single JSON array or, with
// older glab versions using --paginate, several JSON arrays concatenated together.
func decodeJSONArrays(data []byte) ([]map[string]any, error) {
	var result []map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	for decoder.More() {
		var page []map[string]any
		if err := decoder.Decode(&page); err != nil {
			return nil, err
		}
		result = append(result, page...)
	}
	return result, nil
}

// draftPrefix matches the title prefixes GitLab uses to mark an MR as draft.
var draftPrefix = regexp.MustCompile(`(?i)^\s*(\[draft\]|\(draft\)|draft:|\[wip\]|wip:)\s*`)

// toggleDraftTitle returns the title that flips an MR's draft state.
func toggleDraftTitle(title string, isDraft bool) string {
	if !isDraft {
		return "Draft: " + title
	}
	for draftPrefix.MatchString(title) {
		title = draftPrefix.ReplaceAllString(title, "")
	}
	return title
}

// aggregateJobStatuses derives a pipeline status from its job statuses.
// Priority: failed > running > pending (incl. created/manual/...) > success.
// Returns "" if no job has a recognised status, so callers can keep the API's status.
func aggregateJobStatuses(jobStatuses []string) PipelineStatus {
	hasFailed, hasRunning, hasPending, hasSuccess := false, false, false, false

	for _, status := range jobStatuses {
		switch strings.ToLower(status) {
		case "failed":
			hasFailed = true
		case "running":
			hasRunning = true
		case "pending", "created", "preparing", "waiting_for_resource", "scheduled", "manual":
			hasPending = true
		case "success":
			hasSuccess = true
		}
	}

	switch {
	case hasFailed:
		return PipelineFailed
	case hasRunning:
		return PipelineRunning
	case hasPending:
		return PipelinePending
	case hasSuccess:
		return PipelineSuccess
	default:
		return ""
	}
}

// parseRESTJobs converts the REST /pipelines/:id/jobs response into jobs, tolerating missing fields.
func parseRESTJobs(jobsData []map[string]any) []PipelineJob {
	jobs := make([]PipelineJob, 0, len(jobsData))
	for _, jobData := range jobsData {
		job := PipelineJob{}
		if id, ok := jobData["id"].(float64); ok {
			job.ID = strconv.FormatInt(int64(id), 10)
		}
		if name, ok := jobData["name"].(string); ok {
			job.Name = name
		}
		if status, ok := jobData["status"].(string); ok {
			job.Status = strings.ToLower(status)
		}
		if stage, ok := jobData["stage"].(string); ok {
			job.Stage = stage
		}
		jobs = append(jobs, job)
	}
	return jobs
}

// parseDiscussions converts the REST /merge_requests/:iid/discussions response into notes.
// Each note carries its discussion ID so it can be replied to or resolved. System notes are skipped.
func parseDiscussions(data []byte) ([]MRNote, error) {
	type restDiscussion struct {
		ID    string `json:"id"`
		Notes []struct {
			Body   string `json:"body"`
			Author struct {
				Username string `json:"username"`
			} `json:"author"`
			CreatedAt  string `json:"created_at"`
			System     bool   `json:"system"`
			Resolvable bool   `json:"resolvable"`
			Resolved   bool   `json:"resolved"`
		} `json:"notes"`
	}

	var discussions []restDiscussion
	decoder := json.NewDecoder(bytes.NewReader(data))
	for decoder.More() {
		var page []restDiscussion
		if err := decoder.Decode(&page); err != nil {
			return nil, fmt.Errorf("failed to parse discussions: %w", err)
		}
		discussions = append(discussions, page...)
	}

	notes := make([]MRNote, 0)
	for _, d := range discussions {
		for _, n := range d.Notes {
			if n.System {
				continue
			}
			notes = append(notes, MRNote{
				ID:         d.ID,
				Author:     n.Author.Username,
				Body:       n.Body,
				CreatedAt:  n.CreatedAt,
				Resolvable: n.Resolvable,
				Resolved:   n.Resolved,
			})
		}
	}
	return notes, nil
}
