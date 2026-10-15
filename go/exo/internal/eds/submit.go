package eds

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Submit posts a download request to downloadURL and returns the resulting
// operation. scope is omitted from the request body when empty, so the EDS
// applies its own deployment-aware default.
func Submit(client *http.Client, downloadURL, bearerToken, projectID, version, scope string) (*SubmitResponse, error) {
	body, err := json.Marshal(SubmitRequest{
		ProjectID: projectID,
		Version:   version,
		Scope:     scope,
	})
	if err != nil {
		return nil, fmt.Errorf("encoding submit request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, downloadURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building submit request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	resp, err := client.Do(req)
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

// detailOrBody extracts a FastAPI-style {"detail": "..."} message from body,
// falling back to the raw body text.
func detailOrBody(body []byte) string {
	var withDetail struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &withDetail); err == nil && withDetail.Detail != "" {
		return withDetail.Detail
	}
	return string(body)
}
