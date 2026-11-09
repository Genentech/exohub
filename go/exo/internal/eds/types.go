// Package eds submits and follows dataset downloads on a remote ExoHub
// Data Service (EDS). It talks to the EDS REST endpoints directly and does
// not depend on the ArtifactDB catalog client, since a remote EDS download
// never touches the catalog from the CLI's side.
package eds

import "encoding/json"

// Endpoints are the EDS-advertised, origin-relative paths for its REST API.
type Endpoints struct {
	Download   string `json:"download"`
	Operations string `json:"operations"`
	MCP        string `json:"mcp"`
	Save       string `json:"save"`
	Docs       string `json:"docs"`
}

// Info is the deployment info embedded in an index entry, or returned
// directly by an EDS root (GET /).
type Info struct {
	DeploymentID  string    `json:"deployment_id"`
	DeploymentIDs []string  `json:"deployment_ids,omitempty"`
	Scopes        []string  `json:"scopes,omitempty"`
	Endpoints     Endpoints `json:"endpoints"`
}

// IndexEntry is one deployment listed in the ExoHub /eds index.
type IndexEntry struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Info   Info   `json:"info"`
}

// IndexResponse is the body of GET <BaseURL>/eds.
type IndexResponse struct {
	Deployments []IndexEntry `json:"deployments"`
}

// SubmitRequest is the body posted to endpoints.download. Content,
// Worktrees, Checkout, and Force are pointers so that a flag the caller
// never set is omitted from the request entirely (as opposed to being sent
// as its Go zero value) — the EDS rejects any body key it doesn't
// recognise (model_config = {"extra": "forbid"}), so an EDS predating a
// field must never see it, and a flag that wasn't explicitly set should
// defer to the EDS's own default rather than overriding it.
//
// Extra carries additional keys from --eds-params, merged into the body at
// marshal time. Callers (command_eds.go) are responsible for rejecting any
// Extra key that collides with a typed field before constructing a
// SubmitRequest; MarshalJSON does not re-check for collisions.
type SubmitRequest struct {
	ProjectID string `json:"project_id"`
	Version   string `json:"version,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Content   *bool  `json:"content,omitempty"`
	Worktrees *bool  `json:"worktrees,omitempty"`
	Checkout  *bool  `json:"checkout,omitempty"`
	Force     *bool  `json:"force,omitempty"`

	Extra map[string]any `json:"-"`
}

// MarshalJSON encodes the typed fields and then merges Extra on top, so
// --eds-params can carry forward-compatible keys the typed fields don't
// know about yet.
func (r SubmitRequest) MarshalJSON() ([]byte, error) {
	type typed SubmitRequest
	base, err := json.Marshal(typed(r))
	if err != nil {
		return nil, err
	}
	if len(r.Extra) == 0 {
		return base, nil
	}

	var merged map[string]any
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	for k, v := range r.Extra {
		merged[k] = v
	}
	return json.Marshal(merged)
}

// SubmitResponse is the 202 body returned by endpoints.download.
type SubmitResponse struct {
	OperationID  string `json:"operation_id"`
	OperationURL string `json:"operation_url"`
}

// Progress carries the sync counters reported while an operation runs.
// It is nil once an operation has completed; the final numbers live in
// Result instead.
type Progress struct {
	Step            string     `json:"step"`
	TS              string     `json:"ts"`
	FilesSynced     int64      `json:"files_synced"`
	BytesSynced     int64      `json:"bytes_synced"`
	FilesStaged     int64      `json:"files_staged"`
	BytesStaged     int64      `json:"bytes_staged"`
	FilesTotal      int64      `json:"files_total"`
	AnnexFilesTotal int64      `json:"annex_files_total"`
	BytesTotal      int64      `json:"bytes_total"`
	Activities      []Activity `json:"activities"`
}

// Activity is one pending Temporal activity reported inside Progress.
// State is the lower-cased Temporal pending-activity state (e.g.
// "scheduled", "started"). Attempt is greater than 1 on a retry, with
// LastFailure carrying the reason.
type Activity struct {
	State       string  `json:"state"`
	Attempt     int     `json:"attempt"`
	LastFailure *string `json:"last_failure"`
}

// Result is the outcome reported once an operation completes. RepoDir is
// where the dataset now lives on the EDS; it may be empty even on a
// successful result, so callers must check before printing it.
type Result struct {
	Status          string   `json:"status"`
	Error           string   `json:"error,omitempty"`
	Scope           string   `json:"scope,omitempty"`
	RepoDir         string   `json:"repo_dir"`
	FilesSynced     int64    `json:"files_synced"`
	BytesSynced     int64    `json:"bytes_synced"`
	FilesTotal      int64    `json:"files_total"`
	AnnexFilesTotal int64    `json:"annex_files_total"`
	BytesTotal      int64    `json:"bytes_total"`
	Verified        bool     `json:"verified"`
	MissingFiles    []string `json:"missing_files,omitempty"`
	FilesAdded      int64    `json:"files_added"`
	CommitSHA       string   `json:"commit_sha"`
}

// Operation is the JSON body returned by the operation status endpoint.
type Operation struct {
	Status   string    `json:"status"`
	Progress *Progress `json:"progress,omitempty"`
	Result   *Result   `json:"result,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Message  string    `json:"message,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// Terminal statuses for an operation.
const (
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusCanceled   = "canceled"
	StatusTerminated = "terminated"
)

// ResultOK is the success result status, printed when result.status is
// absent as well.
const ResultOK = "ok"

// ResultDegraded means the EDS verified the download and files are still
// missing (exohub-workers #66).
const ResultDegraded = "degraded"

// IsTerminal reports whether status is one of the terminal operation states.
func IsTerminal(status string) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCanceled, StatusTerminated:
		return true
	default:
		return false
	}
}

// Deployment is a resolved EDS: its base URL and advertised info, ready for
// endpoint resolution.
type Deployment struct {
	URL  string
	Info Info
}
