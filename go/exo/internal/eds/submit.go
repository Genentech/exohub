package eds

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Submit posts req to downloadURL and returns the resulting operation.
// Fields req doesn't set (empty strings, nil pointers, no Extra keys) are
// omitted from the request body entirely, so the EDS applies its own
// deployment-aware defaults and an EDS predating a newer field never sees
// it (model_config = {"extra": "forbid"} on the server means any unknown
// key is a 422).
func Submit(client *http.Client, downloadURL, bearerToken string, req SubmitRequest) (*SubmitResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encoding submit request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, downloadURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building submit request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+bearerToken)

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("submitting download to %s: %w", downloadURL, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%s", detailOrBody(respBody))
	}
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("submit failed: status %d: %s", resp.StatusCode, detailOrBody(respBody))
	}

	var sub SubmitResponse
	if err := json.Unmarshal(respBody, &sub); err != nil {
		return nil, fmt.Errorf("decoding submit response: %w", err)
	}
	if sub.OperationID == "" {
		return nil, fmt.Errorf("submit response missing operation_id")
	}
	return &sub, nil
}

// validationError is one entry of a FastAPI/pydantic 422 "detail" list:
// {"loc": ["body", "content"], "msg": "Input should be a valid boolean", "type": "bool_parsing"}.
type validationError struct {
	Loc []any  `json:"loc"`
	Msg string `json:"msg"`
}

// detailOrBody extracts a human-readable message from a FastAPI error body.
// It handles two shapes:
//   - {"detail": "some string"} — returned as-is.
//   - {"detail": [{"loc": [...], "msg": "..."}, ...]} — FastAPI's 422
//     validation-error shape, rendered as one "loc: msg" line per entry
//     (e.g. "body.content: Input should be a valid boolean",
//     "body.foo: Extra inputs are not permitted").
//
// It falls back to the raw body text when neither shape parses.
func detailOrBody(body []byte) string {
	var withDetail struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &withDetail); err == nil && withDetail.Detail != "" {
		return withDetail.Detail
	}

	var withList struct {
		Detail []validationError `json:"detail"`
	}
	if err := json.Unmarshal(body, &withList); err == nil && len(withList.Detail) > 0 {
		lines := make([]string, 0, len(withList.Detail))
		for _, e := range withList.Detail {
			lines = append(lines, fmt.Sprintf("%s: %s", formatLoc(e.Loc), e.Msg))
		}
		return strings.Join(lines, "\n")
	}

	return string(body)
}

// formatLoc renders a pydantic error location (e.g. ["body", "content"])
// as a dotted path ("body.content").
func formatLoc(loc []any) string {
	parts := make([]string, 0, len(loc))
	for _, p := range loc {
		parts = append(parts, fmt.Sprintf("%v", p))
	}
	return strings.Join(parts, ".")
}
