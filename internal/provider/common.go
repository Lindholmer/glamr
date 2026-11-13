package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Helpers shared by the REST and GraphQL GitLab providers.

// urlEncode encodes a project path for use as a single GitLab API path segment
// (e.g. "group/project" -> "group%2Fproject").
func urlEncode(s string) string {
	return url.PathEscape(s)
}

// commandError describes a failed glab/gh invocation. Both CLIs print the API's JSON error
// body on stdout and only a status line ("gh: Unprocessable Entity (HTTP 422)") on stderr,
// so the API's own reason comes first, then the status line.
func commandError(msg string, err error) error {
	var stdout []byte
	var failed *cliError
	if errors.As(err, &failed) {
		stdout = failed.stdout
	}
	var stderr string
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		stderr = strings.TrimSpace(string(exitErr.Stderr))
	}

	reason := apiErrorMessage(stdout)
	var text string
	switch {
	case reason != "" && stderr != "":
		text = reason + "\n" + stderr
	case reason != "":
		text = reason
	case stderr != "":
		text = stderr
	default:
		text = err.Error()
	}
	return &wrappedError{msg: msg + ": " + text, err: err}
}

// wrappedError carries a readable message while keeping the cause for errors.Is/As.
type wrappedError struct {
	msg string
	err error
}

func (e *wrappedError) Error() string { return e.msg }
func (e *wrappedError) Unwrap() error { return e.err }

// apiErrorMessage extracts the human-readable reason from a GitLab or GitHub JSON error
// body, e.g. GitHub's {"message":"Unprocessable Entity","errors":["Can not approve your own
// pull request"]} or GitLab's {"message":"403 Forbidden"}. Returns "" if there is none.
func apiErrorMessage(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return ""
	}
	var parsed struct {
		Message          json.RawMessage   `json:"message"`
		Errors           []json.RawMessage `json:"errors"`
		Error            string            `json:"error"`
		ErrorDescription string            `json:"error_description"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return ""
	}

	var reasons []string
	add := func(r string) {
		if r = strings.TrimSpace(r); r != "" && !slices.Contains(reasons, r) {
			reasons = append(reasons, r)
		}
	}

	// Specific errors (GitHub REST/GraphQL) beat the generic message
	for _, raw := range parsed.Errors {
		var text string
		var obj struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Field   string `json:"field"`
		}
		switch {
		case json.Unmarshal(raw, &text) == nil:
			add(text)
		case json.Unmarshal(raw, &obj) == nil && obj.Message != "":
			add(obj.Message)
		case obj.Code != "":
			add(strings.TrimSpace(obj.Field + " " + obj.Code))
		}
	}

	if len(reasons) == 0 && len(parsed.Message) > 0 {
		var text string
		var fields map[string][]string // GitLab validation errors: {"title": ["is too long"]}
		switch {
		case json.Unmarshal(parsed.Message, &text) == nil:
			add(text)
		case json.Unmarshal(parsed.Message, &fields) == nil:
			keys := make([]string, 0, len(fields))
			for k := range fields {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				add(k + " " + strings.Join(fields[k], ", "))
			}
		}
	}

	if parsed.Error != "" {
		add(strings.TrimSpace(parsed.Error + ": " + parsed.ErrorDescription))
	}
	return strings.TrimSuffix(strings.Join(reasons, "; "), ":")
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

// mentionWindowPages caps how many pages of mention candidates are read per refresh.
const mentionWindowPages = 10

// mentionsUser reports whether text @mentions login (case-insensitive). The @ must start a
// word (so "me@example.com" doesn't count) and the name must end there ("@bob" isn't "@bobby").
func mentionsUser(text, login string) bool {
	if login == "" {
		return false
	}
	lower, needle := strings.ToLower(text), "@"+strings.ToLower(login)
	isNameChar := func(b byte) bool {
		return b == '-' || b == '_' || b == '.' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z')
	}
	for i := 0; ; {
		j := strings.Index(lower[i:], needle)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(needle)
		before := start == 0 || !(isNameChar(lower[start-1]) || lower[start-1] == '/')
		// A trailing "." ends a sentence, not the name
		after := end == len(lower) || !isNameChar(lower[end]) || (lower[end] == '.' && (end+1 == len(lower) || !isNameChar(lower[end+1])))
		if before && after {
			return true
		}
		i = start + 1
	}
}
